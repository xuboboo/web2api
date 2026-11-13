#!/usr/bin/env node
/**
 * OpenSERP end-to-end test suite.
 *
 *   node deploy/e2e.mjs [baseUrl]
 *   OPENSERP_BASE_URL=http://127.0.0.1:7000 node deploy/e2e.mjs
 *
 * Exit code 0 only when every *critical* test passes. Engine tests that depend
 * on anti-bot behaviour (Yandex, Ecosia) are marked non-critical: a captcha or
 * IP block from the deployment's egress is an environment condition, not a
 * product defect, and is reported separately.
 *
 * Writes a JSON report to deploy/e2e-results/<timestamp>.json.
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
  try {
    const res = await fetch(BASE + path, { method, headers, body, signal: ac.signal, redirect: 'manual' });
    const text = await res.text();
    let json;
    try { json = JSON.parse(text); } catch { json = undefined; }
    return { status: res.status, headers: res.headers, text, json, url: path };
  } finally {
    clearTimeout(timer);
  }
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
function assert(cond, msg) { if (!cond) throw new Error(msg); }

function checkSearchEnvelope(r, { engine, limit } = {}) {
  assert(r.status === 200, 'expected HTTP 200, got ' + r.status + ' ' + r.text.slice(0, 200));
  assert(r.json, 'response is not JSON');
  assert(r.json.query && typeof r.json.query.text === 'string', 'missing query.text');
  assert(Array.isArray(r.json.results), 'missing results array');
  assert(r.json.meta && typeof r.json.meta.took_ms === 'number', 'missing meta.took_ms');
  const organic = r.json.results.filter((x) => x.type === 'organic');
  assert(organic.length > 0, 'no organic results returned');
  for (const item of organic) {
    assert(typeof item.rank === 'number' && item.rank > 0, 'organic result without numeric rank');
    assert(typeof item.url === 'string' && /^https?:\/\//.test(item.url), 'organic result without absolute url');
    assert(typeof item.title === 'string' && item.title.length > 0, 'organic result without title');
    if (engine) assert(item.engine === engine, 'unexpected engine on result: ' + item.engine);
  }
  if (limit) assert(organic.length <= limit, 'limit not enforced: got ' + organic.length + ' organic results for limit ' + limit);
  return { organic: organic.length, total: r.json.results.length, took_ms: r.json.meta.took_ms, first: organic[0].url };
}

/**
 * Runs the same logical check against engines in order until one succeeds.
 * Search endpoints are anti-bot gated, so a single-transient 403/429 must not
 * fail a capability test; only an all-engine failure is a real defect.
 */
async function tryEngines(engines, pathFor, validate) {
  const errors = [];
  for (const engine of engines) {
    const r = await req(pathFor(engine));
    if (r.status !== 200) {
      errors.push(engine + ':http ' + r.status);
      continue;
    }
    try {
      return { engine, value: validate(r) };
    } catch (err) {
      errors.push(engine + ':' + err.message);
    }
  }
  throw new Error('all engines failed - ' + errors.join(' | '));
}

const looksEnvironmental = (msg) => /captcha|blocked|rate_limited|429|403|circuit|eof|timeout|failed to extract/i.test(String(msg));

async function test(group, name, fn, { critical = true, retries = 0 } = {}) {
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
      if (attempt < retries) await sleep(3000);
    }
  }
  const message = String((lastErr && lastErr.message) || lastErr);
  results.push({
    group, name, critical, ok: false, attempts: retries + 1, ms: Date.now() - started,
    error: message,
    classification: critical ? 'product' : (looksEnvironmental(message) ? 'environment' : 'product'),
  });
  console.log('  FAIL  ' + name + '  -> ' + message);
}

