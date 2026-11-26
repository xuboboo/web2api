#!/usr/bin/env node
/**
 * Web2API 收录查询平台 E2E 冒烟套件(零依赖,Node 18+ 内置 fetch)。
 *
 * 覆盖固定 /web-tool 子路径下的契约面:
 *   - 前缀与根路径:根路径无任何路由(404),/web-tool/* 才是全部端点
 *   - 健康端点:/web-tool/health /ready /stats
 *   - 静态挂载:官网 /web-tool/ 返回 index.html;管理控制台 /web-tool/console/
 *     返回 index.html(no-slash 301 → /console/),各自 assets 静态资源可达
 *   - 管理 API:登录(成功/失败)、/api/me、/api/keys 生命周期、统计端点
 *   - 公网 API:401/429/400 错误信封、/v1/quota、/v1/engines、/v1/index/check
 *   - Agent 平面:MCP initialize→initialized→tools/list→tools/call 握手链、
 *     /web-tool/openapi.yaml 与 /web-tool/llms.txt
 *   - 稳定性:cooldown force→503 engine_cooldown(+Retry-After)→reset 恢复、
 *     /api/playground/check
 *   - 安全:登录连续失败锁定(429 too_many_attempts+Retry-After)、quota_exceeded+Retry-After
 *
 *   node deploy/e2e.mjs [baseUrl]
 *   WEB2API_BASE_URL=http://127.0.0.1:7000 node deploy/e2e.mjs
 *
 * 可调环境变量:
 *   WEB2API_BASE_URL / 第 1 个参数   目标地址(不含 /web-tool 前缀)
 *   WEB2API_ADMIN_USER / WEB2API_ADMIN_PASSWORD  管理员凭据(默认 admin/admin123)
 *   E2E_ISOLATED=1                   在独立测试实例运行完整契约（创建密钥、冷却和锁定）
 *   E2E_LIVE_INDEX_CHECK=1           启用依赖真实引擎的检查（仅独立实例）
 *   E2E_SKIP_LOCKOUT=1               跳过登录锁定测试(见下)
 *
 * 默认只检查公开页面和响应契约，不登录或写入状态。
 * 独立实例的登录锁定用例会在套件末尾连续输错密码，触发 15 分钟锁定
 *   (security.login_lockout 默认值),套件跑完后该用户名(跨 IP)在锁定窗口内无法
 *   登录控制台。CI/无人值守环境可放心;人工环境可设 E2E_SKIP_LOCKOUT=1 跳过。
 * 退出码:存在关键失败时为 1,可直接接入 CI。
 */
import { mkdirSync, writeFileSync } from 'node:fs';

const BASE = (process.env.WEB2API_BASE_URL || process.argv[2] || 'http://127.0.0.1:7000').replace(/\/+$/, '');
const ROOT = BASE + '/web-tool';
const ADMIN_USER = process.env.WEB2API_ADMIN_USER || 'admin';
const ADMIN_PASSWORD = process.env.WEB2API_ADMIN_PASSWORD || 'admin123';
const ISOLATED = process.env.E2E_ISOLATED === '1';
const LIVE = process.env.E2E_LIVE_INDEX_CHECK === '1';
const target = new URL(BASE);
if (!['http:', 'https:'].includes(target.protocol) || target.username || target.password || target.pathname !== '/' || target.search || target.hash) {
  throw new Error('Expected an HTTP(S) origin without credentials, path, query or fragment');
}
if (ISOLATED && target.protocol === 'http:' && !['localhost', '127.0.0.1', '[::1]'].includes(target.hostname)) {
  throw new Error('Isolated E2E requires HTTPS or loopback HTTP');
}
if (LIVE && !ISOLATED) throw new Error('E2E_LIVE_INDEX_CHECK requires E2E_ISOLATED=1');

const results = [];
const temporaryKeys = new Set();
let token = '';
let critical = 0;

async function req(path, { method = 'GET', headers = {}, body } = {}) {
  return request(ROOT + path, { method, headers, body });
}

async function request(url, { method = 'GET', headers = {}, body } = {}) {
  if (body !== undefined) headers = { 'Content-Type': 'application/json', ...headers };
  const res = await fetch(url, {
    method,
    headers,
    body: body === undefined ? undefined : (typeof body === 'string' ? body : JSON.stringify(body)),
    redirect: 'manual',
    signal: AbortSignal.timeout(120_000),
  });
  let json = null, text = '';
  try { text = await res.text(); json = JSON.parse(text); } catch { /* html or empty */ }
  return { status: res.status, headers: res.headers, text, json };
}

