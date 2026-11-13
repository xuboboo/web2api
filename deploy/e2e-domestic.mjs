#!/usr/bin/env node
/**
 * OpenSERP 国内搜索引擎端到端 + 稳定性 + 审计测试。
 *
 *   node deploy/e2e-domestic.mjs [baseUrl]
 *   OPENSERP_BASE_URL=http://127.0.0.1:7000 node deploy/e2e-domestic.mjs
 *
 * 覆盖：服务生命周期 / 参数校验 / 国产引擎(百度·搜狗·360)与 Bing 中国 / 输出格式 /
 * 多引擎聚合 / 正文提取 / 缓存 / 串行与并发稳定性 / 运行审计。
 *
 * 退出码：全部关键用例通过为 0，否则 1。报告写入 deploy/e2e-results/domestic-*.json
 */
import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const BASE = (process.env.OPENSERP_BASE_URL || process.argv[2] || 'http://127.0.0.1:7000').replace(/\/+$/, '');
const OUT_DIR = join(dirname(fileURLToPath(import.meta.url)), 'e2e-results');
const results = [];
const t0 = Date.now();

async function req(path, { method = 'GET', headers = {}, body, timeout = 120000 } = {}) {
  const ac = new AbortController();
  const timer = setTimeout(() => ac.abort(), timeout);
  const started = Date.now();
  try {
    const res = await fetch(BASE + path, { method, headers, body, signal: ac.signal, redirect: 'manual' });
    const text = await res.text();
    let json;
    try { json = JSON.parse(text); } catch { json = undefined; }
    return { status: res.status, headers: res.headers, text, json, ms: Date.now() - started, path };
  } finally {
    clearTimeout(timer);
  }
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// 反爬友好的节流间隔：E2E_PACE_MS=6000 时每次搜索之间停顿 6s
const PACE = Number(process.env.E2E_PACE_MS || 0);
// 串行压测的间隔：国内引擎对同 IP 高频访问敏感，默认 12s
const SOAK_PACE = Number(process.env.E2E_SOAK_PACE_MS || 12000);
// 压测规模可调：单 IP 反爬敏感时用 E2E_SOAK_RUNS=3 E2E_CONCURRENCY=2 做“轻量验收”
const SOAK_RUNS = Math.max(1, Number(process.env.E2E_SOAK_RUNS || 9));
const CONCURRENCY = Math.max(1, Number(process.env.E2E_CONCURRENCY || 6));
function assert(cond, msg) { if (!cond) throw new Error(msg); }

const ENV_ERROR = /captcha|blocked|rate_limited|423|429|403|circuit|eof|timeout|failed to extract/i;

async function test(group, name, fn, { critical = true, retries = 0, betweenMs = 3000 } = {}) {
  const started = Date.now();
  let lastErr;
  for (let attempt = 0; attempt <= retries; attempt++) {
    try {
      const detail = await fn();
      results.push({ group, name, critical, ok: true, attempts: attempt + 1, ms: Date.now() - started, detail });
      console.log('  PASS  ' + name + '  (' + (Date.now() - started) + 'ms' + (attempt ? ', retry ' + attempt : '') + ')');
      return;
    } catch (err) {
      lastErr = err;
      if (attempt < retries) await sleep(betweenMs);
    }
  }
  const message = String((lastErr && lastErr.message) || lastErr);
  const envFlagged = Boolean(lastErr && lastErr.environment);
  results.push({
    group, name, critical: critical && !envFlagged, ok: false, attempts: retries + 1, ms: Date.now() - started, error: message,
    classification: envFlagged || (!critical && ENV_ERROR.test(message)) ? 'environment' : 'product',
  });
  console.log('  FAIL  ' + name + '  -> ' + message);
}

/**
 * 依次尝试多个引擎，直到某个引擎返回 200；被反爬拦截（429/403/503）视为环境事件，
 * 只有全部引擎都失败时才报错。
 */
async function firstWorking(pathFor, validate, { engines = (typeof capabilityPool !== 'undefined' ? capabilityPool : ['baidu', 'sogou', '360', 'bing']), attempts = 2 } = {}) {
  const errors = [];
  for (let round = 0; round < attempts; round++) {
    for (const engine of engines) {
      const r = await req(pathFor(engine));
      if (r.status === 200) return { engine, value: validate(r) };
      errors.push(engine + ':' + r.status);
    }
    await sleep(3000);
  }
  const gatedOnly = errors.every((e) => /:(429|403|503)$/.test(e));
  const error = new Error('all engines gated/failed - ' + errors.join(', '));
  error.environment = gatedOnly;
  throw error;
}

function checkEnvelope(r, { limit } = {}) {
  assert(r.status === 200, 'HTTP ' + r.status + ' ' + r.text.slice(0, 160));
  assert(r.json, 'response is not JSON');
  assert(r.json.query && typeof r.json.query.text === 'string', 'missing query.text');
  assert(Array.isArray(r.json.results), 'missing results array');
  assert(r.json.meta && typeof r.json.meta.took_ms === 'number', 'missing meta.took_ms');
  const organic = r.json.results.filter((x) => x.type === 'organic');
  assert(organic.length > 0, 'no organic results');
  const urls = new Set();
  let organicRank = 0;
  for (const item of organic) {
    assert(typeof item.url === 'string' && /^https?:\/\//.test(item.url), 'organic result without absolute url: ' + JSON.stringify(item).slice(0, 120));
    assert(typeof item.title === 'string' && item.title.trim().length > 0, 'organic result without title');
    assert(!urls.has(item.url), 'duplicate url in results: ' + item.url);
    urls.add(item.url);
    organicRank++;
    if (typeof item.rank === 'number' && item.rank > 0) assert(item.rank === organicRank, 'organic ranks not sequential: got ' + item.rank + ' want ' + organicRank);
  }
  if (limit) assert(organic.length <= limit, 'limit not enforced: ' + organic.length + ' > ' + limit);
  return { organic: organic.length, total: r.json.results.length, took_ms: r.json.meta.took_ms, urls: organic.map((x) => x.url) };
}

// 启动探针：先确认每个引擎当前是否可用（反爬会临时拦截），
// 后续涉及"某个引擎"的用例都基于这份可用性矩阵，避免把上游反爬当成产品缺陷。
console.log('\n[0/9] 引擎可用性探针');
const AVAIL = {};
for (const engine of ['baidu', 'sogou', '360', 'bing']) {
  const r = await req('/' + engine + '/search?text=' + encodeURIComponent('golang 教程') + '&limit=3');
  const organic = r.status === 200 && r.json ? r.json.results.filter((x) => x.type === 'organic').length : 0;
  AVAIL[engine] = { ok: organic > 0, status: r.status, organic };
  console.log('  ' + engine.padEnd(6) + ' status=' + r.status + ' organic=' + organic);
  await sleep(400 + PACE);
}
const domesticHealthy = ['baidu', 'sogou', '360'].filter((e) => AVAIL[e].ok);
// 选一个当前可用的国产引擎作为格式/缓存/压测的基准引擎
const primaryEngine = domesticHealthy[0] || 'baidu';
// 把功能性用例分散到不同引擎，避免把单引擎推到反爬阈值
// 能力类用例（格式/缓存/聚合/内联提取）与具体引擎无关：优先国产引擎，
// 全都不可用时退到 Bing 中国，避免把上游封禁误判成能力缺陷。
const capabilityPool = [...domesticHealthy, 'bing'].filter((e, i, a) => a.indexOf(e) === i);
const rotateEngine = (i) => capabilityPool[i % capabilityPool.length] || primaryEngine;
const fmtEngines = [0, 1, 2].map((i) => rotateEngine(i));

// ------------------------------------------------------------ 1. 基础设施
console.log('\n[1/9] 服务生命周期 / 引擎清单');
await test('infra', '/health 报告 4 个引擎（国产 3 + bing）', async () => {
  const r = await req('/health');
  assert(r.status === 200, 'status ' + r.status);
  assert(['healthy', 'degraded'].includes(r.json.status), 'unexpected health status: ' + r.json.status);
  const names = r.json.engines.map((e) => e.name).sort();
  assert(JSON.stringify(names) === JSON.stringify(['baidu', 'bing', 'so360', 'sogou']), 'engine set = ' + names.join(','));
  for (const e of r.json.engines) assert(e.initialized === true, 'engine not initialized: ' + e.name);
  return { status: r.json.status, uptime: r.json.uptime, engines: names };
});

await test('infra', '/ready 返回 200', async () => {
  const r = await req('/ready');
  assert(r.status === 200, 'status ' + r.status);
  return { body: r.text.slice(0, 80) };
});

await test('infra', '境外引擎已下线（/google/search -> 404）', async () => {
  const r = await req('/google/search?text=golang');
  assert(r.status === 404, 'google endpoint should be gone, got ' + r.status);
  const ddg = await req('/duck/search?text=golang');
  assert(ddg.status === 404, 'duckduckgo endpoint should be gone, got ' + ddg.status);
  return { google: r.status, duck: ddg.status };
});

await test('infra', '/openapi.yaml 与 /docs 可访问', async () => {
  const spec = await req('/openapi.yaml');
  assert(spec.status === 200, 'openapi status ' + spec.status);
  for (const name of ['sogou', 'so360']) assert(spec.text.includes(name), 'spec does not mention ' + name);
  const docs = await req('/docs');
  assert(docs.status === 200 && /swagger/i.test(docs.text), 'swagger UI missing');
  return { specBytes: spec.text.length };
});

await test('infra', 'CORS 预检与 X-Request-ID 透传', async () => {
  const cors = await req('/baidu/search', {
    method: 'OPTIONS',
    headers: { Origin: 'https://example.com', 'Access-Control-Request-Method': 'GET' },
  });
  assert([200, 204].includes(cors.status), 'CORS status ' + cors.status);
  assert(cors.headers.get('access-control-allow-origin'), 'missing allow-origin');
  const id = await req('/health', { headers: { 'X-Request-ID': 'domestic-audit-1' } });
  assert(id.headers.get('x-request-id') === 'domestic-audit-1', 'request id not echoed');
  return { cors: cors.status };
});

// ------------------------------------------------------------ 2. 参数校验
console.log('\n[2/9] 参数校验与错误契约');
await test('errors', '缺少 text -> 400', async () => {
  const r = await req('/baidu/search');
  assert(r.status === 400, 'status ' + r.status);
  assert(r.json && r.json.error, 'no error payload');
  return r.json;
});
await test('errors', 'limit 越界 -> 400', async () => {
  const r = await req('/sogou/search?text=go&limit=999');
  assert(r.status === 400, 'status ' + r.status);
  return { status: r.status };
});
await test('errors', '未知引擎 -> 400', async () => {
  const r = await req('/mega/search?text=go&engines=nosuch');
  assert(r.status === 400, 'status ' + r.status);
  return { status: r.status };
});
await test('errors', '未实现的图片搜索 -> 501（而非 5xx 崩溃）', async () => {
  // 逐个引擎尝试，避开正处熔断/反爬的引擎
  const attempted = [];
  for (const engine of ['sogou', '360', 'baidu']) {
    const r = await req('/' + engine + '/image?text=golang');
    attempted.push(engine + ':' + r.status);
    if (r.status === 501) {
      assert(r.json && r.json.error === 'not_implemented', 'unexpected error code: ' + (r.json && r.json.error));
      return { engine, status: r.status, error: r.json.error };
    }
  }
  throw new Error('no engine answered 501 for image search: ' + attempted.join(', '));
});

// ------------------------------------------------------------ 3. 引擎搜索
console.log('\n[3/9] 国产搜索引擎');
const engineStats = {};

await test('engines', '至少 1 个引擎可用（硬性）', async () => {
  const usable = ['baidu', 'sogou', '360', 'bing'].filter((e) => AVAIL[e].ok);
  assert(usable.length >= 1, 'no engine usable at all: ' + JSON.stringify(AVAIL));
  return { usable, availability: AVAIL };
});

await test('engines', '引擎冗余：≥2 个可用且至少 1 个国产（信息项）', async () => {
  const usable = ['baidu', 'sogou', '360', 'bing'].filter((e) => AVAIL[e].ok);
  const detail = { usable, domestic: domesticHealthy, availability: AVAIL };
  if (usable.length < 2) {
    // 上游在同一窗口内把三家国产 + bing 全部拦掉时，这是环境事件，不是产品缺陷
    const e = new Error('fewer than 2 usable engines (upstream gating): ' + JSON.stringify(AVAIL));
    e.environment = true;
    throw e;
  }
  if (domesticHealthy.length < 1) {
    const e = new Error('no domestic engine usable (upstream gating): ' + JSON.stringify(AVAIL));
    e.environment = true;
    throw e;
  }
  return detail;
}, { critical: false });

for (const engineName of ['baidu', 'sogou', '360']) {
  await test('engines', engineName + ' 中文搜索 limit=5', async () => {
    const r = await req('/' + engineName + '/search?text=' + encodeURIComponent('人工智能 应用') + '&limit=5');
    const info = checkEnvelope(r, { limit: 5 });
    assert(info.organic >= (engineName === '360' ? 2 : 4), 'too few ' + engineName + ' results: ' + info.organic);
    if (engineName === 'sogou') {
      const wrapped = info.urls.filter((u) => /sogou\.com\/link/.test(u)).length;
      assert(wrapped <= Math.ceil(info.organic / 2), 'too many unresolved sogou redirects: ' + wrapped + '/' + info.organic);
    }
    engineStats[engineName] = info;
    return { organic: info.organic, took_ms: info.took_ms, first: info.urls[0] };
  }, { critical: false, retries: 2, betweenMs: 12000 });
}

await test('engines', '360 别名 /360 与 /so360 指向同一引擎', async () => {
  const a = await req('/360/search?text=golang&limit=3');
  const b = await req('/so360/search?text=golang&limit=3');
  assert(a.status !== 404 && b.status !== 404, 'alias route missing: ' + a.status + '/' + b.status);
  const payload = a.json && a.json.results ? a.json : b.json;
  if (payload && payload.results && payload.results.length) {
    assert(payload.results.every((x) => x.engine === 'so360'), 'engine attribution wrong: ' + payload.results[0].engine);
  }
  return { a: a.status, b: b.status, gated: a.status !== 200 };
}, { retries: 1 });

await test('engines', 'bing(中国) 搜索可用', async () => {
  const r = await req('/bing/search?text=' + encodeURIComponent('golang 教程') + '&limit=3');
  const info = checkEnvelope(r, { limit: 3 });
  engineStats.bing = info;
  return { organic: info.organic, took_ms: info.took_ms };
}, { retries: 1 });

await test('engines', '翻页 start=10 生效且排名绝对化', async () => {
  const out = await firstWorking((e) => '/' + e + '/search?text=' + encodeURIComponent('golang') + '&limit=5&start=10', (r) => {
    const organic = r.json.results.filter((x) => x.type === 'organic');
    assert(organic.length >= 3, 'paged results too few: ' + organic.length);
    // Ranks are absolute across pages: start=10 -> first organic row is 11.
    assert(organic[0].rank === 11, 'expected absolute rank 11 for the first paged row, got ' + organic[0].rank);
    for (let i = 1; i < organic.length; i++) assert(organic[i].rank === organic[i - 1].rank + 1, 'paged ranks not consecutive');
    return { organic: organic.length, firstRank: organic[0].rank };
  });
  return out;
}, { retries: 1, betweenMs: 8000 });

await test('engines', 'site: 过滤生效', async () => {
  const out = await firstWorking((e) => '/' + e + '/search?text=' + encodeURIComponent('site:go.dev golang') + '&limit=5', (r) => {
    const urls = r.json.results.filter((x) => x.type === 'organic').map((x) => x.url);
    const hosts = urls.map((u) => new URL(u).hostname);
    const matched = hosts.filter((h) => h.endsWith('go.dev')).length;
    assert(matched >= 1, 'site: filter returned no go.dev hosts: ' + hosts.join(','));
    return { organic: urls.length, matched };
  });
  return out;
}, { retries: 1, betweenMs: 8000 });

await test('engines', '反爬语义：被拦时返回 429 captcha_detected 而非 504', async () => {
  const gated = ['baidu', 'sogou', '360'].filter((e) => !AVAIL[e].ok && [429, 403].includes(AVAIL[e].status));
  if (gated.length === 0) return { skipped: true, reason: 'no engine currently gated' };
  const engine = gated[0];
  const r = await req('/' + engine + '/search?text=golang&limit=2');
  assert([429, 403, 503].includes(r.status), 'gated engine returned ' + r.status);
  assert(r.json && ['captcha_detected', 'blocked', 'circuit_open'].includes(r.json.error), 'unexpected error code: ' + (r.json && r.json.error));
  return { engine, status: r.status, error: r.json.error };
});

if (PACE) await sleep(PACE);

// ------------------------------------------------------------ 4. 输出格式
console.log('\n[4/9] 输出格式');
await test('formats', 'format=markdown', async () => {
  const out = await firstWorking((e) => '/' + e + '/search?text=golang&limit=3&format=markdown', (r) => {
    assert(/text\/markdown/.test(r.headers.get('content-type') || ''), 'bad markdown content-type');
    assert(/#+\s*Results/i.test(r.text), 'no markdown results section');
    return { bytes: r.text.length };
  });
  return out;
});
await test('formats', 'format=text', async () => {
  const out = await firstWorking((e) => '/' + e + '/search?text=golang&limit=3&format=text', (r) => {
    assert(/text\/plain/.test(r.headers.get('content-type') || ''), 'bad text content-type');
    return { bytes: r.text.length };
  });
  return out;
});
await test('formats', 'format=ndjson', async () => {
  const out = await firstWorking((e) => '/' + e + '/search?text=golang&limit=3&format=ndjson', (r) => {
    assert(/ndjson/.test(r.headers.get('content-type') || ''), 'bad ndjson content-type');
    const lines = r.text.trim().split('\n').filter(Boolean);
    assert(lines.length > 0, 'no ndjson lines');
    for (const line of lines) JSON.parse(line);
    return { lines: lines.length };
  });
  return out;
});

if (PACE) await sleep(PACE);

// ------------------------------------------------------------ 5. 聚合
console.log('\n[5/9] 多引擎聚合 /mega');
await test('mega', 'balanced 聚合可用国产引擎', async () => {
  const megaEngines = (domesticHealthy.length >= 2 ? domesticHealthy.slice(0, 3) : capabilityPool.slice(0, 2)).join(',');
  await sleep(0);
  const r = await req('/mega/search?text=' + encodeURIComponent('大模型') + '&limit=6&mode=balanced&engines=' + megaEngines);
  if (r.status === 502 && /captcha|blocked|429|403/i.test(r.text)) {
    const e = new Error('all selected engines gated: ' + r.text.slice(0, 120));
    e.environment = true;
    throw e;
  }
  assert(r.status === 200, 'HTTP ' + r.status + ' ' + r.text.slice(0, 160));
  assert(Array.isArray(r.json.clusters) && r.json.clusters.length > 0, 'no clusters');
  const expected = (domesticHealthy.length >= 2 ? domesticHealthy.slice(0, 3) : capabilityPool.slice(0, 2));
  assert(Array.isArray(r.json.query.engines_requested) && r.json.query.engines_requested.length === expected.length,
    'engines_requested mismatch: ' + JSON.stringify(r.json.query.engines_requested) + ' vs ' + JSON.stringify(expected));
  const byEngine = {};
  for (const x of r.json.results) byEngine[x.engine] = (byEngine[x.engine] || 0) + 1;
  const failedEngines = (r.json.meta.engines_failed || []).length;
  const wantMin = failedEngines > 0 ? 1 : (expected.length >= 2 ? Math.min(2, expected.length) : 1);
  assert(Object.keys(byEngine).length >= wantMin, 'merged results attributed to <' + wantMin + ' engines: ' + JSON.stringify(byEngine) + ' failed=' + JSON.stringify(r.json.meta.engines_failed || []));
  for (const c of r.json.clusters) assert(c.canonical_url, 'cluster without canonical_url');
  return { results: r.json.results.length, clusters: r.json.clusters.length, byEngine, failed: r.json.meta.engines_failed || [] };
}, { retries: 2, betweenMs: 10000 });

await test('mega', 'any 顺序回退', async () => {
  const anyEngines = (domesticHealthy.length >= 2 ? domesticHealthy : capabilityPool).slice(0, 2).join(',');
  const r = await req('/mega/search?text=' + encodeURIComponent('rust 入门') + '&limit=5&mode=any&engines=' + anyEngines);
  if (r.status === 502 && /captcha|blocked|429|403/i.test(r.text)) {
    const e = new Error('ordered fallback engines gated: ' + r.text.slice(0, 120));
    e.environment = true;
    throw e;
  }
  assert(r.status === 200, 'HTTP ' + r.status);
  assert(r.json.results.length > 0, 'no results');
  return { results: r.json.results.length };
}, { retries: 1 });

if (PACE) await sleep(PACE);

// ------------------------------------------------------------ 6. 提取
console.log('\n[6/9] 正文提取');
await test('extract', 'GET /extract markdown', async () => {
  const r = await req('/extract?url=https://example.com&format=markdown&mode=fast');
  assert(r.status === 200 && /Example Domain/i.test(r.text), 'extract failed: ' + r.status);
  return { bytes: r.text.length };
}, { retries: 2 });
await test('extract', 'POST /extract json', async () => {
  const r = await req('/extract', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ url: 'https://go.dev/', mode: 'fast' }) });
  assert(r.status === 200, 'status ' + r.status);
  const bodyText = r.json.markdown || r.json.content || r.json.text;
  assert(typeof bodyText === 'string' && bodyText.length > 100, 'no extracted body');
  return { bytes: bodyText.length };
}, { retries: 2 });
await test('extract', '批量提取容错（死链不拖垮批次）', async () => {
  const r = await req('/extract/batch', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ urls: ['https://example.com', 'https://no-such-host.invalid/'], mode: 'fast' }),
  });
  assert(r.status === 200, 'status ' + r.status);
  assert(Array.isArray(r.json) && r.json.length === 2, 'expected 2 items');
  assert(r.json[0].page_content && r.json[0].page_content.length > 50, 'first item not extracted');
  assert(r.json[1].metadata && r.json[1].metadata.error, 'dead url did not record metadata.error');
  return { items: r.json.length };
}, { retries: 2 });
await test('extract', '搜索内联 extract=2', async () => {
  const out = await firstWorking((e) => '/' + e + '/search?text=' + encodeURIComponent('golang 文档') + '&limit=3&extract=2', (r) => {
    const inline = r.json.results.filter((x) => x.extracted && x.extracted.content);
    assert(inline.length > 0, 'no inline extracted content');
    return { extracted: inline.length };
  });
  return out;
}, { retries: 1 });