// ---------------------------------------------------------------- infra
console.log('\n[1/7] Service lifecycle & discovery');
await test('infra', 'GET /health reports every engine', async () => {
  const r = await req('/health');
  assert(r.status === 200, 'status ' + r.status);
  assert(r.json, 'not JSON');
  assert(['healthy', 'degraded'].includes(r.json.status), 'unexpected status field: ' + r.json.status);
  assert(Array.isArray(r.json.engines) && r.json.engines.length === 6, 'expected 6 engines, got ' + (r.json.engines || []).length);
  for (const e of r.json.engines) assert(e.initialized === true && typeof e.status === 'string', 'engine not initialized: ' + JSON.stringify(e));
  const ready = r.json.engines.filter((e) => e.status === 'ready').map((e) => e.name);
  const notReady = r.json.engines.filter((e) => e.status !== 'ready').map((e) => e.name + ':' + e.status);
  assert(ready.length >= 3, 'fewer than 3 engines ready: ' + ready.join(','));
  assert((r.json.status === 'healthy') === (notReady.length === 0),
    'health status ' + r.json.status + ' does not match engine state ' + JSON.stringify(notReady));
  return { status: r.json.status, uptime: r.json.uptime, ready, not_ready: notReady, go: r.json.system && r.json.system.go_version };
});

await test('infra', 'GET /ready returns 200', async () => {
  const r = await req('/ready');
  assert(r.status === 200, 'status ' + r.status);
  return { body: r.text.slice(0, 120) };
});

await test('infra', 'GET /mega/engines lists all engines', async () => {
  const r = await req('/mega/engines');
  assert(r.status === 200, 'status ' + r.status);
  const raw = JSON.stringify(r.json);
  for (const name of ['google', 'bing', 'yandex', 'baidu', 'duckduckgo', 'ecosia']) {
    assert(raw.includes(name), 'engine missing from /mega/engines: ' + name);
  }
  return r.json;
});

await test('infra', 'GET /openapi.yaml serves the spec', async () => {
  const r = await req('/openapi.yaml');
  assert(r.status === 200, 'status ' + r.status);
  assert(/openapi:\s*3/.test(r.text), 'no openapi version in body');
  return { bytes: r.text.length };
});

await test('infra', 'GET /docs serves Swagger UI', async () => {
  const r = await req('/docs');
  assert(r.status === 200, 'status ' + r.status);
  assert(/swagger/i.test(r.text), 'swagger UI markup not found');
  return { bytes: r.text.length };
});

await test('infra', 'GET /stats, /stats/cache, /stats/proxy, /stats/cb respond', async () => {
  const out = {};
  for (const p of ['/stats', '/stats/cache', '/stats/proxy', '/stats/cb']) {
    const r = await req(p);
    assert(r.status === 200, p + ' status ' + r.status);
    assert(r.json !== undefined, p + ' did not return JSON');
    out[p] = Object.keys(r.json).slice(0, 6);
  }
  return out;
});

await test('infra', 'CORS preflight is answered', async () => {
  const r = await req('/google/search', {
    method: 'OPTIONS',
    headers: { Origin: 'https://example.com', 'Access-Control-Request-Method': 'GET', 'Access-Control-Request-Headers': 'content-type' },
  });
  assert(r.status === 200 || r.status === 204, 'status ' + r.status);
  const allow = r.headers.get('access-control-allow-origin');
  assert(allow, 'missing access-control-allow-origin');
  return { status: r.status, allow };
});

await test('infra', 'X-Request-ID is echoed back', async () => {
  const r = await req('/health', { headers: { 'X-Request-ID': 'e2e-probe-1234' } });
  const id = r.headers.get('x-request-id');
  assert(id === 'e2e-probe-1234', 'expected echoed request id, got ' + id);
  return { id };
});

// ------------------------------------------------------- validation/errors
console.log('\n[2/7] Request validation & error contract');
await test('errors', 'missing text query -> 400', async () => {
  const r = await req('/google/search');
  assert(r.status === 400, 'status ' + r.status);
  assert(r.json && r.json.error, 'no error payload');
  return r.json;
});

await test('errors', 'unknown route -> 404', async () => {
  const r = await req('/nope/search?text=x');
  assert(r.status === 404, 'status ' + r.status);
  return { status: r.status };
});

await test('errors', 'unknown mega engine -> 400', async () => {
  const r = await req('/mega/search?text=golang&engines=nosuchengine');
  assert(r.status === 400, 'status ' + r.status);
  assert(r.json && r.json.error, 'no error payload');
  return r.json;
});