function test(group, name, fn) {
  return (async () => {
    const started = Date.now();
    try {
      const detail = await fn();
      results.push({ group, name, ok: true, ms: Date.now() - started, detail });
      console.log(`  ok   [${group}] ${name}`);
    } catch (e) {
      critical++;
      results.push({ group, name, ok: false, ms: Date.now() - started, error: String(e && e.message || e) });
      console.log(`  FAIL [${group}] ${name}: ${e && e.message || e}`);
    }
  })();
}

function assert(cond, msg) { if (!cond) throw new Error(msg); }

async function createKey(name, daily_quota = 0) {
  assert(token, '前置失败:admin 登录未取得 token');
  const res = await req('/api/keys', {
    method: 'POST', headers: { Authorization: `Bearer ${token}` }, body: { name, daily_quota },
  });
  assert(res.status === 200 && res.json?.id, `create ${name}=${res.status}`);
  temporaryKeys.add(res.json.id);
  assert(/^sk-w2a-[0-9a-f]{32}$/.test(res.json.key ?? ''), 'unexpected key format');
  return res.json;
}

async function deleteKey(id) {
  const res = await req(`/api/keys/${id}`, { method: 'DELETE', headers: { Authorization: `Bearer ${token}` } });
  assert(res.status === 200 && res.json?.ok === true, `delete key=${res.status}`);
  temporaryKeys.delete(id);
}

console.log(`Web2API E2E smoke → ${ROOT}\n`);