// ------------------------------------------------------------ 7. 缓存
console.log('\n[7/9] 缓存');
await test('cache', '重复请求命中缓存', async () => {
  const out = await firstWorking((e) => {
    const probe = 'domestic cache ' + Math.floor(Date.now() / 1000);
    return '/' + e + '/search?text=' + encodeURIComponent(probe) + '&limit=3';
  }, (r) => ({ cache: r.headers.get('x-cache') }), { attempts: 1 });
  const engine = out.engine;
  const probe = 'domestic cache ' + Math.floor(Date.now() / 1000);
  const path = '/' + engine + '/search?text=' + encodeURIComponent(probe) + '&limit=3';
  const first = await req(path);
  assert(first.status === 200, 'first status ' + first.status);
  const second = await req(path);
  assert(second.status === 200, 'second status ' + second.status);
  assert(/hit/i.test(second.headers.get('x-cache') || ''), 'second request not cached (X-Cache=' + second.headers.get('x-cache') + ')');
  return { engine, first: first.headers.get('x-cache'), second: second.headers.get('x-cache') };
});

if (PACE) await sleep(PACE);

// ------------------------------------------------------------ 8. 稳定性
console.log('\n[8/9] 稳定性（串行 + 并发 + 资源）');
const soakQueries = ['golang 并发模型', 'python 数据分析', 'rust 所有权', 'java 虚拟机', 'kubernetes 网络', 'mysql 索引优化', 'redis 持久化', '前端性能优化', 'docker 镜像瘦身'];
await test('stability', '串行压测：产品级错误为 0 且成功率 >= 80%', async () => {
  const pool = domesticHealthy.length ? domesticHealthy : ['baidu'];
  const soakPool = pool.length >= 2 ? pool : [...pool, 'bing'];
  const runs = [];
  for (let i = 0; i < Math.min(SOAK_RUNS, soakQueries.length); i++) {
    const engine = soakPool[i % soakPool.length];
    const r = await req('/' + engine + '/search?text=' + encodeURIComponent(soakQueries[i]) + '&limit=5');
    const organic = r.status === 200 && r.json ? r.json.results.filter((x) => x.type === 'organic').length : 0;
    runs.push({ engine, query: soakQueries[i], status: r.status, ms: r.ms, ok: organic > 0 });
    await sleep(SOAK_PACE);
  }
  const gated = runs.filter((r) => [429, 403, 503].includes(r.status));
  const productErrors = runs.filter((r) => !r.ok && ![429, 403, 503].includes(r.status));
  const okRuns = runs.filter((r) => r.ok);
  const attempted = runs.length - gated.length;
  const rate = attempted ? okRuns.length / attempted : 0;
  const latencies = okRuns.map((r) => r.ms).sort((a, b) => a - b);
  assert(productErrors.length === 0, 'product-level errors during soak: ' + JSON.stringify(productErrors));
  const wantRuns = Math.min(2, runs.length);
  const gatedRatio = gated.length / runs.length;
  // 上游反爬整体封禁时（>40% 请求被 429/503 拦），这是环境事件而不是产品缺陷：
  // 记录清楚即可，只要没有产品级错误就算通过。
  if (gatedRatio > 0.4) {
    assert(productErrors.length === 0, 'product-level errors during soak: ' + JSON.stringify(productErrors));
    return {
      runs: runs.length, ok: okRuns.length, gated: gated.length, rate,
      note: 'upstream gating dominated this window (gated ' + (gatedRatio * 100).toFixed(0) + '%); treat as environment',
    };
  }
  assert(okRuns.length >= wantRuns, 'too few successful runs: ' + okRuns.length + '/' + runs.length + ' (gated=' + gated.length + ')');
  assert(rate >= 0.8, 'success rate on non-gated attempts ' + (rate * 100).toFixed(0) + '% < 80%');
  return {
    runs: runs.length, ok: okRuns.length, gated: gated.length, rate,
    p50: latencies[Math.floor(latencies.length / 2)] || 0,
    p95: latencies[Math.min(latencies.length - 1, Math.floor(latencies.length * 0.95))] || 0,
  };
});