await test('errors', 'limit out of range -> 400', async () => {
  const r = await req('/bing/search?text=golang&limit=500');
  assert(r.status === 400, 'status ' + r.status);
  return { status: r.status, body: (r.json && r.json.error) || r.text.slice(0, 80) };
});

// --------------------------------------------------------- engine search
console.log('\n[3/7] Dedicated engine search endpoints');
// Every SERP is anti-bot gated upstream: a 403/429 from the egress IP is an
// environment condition. Individual engine checks are therefore non-critical;
// the aggregate coverage test below is what must pass.
// Rotate the probe query per run so an HTTP-cached SERP from an earlier run
// cannot mask a live engine outage.
const queryRotation = [
  'golang programming language',
  'rust async runtime',
  'postgres vacuum tuning',
  'kubernetes ingress controller',
  'python typing protocols',
  'sqlite wal mode',
];
const probeText = queryRotation[Math.floor(Date.now() / 60000) % queryRotation.length];
const enginePlan = [
  { engine: 'google', text: probeText, retries: 1 },
  { engine: 'bing', text: probeText, retries: 0 },
  { engine: 'duckduckgo', text: probeText, retries: 0 },
  { engine: 'baidu', text: 'go语言', retries: 0 },
  { engine: 'yandex', text: probeText, retries: 1 },
  { engine: 'ecosia', text: probeText, retries: 1 },
];
const engineState = {};
for (const plan of enginePlan) {
  await test('engines', 'GET /' + plan.engine + '/search?limit=5', async () => {
    const r = await req('/' + plan.engine + '/search?text=' + encodeURIComponent(plan.text) + '&limit=5&lang=EN&region=US');
    const info = checkSearchEnvelope(r, { engine: plan.engine, limit: 5 });
    engineState[plan.engine] = { ok: true, organic: info.organic, took_ms: info.took_ms };
    return info;
  }, { critical: false, retries: plan.retries });
}

await test('engines', 'engine coverage: >=3 engines return organic results', async () => {
  const healthy = Object.entries(engineState).filter(([, v]) => v.ok).map(([k]) => k);
  const blocked = results.filter((r) => r.group === 'engines' && !r.ok).map((r) => ({ name: r.name, classification: r.classification || 'environment' }));
  const productFailures = blocked.filter((b) => b.classification === 'product');
  assert(healthy.length >= 3, 'only ' + healthy.length + ' healthy engine(s): ' + healthy.join(','));
  assert(productFailures.length === 0, 'non-environment engine failures: ' + productFailures.map((b) => b.name).join(', '));
  return { healthy, blocked: blocked.map((b) => b.name + ' (' + b.classification + ')') };
});