await test('infra', '健康、就绪、官网和独立文档响应契约', async () => {
  const health = await req('/health');
  assert(health.status === 200 && health.json?.status === 'healthy', `health=${health.status} ${health.headers.get('content-type')}`);
  const ready = await req('/ready');
  assert(ready.status === 200 && ready.json?.status === 'ready', `ready=${ready.status} ${ready.headers.get('content-type')}`);
  const site = await req('/');
  assert(site.status === 200 && site.text.includes('/web-tool/console/docs.html'), '官网缺少 API 文档入口');
  const docs = await req('/console/docs.html');
  assert(docs.status === 200 && docs.text.includes('API 文档'), `API docs=${docs.status}`);
});
await test('agent', '公开规范可下载，数据端点要求鉴权', async () => {
  const spec = await req('/openapi.yaml?raw=1');
  assert(spec.status === 200 && /^openapi:\s*["']?3\.1/m.test(spec.text), `OpenAPI status=${spec.status}`);
  const llms = await req('/llms.txt');
  assert(llms.status === 200 && llms.text.includes('/web-tool/mcp'), `llms.txt status=${llms.status}`);
  const quota = await req('/v1/quota');
  assert(quota.status === 401 && quota.json?.error === 'unauthorized', `quota=${quota.status}`);
});

if (!ISOLATED || critical !== 0) finish();
// ---------------------------------------------------------------------------
// infra:前缀挂载与根路径清空
// ---------------------------------------------------------------------------
await test('infra', '根路径无任何路由(404)', async () => {
  const r = await request(BASE + '/');
  assert(r.status === 404, `root / status=${r.status}, want 404`);
  const h = await request(BASE + '/health');
  assert(h.status === 404, `root /health status=${h.status}, want 404(根路径不留副本)`);
  const m = await request(BASE + '/mcp');
  assert(m.status === 404, `root /mcp status=${m.status}, want 404(MCP 只在 /web-tool/mcp)`);
});

await test('infra', '/web-tool/health 返回引擎清单', async () => {
  const r = await req('/health');
  assert(r.status === 200, `status=${r.status}`);
  assert(r.json && r.json.status === 'healthy', 'body.status=healthy');
  const names = (r.json.engines || []).map((e) => e.name);
  for (const n of ['baidu', 'sogou', 'so360', 'bing']) assert(names.includes(n), `engine ${n} missing`);
});

await test('infra', '/web-tool/stats 要求数据面鉴权', async () => {
  const stats = await req('/stats');
  assert(stats.status === 401 && stats.json?.error === 'unauthorized', `stats=${stats.status}`);
});

await test('infra', '官网 /web-tool/ 与控制台 /web-tool/console/ 静态挂载', async () => {
  const r = await req('/');
  const html = r.text;
  assert(r.status === 200, `site status=${r.status}`);
  assert(html.includes('Web2API'), 'site html missing product name');
  const asset = await req('/assets/app.js');
  assert(asset.status === 200, `assets/app.js status=${asset.status}`);
  // 管理控制台挂在 /web-tool/console/(no-slash 旧书签 301 → /console/)
  const redir = await req('/console');
  assert(redir.status === 301, `console no-slash status=${redir.status}, want 301`);
  const panel = await req('/console/');
  const panelHtml = panel.text;
  assert(panel.status === 200, `console status=${panel.status}`);
  assert(panelHtml.includes('管理控制台'), 'console html missing marker');
  const panelAsset = await req('/console/assets/app.js');
  assert(panelAsset.status === 200, `console/assets/app.js status=${panelAsset.status}`);
});

// ---------------------------------------------------------------------------
// admin:登录与 JWT
// ---------------------------------------------------------------------------
await test('admin', '错误凭据登录 → 401 unauthorized', async () => {
  const r = await req('/api/auth/login', { method: 'POST', body: { username: ADMIN_USER, password: 'wrong' } });
  assert(r.status === 401, `status=${r.status}`);
  assert(r.json && r.json.error === 'unauthorized', 'body error envelope');
});

await test('admin', '正确凭据登录 → token + expires_in=86400', async () => {
  const r = await req('/api/auth/login', { method: 'POST', body: { username: ADMIN_USER, password: ADMIN_PASSWORD } });
  assert(r.status === 200, `status=${r.status}`);
  assert(r.json && r.json.token && r.json.expires_in === 86400 && r.json.user && r.json.user.username === ADMIN_USER,
    `payload keys=${Object.keys(r.json || {})}`);
  token = r.json.token;
});

await test('admin', '无 token 访问 /api/me → 401', async () => {
  const r = await req('/api/me');
  assert(r.status === 401, `status=${r.status}`);
});

await test('admin', '带 token 的 /api/me 与统计端点', async () => {
  const auth = { Authorization: `Bearer ${token}` };
  const me = await req('/api/me', { headers: auth });
  assert(me.status === 200 && me.json.username === ADMIN_USER, `me=${me.status}`);
  for (const f of ['keys_count', 'today_calls', 'total_calls']) assert(f in me.json, `me.${f} missing`);
  for (const p of ['/api/stats/overview', '/api/stats/timeseries?days=14', '/api/usage?limit=50&offset=0', '/api/engines']) {
    const r = await req(p, { headers: auth });
    assert(r.status === 200, `${p}=${r.status}`);
  }
});

// ---------------------------------------------------------------------------
// keys:生命周期与禁用语义
// ---------------------------------------------------------------------------
let keyFull = '', keyID = '';
await test('keys', '创建密钥 → sk-w2a-<32hex>,完整 key 仅此一次', async () => {
  const j = await createKey('e2e-smoke');
  assert(j.status === 'active' && j.id && j.prefix, 'key view fields');
  keyFull = j.key; keyID = j.id;
});

await test('keys', '列表含新密钥(不含完整 key)', async () => {
  const r = await req('/api/keys', { headers: { Authorization: `Bearer ${token}` } });
  assert(r.status === 200, `status=${r.status}`);
  const row = (r.json || []).find((k) => k.id === keyID);
  assert(row, 'created key missing from list');
  assert(!JSON.stringify(r.json).includes(keyFull), 'full key leaked in list');
  for (const f of ['name', 'prefix', 'status', 'daily_quota', 'used_today', 'used_total', 'created_at', 'last_used_at'])
    assert(f in row, `list row.${f} missing`);
});

await test('keys', '有效密钥可读 /stats', async () => {
  const r = await req('/stats', { headers: { 'X-API-Key': keyFull } });
  assert(r.status === 200 && r.json?.cache && r.json?.circuit_breakers, `stats=${r.status}`);
});

await test('keys', 'PATCH 停用后该 key 一律 401', async () => {
  const patch = await req(`/api/keys/${keyID}`, { method: 'PATCH', headers: { Authorization: `Bearer ${token}` }, body: { status: 'disabled' } });
  assert(patch.status === 200 && patch.json.status === 'disabled', `patch=${patch.status}`);
  const blocked = await req('/v1/quota', { headers: { 'X-API-Key': keyFull } });
  assert(blocked.status === 401, `disabled key /v1/quota=${blocked.status}, want 401`);
});

await test('keys', '重新启用后可用,DELETE 后回到 401', async () => {
  const patch = await req(`/api/keys/${keyID}`, { method: 'PATCH', headers: { Authorization: `Bearer ${token}` }, body: { status: 'active' } });
  assert(patch.status === 200, `re-enable=${patch.status}`);
  const ok = await req('/v1/quota', { headers: { 'X-API-Key': keyFull } });
  assert(ok.status === 200, `re-enabled /v1/quota=${ok.status}`);
  await deleteKey(keyID);
  const gone = await req('/v1/quota', { headers: { 'X-API-Key': keyFull } });
  assert(gone.status === 401, `deleted key=${gone.status}, want 401`);
});

// ---------------------------------------------------------------------------
// public:错误信封与配额
// ---------------------------------------------------------------------------
if (LIVE) await test('public', '配额 key(daily_quota=1):第二次请求 → 429', async () => {
  const qk = await createKey('e2e-quota', 1);
  const first = await req('/v1/index/check?url=https%3A%2F%2Fe2e.example.com%2Fa&engines=baidu', { headers: { 'X-API-Key': qk.key } });
  assert(first.status === 200, `first check=${first.status}`);
  const second = await req('/v1/index/check?url=https%3A%2F%2Fe2e.example.com%2Fa&engines=baidu', { headers: { 'X-API-Key': qk.key } });
  assert(second.status === 429 && second.json.error === 'quota_exceeded', `second=${second.status} ${second.text}`);
  assert(/^\d+$/.test(second.headers.get('retry-after') ?? ''), 'quota 429 missing valid Retry-After');
  const quota = await req('/v1/quota', { headers: { 'X-API-Key': qk.key } });
  assert(quota.status === 200 && quota.json.used_today === 1 && quota.json.unlimited === false, `quota=${quota.text}`);
  await deleteKey(qk.id);
});

await test('public', '无 key / 未知 key → 401 unauthorized', async () => {
  const none = await req('/v1/index/check?url=https%3A%2F%2Fe2e.example.com%2Fb');
  assert(none.status === 401 && none.json.error === 'unauthorized', `none=${none.status}`);
  const bogus = await req('/v1/quota', { headers: { 'X-API-Key': 'sk-w2a-' + '0'.repeat(32) } });
  assert(bogus.status === 401, `bogus=${bogus.status}`);
});

await test('public', '400 信封:EMPTY_QUERY / TOO_MANY_URLS / INVALID_PARAM', async () => {
  const mk = await createKey('e2e-400');
  const key = { 'X-API-Key': mk.key };
  const empty = await req('/v1/index/check', { method: 'POST', headers: { ...key, 'Content-Type': 'application/json' }, body: '{"urls":[]}' });
  assert(empty.status === 400 && empty.json.reason === 'EMPTY_QUERY', `empty=${empty.status} ${empty.text}`);
  const many = await req('/v1/index/check', {
    method: 'POST', headers: { ...key, 'Content-Type': 'application/json' },
    body: JSON.stringify({ urls: Array.from({ length: 5001 }, (_, i) => `https://e2e.example.com/${i}`) }),
  });
  assert(many.status === 400 && many.json?.reason === 'TOO_MANY_URLS', `many=${many.status}, use the stock max_urls of 5000`);
  const engine = await req('/v1/index/check?url=https%3A%2F%2Fe2e.example.com%2Fc&engines=nosuch', { headers: key });
  assert(engine.status === 400 && engine.json.reason === 'INVALID_PARAM', `engine=${engine.status} ${engine.text}`);
  await deleteKey(mk.id);
});

await test('public', LIVE ? '/v1/engines 与别名 360' : '/v1/engines', async () => {
  const mk = await createKey('e2e-engines');
  const key = { 'X-API-Key': mk.key };
  const eng = await req('/v1/engines', { headers: key });
  assert(eng.status === 200 && eng.json.engines, `engines=${eng.status}`);
  const names = (Array.isArray(eng.json.engines) ? eng.json.engines : []).map((e) => e.engine || e.name);
  for (const n of ['baidu', 'sogou', 'so360', 'bing']) assert(names.includes(n), `engine ${n} missing`);
  if (LIVE) {
    const alias = await req('/v1/index/check?url=https%3A%2F%2Fe2e.example.com%2Fd&engines=360', { headers: key });
    assert(alias.status === 200, `alias=${alias.status}`);
    assert(alias.json?.meta?.units === 1, `alias units=${alias.json?.meta?.units}`);
    assert(alias.json?.results?.[0]?.engines?.so360, 'alias did not resolve to so360');
  }
  await deleteKey(mk.id);
});

if (LIVE) {
  await test('public', '真实收录判定(环境项:依赖引擎可用性)', async () => {
    const mk = await createKey('e2e-live');
    const r = await req('/v1/index/check?url=https%3A%2F%2Fwww.baidu.com%2F&engines=baidu,bing', { headers: { 'X-API-Key': mk.key } });
    assert(r.status === 200, `status=${r.status}`);
    for (const e of ['baidu', 'bing']) {
      const item = r.json.results[0].engines[e];
      assert(item && !item.error && typeof item.indexed === 'boolean', `${e}: ${item?.error?.code ?? 'missing indexed result'}`);
      console.log(`       ${e}: indexed=${item.indexed} match=${item.match}`);
    }
    await deleteKey(mk.id);
  });
}

// ---------------------------------------------------------------------------
// agent:MCP streamable HTTP 握手链与静态资源
// ---------------------------------------------------------------------------
const auth = { Authorization: `Bearer ${token}` };
let mcpKey = '';     // agent/稳定性测试共用的 API key
let mcpKeyID = '';
await test('agent', '准备:创建 e2e-agent API key', async () => {
  const mk = await createKey('e2e-agent');
  mcpKey = mk.key; mcpKeyID = mk.id;
});

await test('agent', 'MCP 未鉴权 → 401 unauthorized', async () => {
  const r = await req('/mcp', { method: 'POST', body: { jsonrpc: '2.0', id: 1, method: 'tools/list', params: {} } });
  assert(r.status === 401, `status=${r.status}, want 401`);
  assert(r.json && r.json.error === 'unauthorized', `body=${r.text.slice(0, 120)}`);
});

let mcpSession = '';
await test('agent', 'MCP initialize → protocolVersion 2025-06-18 + serverInfo + 会话头', async () => {
  const r = await req('/mcp', {
    method: 'POST',
    headers: { 'X-API-Key': mcpKey },
    body: { jsonrpc: '2.0', id: 1, method: 'initialize', params: { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'e2e', version: '0.0.1' } } },
  });
  assert(r.status === 200, `status=${r.status} ${r.text.slice(0, 160)}`);
  assert(r.json && r.json.result, `no result: ${r.text.slice(0, 160)}`);
  assert(r.json.result.protocolVersion === '2025-06-18', `protocolVersion=${r.json.result.protocolVersion}`);
  assert(r.json.result.serverInfo && r.json.result.serverInfo.name === 'web2api', `serverInfo=${JSON.stringify(r.json.result.serverInfo)}`);
  mcpSession = r.headers.get('mcp-session-id') || '';
  assert(mcpSession !== '', 'Mcp-Session-Id 响应头缺失');
});

await test('agent', 'MCP notifications/initialized → 204', async () => {
  const headers = { 'X-API-Key': mcpKey };
  if (mcpSession) headers['Mcp-Session-Id'] = mcpSession;
  const r = await req('/mcp', { method: 'POST', headers, body: { jsonrpc: '2.0', method: 'notifications/initialized' } });
  assert(r.status === 204, `status=${r.status}, want 204`);
});

await test('agent', 'MCP tools/list → 6 工具齐备且带 inputSchema', async () => {
  const headers = { 'X-API-Key': mcpKey };
  if (mcpSession) headers['Mcp-Session-Id'] = mcpSession;
  const r = await req('/mcp', { method: 'POST', headers, body: { jsonrpc: '2.0', id: 2, method: 'tools/list', params: {} } });
  assert(r.status === 200 && r.json && r.json.result && Array.isArray(r.json.result.tools), `status=${r.status} ${r.text.slice(0, 160)}`);
  const names = r.json.result.tools.map((t) => t.name);
  for (const n of ['web_search', 'check_indexed', 'get_index_job', 'cancel_index_job', 'get_quota', 'list_engines']) assert(names.includes(n), `tool ${n} missing`);
  for (const t of r.json.result.tools) assert(t.inputSchema && t.inputSchema.type === 'object', `tool ${t.name} inputSchema missing`);
});

await test('agent', 'MCP tools/call get_quota(免计费工具)→ result.content', async () => {
  const headers = { 'X-API-Key': mcpKey };
  if (mcpSession) headers['Mcp-Session-Id'] = mcpSession;
  const r = await req('/mcp', { method: 'POST', headers, body: { jsonrpc: '2.0', id: 3, method: 'tools/call', params: { name: 'get_quota', arguments: {} } } });
  assert(r.status === 200 && r.json && r.json.result, `status=${r.status} ${r.text.slice(0, 160)}`);
  assert(r.json.result.isError !== true, `isError=true: ${r.text.slice(0, 200)}`);
  assert(Array.isArray(r.json.result.content) && r.json.result.content.length > 0, 'result.content 为空');
});

await test('agent', '/web-tool/openapi.yaml 与 /web-tool/llms.txt → 200', async () => {
  const spec = await req('/openapi.yaml?raw=1');
  assert(spec.status === 200, `openapi.yaml status=${spec.status}`);
  const specText = spec.text;
  assert(/openapi:\s*["']?3\.1/.test(specText), 'openapi.yaml 缺少 openapi: 3.1 声明');
  const llms = await req('/llms.txt');
  assert(llms.status === 200, `llms.txt status=${llms.status}`);
  const llmsText = llms.text;
  assert(llmsText.includes('/web-tool/mcp') || llmsText.includes('/web-tool'), 'llms.txt 未引用 /web-tool 端点');
});

// ---------------------------------------------------------------------------
// stability:冷却状态机 force→503→reset 与 playground
// ---------------------------------------------------------------------------
await test('stability', 'cooldown force → /api/engines 显示 cooldown + until', async () => {
  const r = await req('/api/engines/sogou/cooldown/force', { method: 'POST', headers: auth, body: { minutes: 1 } });
  assert(r.status === 200 && r.json && r.json.ok === true, `force=${r.status} ${r.text.slice(0, 160)}`);
  const list = await req('/api/engines', { headers: auth });
  assert(list.status === 200 && Array.isArray(list.json.engines), `engines=${list.status}`);
  const sogou = list.json.engines.find((e) => e.engine === 'sogou');
  assert(sogou, 'sogou missing from /api/engines');
  assert(sogou.state === 'cooldown', `state=${sogou.state}`);
  assert(sogou.until && !Number.isNaN(Date.parse(sogou.until)), `until=${sogou.until}`);
  assert(sogou.reason === 'FORCED', `reason=${sogou.reason}(cooldown/force 强制冷却标记为 FORCED,自然冷却为 COOLDOWN)`);
  assert(Number.isInteger(sogou.consecutive_blocks) && sogou.consecutive_blocks >= 0, `consecutive_blocks=${sogou.consecutive_blocks}(强制冷却不计连续拦截)`);
});

await test('stability', '冷却引擎 search 快速失败:503 engine_cooldown + Retry-After(不发真实请求)', async () => {
  const r = await req('/sogou/search?text=e2e-cooldown-probe&limit=1', { headers: { 'X-API-Key': mcpKey } });
  assert(r.status === 503, `status=${r.status}, want 503`);
  assert(r.json && r.json.error === 'engine_cooldown' && r.json.reason === 'COOLDOWN', `body=${r.text.slice(0, 160)}`);
  assert(Number.isInteger(r.json.retry_after_seconds) && r.json.retry_after_seconds > 0, `retry_after_seconds=${r.json.retry_after_seconds}`);
  const retryAfter = Number(r.headers.get('retry-after'));
  assert(Number.isInteger(retryAfter) && retryAfter > 0, `Retry-After header=${r.headers.get('retry-after')}`);
});

await test('stability', '冷却引擎 /v1/index/check 快速失败:503 engine_cooldown', async () => {
  const r = await req('/v1/index/check?url=https%3A%2F%2Fe2e.example.com%2Fcd&engines=sogou', { headers: { 'X-API-Key': mcpKey } });
  assert(r.status === 503, `status=${r.status}, want 503`);
  assert(r.json && r.json.error === 'engine_cooldown' && r.json.reason === 'COOLDOWN', `body=${r.text.slice(0, 160)}`);
});

await test('stability', 'cooldown reset → /api/engines 恢复 ok', async () => {
  const r = await req('/api/engines/sogou/cooldown/reset', { method: 'POST', headers: auth });
  assert(r.status === 200 && r.json && r.json.ok === true, `reset=${r.status} ${r.text.slice(0, 160)}`);
  const list = await req('/api/engines', { headers: auth });
  const sogou = (list.json.engines || []).find((e) => e.engine === 'sogou');
  assert(sogou && sogou.state === 'ok', `state=${sogou && sogou.state}`);
});

if (LIVE) await test('stability', '/api/playground/check → 与 /v1/index/check 同构(不计费)', async () => {
  const r = await req('/api/playground/check', { method: 'POST', headers: { ...auth, 'Content-Type': 'application/json' }, body: { urls: ['https://e2e.example.com/pg'], engines: ['so360'] } });
  assert(r.status === 200, `status=${r.status} ${r.text.slice(0, 160)}`);
  assert(r.json && Array.isArray(r.json.results) && r.json.results.length === 1, `results=${r.text.slice(0, 160)}`);
  assert(r.json.results[0].url === 'https://e2e.example.com/pg', `row.url=${r.json.results[0].url}`);
  assert(r.json.results[0].engines && 'so360' in r.json.results[0].engines, 'row.engines.so360 missing');
  assert(r.json.meta && r.json.meta.units === 1, `meta.units=${r.json.meta && r.json.meta.units}`);
});

await test('agent', '清理:删除 e2e-agent key', async () => {
  await deleteKey(mcpKeyID);
  const gone = await req('/v1/quota', { headers: { 'X-API-Key': mcpKey } });
  assert(gone.status === 401, `deleted key quota=${gone.status}, want 401`);
});

for (const id of temporaryKeys) await test('keys', '清理失败用例遗留的临时密钥', () => deleteKey(id));

// ---------------------------------------------------------------------------
// security:登录连续失败锁定(最后执行——会锁定 admin@<IP> 一个锁定窗口)
// ---------------------------------------------------------------------------
if (process.env.E2E_SKIP_LOCKOUT === '1') {
  console.log('  skip [security] 登录锁定测试(E2E_SKIP_LOCKOUT=1)');
} else {
  await test('security', '连续 5 次错误凭据后下一次登录 → 429 too_many_attempts + Retry-After', async () => {
    let locked = false;
    for (let i = 1; i <= 8 && !locked; i++) {
      const r = await req('/api/auth/login', { method: 'POST', body: { username: ADMIN_USER, password: `wrong-${i}` } });
      if (r.status === 429) {
        locked = true;
        assert(r.json && r.json.error === 'too_many_attempts', `body=${r.text.slice(0, 160)}`);
        const retryAfter = Number(r.headers.get('retry-after'));
        assert(Number.isInteger(retryAfter) && retryAfter > 0, `Retry-After header=${r.headers.get('retry-after')}`);
      } else {
        assert(r.status === 401, `attempt ${i} status=${r.status}, want 401(未锁定期)`);
      }
    }
    assert(locked, `连续 8 次失败仍未锁定(fails 默认应为 5)`);
    const okAttempt = await req('/api/auth/login', { method: 'POST', body: { username: ADMIN_USER, password: ADMIN_PASSWORD } });
    assert(okAttempt.status === 429 && okAttempt.json.error === 'too_many_attempts',
      `锁定期内正确凭据登录=${okAttempt.status}, 契约要求一律 429`);
  });
}
// ---------------------------------------------------------------------------
// 汇总
// ---------------------------------------------------------------------------
finish();

function finish() {
  console.log(`\n${results.length - critical}/${results.length} passed`);
  const report = {
    target: ROOT,
    mode: ISOLATED ? (LIVE ? 'isolated-live' : 'isolated') : 'read-only',
    finished_at: new Date().toISOString(),
    passed: results.length - critical,
    total: results.length,
    critical_failures: critical,
    results,
  };
  try {
    mkdirSync(new URL('./e2e-results/', import.meta.url), { recursive: true });
    const out = new URL(`./e2e-results/platform-${Date.now()}.json`, import.meta.url);
    writeFileSync(out, JSON.stringify(report, null, 2));
    console.log(`report: ${decodeURIComponent(out.pathname)}`);
  } catch { /* report write is best-effort */ }

  process.exit(critical > 0 ? 1 : 0);
}