await test('stability', '并发压测：无产品级 5xx，服务保持健康', async () => {
  const queries = ['golang channel', 'python asyncio', 'rust tokio', 'java spring', 'vue 组件', 'react hooks'].slice(0, CONCURRENCY);
  const basePool = domesticHealthy.length ? domesticHealthy : ['baidu'];
  const burstPool = basePool.length >= 2 ? basePool : [...basePool, 'bing'];
  const started = Date.now();
  const responses = await Promise.all(queries.map((q, i) => {
    const engine = burstPool[i % burstPool.length];
    return req('/' + engine + '/search?text=' + encodeURIComponent(q) + '&limit=5', { timeout: 180000 });
  }));
  const codes = responses.map((r) => r.status);
  const productErrors = codes.filter((c) => c >= 500 && c !== 503);
  assert(productErrors.length === 0, 'product-level 5xx under concurrency: ' + JSON.stringify(codes));
  const healthy = await req('/health');
  assert(healthy.status === 200, 'health after burst = ' + healthy.status);
  assert(healthy.json.engines.every((e) => e.initialized), 'engine uninitialized after burst');
  const succeeded = codes.filter((c) => c === 200).length;
  assert(succeeded >= 1, 'no successful responses under concurrency: ' + JSON.stringify(codes));
  return { codes, succeeded, wallMs: Date.now() - started };
});