// -------------------------------------------------------------- formats
console.log('\n[4/7] Output formats');
const fmtEngine = engineState.google && engineState.google.ok ? 'google' : 'bing';
const fallbackEngines = [fmtEngine, ...['google', 'bing', 'duckduckgo', 'baidu'].filter((e) => e !== fmtEngine)];
await test('formats', 'format=markdown returns markdown document', async () => {
  const out = await tryEngines(fallbackEngines, (e) => '/' + e + '/search?text=golang&limit=3&format=markdown', (r) => {
    assert(/text\/markdown/.test(r.headers.get('content-type') || ''), 'content-type: ' + r.headers.get('content-type'));
    assert(/#+\s*Results/i.test(r.text), 'no markdown results section');
    return { bytes: r.text.length };
  });
  return { engine: out.engine, ...out.value };
});

await test('formats', 'format=text returns plain text', async () => {
  const out = await tryEngines(fallbackEngines, (e) => '/' + e + '/search?text=golang&limit=3&format=text', (r) => {
    assert(/text\/plain/.test(r.headers.get('content-type') || ''), 'content-type: ' + r.headers.get('content-type'));
    assert(r.text.length > 50, 'body too short');
    return { bytes: r.text.length };
  });
  return { engine: out.engine, ...out.value };
});

await test('formats', 'format=ndjson emits one JSON object per line', async () => {
  const out = await tryEngines(fallbackEngines, (e) => '/' + e + '/search?text=golang&limit=3&format=ndjson', (r) => {
    assert(/ndjson/.test(r.headers.get('content-type') || ''), 'content-type: ' + r.headers.get('content-type'));
    const lines = r.text.trim().split('\n').filter(Boolean);
    assert(lines.length > 0, 'no ndjson lines');
    for (const line of lines) JSON.parse(line);
    return { lines: lines.length };
  });
  return { engine: out.engine, ...out.value };
});

// --------------------------------------------------------- image search
console.log('\n[5/7] Image search');
await test('images', 'GET /bing/image?limit=5', async () => {
  const r = await req('/bing/image?text=golang+logo&limit=5');
  assert(r.status === 200, 'status ' + r.status + ' ' + r.text.slice(0, 160));
  assert(r.json && Array.isArray(r.json.results), 'no results array');
  const imgs = r.json.results.filter((x) => x.type === 'image' || x.image || x.thumbnail || x.thumb);
  assert(imgs.length > 0, 'no image results (types: ' + r.json.results.map((x) => x.type).join(',') + ')');
  return { count: imgs.length, sample: JSON.stringify(imgs[0]).slice(0, 200) };
});

// ----------------------------------------------------------- aggregation
console.log('\n[6/7] Mega aggregation & extraction');
await test('mega', 'GET /mega/search mode=balanced returns clusters', async () => {
  const r = await req('/mega/search?text=golang&limit=8&mode=balanced&engines=google,bing,baidu');
  assert(r.status === 200, 'status ' + r.status + ' ' + r.text.slice(0, 200));
  assert(r.json && Array.isArray(r.json.results), 'no results array');
  assert(Array.isArray(r.json.clusters), 'no clusters array');
  assert(r.json.query && Array.isArray(r.json.query.engines_requested) && r.json.query.engines_requested.length === 3,
    'query.engines_requested missing/short: ' + JSON.stringify(r.json.query));
  const used = new Set(r.json.results.map((x) => x.engine));
  assert(used.size >= 1, 'no engine attribution on merged results');
  for (const c of r.json.clusters) {
    assert(Array.isArray(c.occurrences) && c.occurrences.length > 0, 'cluster without occurrences');
    assert(typeof c.canonical_url === 'string', 'cluster without canonical_url');
  }
  return { clusters: r.json.clusters.length, results: r.json.results.length, engines: [...used], failed: r.json.meta.engines_failed || [] };
});

await test('mega', 'GET /mega/search mode=any ordered fallback', async () => {
  const r = await req('/mega/search?text=golang&limit=5&mode=any&engines=bing,google');
  assert(r.status === 200, 'status ' + r.status);
  assert(r.json.results.length > 0, 'no results');
  return { results: r.json.results.length };
});

await test('mega', 'GET /mega/search mode=fast', async () => {
  const r = await req('/mega/search?text=golang&limit=5&mode=fast&engines=google,bing');
  assert(r.status === 200, 'status ' + r.status);
  assert(r.json.results.length > 0, 'no results');
  return { results: r.json.results.length, took_ms: r.json.meta && r.json.meta.took_ms };
});

await test('extract', 'GET /extract?url=...&format=markdown', async () => {
  const r = await req('/extract?url=https://example.com&format=markdown&mode=fast');
  assert(r.status === 200, 'status ' + r.status + ' ' + r.text.slice(0, 200));
  assert(/Example Domain/i.test(r.text), 'example.com content not extracted');
  return { bytes: r.text.length, head: r.text.slice(0, 120) };
}, { retries: 1 });

await test('extract', 'POST /extract (json body)', async () => {
  const r = await req('/extract', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url: 'https://go.dev/', mode: 'fast' }),
  });
  assert(r.status === 200, 'status ' + r.status + ' ' + r.text.slice(0, 200));
  assert(r.json, 'not JSON');
  const body = r.json.markdown || r.json.content || r.json.text;
  assert(typeof body === 'string' && body.length > 100, 'no extracted body (keys: ' + Object.keys(r.json).join(',') + ')');
  assert(typeof r.json.url === 'string', 'missing url');
  return { bytes: body.length, keys: Object.keys(r.json) };
}, { retries: 1 });

