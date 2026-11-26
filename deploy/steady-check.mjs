#!/usr/bin/env node
/**
 * 低节流稳态检查：按固定间隔（默认 30s）对每个国内引擎发少量请求，
 * 用于验证“单 IP + 合理节流”下的持续可用性，而不是突发压测。
 *
 * 引擎冷却感知：503 {"error":"engine_cooldown","reason":"COOLDOWN",
 * "retry_after_seconds":N}（引擎冷却状态机，见 config.yaml stability 节与
 * docs/API.md §8.1）会被单独标记为 cooldown(...) 而不是普通错误——冷却期内
 * 该引擎不发真实请求，观测值不代表引擎健康度。
 *
 *   node deploy/steady-check.mjs [baseUrl] [rounds] [intervalMs]
 *   WEB2API_API_KEY 用于开启数据面鉴权的实例。
 */
// 整个应用固定部署在 /web-tool 子路径之下(健康端点与搜索端点一致)。
const BASE = (process.env.WEB2API_BASE_URL || process.argv[2] || 'http://127.0.0.1:7000').replace(/\/+$/, '') + '/web-tool';
const target = new URL(BASE);
const apiKey = process.env.WEB2API_API_KEY;
if (apiKey && target.protocol !== 'https:' && !['localhost', '127.0.0.1', '[::1]'].includes(target.hostname)) {
  throw new Error('An API key requires HTTPS or loopback HTTP');
}
const headers = apiKey ? { 'X-API-Key': apiKey } : {};
const ROUNDS = Number(process.argv[3] || 3);
const INTERVAL = Number(process.argv[4] || 30000);
const engines = (process.env.STEADY_ENGINES || 'baidu,sogou,360,bing').split(',');
const queries = ['golang 并发模型', 'python 数据分析', '前端性能优化', '数据库索引', '容器编排', '网络安全'];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const runs = [];
for (let round = 0; round < ROUNDS; round++) {
  for (const engine of engines) {
    const text = queries[(round * engines.length + engines.indexOf(engine)) % queries.length];
    const started = Date.now();
    let status = 0, organic = 0, err = '', cooldown = false;
    try {
      const res = await fetch(BASE + '/' + engine + '/search?text=' + encodeURIComponent(text) + '&limit=5', {
        headers, redirect: 'manual', signal: AbortSignal.timeout(120_000),
      });
      status = res.status;
      const body = await res.text();
      try {
        const json = JSON.parse(body);
        organic = (json.results || []).filter((x) => x.type === 'organic').length;
        if (status !== 200) {
          if (status === 503 && json.error === 'engine_cooldown') {
            cooldown = true;
            err = `cooldown(retry_after=${json.retry_after_seconds ?? res.headers.get('retry-after')})`;
          } else {
            err = json.error || '';
          }
        }
      } catch { err = 'non-json'; }
    } catch (e) { err = 'network'; }
    runs.push({ engine, text, status, organic, ms: Date.now() - started, err, cooldown });
    console.log(new Date().toISOString().slice(11, 19) + '  ' + engine.padEnd(6) + ' status=' + status + ' organic=' + organic + ' ' + (Date.now() - started) + 'ms ' + err);
    await sleep(INTERVAL);
  }
}

const summary = {};
for (const e of engines) {
  const list = runs.filter((r) => r.engine === e);
  const ok = list.filter((r) => r.status === 200 && r.organic > 0).length;
  const cd = list.filter((r) => r.cooldown).length;
  summary[e] = { attempts: list.length, ok, cooldown: cd, rate: list.length ? +(ok / list.length).toFixed(2) : 0 };
}
console.log('\nsummary:', JSON.stringify(summary));
const totalOk = Object.values(summary).reduce((a, s) => a + s.ok, 0);
const total = runs.length;
console.log('overall: ' + totalOk + '/' + total);
process.exit(totalOk / total >= 0.8 ? 0 : 1);