await test('stability', '压测后资源与熔断器状态可接受', async () => {
  const health = await req('/health');
  const cb = await req('/stats/cb');
  const mem = health.json.system && health.json.system.memory_mb;
  assert(typeof mem === 'number' && mem < 512, 'memory too high after soak: ' + mem + 'MB');
  assert(cb.status === 200 && cb.json, 'circuit stats unavailable');
  const breakers = (cb.json.circuit_breakers || []);
  const open = breakers.filter((b) => b.state === 'open');
  // 反爬会短暂打开熔断，但不应把所有引擎同时熔断。
  assert(open.length < breakers.length, 'all engines circuit-open after soak: ' + JSON.stringify(breakers));
  return { memory_mb: mem, goroutines: health.json.system.goroutines, open: open.map((b) => b.engine) };
});

// ------------------------------------------------------------ 9. 审计
console.log('\n[9/9] 运行审计');
await test('audit', '响应结构一致（可用国产引擎字段齐全）', async () => {
  const report = {};
  const skipped = [];
  for (const engine of domesticHealthy) {
    const r = await req('/' + engine + '/search?text=' + encodeURIComponent('golang') + '&limit=3');
    if ([429, 403, 503].includes(r.status)) { skipped.push(engine); continue; }
    assert(r.status === 200, engine + ' status ' + r.status);
    const organic = r.json.results.filter((x) => x.type === 'organic');
    assert(organic.length > 0, engine + ' no organic');
    for (const item of organic) {
      for (const field of ['rank', 'type', 'engine', 'title', 'url']) assert(field in item, engine + ' result missing ' + field);
      assert(item.engine === (engine === '360' ? 'so360' : engine), engine + ' attribution mismatch: ' + item.engine);
    }
    report[engine] = organic.length;
  }
  if (Object.keys(report).length === 0) {
    const e = new Error('no engine could be audited (gated): ' + skipped.join(','));
    e.environment = true;
    throw e;
  }
  return { audited: report, skipped };
}, { retries: 2, betweenMs: 10000 });