await test('extract', 'POST /extract/batch handles a dead URL without failing the batch', async () => {
  const r = await req('/extract/batch', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ urls: ['https://example.com', 'https://this-host-does-not-exist.invalid/'], mode: 'fast' }),
  });
  assert(r.status === 200, 'status ' + r.status);
  assert(Array.isArray(r.json), 'expected bare array response');
  assert(r.json.length === 2, 'expected 2 items, got ' + r.json.length);
  assert(r.json[0].page_content && r.json[0].page_content.length > 50, 'first item not extracted');
  const failed = r.json[1].metadata && r.json[1].metadata.error;
  assert(failed, 'dead URL did not surface metadata.error');
  return { items: r.json.length, firstBytes: r.json[0].page_content.length, error: String(failed).slice(0, 80) };
}, { retries: 1 });

await test('extract', 'search with extract=2 inlines page markdown', async () => {
  const q = encodeURIComponent('golang documentation');
  const out = await tryEngines(fallbackEngines, (e) => '/' + e + '/search?text=' + q + '&limit=3&extract=2', (r) => {
    const withContent = r.json.results.filter((x) => x.extracted && typeof x.extracted.content === 'string' && x.extracted.content.length > 0);
    assert(withContent.length > 0, 'no inline extracted content');
    return { extracted: withContent.length, bytes: withContent[0].extracted.content.length };
  });
  return { engine: out.engine, ...out.value };
}, { retries: 1 });

// -------------------------------------------------------------- caching
console.log('\n[7/7] Cache behaviour');
await test('cache', 'repeated request served from cache (X-Cache)', async () => {
  const probe = 'cache probe ' + Math.floor(Date.now() / 1000);
  const errors = [];
  for (const engine of ['bing', 'google', 'duckduckgo']) {
    const path = '/' + engine + '/search?text=' + encodeURIComponent(probe) + '&limit=3';
    const first = await req(path);
    if (first.status !== 200) { errors.push(engine + ':http ' + first.status); continue; }
    const second = await req(path);
    assert(second.status === 200, 'second status ' + second.status);
    const c1 = first.headers.get('x-cache');
    const c2 = second.headers.get('x-cache');
    assert(c2 && /hit/i.test(c2), 'second request not served from cache (X-Cache=' + c2 + ')');
    return { engine, first: c1, second: c2, probe };
  }
  throw new Error('no engine answered for the cache probe - ' + errors.join(' | '));
});

// -------------------------------------------------------------- summary
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
  service: 'openserp',
  base_url: BASE,
  started_at: new Date(t0).toISOString(),
  duration_ms: Date.now() - t0,
  totals: { total, passed, failed: total - passed, failed_critical: failedCritical.length, environment_issues: environmentIssues.length },
  by_group: byGroup,
  results,
};

mkdirSync(OUT_DIR, { recursive: true });
const stamp = new Date(t0).toISOString().replace(/[:.]/g, '-');
const outFile = join(OUT_DIR, 'e2e-' + stamp + '.json');
writeFileSync(outFile, JSON.stringify(report, null, 2));

console.log('\n================ SUMMARY ================');
for (const [group, s] of Object.entries(byGroup)) {
  console.log(group.padEnd(10) + ' pass=' + s.pass + ' fail=' + s.fail);
}
console.log('-'.repeat(46));
console.log('total=' + total + ' passed=' + passed + ' failed=' + (total - passed) +
  ' critical_failures=' + failedCritical.length + ' environment_issues=' + environmentIssues.length);
console.log('duration=' + (report.duration_ms / 1000).toFixed(1) + 's');
console.log('report=' + outFile);
if (failedCritical.length) {
  console.log('\nCRITICAL (product) FAILURES:');
  for (const f of failedCritical) console.log(' - [' + f.group + '] ' + f.name + ': ' + f.error);
}
if (environmentIssues.length) {
  console.log('\nENVIRONMENT-DEPENDENT (non-critical) FAILURES:');
  for (const f of environmentIssues) console.log(' - [' + f.group + '] ' + f.name + ': ' + String(f.error).slice(0, 160));
}
process.exit(failedCritical.length ? 1 : 0);