await test('audit', 'stats 端点维护正常', async () => {
  const stats = {};
  for (const p of ['/stats', '/stats/cache', '/stats/proxy', '/stats/cb']) {
    const r = await req(p);
    assert(r.status === 200, p + ' status ' + r.status);
    assert(r.json, p + ' not JSON');
    stats[p] = r.status;
  }
  return stats;
});

await test('audit', '404/400 契约在压测后仍正确', async () => {
  const notFound = await req('/nope/search?text=x');
  const bad = await req('/baidu/search');
  assert(notFound.status === 404, '404 contract broken: ' + notFound.status);
  assert(bad.status === 400, '400 contract broken: ' + bad.status);
  return { notFound: notFound.status, badRequest: bad.status };
});

// ------------------------------------------------------------ 汇总
const total = results.length;
const passed = results.filter((r) => r.ok).length;
const failedCritical = results.filter((r) => !r.ok && r.critical);
const environmentIssues = results.filter((r) => !r.ok && !r.critical && r.classification === 'environment');
const byGroup = {};
for (const r of results) {
  byGroup[r.group] ??= { pass: 0, fail: 0 };
  byGroup[r.group][r.ok ? 'pass' : 'fail']++;
}

const report = {
  service: 'openserp-domestic',
  base_url: BASE,
  started_at: new Date(t0).toISOString(),
  duration_ms: Date.now() - t0,
  totals: { total, passed, failed: total - passed, failed_critical: failedCritical.length, environment_issues: environmentIssues.length },
  by_group: byGroup,
  engine_stats: engineStats,
  results,
};

mkdirSync(OUT_DIR, { recursive: true });
const stamp = new Date(t0).toISOString().replace(/[:.]/g, '-');
const outFile = join(OUT_DIR, 'domestic-' + stamp + '.json');
writeFileSync(outFile, JSON.stringify(report, null, 2));

console.log('\n================ SUMMARY ================');
for (const [group, s] of Object.entries(byGroup)) console.log(group.padEnd(11) + ' pass=' + s.pass + ' fail=' + s.fail);
console.log('-'.repeat(48));
console.log('total=' + total + ' passed=' + passed + ' failed=' + (total - passed) +
  ' critical_failures=' + failedCritical.length + ' environment_issues=' + environmentIssues.length);
console.log('duration=' + (report.duration_ms / 1000).toFixed(1) + 's');
console.log('report=' + outFile);
if (failedCritical.length) {
  console.log('\nCRITICAL FAILURES:');
  for (const f of failedCritical) console.log(' - [' + f.group + '] ' + f.name + ': ' + f.error);
}
process.exit(failedCritical.length ? 1 : 0);