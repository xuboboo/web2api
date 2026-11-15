/* ============================================================
 * Web2API 网站收录查询开放平台 · 管理控制台
 * 原生 ES2018 JavaScript,零依赖、零构建。
 *
 * 结构:
 *   1. 常量与内联图标(API_BASE 与 core.BasePath 对齐;PAGE_META 页头块)
 *   2. DOM 工具 / 格式化工具 / 骨架屏
 *   3. Toast / 剪贴板 / 按钮三态
 *   3b. 双主题(html.dark + localStorage w2a_theme)
 *   3c. 全局快捷键(/ 或 ⌘K 聚焦搜索,带输入守卫)
 *   4. 登录态与 fetch 统一封装(统一 /web-tool 前缀,401 自动回登录页)
 *   5. 弹窗 / 确认框
 *   6. Hash 路由与视图切换(页头块渲染进内容区;未知路由渲染 404 视图;页间过渡)
 *   7. 页面:总览 / 引擎状态 / API 密钥 / 使用日志
 *   8. 页面:在线调试 / Agent 集成 / API 文档 / 系统设置
 *   9. 启动
 * ============================================================ */
(function () {
  'use strict';

  /* ================= 1. 常量与内联图标 ================= */

  var LS_TOKEN = 'w2a_token';
  var LS_TOKEN_EXP = 'w2a_token_exp';
  var LS_USERNAME = 'w2a_username';
  var LS_THEME = 'w2a_theme'; // V2 新增:'light' | 'dark'(缺省 light)

  /* 所有管理 API 与公网 API 都挂在固定前缀 /web-tool 之下(与 core.BasePath
   * 常量一致;契约固定、不可配置)。api() 统一拼前缀,调用点保持 '/api/...'
   * 字面量,不再出现根绝对路径 fetch。 */
  var API_BASE = '/web-tool';

  /* 页头块(在内容区,不在顶栏):title = h1 衬线,desc = 副标(只写真实功能) */
  var PAGE_META = {
    '#/overview': { title: '总览', desc: '平台调用统计、近 14 日用量趋势与引擎健康一览。' },
    '#/engines': { title: '引擎状态', desc: '四大搜索引擎的运行、冷却与熔断状态,支持强制恢复与强制冷却。' },
    '#/keys': { title: 'API 密钥', desc: '公网 API 鉴权密钥的创建、配额与启停管理。' },
    '#/usage': { title: '使用日志', desc: '每次收录查询与搜索调用的明细记录,支持按密钥筛选。' },
    '#/playground': { title: '在线调试', desc: '发起一次真实收录判定,响应结构与 /v1/index/check 完全一致。' },
    '#/agents': { title: 'Agent 集成', desc: 'MCP 端点与 Claude / Dify / Coze / n8n / LangChain 的接入配置。' },
    '#/docs': { title: 'API 文档', desc: '全部 REST 端点、MCP 工具与错误码的完整说明。' },
    '#/settings': { title: '系统设置', desc: '管理员密码修改与平台安全机制说明。' }
  };
  var ROUTE_RE = /^#\/(overview|engines|keys|usage|playground|agents|docs|settings)$/;

  var ENGINE_LABEL = { baidu: '百度', sogou: '搜狗', so360: '360 搜索', bing: '必应' };
  var ENGINE_ORDER = ['baidu', 'sogou', 'so360', 'bing'];
  var BREAKER_LABEL = { closed: '熔断正常', open: '熔断中', 'half-open': '恢复中', halfopen: '恢复中', half_open: '恢复中' };

  var ICONS = {
    plus: '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"></line><line x1="5" y1="12" x2="19" y2="12"></line></svg>',
    copy: '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"></rect><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg>',
    refresh: '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="23 4 23 10 17 10"></polyline><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"></path></svg>',
    warn: '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"></path><line x1="12" y1="9" x2="12" y2="13"></line><line x1="12" y1="17" x2="12.01" y2="17"></line></svg>',
    external: '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"></path><polyline points="15 3 21 3 21 9"></polyline><line x1="10" y1="14" x2="21" y2="3"></line></svg>',
    toastOk: '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"></path><polyline points="22 4 12 14.01 9 11.01"></polyline></svg>',
    toastErr: '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="8" x2="12" y2="12"></line><line x1="12" y1="16" x2="12.01" y2="16"></line></svg>',
    toastInfo: '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="16" x2="12" y2="12"></line><line x1="12" y1="8" x2="12.01" y2="8"></line></svg>',
    statToday: '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"></circle><polyline points="12 6 12 12 16 14"></polyline></svg>',
    statTotal: '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="20" x2="18" y2="10"></line><line x1="12" y1="20" x2="12" y2="4"></line><line x1="6" y1="20" x2="6" y2="14"></line></svg>',
    statKey: '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0 3 3L22 7l-3-3m-3.5 3.5L19 4"></path></svg>',
    statRate: '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="23 6 13.5 15.5 8.5 10.5 1 18"></polyline><polyline points="17 6 23 6 23 12"></polyline></svg>',
    empty24: '<svg viewBox="0 0 24 24" width="32" height="32" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><polyline points="22 12 16 12 14 15 10 15 8 12 2 12"></polyline><path d="M5.45 5.11L2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"></path></svg>',
    alert24: '<svg viewBox="0 0 24 24" width="32" height="32" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="8" x2="12" y2="12"></line><line x1="12" y1="16" x2="12.01" y2="16"></line></svg>',
    cpu24: '<svg viewBox="0 0 24 24" width="32" height="32" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="16" height="16" rx="2"></rect><rect x="9" y="9" width="6" height="6" rx="1"></rect><line x1="9" y1="1" x2="9" y2="4"></line><line x1="15" y1="1" x2="15" y2="4"></line><line x1="9" y1="20" x2="9" y2="23"></line><line x1="15" y1="20" x2="15" y2="23"></line><line x1="20" y1="9" x2="23" y2="9"></line><line x1="20" y1="14" x2="23" y2="14"></line><line x1="1" y1="9" x2="4" y2="9"></line><line x1="1" y1="14" x2="4" y2="14"></line></svg>'
  };

  var state = {
    username: '',
    keysCache: [],
    usage: { limit: 50, offset: 0, keyId: '' }
  };
  var pendingHash = null;
  var pageTimers = [];

  /* ================= 2. DOM 工具 / 格式化工具 / 骨架屏 ================= */

  function $(sel, root) {
    return (root || document).querySelector(sel);
  }

  /* 轻量 DOM 构造器:el(tag, attrs, ...children) */
  function el(tag, attrs) {
    var node = document.createElement(tag);
    if (attrs) {
      Object.keys(attrs).forEach(function (k) {
        var v = attrs[k];
        if (v === null || v === undefined || v === false) return;
        if (k === 'class') node.className = String(v);
        else if (k === 'text') node.textContent = String(v);
        else if (k === 'html') node.innerHTML = String(v); // 仅用于受控的静态内联图标
        else if (k.length > 2 && k.slice(0, 2) === 'on' && typeof v === 'function') {
          node.addEventListener(k.slice(2).toLowerCase(), v);
        } else node.setAttribute(k, String(v));
      });
    }
    for (var i = 2; i < arguments.length; i++) {
      appendChild(node, arguments[i]);
    }
    return node;
  }

  function appendChild(node, c) {
    if (c === null || c === undefined || c === false) return;
    if (Array.isArray(c)) {
      for (var i = 0; i < c.length; i++) appendChild(node, c[i]);
      return;
    }
    if (typeof c === 'string') {
      node.appendChild(document.createTextNode(c));
      return;
    }
    node.appendChild(c);
  }

  function pad2(n) {
    n = Math.floor(n);
    return n < 10 ? '0' + n : String(n);
  }

  /* 日期时间格式化:RFC3339 / 时间戳 → "YYYY-MM-DD HH:mm:ss" */
  function fmtDateTime(v) {
    if (v === null || v === undefined || v === '') return '—';
    var d = parseWhen(v);
    if (!d) return String(v);
    return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate()) +
      ' ' + pad2(d.getHours()) + ':' + pad2(d.getMinutes()) + ':' + pad2(d.getSeconds());
  }

  /* 时间解析:秒/毫秒时间戳或 RFC3339 字符串 → Date,失败返回 null */
  function parseWhen(v) {
    if (v === null || v === undefined || v === '') return null;
    var d;
    if (typeof v === 'number') {
      d = new Date(v < 1e12 ? v * 1000 : v);
    } else {
      d = new Date(String(v));
    }
    return isNaN(d.getTime()) ? null : d;
  }

  /* 剩余时长 mm:ss(超 1 小时进位 hh:mm:ss) */
  function fmtRemaining(ms) {
    if (ms <= 0) return '00:00';
    var s = Math.ceil(ms / 1000);
    var h = Math.floor(s / 3600);
    s -= h * 3600;
    var m = Math.floor(s / 60);
    s -= m * 60;
    return (h > 0 ? pad2(h) + ':' : '') + pad2(m) + ':' + pad2(s);
  }

  /* 时长格式化:毫秒 → "820 ms" / "3.42 s" / "1 分 05 秒" */
  function fmtDuration(ms) {
    if (ms === null || ms === undefined || ms === '') return '—';
    var n = Number(ms);
    if (isNaN(n) || n < 0) return String(ms);
    if (n < 1000) return Math.round(n) + ' ms';
    if (n < 60000) {
      var s = n / 1000;
      return (n < 10000 ? s.toFixed(2) : s.toFixed(1)) + ' s';
    }
    var m = Math.floor(n / 60000);
    var rest = Math.round((n % 60000) / 1000);
    return m + ' 分 ' + pad2(rest) + ' 秒';
  }

  /* 千分位数字 */
  function fmtNumber(v) {
    if (v === null || v === undefined || v === '') return '—';
    var n = Number(v);
    if (isNaN(n)) return String(v);
    return n.toLocaleString('en-US');
  }

  /* 成功率:兼容 0..1 比例与 0..100 百分比两种口径 */
  function fmtPercent(v) {
    if (v === null || v === undefined || v === '') return '—';
    var n = Number(v);
    if (isNaN(n)) return String(v);
    var pct = n <= 1 ? n * 100 : n;
    if (pct < 0) pct = 0;
    if (pct > 100) pct = 100;
    return (Math.round(pct * 10) / 10) + '%';
  }

  /* 引擎字段展示:数组 → 中文名并列;数字 → 数量;其余原样 */
  function fmtEngines(v) {
    if (v === null || v === undefined || v === '') return '—';
    if (Array.isArray(v)) {
      if (!v.length) return '—';
      return v.map(function (e) {
        var k = String(e).toLowerCase();
        return ENGINE_LABEL[k] || String(e);
      }).join('、');
    }
    if (typeof v === 'number') return v + ' 个引擎';
    return String(v);
  }

  /* 页面级定时器:切换路由时统一清理,防止后台轮询泄漏 */
  function setPageInterval(fn, ms) {
    var id = setInterval(fn, ms);
    pageTimers.push(id);
    return id;
  }

  function clearPageTimers() {
    pageTimers.forEach(function (id) { clearInterval(id); });
    pageTimers = [];
  }

  /* 骨架屏:统一加载占位 */
  function skeletonBlock(lines) {
    var box = el('div', { class: 'skeleton' });
    (lines || [{ w: 'w40' }, { w: 'w80' }, { w: 'w60' }, { w: 'w80' }]).forEach(function (l) {
      box.appendChild(el('div', { class: 'sk-line ' + (l.big ? 'big ' : '') + (l.w || '') }));
    });
    return box;
  }

  function skeletonCards(n) {
    var grid = el('div', { class: 'engine-grid' });
    for (var i = 0; i < (n || 4); i++) {
      grid.appendChild(el('div', { class: 'engine-card' },
        skeletonBlock([{ w: 'w40' }, { big: true, w: 'w60' }, { w: 'w80' }])));
    }
    return grid;
  }

  /* ================= 3. Toast / 剪贴板 / 按钮三态 ================= */

  function toast(msg, type) {
    var box = $('#toast-box');
    if (!box) return;
    while (box.children.length >= 4) {
      if (box.firstChild) box.removeChild(box.firstChild);
    }
    var icon = type === 'success' ? ICONS.toastOk : (type === 'error' ? ICONS.toastErr : ICONS.toastInfo);
    var t = el('div', { class: 'toast toast-' + (type || 'info') });
    t.appendChild(el('span', { html: icon }));
    t.appendChild(el('span', { class: 'toast-msg', text: String(msg) }));
    box.appendChild(t);
    setTimeout(function () {
      t.classList.add('toast-out');
      setTimeout(function () {
        if (t.parentNode) t.parentNode.removeChild(t);
      }, 220);
    }, 3200);
  }

  function copyText(text) {
    var str = String(text);
    function legacy() {
      try {
        var ta = document.createElement('textarea');
        ta.value = str;
        ta.style.position = 'fixed';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        var ok = document.execCommand('copy');
        document.body.removeChild(ta);
        toast(ok ? '已复制到剪贴板' : '复制失败,请手动选择复制', ok ? 'success' : 'error');
      } catch (e) {
        toast('复制失败,请手动选择复制', 'error');
      }
    }
    if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
      navigator.clipboard.writeText(str).then(function () {
        toast('已复制到剪贴板', 'success');
      }, legacy);
    } else {
      legacy();
    }
  }

  /* 按钮三态:默认 / hover(CSS)/ loading(禁用 + 旋转指示)。
   * loadingText 传入时以纯文字替换按钮内容,结束时恢复原 innerHTML。 */
  function setBtnLoading(btn, on, loadingText) {
    if (!btn) return;
    if (on) {
      if (!btn.dataset.origHtml) btn.dataset.origHtml = btn.innerHTML;
      if (loadingText !== undefined && loadingText !== null) btn.textContent = String(loadingText);
      btn.classList.add('is-loading');
      btn.disabled = true;
    } else {
      btn.classList.remove('is-loading');
      btn.disabled = false;
      if (btn.dataset.origHtml) {
        btn.innerHTML = btn.dataset.origHtml;
        delete btn.dataset.origHtml;
      }
    }
  }

  /* ================= 3b. 双主题(html.dark + localStorage w2a_theme) ================= */

  function currentTheme() {
    return document.documentElement.classList.contains('dark') ? 'dark' : 'light';
  }

  function applyTheme(theme) {
    var dark = theme === 'dark';
    document.documentElement.classList.toggle('dark', dark);
    var meta = document.querySelector('meta[name="color-scheme"]');
    if (meta) meta.setAttribute('content', dark ? 'dark' : 'light');
    // 两个切换钮(登录页 + 顶栏)同步无障碍状态
    ['btn-theme', 'btn-login-theme'].forEach(function (id) {
      var b = document.getElementById(id);
      if (b) b.setAttribute('aria-pressed', dark ? 'true' : 'false');
    });
  }

  function initTheme() {
    var stored = '';
    try { stored = localStorage.getItem(LS_THEME) || ''; } catch (e) { /* 忽略 */ }
    // head 内联脚本已按存储值预置 class;此处统一兜底并同步按钮状态
    applyTheme(stored === 'dark' ? 'dark' : 'light');
  }

  function toggleTheme() {
    var next = currentTheme() === 'dark' ? 'light' : 'dark';
    try { localStorage.setItem(LS_THEME, next); } catch (e) { /* 忽略 */ }
    applyTheme(next);
  }

  function bindThemeButtons() {
    ['btn-theme', 'btn-login-theme'].forEach(function (id) {
      var b = document.getElementById(id);
      if (b) b.addEventListener('click', toggleTheme);
    });
  }

  /* ================= 3c. 全局快捷键:/ 或 ⌘K 聚焦当前页搜索框 =================
   * 输入守卫:事件目标为 INPUT/TEXTAREA/SELECT 或 isContentEditable 时,
   * `/` 直接放行不抢焦点(保证 playground URL、密钥名称等输入框可正常键入 /);
   * ⌘K / Ctrl+K 作为标准快捷键始终可用。 */
  function isEditableTarget(t) {
    if (!t) return false;
    var tag = t.tagName;
    return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || t.isContentEditable === true;
  }

  function focusPageSearch() {
    var input = document.querySelector('#page .js-page-search');
    if (!input) return false;
    input.focus();
    if (typeof input.select === 'function') {
      try { input.select(); } catch (e) { /* 忽略 */ }
    }
    return true;
  }

  function bindSearchHotkey() {
    document.addEventListener('keydown', function (e) {
      var k = String(e.key || '').toLowerCase();
      if ((e.metaKey || e.ctrlKey) && k === 'k') {
        e.preventDefault();
        focusPageSearch();
        return;
      }
      if (isEditableTarget(e.target)) return; // 输入守卫
      if (e.key === '/' && !e.metaKey && !e.ctrlKey && !e.altKey) {
        e.preventDefault();
        focusPageSearch();
      }
    });
  }

  /* ================= 4. 登录态与 fetch 统一封装 ================= */

  function getToken() {
    try {
      return localStorage.getItem(LS_TOKEN) || '';
    } catch (e) {
      return '';
    }
  }

  function tokenExpired() {
    try {
      var exp = Number(localStorage.getItem(LS_TOKEN_EXP) || 0);
      return exp > 0 && Date.now() >= exp;
    } catch (e) {
      return false;
    }
  }

  function setAuth(token, expiresIn, username) {
    try {
      localStorage.setItem(LS_TOKEN, token);
      var ms = Number(expiresIn);
      if (!isNaN(ms) && ms > 0) {
        localStorage.setItem(LS_TOKEN_EXP, String(Date.now() + ms * 1000 - 60000)); // 预留 60s 偏差
      } else {
        localStorage.removeItem(LS_TOKEN_EXP);
      }
      if (username) localStorage.setItem(LS_USERNAME, username);
    } catch (e) { /* localStorage 不可用时忽略,仅内存态 */ }
    state.username = username || '';
  }

  function clearAuth() {
    try {
      localStorage.removeItem(LS_TOKEN);
      localStorage.removeItem(LS_TOKEN_EXP);
      localStorage.removeItem(LS_USERNAME);
    } catch (e) { /* 忽略 */ }
    state.username = '';
  }

  function isLoggedIn() {
    return !!getToken() && !tokenExpired();
  }

  /* 机器可读错误 → 面向管理员的中文提示 */
  function extractErrMsg(data, status) {
    if (data && typeof data === 'object') {
      if (typeof data.message === 'string' && data.message && data.error === 'server_error') {
        return '服务端错误:' + data.message;
      }
      if (typeof data.error === 'string' && data.error) {
        if (data.error === 'unauthorized') return '未授权或登录已过期';
        if (data.error === 'quota_exceeded') return '调用配额已用尽';
        if (data.error === 'too_many_attempts') return '登录尝试次数过多,已临时锁定,请稍后再试';
        if (data.error === 'engine_cooldown') return '引擎冷却中,请稍后重试';
        if (data.error === 'not_found') return '接口或资源不存在(若刚部署,请确认平台 API 已启用)';
        if (data.error === 'server_error') return '服务端内部错误';
        if (data.error === 'bad_request') {
          return '参数错误' + (data.reason ? '(' + data.reason + ')' : '');
        }
        return data.error;
      }
      if (data.error && typeof data.error === 'object' && typeof data.error.message === 'string') {
        return data.error.message;
      }
    }
    return '请求失败(HTTP ' + status + ')';
  }

  /*
   * fetch 统一封装:自动带 Authorization: Bearer;JSON 解析;
   * 401(登录接口除外)→ 清 token 并回登录页;非 2xx 抛 Error(message)。
   * 所有路径统一拼 API_BASE 前缀(调用点传入 '/api/...' 字面量)。
   * 429/503 的 Retry-After 响应头会挂到 err.retryAfter(秒)上。
   */
  function api(method, path, body, opts) {
    opts = opts || {};
    var headers = { 'Accept': 'application/json' };
    if (body !== undefined && body !== null) headers['Content-Type'] = 'application/json';
    var token = getToken();
    if (token) headers['Authorization'] = 'Bearer ' + token;

    return fetch(API_BASE + path, {
      method: method,
      headers: headers,
      body: (body !== undefined && body !== null) ? JSON.stringify(body) : undefined
    }).then(function (res) {
      var retryAfter = Number(res.headers.get('Retry-After'));
      return res.text().then(function (text) {
        var data = null;
        if (text) {
          try { data = JSON.parse(text); } catch (e) { data = null; }
        }
        if (res.status === 401 && !opts.skipAuthRedirect) {
          onUnauthorized();
        }
        if (!res.ok) {
          var err = new Error(extractErrMsg(data, res.status));
          err.status = res.status;
          err.data = data;
          if (!isNaN(retryAfter) && retryAfter > 0) err.retryAfter = retryAfter;
          throw err;
        }
        return data;
      });
    });
  }

  function onUnauthorized() {
    clearAuth();
    showLogin();
    toast('登录已过期,请重新登录', 'error');
    if (location.hash !== '#/login') location.hash = '#/login';
  }

  /* ================= 5. 弹窗 / 确认框 ================= */

  var modalStack = [];

  function openModal(opts) {
    var overlay = document.createElement('div');
    overlay.className = 'modal-overlay';
    var modal = el('div', { class: 'modal' + (opts.wide ? ' modal-wide' : ''), role: 'dialog', 'aria-modal': 'true' });

    var closeBtn = null;
    var head = el('div', { class: 'modal-head' }, el('h2', { class: 'modal-title', text: opts.title || '' }));
    if (opts.closable !== false) {
      closeBtn = el('button', {
        class: 'modal-close', type: 'button', 'aria-label': '关闭',
        html: '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>'
      });
      head.appendChild(closeBtn);
    }
    modal.appendChild(head);
    if (opts.body) modal.appendChild(opts.body);
    if (opts.footer) modal.appendChild(opts.footer);
    overlay.appendChild(modal);
    document.body.appendChild(overlay);

    function close() {
      var i = modalStack.indexOf(close);
      if (i >= 0) modalStack.splice(i, 1);
      document.removeEventListener('keydown', onKey, true);
      if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
      if (typeof opts.onClose === 'function') opts.onClose();
    }
    function onKey(e) {
      if (modalStack[modalStack.length - 1] !== close) return; // 仅最顶层响应 Esc
      if (e.key === 'Escape') {
        if (opts.dismissable === false) return;
        e.preventDefault();
        close();
      }
    }

    document.addEventListener('keydown', onKey, true);
    if (closeBtn) closeBtn.addEventListener('click', close);
    if (opts.dismissable !== false) {
      overlay.addEventListener('mousedown', function (e) {
        if (e.target === overlay) close();
      });
    }
    modalStack.push(close);
    return { overlay: overlay, close: close };
  }

  function confirmDialog(opts) {
    opts = opts || {};
    return new Promise(function (resolve) {
      var settled = false;
      var handle = null;
      function finish(v) {
        if (settled) return;
        settled = true;
        if (handle) handle.close();
        resolve(v);
      }
      var body = el('div', { class: 'modal-body' },
        el('p', { class: 'confirm-msg', text: opts.message || '确定执行该操作吗?' }));
      var foot = el('div', { class: 'modal-foot' },
        el('button', {
          class: 'btn btn-secondary', type: 'button', text: opts.cancelText || '取消',
          onclick: function () { finish(false); }
        }),
        el('button', {
          class: 'btn ' + (opts.danger === false ? 'btn-primary' : 'btn-danger'),
          type: 'button', text: opts.confirmText || '确认',
          onclick: function () { finish(true); }
        }));
      handle = openModal({
        title: opts.title || '确认操作',
        body: body,
        footer: foot,
        dismissable: false, // 防止误点遮罩跳过确认
        onClose: function () { finish(false); }
      });
      setTimeout(function () {
        var cancel = foot.querySelector('button');
        if (cancel) cancel.focus();
      }, 0);
    });
  }

  /* ================= 6. Hash 路由与视图切换 ================= */

  function showLogin() {
    clearPageTimers();
    var login = $('#view-login');
    var app = $('#app');
    if (app) app.classList.add('hidden');
    if (login) {
      login.classList.remove('hidden');
      var errBox = $('#login-error');
      if (errBox) errBox.classList.add('hidden');
      var pwd = $('#login-password');
      if (pwd) pwd.value = '';
    }
    document.title = '登录 · Web2API 管理控制台';
    var h = location.hash || '';
    pendingHash = ROUTE_RE.test(h) ? h : null;
    setTimeout(function () {
      var u = $('#login-username');
      if (u) u.focus();
    }, 0);
  }

  function showApp() {
    var login = $('#view-login');
    var app = $('#app');
    if (login) login.classList.add('hidden');
    if (app) app.classList.remove('hidden');
    var name = state.username || 'admin';
    var un = $('#topbar-username');
    if (un) un.textContent = name;
    var av = $('#user-avatar');
    if (av) av.textContent = name.charAt(0).toUpperCase();
  }

  function setActiveNav(hash) {
    var items = document.querySelectorAll('.nav-item');
    for (var i = 0; i < items.length; i++) {
      var it = items[i];
      if (it.getAttribute('data-route') === hash) it.classList.add('active');
      else it.classList.remove('active');
    }
  }

  /* 未知路由的 404 视图:点阵底纹 + 衬线标题 + mono 未知 hash + 反色返回钮 */
  function renderNotFound(root, hash) {
    var back = el('button', {
      class: 'btn btn-primary', type: 'button', text: '返回总览',
      onclick: function () { location.hash = '#/overview'; }
    });
    root.appendChild(el('div', { class: 'notfound' },
      el('div', { class: 'nf-code', text: '404' }),
      el('h1', { class: 'nf-title', text: '页面不存在' }),
      el('div', { class: 'nf-hash', text: hash || '(空 hash)' }),
      el('div', { class: 'nf-desc', text: '该地址没有对应的控制台视图,请检查地址或从下方返回。' }),
      back));
  }

  /* 页头块:渲染进内容区(h1 衬线 30px/600 + 副标 14px muted + 页头动作区)。
   * 返回动作区容器,页面把刷新/新建等按钮放进 .page-actions。 */
  function renderPageHead(root, hash) {
    var meta = PAGE_META[hash] || { title: '管理控制台', desc: '' };
    var actions = el('div', { class: 'page-actions' });
    root.appendChild(el('div', { class: 'page-head' },
      el('div', { class: 'page-head-text' },
        el('h1', { class: 'page-title', text: meta.title }),
        meta.desc ? el('p', { class: 'page-desc', text: meta.desc }) : null),
      actions));
    return actions;
  }

  function renderRoute() {
    if (!isLoggedIn()) {
      showLogin();
      return;
    }
    clearPageTimers(); // 离开上一页时清理轮询/倒计时
    var hash = location.hash || '';
    var root = $('#page');
    if (!root) return;
    if (hash === '' || hash === '#') {
      /* 已登录会话从 canonical 地址(/web-tool/console/,无 hash)直入:
       * 默认进总览,而不是渲染 404。未登录分支已在上方处理(登录后由 pendingHash 接管)。 */
      location.hash = '#/overview';
      return; // hashchange 会再次触发 renderRoute
    }
    if (!ROUTE_RE.test(hash)) {
      showApp();
      setActiveNav('');
      document.title = '页面不存在 · Web2API 管理控制台';
      root.innerHTML = '';
      renderNotFound(root, hash);
      animatePage(root);
      return;
    }
    showApp();
    setActiveNav(hash);
    var meta = PAGE_META[hash] || { title: '管理控制台' };
    document.title = (meta.title || '管理控制台') + ' · Web2API 管理控制台';
    root.innerHTML = '';
    var actions = renderPageHead(root, hash);
    if (hash === '#/overview') pageOverview(root, actions);
    else if (hash === '#/engines') pageEngines(root, actions);
    else if (hash === '#/keys') pageKeys(root, actions);
    else if (hash === '#/usage') pageUsage(root, actions);
    else if (hash === '#/playground') pagePlayground(root);
    else if (hash === '#/agents') pageAgents(root);
    else if (hash === '#/settings') pageSettings(root);
    else pageDocs(root);
    animatePage(root);
  }

  /* 页间切换过渡:fade + 8px 位移(180ms),重排触发以确保每次路由都重放 */
  function animatePage(root) {
    root.classList.remove('page-anim');
    void root.offsetWidth;
    root.classList.add('page-anim');
  }

  /* ================= 通用节点 ================= */

  function loadingNode(msg) {
    return el('div', { class: 'loading' },
      el('span', { class: 'spinner', 'aria-hidden': 'true' }),
      el('span', { text: msg || '加载中…' }));
  }

  function emptyNode(title, desc, actionBtn, iconHtml) {
    return el('div', { class: 'empty' },
      el('div', { class: 'empty-icon', html: iconHtml || ICONS.empty24 }),
      el('div', { class: 'empty-title', text: title || '暂无数据' }),
      desc ? el('div', { class: 'empty-desc', text: desc }) : null,
      actionBtn || null);
  }

  function errorNode(e, retry) {
    var retryBtn = el('button', {
      class: 'btn btn-secondary btn-sm', type: 'button', text: '重试',
      onclick: function () { if (typeof retry === 'function') retry(); }
    });
    return el('div', { class: 'card' },
      el('div', { class: 'empty' },
        el('div', { class: 'empty-icon', html: ICONS.alert24 }),
        el('div', { class: 'empty-title', text: '加载失败' }),
        el('div', { class: 'empty-desc', text: e && e.message ? e.message : '网络异常,请稍后重试' }),
        retryBtn));
  }

  /* ================= 7. 页面:总览 ================= */

  function pageOverview(root, actions) {
    var refreshBtn = el('button', {
      class: 'btn btn-secondary btn-sm', type: 'button',
      html: ICONS.refresh + '<span>手动刷新</span>',
      onclick: function () { loadAll(); loadRecent(); }
    });
    actions.appendChild(refreshBtn);

    var wrap = el('div', null);
    root.appendChild(wrap);

    /* 最近日志:独立区块(数据 GET /api/usage?limit=5,只读端点) */
    var recentCard = el('div', { class: 'card' });
    root.appendChild(recentCard);

    function loadAll() {
      wrap.innerHTML = '';
      wrap.appendChild(skeletonBlock([{ w: 'w30' }, { big: true, w: 'w60' }, { w: 'w40' }]));
      Promise.all([
        api('GET', '/api/stats/overview'),
        api('GET', '/api/stats/timeseries?days=14'),
        api('GET', '/api/engines')
      ]).then(function (res) {
        wrap.innerHTML = '';
        var stats = (res[0] && typeof res[0] === 'object') ? res[0] : {};
        var series = extractTimeseries(res[1]);
        var engines = normalizeEngines(res[2]);
        wrap.appendChild(buildStatBand(stats));
        var grid = el('div', { class: 'grid-2' },
          buildChartCard(series),
          buildEngineCard(engines));
        wrap.appendChild(grid);
      }).catch(function (e) {
        wrap.innerHTML = '';
        wrap.appendChild(errorNode(e, function () { loadAll(); }));
      });
    }

    function loadRecent() {
      recentCard.innerHTML = '';
      recentCard.appendChild(el('div', { class: 'card-head' },
        el('h2', { class: 'card-title' }, '最近日志',
          el('span', { class: 'card-head-sub', text: 'GET /web-tool/api/usage?limit=5' })),
        el('a', { class: 'link-more', href: '#/usage', text: '查看全部 >' })));
      var body = el('div', { class: 'card-body flush' });
      recentCard.appendChild(body);
      body.appendChild(skeletonBlock([{ w: 'w40' }, { w: 'w80' }, { w: 'w70' }]));
      api('GET', '/api/usage?limit=5').then(function (data) {
        data = data || {};
        var items = Array.isArray(data.items) ? data.items : [];
        body.innerHTML = '';
        if (!items.length) {
          body.appendChild(el('div', { class: 'empty' },
            el('div', { class: 'empty-dot' },
              el('div', { class: 'empty-title', text: '暂无调用记录' }),
              el('div', { class: 'empty-desc', text: '发起一次 /v1/index/check 调用后,最近 5 条日志会显示在这里。' }))));
          return;
        }
        var tbody = el('tbody');
        items.forEach(function (it) {
          it = it || {};
          tbody.appendChild(el('tr', null,
            el('td', { class: 'td-mono', text: fmtDateTime(it.time) }),
            el('td', { class: 'td-strong', text: it.key_name || ('#' + it.key_id) }),
            el('td', null, el('span', { class: 'chip', text: it.endpoint || '-' })),
            el('td', null, statusPill(it.status))));
        });
        body.appendChild(el('div', { class: 'table-wrap' }, el('table', { class: 'table' },
          el('thead', null, el('tr', null,
            el('th', { text: '时间' }),
            el('th', { text: '密钥' }),
            el('th', { text: '端点' }),
            el('th', { text: '状态' }))),
          tbody)));
      }).catch(function (e) {
        body.innerHTML = '';
        body.appendChild(el('div', { class: 'empty' },
          el('div', { class: 'empty-title', text: '最近日志加载失败' }),
          el('div', { class: 'empty-desc', text: e && e.message ? e.message : '网络异常,请稍后重试' })));
      });
    }

    loadAll();
    loadRecent();
  }

  function extractTimeseries(res) {
    if (Array.isArray(res)) return res;
    if (res && typeof res === 'object') {
      if (Array.isArray(res.items)) return res.items;
      if (Array.isArray(res.series)) return res.series;
      if (Array.isArray(res.data)) return res.data;
    }
    return [];
  }

  /* 统计带:一个 hairline 容器内 4 格竖线分隔(§4.4),非独立卡 */
  function buildStatBand(stats) {
    var defs = [
      { label: '今日调用', value: fmtNumber(stats.today_calls), sub: '今日 units ' + fmtNumber(stats.today_units) },
      { label: '累计调用', value: fmtNumber(stats.total_calls), sub: '累计 units ' + fmtNumber(stats.total_units) },
      { label: '活跃密钥', value: fmtNumber(stats.active_keys), sub: '状态为启用的 API 密钥' },
      { label: '24h 成功率', value: fmtPercent(stats.success_rate_24h), sub: '近 24 小时请求成功率' }
    ];
    var band = el('div', { class: 'stat-band' });
    defs.forEach(function (d) {
      band.appendChild(el('div', { class: 'stat-cell' },
        el('div', { class: 'stat-value', text: d.value }),
        el('div', { class: 'stat-label', text: d.label }),
        el('div', { class: 'stat-sub', text: d.sub })));
    });
    return band;
  }

  function buildChartCard(series) {
    var body = el('div', { class: 'card-body' });
    var card = el('div', { class: 'card' },
      el('div', { class: 'card-head' },
        el('h2', { class: 'card-title' }, '近 14 日调用', el('span', { class: 'card-head-sub', text: 'GET /web-tool/api/stats/timeseries' }))),
      body);
    if (!series.length) {
      body.appendChild(emptyNode('暂无调用数据', '发起一次 /v1/index/check 调用后,这里会展示近 14 日的柱状图。'));
      return card;
    }
    body.appendChild(barChart(series));
    var legend = el('div', { class: 'chart-legend' },
      el('span', null, el('span', { class: 'dot' }), '调用次数'),
      el('span', { class: 'muted', text: '悬停柱形可查看 units 与成功数' }));
    body.appendChild(legend);
    return card;
  }

  /* 近 14 日柱状图:原生 SVG 手绘,无图表库 */
  function barChart(series) {
    var NS = 'http://www.w3.org/2000/svg';
    function s(name, attrs) {
      var n = document.createElementNS(NS, name);
      if (attrs) {
        Object.keys(attrs).forEach(function (k) { n.setAttribute(k, String(attrs[k])); });
      }
      return n;
    }
    var W = 760, H = 280;
    var m = { top: 30, right: 14, bottom: 34, left: 52 };
    var iw = W - m.left - m.right;
    var ih = H - m.top - m.bottom;
    var svg = s('svg', { viewBox: '0 0 ' + W + ' ' + H, class: 'chart-svg', role: 'img' });

    var calls = series.map(function (d) { return Math.max(0, Number(d.calls) || 0); });
    var maxV = 1;
    for (var i = 0; i < calls.length; i++) {
      if (calls[i] > maxV) maxV = calls[i];
    }
    var top = niceCeil(maxV);

    // 网格线 + Y 轴刻度(hairline + mono 数字);步数取能整除 top 的最大档,保证刻度全为整数
    var steps = 1;
    if (top % 2 === 0) steps = 2;
    if (top % 3 === 0) steps = 3;
    if (top % 4 === 0) steps = 4;
    if (top % 5 === 0) steps = 5;
    for (var g = 0; g <= steps; g++) {
      var val = top * g / steps;
      var y = m.top + ih - (ih * g / steps);
      // SVG 呈现属性写进 style,使 var() 随主题切换
      svg.appendChild(s('line', { x1: m.left, y1: y, x2: W - m.right, y2: y, style: 'stroke:var(--border);stroke-width:1' }));
      var yt = s('text', { x: m.left - 8, y: y + 4, 'text-anchor': 'end', 'font-size': 10, style: 'fill:var(--muted-fg);font-family:var(--mono)' });
      yt.textContent = chartTick(val);
      svg.appendChild(yt);
    }

    var n = series.length;
    var slot = iw / (n > 0 ? n : 1);
    var bw = Math.min(34, Math.max(6, slot * 0.52));

    series.forEach(function (d, idx) {
      var v = Math.max(0, Number(d.calls) || 0);
      var h = (v / top) * ih;
      var x = m.left + slot * idx + (slot - bw) / 2;
      var dateStr = String(d.date || '');

      var title = s('title');
      title.textContent = dateStr + ' · 调用 ' + v + ' · units ' + (Number(d.units) || 0) + ' · 成功 ' + (Number(d.ok) || 0);

      var rect;
      if (h > 0) {
        rect = s('rect', { x: x, y: m.top + ih - h, width: bw, height: h, rx: 2, style: 'fill:var(--chart-1)' });
      } else {
        rect = s('rect', { x: x, y: m.top + ih - 2, width: bw, height: 2, rx: 1, style: 'fill:var(--border)' }); // 零值基线占位
      }
      rect.appendChild(title);
      svg.appendChild(rect);

      if (v > 0 && slot >= 30) {
        var vt = s('text', { x: x + bw / 2, y: m.top + ih - h - 6, 'text-anchor': 'middle', 'font-size': 10, style: 'fill:var(--fg);font-family:var(--mono)' });
        vt.textContent = v >= 10000 ? Math.round(v / 1000) + 'k' : String(v);
        svg.appendChild(vt);
      }
      var xt = s('text', { x: x + bw / 2, y: H - m.bottom + 16, 'text-anchor': 'middle', 'font-size': 10, style: 'fill:var(--muted-fg)' });
      xt.textContent = dateStr.length >= 10 ? dateStr.slice(5) : dateStr;
      svg.appendChild(xt);
    });

    return svg;
  }

  function niceCeil(v) {
    if (v <= 5) return 5;
    var pow = Math.pow(10, Math.floor(Math.log10(v)));
    var cands = [1, 2, 2.5, 5, 10].map(function (c) { return c * pow; });
    for (var i = 0; i < cands.length; i++) {
      if (cands[i] >= v) return cands[i];
    }
    return cands[cands.length - 1];
  }

  function chartTick(v) {
    var r = Math.round(v * 100) / 100;
    if (r >= 10000) return (r / 1000) + 'k';
    return String(r);
  }

  /* 引擎状态归一化:兼容契约新字段(state/until/reason/consecutive_blocks/
   * last_error_at)与旧字段(breaker)。多形状 → 统一内部对象。 */
  function normalizeEngines(res) {
    var list = [];
    if (Array.isArray(res)) {
      list = res;
    } else if (res && Array.isArray(res.engines)) {
      list = res.engines;
    } else if (res && typeof res === 'object') {
      Object.keys(res).forEach(function (k) {
        var v = res[k];
        if (v && typeof v === 'object' && !Array.isArray(v)) {
          var item = {};
          for (var p in v) {
            if (Object.prototype.hasOwnProperty.call(v, p)) item[p] = v[p];
          }
          item.name = item.name || item.engine || k;
          list.push(item);
        }
      });
    }
    var out = [];
    list.forEach(function (e) {
      if (!e || typeof e !== 'object') return;
      var name = String(e.name || e.engine || e.id || '').toLowerCase();
      if (!name) return;
      var enabled = null;
      if (typeof e.enabled === 'boolean') enabled = e.enabled;
      else if (typeof e.disabled === 'boolean') enabled = !e.disabled;
      else if (typeof e.status === 'string') enabled = (e.status === 'active' || e.status === 'enabled' || e.status === 'ok');
      var raw = e.breaker || e.breaker_state || e.breakerStatus || e.circuit || e.circuit_breaker || e.circuit_state || null;
      var breaker = '';
      if (typeof raw === 'string') breaker = raw.toLowerCase();
      else if (raw && typeof raw === 'object') breaker = String(raw.state || raw.status || raw.phase || '').toLowerCase();
      var engState = String(e.state || '').toLowerCase();
      if (!engState) {
        if (breaker === 'open') engState = 'circuit_open';
        else if (enabled === false) engState = 'disabled';
        else engState = 'ok';
      }
      out.push({
        name: name,
        enabled: enabled,
        breaker: breaker,
        state: engState,
        until: parseWhen(e.until),
        reason: e.reason !== null && e.reason !== undefined ? String(e.reason) : '',
        consecutiveBlocks: (typeof e.consecutive_blocks === 'number') ? e.consecutive_blocks : null,
        lastErrorAt: parseWhen(e.last_error_at),
        endpoint: e.endpoint ? String(e.endpoint) : '',
        calls: (typeof e.calls === 'number') ? e.calls : null,
        ok: (typeof e.ok === 'number') ? e.ok : null
      });
    });
    out.sort(function (a, b) {
      var ia = ENGINE_ORDER.indexOf(a.name);
      var ib = ENGINE_ORDER.indexOf(b.name);
      if (ia === -1) ia = 99;
      if (ib === -1) ib = 99;
      return ia - ib;
    });
    return out;
  }

  /* 引擎状态 → pill(共享) */
  function engineStateInfo(eng) {
    if (eng.enabled === false || eng.state === 'disabled') return { pill: 'pill-gray', label: '停用' };
    if (eng.state === 'cooldown') return { pill: 'pill-amber', label: '冷却中' };
    if (eng.state === 'circuit_open') return { pill: 'pill-red', label: '熔断开启' };
    if (eng.state === 'ok') return { pill: 'pill-green', label: '运行中' };
    if (eng.breaker === 'open') return { pill: 'pill-red', label: '熔断开启' };
    if (eng.breaker === 'half-open' || eng.breaker === 'halfopen' || eng.breaker === 'half_open') return { pill: 'pill-amber', label: '恢复中' };
    return { pill: 'pill-green', label: '运行中' };
  }

  function buildEngineCard(engines) {
    var body = el('div', { class: 'card-body' });
    var card = el('div', { class: 'card' },
      el('div', { class: 'card-head' },
        el('h2', { class: 'card-title' }, '引擎状态', el('span', { class: 'card-head-sub', text: 'GET /web-tool/api/engines' }))),
      body);
    if (!engines.length) {
      body.appendChild(emptyNode('暂无引擎状态', '服务端未返回引擎数据。'));
      return card;
    }
    var list = el('div', { class: 'engine-list' });
    engines.forEach(function (eng) {
      var info = engineStateInfo(eng);
      var meta = el('div', { class: 'engine-meta' });
      meta.appendChild(el('span', { class: 'pill ' + info.pill, text: info.label }));
      if (eng.state === 'cooldown' && eng.until) {
        meta.appendChild(el('span', { class: 'muted td-mono', text: '至 ' + fmtDateTime(eng.until) }));
      }
      if (eng.consecutiveBlocks !== null && eng.consecutiveBlocks > 0) {
        meta.appendChild(el('span', { class: 'muted', text: '连续拦截 ' + eng.consecutiveBlocks + ' 次' }));
      }
      list.appendChild(el('div', { class: 'engine-row' },
        el('div', { class: 'engine-name' },
          el('span', { class: 'chip', text: eng.name }),
          el('span', { class: 'muted', text: ENGINE_LABEL[eng.name] || '' })),
        meta));
    });
    body.appendChild(list);
    var more = el('div', { style: 'padding:10px 0 0;' },
      el('a', { class: 'link-more', href: '#/engines', text: '查看冷却看板与运维操作 >' }));
    body.appendChild(more);
    return card;
  }

  /* ================= 7b. 页面:引擎状态(冷却看板) ================= */

  function pageEngines(root, actions) {
    var viewState = { view: 'cards' };
    /* 引擎分组筛选(STYLE-v2 §5:工具栏含引擎分组 toggle-group):
     * 平台引擎均为搜索类,按引擎粒度分组——全部 / 各引擎;切换页面后重置为「全部」。 */
    var groupState = 'all';

    var groupDefs = [['all', '全部']].concat(Object.keys(ENGINE_LABEL).map(function (name) {
      return [name, ENGINE_LABEL[name]];
    }));
    var groupBtns = {};
    var groupSeg = el('div', { class: 'seg', role: 'group', 'aria-label': '引擎分组筛选' });
    groupDefs.forEach(function (pair) {
      var b = el('button', {
        class: pair[0] === groupState ? 'on' : '', type: 'button', text: pair[1],
        onclick: function () {
          if (groupState === pair[0]) return;
          groupState = pair[0];
          Object.keys(groupBtns).forEach(function (k) { groupBtns[k].classList.toggle('on', k === groupState); });
          renderGrid(lastList);
          tickCountdowns();
        }
      });
      groupBtns[pair[0]] = b;
      groupSeg.appendChild(b);
    });

    var segCards = el('button', { class: 'on', type: 'button', text: '卡片' });
    var segTable = el('button', { type: 'button', text: '表格' });
    var seg = el('div', { class: 'seg', role: 'group', 'aria-label': '卡片 / 表格视图切换' }, segCards, segTable);
    segCards.addEventListener('click', function () { switchView('cards'); });
    segTable.addEventListener('click', function () { switchView('table'); });
    var refreshBtn = el('button', {
      class: 'btn btn-secondary btn-sm', type: 'button',
      html: ICONS.refresh + '<span>手动刷新</span>',
      onclick: function () { load(false); }
    });
    actions.appendChild(groupSeg);
    actions.appendChild(seg);
    actions.appendChild(refreshBtn);

    root.appendChild(el('p', {
      class: 'page-note',
      text: '连续拦截触发冷却:第 1 次 10 分钟、第 2 次 30 分钟、第 3 次及以上 120 分钟;连续 240 分钟无拦截自动归零。到期后放行一个探测请求,成功恢复、失败进下一档。每 10 秒自动刷新。'
    }));

    var wrap = el('div', null);
    root.appendChild(wrap);
    var lastList = [];
    var reloadQueued = false;

    function switchView(v) {
      if (viewState.view === v) return;
      viewState.view = v;
      segCards.className = v === 'cards' ? 'on' : '';
      segTable.className = v === 'table' ? 'on' : '';
      renderGrid(lastList);
      tickCountdowns();
    }

    function renderGrid(list) {
      wrap.innerHTML = '';
      var scoped = (groupState === 'all') ? list : list.filter(function (eng) {
        return eng && eng.name === groupState;
      });
      if (!scoped.length) {
        if (groupState !== 'all' && list.length) {
          wrap.appendChild(el('div', { class: 'card' },
            emptyNode('该分组暂无引擎',
              '当前分组「' + (ENGINE_LABEL[groupState] || groupState) + '」没有返回引擎数据,可切换分组或确认引擎已在 config.yaml 中启用。')));
        } else {
          wrap.appendChild(el('div', { class: 'card' },
            emptyNode('暂无引擎状态', '服务端未返回引擎数据。请确认引擎已在 config.yaml 中启用。')));
        }
        return;
      }
      if (viewState.view === 'table') {
        wrap.appendChild(buildEnginesTable(scoped));
      } else {
        var grid = el('div', { class: 'engine-grid' });
        scoped.forEach(function (eng) {
          grid.appendChild(engineCardNode(eng));
        });
        wrap.appendChild(grid);
      }
    }

    /* 表格视图(§4.5 范式):列 = 快照实有字段 */
    function buildEnginesTable(list) {
      var tbody = el('tbody');
      list.forEach(function (eng) {
        var info = engineStateInfo(eng);
        var btns = buildEngineButtons(eng);
        var cdCell;
        if (eng.state === 'cooldown' && eng.until) {
          var left = eng.until.getTime() - Date.now();
          cdCell = el('td', { class: 'td-num' },
            el('span', { class: 'cd-value', 'data-cd-until': String(eng.until.getTime()), text: left > 0 ? fmtRemaining(left) : '等待恢复探测…' }));
        } else if (eng.state === 'cooldown') {
          cdCell = el('td', { class: 'td-num muted', text: '冷却中' });
        } else {
          cdCell = el('td', { class: 'td-num muted', text: '—' });
        }
        tbody.appendChild(el('tr', null,
          el('td', null, el('b', { text: ENGINE_LABEL[eng.name] || eng.name }), ' ', el('span', { class: 'chip', text: eng.name })),
          el('td', { class: 'td-mono', text: eng.endpoint || '—' }),
          el('td', null, el('span', { class: 'pill ' + info.pill, text: info.label })),
          cdCell,
          el('td', { class: 'td-num', text: eng.consecutiveBlocks !== null ? String(eng.consecutiveBlocks) : '—' }),
          el('td', { class: 'td-mono', text: eng.lastErrorAt ? fmtDateTime(eng.lastErrorAt) : '—' }),
          el('td', { class: 'muted', text: eng.reason || '—' }),
          el('td', null, el('div', { class: 'row-actions' }, btns.resetBtn, btns.forceBtn))));
      });
      return el('div', { class: 'card' }, el('div', { class: 'table-wrap' }, el('table', { class: 'table' },
        el('thead', null, el('tr', null,
          el('th', { text: '引擎' }),
          el('th', { text: '端点' }),
          el('th', { text: '状态' }),
          el('th', { class: 'td-num-h', text: '冷却剩余' }),
          el('th', { class: 'td-num-h', text: '连续拦截' }),
          el('th', { text: '最近错误' }),
          el('th', { text: '冷却原因' }),
          el('th', { text: '操作' }))),
        tbody)));
    }

    /* 单秒倒计时:刷新所有 data-cd-until 元素;有冷却到期时触发一次静默刷新 */
    function tickCountdowns() {
      var nodes = wrap.querySelectorAll('[data-cd-until]');
      var expired = false;
      for (var i = 0; i < nodes.length; i++) {
        var until = Number(nodes[i].getAttribute('data-cd-until'));
        var left = until - Date.now();
        if (left > 0) {
          nodes[i].textContent = fmtRemaining(left);
        } else {
          nodes[i].textContent = '等待恢复探测…';
          nodes[i].classList.add('done');
          expired = true;
        }
      }
      if (expired && !reloadQueued) {
        reloadQueued = true;
        load(true);
        setTimeout(function () { reloadQueued = false; }, 12000);
      }
    }

    function load(silent) {
      if (!silent) {
        wrap.innerHTML = '';
        wrap.appendChild(skeletonCards(4));
      }
      api('GET', '/api/engines').then(function (res) {
        lastList = normalizeEngines(res);
        renderGrid(lastList);
        tickCountdowns();
      }).catch(function (e) {
        wrap.innerHTML = '';
        wrap.appendChild(errorNode(e, function () { load(false); }));
      });
    }

    /* 强制恢复 / 强制冷却按钮(卡片与表格视图共用;上下文敏感禁用) */
    function buildEngineButtons(eng) {
      var resetBtn = el('button', { class: 'btn btn-secondary btn-sm', type: 'button', text: '强制恢复' });
      resetBtn.addEventListener('click', function () {
        confirmDialog({
          title: '强制恢复引擎',
          message: '确定强制恢复引擎「' + (ENGINE_LABEL[eng.name] || eng.name) + '(' + eng.name + ')」吗?将清除冷却状态与连续拦截计数,恢复后立即放行真实请求。',
          confirmText: '强制恢复',
          danger: false
        }).then(function (yes) {
          if (!yes) return;
          setBtnLoading(resetBtn, true);
          api('POST', '/api/engines/' + encodeURIComponent(eng.name) + '/cooldown/reset')
            .then(function () {
              toast('「' + (ENGINE_LABEL[eng.name] || eng.name) + '」已强制恢复', 'success');
              load(true);
            })
            .catch(function (e) {
              toast('恢复失败:' + e.message, 'error');
            })
            .then(function () { setBtnLoading(resetBtn, false); });
        });
      });

      var forceBtn = el('button', { class: 'btn btn-secondary btn-sm', type: 'button', text: '强制冷却' });
      forceBtn.addEventListener('click', function () {
        openForceCooldownModal(eng, function () { load(true); });
      });

      /* 按钮上下文敏感:正常运行/停用时无需强制恢复,冷却中无需叠加冷却 */
      if (eng.state !== 'cooldown' && eng.state !== 'circuit_open') {
        resetBtn.disabled = true;
        resetBtn.title = '引擎当前无需强制恢复';
      }
      if (eng.state === 'cooldown') {
        forceBtn.disabled = true;
        forceBtn.title = '冷却中,无需再次强制冷却';
      }
      return { resetBtn: resetBtn, forceBtn: forceBtn };
    }

    /* 每引擎一张状态卡:字段仅限 /api/engines 快照
     * (engine/endpoint/enabled/breaker/state/until/reason/consecutive_blocks/last_error_at) */
    function engineCardNode(eng) {
      var info = engineStateInfo(eng);
      var cardCls = 'engine-card';
      if (eng.state === 'cooldown') cardCls += ' is-cooldown';
      if (eng.state === 'circuit_open') cardCls += ' is-circuit';

      var head = el('div', { class: 'engine-card-head' },
        el('div', { class: 'engine-card-title' },
          el('b', { text: ENGINE_LABEL[eng.name] || eng.name }),
          el('span', { class: 'chip', text: eng.name })),
        el('span', { class: 'pill ' + info.pill, text: info.label }));

      var body;
      if (eng.state === 'cooldown' && eng.until) {
        var left = eng.until.getTime() - Date.now();
        body = el('div', null,
          el('div', { class: 'cd-value', 'data-cd-until': String(eng.until.getTime()), text: left > 0 ? fmtRemaining(left) : '等待恢复探测…' }),
          el('div', { class: 'cd-label', text: '冷却剩余(至 ' + fmtDateTime(eng.until) + ')' }));
      } else if (eng.state === 'cooldown') {
        body = el('div', null,
          el('div', { class: 'cd-value done', text: '冷却' }),
          el('div', { class: 'cd-label', text: '冷却中(服务端未返回恢复时间,等待下次刷新)' }));
      } else if (eng.state === 'circuit_open') {
        body = el('div', null,
          el('div', { class: 'cd-value done', text: '熔断' }),
          el('div', { class: 'cd-label', text: '熔断开启:连续失败达阈值,等待半开探测' + (eng.breaker === 'half-open' || eng.breaker === 'halfopen' || eng.breaker === 'half_open' ? '(当前半开)' : '') }));
      } else {
        body = el('div', null,
          el('div', { class: 'cd-value done', text: eng.enabled === false ? '停用' : '正常' }),
          el('div', { class: 'cd-label', text: eng.enabled === false ? '该引擎已在 config.yaml 中停用' : '运行正常,请求正常放行' }));
      }

      var btns = buildEngineButtons(eng);
      var facts = el('div', { class: 'engine-facts' });
      facts.appendChild(el('div', { class: 'fact' }, el('b', { text: '端点' }), el('span', { text: eng.endpoint || '—' })));
      facts.appendChild(el('div', { class: 'fact' }, el('b', { text: '冷却原因' }), el('span', { text: eng.reason || '—' })));
      facts.appendChild(el('div', { class: 'fact' }, el('b', { text: '连续拦截' }), el('span', { text: eng.consecutiveBlocks !== null ? eng.consecutiveBlocks + ' 次' : '—' })));
      facts.appendChild(el('div', { class: 'fact' }, el('b', { text: '最近错误' }), el('span', { text: eng.lastErrorAt ? fmtDateTime(eng.lastErrorAt) : '—' })));

      var cardActions = el('div', { class: 'engine-actions' }, btns.resetBtn, btns.forceBtn);

      return el('div', { class: cardCls }, head, body, facts, cardActions);
    }

    /* 强制冷却弹窗:minutes 1–720 */
    function openForceCooldownModal(eng, reload) {
      var label = ENGINE_LABEL[eng.name] || eng.name;
      var minutesInput = el('input', { class: 'input', type: 'number', min: '1', max: '720', step: '1', value: '10' });
      var err = el('div', { class: 'form-err hidden' });
      var body = el('div', { class: 'modal-body' },
        el('div', { class: 'field' },
          el('label', { class: 'field-label', text: '冷却时长(分钟)' }),
          minutesInput,
          el('div', { class: 'form-hint', text: '1–720 分钟。冷却期内「' + label + '(' + eng.name + ')」的所有 search / check 请求将快速失败,返回 503 engine_cooldown,不发出真实请求。' })),
        err);
      var submitBtn = el('button', { class: 'btn btn-primary', type: 'button', text: '强制冷却' });
      var cancelBtn = el('button', {
        class: 'btn btn-secondary', type: 'button', text: '取消',
        onclick: function () { handle.close(); }
      });
      var foot = el('div', { class: 'modal-foot' }, cancelBtn, submitBtn);
      var handle = openModal({ title: '强制冷却「' + label + '」', body: body, footer: foot, dismissable: false });

      function showErr(msg) {
        err.textContent = msg;
        err.classList.remove('hidden');
      }

      submitBtn.addEventListener('click', function () {
        var n = Number(minutesInput.value);
        if (!minutesInput.value || isNaN(n) || n < 1 || n > 720 || Math.floor(n) !== n) {
          showErr('冷却时长必须是 1–720 之间的整数(分钟)');
          return;
        }
        err.classList.add('hidden');
        setBtnLoading(submitBtn, true, '下发中…');
        api('POST', '/api/engines/' + encodeURIComponent(eng.name) + '/cooldown/force', { minutes: Math.floor(n) })
          .then(function () {
            handle.close();
            toast('「' + label + '」已进入强制冷却 ' + Math.floor(n) + ' 分钟', 'success');
            reload();
          })
          .catch(function (e) {
            showErr(e.message);
          })
          .then(function () { setBtnLoading(submitBtn, false); });
      });

      minutesInput.addEventListener('keydown', function (e) {
        if (e.key === 'Enter') {
          e.preventDefault();
          submitBtn.click();
        }
      });
      setTimeout(function () { minutesInput.focus(); }, 0);
    }

    load(false);
    setPageInterval(function () { load(true); }, 10000); // 10s 轮询
    setPageInterval(tickCountdowns, 1000);                // 1s 倒计时
  }

  /* ================= 7c. 页面:API 密钥 ================= */

  function pageKeys(root, actions) {
    var searchInput = el('input', {
      class: 'input js-page-search', type: 'search',
      placeholder: '搜索名称或前缀…', 'aria-label': '搜索密钥'
    });
    var searchIcon = el('span', { class: 'search-icon', html: '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"></circle><line x1="21" y1="21" x2="16.65" y2="16.65"></line></svg>' });
    var searchKbd = el('kbd', { text: isMacLike() ? '⌘K' : '/' });
    var searchBox = el('div', { class: 'search-box' }, searchIcon, searchInput, searchKbd);
    searchInput.addEventListener('input', function () { renderTable(); });

    var createBtn = el('button', {
      class: 'btn btn-primary', type: 'button',
      html: ICONS.plus + '<span>创建密钥</span>',
      onclick: function () { openCreateKeyModal(loadAll); }
    });
    actions.appendChild(searchBox);
    actions.appendChild(createBtn);

    var card = el('div', { class: 'card' });
    root.appendChild(card);

    var keys = [];

    function loadAll() {
      card.innerHTML = '';
      card.appendChild(skeletonBlock([{ w: 'w30' }, { w: 'w80' }, { w: 'w80' }, { w: 'w60' }]));
      api('GET', '/api/keys').then(function (data) {
        keys = Array.isArray(data) ? data : (data && Array.isArray(data.items) ? data.items : []);
        state.keysCache = keys;
        card.innerHTML = '';
        renderTable(true);
      }).catch(function (e) {
        card.innerHTML = '';
        card.appendChild(errorNode(e, loadAll));
      });
    }

    /* 前端过滤:GET /api/keys 为全量返回,搜索只过滤已加载列表(不改请求) */
    function filteredKeys() {
      var q = searchInput.value.trim().toLowerCase();
      if (!q) return keys;
      return keys.filter(function (k) {
        k = k || {};
        return String(k.name || '').toLowerCase().indexOf(q) >= 0 ||
          String(k.prefix || '').toLowerCase().indexOf(q) >= 0;
      });
    }

    function renderTable(fresh) {
      card.innerHTML = '';
      if (!keys.length) {
        var emptyCreate = el('button', {
          class: 'btn btn-primary btn-sm', type: 'button', text: '创建第一个密钥',
          onclick: function () { openCreateKeyModal(loadAll); }
        });
        card.appendChild(emptyNode('还没有 API 密钥', '创建一个密钥,即可开始调用收录查询 API。', emptyCreate));
        return;
      }
      var list = filteredKeys();
      if (!list.length) {
        card.appendChild(el('div', { class: 'empty' },
          el('div', { class: 'empty-dot' },
            el('div', { class: 'empty-title', text: '没有匹配的密钥' }),
            el('div', { class: 'empty-desc', text: '试试更换关键词,或清空搜索框查看全部密钥。' }))));
        return;
      }
      card.appendChild(buildKeysTable(list, loadAll));
      if (!fresh && list.length !== keys.length) {
        var info = el('div', { class: 'pagination', style: 'border-top:none;padding-top:0;' },
          el('span', { class: 'page-info', text: '匹配 ' + list.length + ' / 共 ' + keys.length + ' 条(前端过滤,不改请求)' }));
        card.appendChild(info);
      }
    }
    loadAll();
  }

  /* 平台判定(仅用于 ⌘K kbd 帽文案,不改变快捷键行为:两种都注册) */
  function isMacLike() {
    return /Mac|iPhone|iPad/i.test(navigator.platform || navigator.userAgent || '');
  }

  function buildKeysTable(keys, reload) {
    var thead = el('thead', null, el('tr', null,
      el('th', { text: '名称' }),
      el('th', { text: '前缀' }),
      el('th', { text: '状态' }),
      el('th', { class: 'td-num-h', text: '今日用量' }),
      el('th', { class: 'td-num-h', text: '累计' }),
      el('th', { class: 'td-num-h', text: '日配额' }),
      el('th', { text: '创建时间' }),
      el('th', { text: '最后使用' }),
      el('th', { text: '操作' })));
    var tbody = el('tbody');

    keys.forEach(function (k) {
      k = k || {};
      var status = String(k.status || '');
      var isActive = status === 'active';

      // 启停开关
      var cb = el('input', { type: 'checkbox', 'aria-label': '启用/停用密钥' });
      cb.checked = isActive;
      var sw = el('label', { class: 'switch', title: isActive ? '点击停用' : '点击启用' }, cb, el('span', { class: 'slider' }));
      cb.addEventListener('change', function () {
        var next = cb.checked ? 'active' : 'disabled';
        sw.classList.add('loading');
        api('PATCH', '/api/keys/' + encodeURIComponent(String(k.id)), { status: next })
          .then(function () {
            toast(next === 'active' ? '密钥已启用' : '密钥已停用', 'success');
            if (typeof reload === 'function') reload();
          })
          .catch(function (e) {
            cb.checked = !cb.checked; // 失败回滚
            toast(e.message, 'error');
          })
          .then(function () {
            sw.classList.remove('loading');
          });
      });

      // 删除(确认框)
      var del = el('button', { class: 'btn btn-text-danger', type: 'button', text: '删除' });
      del.addEventListener('click', function () {
        confirmDialog({
          title: '删除密钥',
          message: '确定删除密钥「' + (k.name || '未命名') + '(' + (k.prefix || '-') + ')」吗?删除后使用该密钥的调用将立即失效,且不可恢复。',
          confirmText: '删除'
        }).then(function (yes) {
          if (!yes) return;
          setBtnLoading(del, true);
          api('DELETE', '/api/keys/' + encodeURIComponent(String(k.id)))
            .then(function () {
              toast('密钥已删除', 'success');
              if (typeof reload === 'function') reload();
            })
            .catch(function (e) {
              toast(e.message, 'error');
              setBtnLoading(del, false);
            });
        });
      });

      // 今日用量 + 配额进度细条(daily_quota=0 不限,不渲染进度)
      var usedNum = Number(k.used_today) || 0;
      var quotaNum = Number(k.daily_quota) || 0;
      var usedCell;
      if (quotaNum > 0) {
        var pct = Math.min(100, Math.round(usedNum / quotaNum * 100));
        var barCls = 'qbar' + (pct >= 100 ? ' full' : (pct >= 80 ? ' hot' : ''));
        usedCell = el('td', { class: 'td-num' },
          el('div', { text: fmtNumber(k.used_today) }),
          el('div', { class: barCls, title: '今日 ' + fmtNumber(usedNum) + ' / ' + fmtNumber(quotaNum) + ' units(' + pct + '%)' },
            el('i', { style: 'width:' + pct + '%' })));
      } else {
        usedCell = el('td', { class: 'td-num', text: fmtNumber(k.used_today) });
      }

      tbody.appendChild(el('tr', null,
        el('td', { class: 'td-strong', text: k.name || '未命名' }),
        el('td', null, el('span', { class: 'chip', text: k.prefix || '-' })),
        el('td', null, el('span', { class: 'pill ' + (isActive ? 'pill-green' : 'pill-gray'), text: isActive ? '启用' : '停用' })),
        usedCell,
        el('td', { class: 'td-num', text: fmtNumber(k.used_total) }),
        el('td', { class: 'td-num', text: fmtQuota(k.daily_quota) }),
        el('td', { class: 'td-mono', text: fmtDateTime(k.created_at) }),
        el('td', { class: 'td-mono', text: (k.last_used_at === null || k.last_used_at === undefined || k.last_used_at === '') ? '从未使用' : fmtDateTime(k.last_used_at) }),
        el('td', null, el('div', { class: 'row-actions' }, sw, del))));
    });

    // keys 页无分页:GET /api/keys 全量返回;行多时容器内纵向滚动,不伪造分页控件
    return el('div', { class: 'table-scroll' }, el('table', { class: 'table' }, thead, tbody));
  }

  function fmtQuota(v) {
    if (v === null || v === undefined || v === '' || isNaN(Number(v)) || Number(v) === 0) return '不限';
    return fmtNumber(Number(v));
  }

  /* 创建密钥弹窗 */
  function openCreateKeyModal(onCreated) {
    var nameInput = el('input', { class: 'input', type: 'text', maxlength: '64', placeholder: '例如:生产服务 / 测试环境' });
    var quotaInput = el('input', { class: 'input', type: 'number', min: '0', step: '1', placeholder: '0 表示不限' });
    var err = el('div', { class: 'form-err hidden', role: 'alert' });
    var body = el('div', { class: 'modal-body' },
      el('div', { class: 'field' },
        el('label', { class: 'field-label', text: '名称' }),
        nameInput),
      el('div', { class: 'field' },
        el('label', { class: 'field-label', text: '日配额(units / 天)' }),
        quotaInput,
        el('div', { class: 'form-hint', text: '1 URL × 1 引擎 = 1 unit;留空或 0 表示不限量。' })),
      err);
    var submitBtn = el('button', { class: 'btn btn-primary', type: 'button', text: '创建' });
    var cancelBtn = el('button', {
      class: 'btn btn-secondary', type: 'button', text: '取消',
      onclick: function () { handle.close(); }
    });
    var foot = el('div', { class: 'modal-foot' }, cancelBtn, submitBtn);
    var handle = openModal({ title: '创建 API 密钥', body: body, footer: foot, dismissable: false });

    function showErr(msg) {
      err.textContent = msg;
      err.classList.remove('hidden');
    }

    submitBtn.addEventListener('click', function () {
      var name = nameInput.value.trim();
      if (!name) { showErr('请输入密钥名称'); return; }
      var payload = { name: name };
      var q = quotaInput.value.trim();
      if (q !== '') {
        var n = Number(q);
        if (isNaN(n) || n < 0) { showErr('日配额必须是不小于 0 的数字'); return; }
        payload.daily_quota = Math.floor(n);
      }
      err.classList.add('hidden');
      setBtnLoading(submitBtn, true, '创建中…');
      cancelBtn.disabled = true;
      api('POST', '/api/keys', payload)
        .then(function (created) {
          handle.close();
          openKeySuccessModal(created || {});
          if (typeof onCreated === 'function') onCreated();
        })
        .catch(function (e) {
          showErr(e.message);
        })
        .then(function () {
          setBtnLoading(submitBtn, false);
          cancelBtn.disabled = false;
        });
    });

    nameInput.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') {
        e.preventDefault();
        submitBtn.click();
      }
    });
    setTimeout(function () { nameInput.focus(); }, 0);
  }

  /* 创建成功弹窗:完整密钥仅显示一次 */
  function openKeySuccessModal(k) {
    var keyStr = String(k.key || '');
    var body = el('div', { class: 'modal-body' },
      el('div', { class: 'key-note', html: ICONS.warn + '<span><b>请立即保存:</b>完整密钥仅显示这一次,关闭后将无法再次查看。</span>' }),
      el('div', { class: 'key-box', text: keyStr || '(服务端未返回密钥)' }),
      el('div', { class: 'key-meta' },
        el('span', null, el('b', { text: '名称:' }), k.name || '—'),
        el('span', null, el('b', { text: '前缀:' }), k.prefix || '—'),
        el('span', null, el('b', { text: '日配额:' }), fmtQuota(k.daily_quota))));
    var copyBtn = el('button', {
      class: 'btn btn-secondary', type: 'button',
      html: ICONS.copy + '<span>复制完整密钥</span>',
      onclick: function () { if (keyStr) copyText(keyStr); }
    });
    var okBtn = el('button', {
      class: 'btn btn-primary', type: 'button', text: '我已保存,关闭',
      onclick: function () { handle.close(); }
    });
    var foot = el('div', { class: 'modal-foot' }, copyBtn, okBtn);
    var handle = openModal({
      title: '密钥创建成功',
      body: body,
      footer: foot,
      closable: false,   // 必须显式确认,避免误关丢密钥
      dismissable: false
    });
    setTimeout(function () { copyBtn.focus(); }, 0);
  }

  /* ================= 7d. 页面:使用日志 ================= */

  function pageUsage(root, actions) {
    var st = state.usage;
    /* 级别 / 时间筛选:GET /api/usage 仅有 limit/offset/key_id 参数,
     * 无级别与时间过滤——这两组筛选在当前页数据上前端过滤(下方如实提示),不改请求。 */
    var localFilter = { level: 'all', range: 'all' };
    /* 列排序:作用于当前页已加载并经筛选的数据(接口无排序参数,不改请求/分页契约);
     * 默认 时间 ↓,与接口返回的新→旧原始顺序一致。 */
    var sortState = { key: 'time', dir: 'desc' };

    var filterSelect = el('select', { class: 'inline-select', 'aria-label': '按密钥筛选' });
    filterSelect.appendChild(el('option', { value: '', text: '全部密钥' }));
    filterSelect.addEventListener('change', function () {
      st.keyId = filterSelect.value;
      st.offset = 0;
      loadList();
    });
    var refreshBtn = el('button', {
      class: 'btn btn-secondary btn-sm', type: 'button',
      html: ICONS.refresh + '<span>刷新</span>',
      onclick: function () { loadKeysForFilter(); loadList(); }
    });

    // 级别 chip:全部 / 2xx / 4xx / 5xx(前端过滤当前页)
    var levelChips = {};
    var levelBox = el('div', { class: 'toolbar-actions', role: 'group', 'aria-label': '按状态级别筛选' });
    [['all', '全部'], ['2xx', '2xx'], ['4xx', '4xx'], ['5xx', '5xx']].forEach(function (pair) {
      var b = el('button', { class: 'fchip' + (pair[0] === 'all' ? ' on' : ''), type: 'button', text: pair[1] });
      b.addEventListener('click', function () {
        localFilter.level = pair[0];
        Object.keys(levelChips).forEach(function (k) { levelChips[k].classList.toggle('on', k === pair[0]); });
        renderTable();
      });
      levelChips[pair[0]] = b;
      levelBox.appendChild(b);
    });

    // 时间 underline tabs:今天 / 7 天 / 30 天(前端过滤当前页)
    var rangeTabs = {};
    var rangeBox = el('div', { class: 'utabs', role: 'group', 'aria-label': '按时间范围筛选' });
    [['all', '全部时间'], ['today', '今天'], ['7d', '7 天'], ['30d', '30 天']].forEach(function (pair) {
      var b = el('button', { class: pair[0] === 'all' ? 'on' : '', type: 'button', text: pair[1] });
      b.addEventListener('click', function () {
        localFilter.range = pair[0];
        Object.keys(rangeTabs).forEach(function (k) { rangeTabs[k].classList.toggle('on', k === pair[0]); });
        renderTable();
      });
      rangeTabs[pair[0]] = b;
      rangeBox.appendChild(b);
    });

    var toolbar = el('div', { class: 'page-toolbar' },
      el('div', { class: 'toolbar-actions' },
        el('span', { class: 'muted', text: '密钥' }),
        filterSelect,
        el('span', { class: 'muted', text: '级别' }),
        levelBox,
        rangeBox),
      el('div', { class: 'toolbar-actions' }, refreshBtn));
    root.appendChild(toolbar);
    root.appendChild(el('p', { class: 'page-note', text: '级别与时间筛选作用于当前页已加载的数据(接口仅支持密钥筛选与分页);每次 /v1/index/check 与 MCP 工具调用都会记录一条使用日志。' }));

    var card = el('div', { class: 'card' });
    root.appendChild(card);

    var lastItems = [];
    var lastTotal = 0;

    function currentUrl() {
      var q = '?limit=' + st.limit + '&offset=' + st.offset;
      if (st.keyId !== '') q += '&key_id=' + encodeURIComponent(st.keyId);
      return '/api/usage' + q;
    }

    function inRange(timeStr) {
      if (localFilter.range === 'all') return true;
      var d = parseWhen(timeStr);
      if (!d) return false;
      var now = new Date();
      if (localFilter.range === 'today') {
        return d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();
      }
      var days = localFilter.range === '7d' ? 7 : 30;
      return d.getTime() >= now.getTime() - days * 86400000;
    }

    function inLevel(status) {
      if (localFilter.level === 'all') return true;
      var n = Number(status);
      if (isNaN(n)) return false;
      if (localFilter.level === '2xx') return n >= 200 && n < 300;
      if (localFilter.level === '4xx') return n >= 400 && n < 500;
      return n >= 500;
    }

    function loadList() {
      card.innerHTML = '';
      card.appendChild(skeletonBlock([{ w: 'w40' }, { w: 'w80' }, { w: 'w80' }, { w: 'w60' }]));
      api('GET', currentUrl()).then(function (data) {
        data = data || {};
        lastTotal = (typeof data.total === 'number') ? data.total : Number(data.total) || 0;
        lastItems = Array.isArray(data.items) ? data.items : [];
        if (!lastItems.length && st.offset > 0 && lastTotal > 0) {
          st.offset = Math.max(0, Math.floor((lastTotal - 1) / st.limit) * st.limit); // offset 越界回调
          loadList();
          return;
        }
        renderTable();
      }).catch(function (e) {
        card.innerHTML = '';
        card.appendChild(errorNode(e, loadList));
      });
    }

    function renderTable() {
      card.innerHTML = '';
      if (!lastItems.length) {
        card.appendChild(emptyNode('暂无调用记录',
          st.keyId ? '当前筛选条件下还没有日志,试试切换密钥或点击刷新。' : '发起一次 /v1/index/check 调用后,这里会出现日志。'));
        return;
      }
      var visible = lastItems.filter(function (it) {
        it = it || {};
        return inLevel(it.status) && inRange(it.time);
      });
      if (!visible.length) {
        card.appendChild(el('div', { class: 'empty' },
          el('div', { class: 'empty-dot' },
            el('div', { class: 'empty-title', text: '当前筛选条件下无记录' }),
            el('div', { class: 'empty-desc', text: '级别与时间筛选仅作用于当前页数据,可切换筛选或翻页查看。' }))));
        return;
      }
      card.appendChild(buildUsageTable(sortItems(visible, sortState), sortState, function (key) {
        if (sortState.key === key) {
          sortState.dir = sortState.dir === 'asc' ? 'desc' : 'asc';
        } else {
          sortState.key = key;
          sortState.dir = 'asc';
        }
        renderTable();
      }));
      card.appendChild(buildPagination(lastTotal, lastItems.length, loadList));
    }

    function loadKeysForFilter() {
      api('GET', '/api/keys').then(function (data) {
        var keys = Array.isArray(data) ? data : (data && Array.isArray(data.items) ? data.items : []);
        state.keysCache = keys;
        var cur = st.keyId;
        filterSelect.innerHTML = '';
        filterSelect.appendChild(el('option', { value: '', text: '全部密钥' }));
        keys.forEach(function (k) {
          k = k || {};
          filterSelect.appendChild(el('option', {
            value: String(k.id),
            text: (k.name || '未命名') + '(' + (k.prefix || k.id) + ')'
          }));
        });
        filterSelect.value = cur;
        if (filterSelect.value !== cur) { // 选中项已不存在(密钥被删)则回全部
          filterSelect.value = '';
          st.keyId = '';
        }
      }).catch(function (e) {
        toast(e.message, 'error');
      });
    }

    loadKeysForFilter();
    loadList();
  }

  /* 使用日志表:全部列保留(时间/密钥/端点/引擎/URL 数/Units/HTTP 状态/耗时);行点击展开详情 */
  /* 使用日志列定义:8 列全部可排序(§4.5 可排序列名后置 ⇅ 图标)。
   * val() 取排序键:时间/数字取数值,文本列取展示字符串。 */
  var USAGE_COLS = [
    { key: 'time', label: '时间', num: false, val: function (it) { var d = parseWhen(it.time); return d ? d.getTime() : 0; } },
    { key: 'key', label: '密钥', num: false, val: function (it) { return String(it.key_name || ('#' + it.key_id) || ''); } },
    { key: 'endpoint', label: '端点', num: false, val: function (it) { return String(it.endpoint || ''); } },
    { key: 'engines', label: '引擎', num: false, val: function (it) { return fmtEngines(it.engines); } },
    { key: 'urls', label: 'URL 数', num: true, val: function (it) { return Number(it.urls) || 0; } },
    { key: 'units', label: 'Units', num: true, val: function (it) { return Number(it.units) || 0; } },
    { key: 'status', label: 'HTTP 状态', num: true, val: function (it) { return Number(it.status) || 0; } },
    { key: 'took_ms', label: '耗时', num: true, val: function (it) { return Number(it.took_ms) || 0; } }
  ];

  function usageColByKey(key) {
    for (var i = 0; i < USAGE_COLS.length; i++) {
      if (USAGE_COLS[i].key === key) return USAGE_COLS[i];
    }
    return null;
  }

  function sortItems(items, st) {
    var col = usageColByKey(st && st.key);
    if (!col) return items.slice();
    var dir = (st.dir === 'asc') ? 1 : -1;
    return items.slice().sort(function (a, b) {
      var va = col.val(a || {});
      var vb = col.val(b || {});
      var r = (typeof va === 'number' && typeof vb === 'number')
        ? (va - vb)
        : String(va).localeCompare(String(vb), 'zh-Hans-CN');
      if (r === 0) r = (parseWhen((a || {}).time) || 0) - (parseWhen((b || {}).time) || 0);
      return r * dir;
    });
  }

  function buildUsageTable(items, sortState, onSort) {
    var headCells = USAGE_COLS.map(function (col) {
      var active = sortState && sortState.key === col.key;
      var dir = active ? sortState.dir : null;
      var ic = active ? (dir === 'asc' ? '↑' : '↓') : '⇅';
      var th = el('th', {
        class: 'th-sort' + (col.num ? ' td-num-h' : ''),
        'aria-sort': active ? (dir === 'asc' ? 'ascending' : 'descending') : 'none',
        tabindex: '0',
        title: '点击按「' + col.label + '」排序',
        onclick: function () { if (typeof onSort === 'function') onSort(col.key); },
        onkeydown: function (ev) {
          if (ev && (ev.key === 'Enter' || ev.key === ' ')) {
            ev.preventDefault();
            if (typeof onSort === 'function') onSort(col.key);
          }
        }
      },
        el('span', { text: col.label }),
        el('span', { class: 'sort-ic', 'aria-hidden': 'true', text: ic }));
      return th;
    });
    var thead = el('thead', null, el('tr', null, headCells));
    var tbody = el('tbody');
    items.forEach(function (it) {
      it = it || {};
      var tr = el('tr', { class: 'row-click', title: '点击展开请求详情' },
        el('td', { class: 'td-mono', text: fmtDateTime(it.time) }),
        el('td', { class: 'td-strong', text: it.key_name || ('#' + it.key_id) }),
        el('td', null, el('span', { class: 'chip', text: it.endpoint || '-' })),
        el('td', { text: fmtEngines(it.engines) }),
        el('td', { class: 'td-num', text: fmtNumber(it.urls) }),
        el('td', { class: 'td-num', text: fmtNumber(it.units) }),
        el('td', null, statusPill(it.status)),
        el('td', { class: 'td-num', text: fmtDuration(it.took_ms) }));
      var detailTr = el('tr', { class: 'detail-row hidden' },
        el('td', { colspan: '8' }));
      tr.addEventListener('click', function () {
        var open = detailTr.classList.contains('hidden');
        detailTr.classList.toggle('hidden', !open);
        tr.classList.toggle('row-open', open);
        if (open && !detailTr.firstChild.firstChild) {
          detailTr.firstChild.appendChild(buildUsageDetail(it));
        }
      });
      tbody.appendChild(tr);
      tbody.appendChild(detailTr);
    });
    return el('div', { class: 'table-wrap' }, el('table', { class: 'table' }, thead, tbody));
  }

  /* 行展开详情:终端卡呈现该条日志记录的全部契约字段(§4.6) */
  function buildUsageDetail(it) {
    var record = {
      time: it.time || null,
      key_id: it.key_id || '',
      key_name: it.key_name || '',
      endpoint: it.endpoint || '',
      engines: Array.isArray(it.engines) ? it.engines : [],
      urls: Number(it.urls) || 0,
      units: Number(it.units) || 0,
      status: Number(it.status) || 0,
      took_ms: Number(it.took_ms) || 0
    };
    var statusCls = 'term-status';
    var n = Number(it.status);
    if (!isNaN(n)) {
      if (n >= 200 && n < 300) statusCls += ' ok';
      else if (n >= 400) statusCls += ' err';
    }
    return el('div', { class: 'term detail-term' },
      el('div', { class: 'term-head' },
        el('div', { class: 'term-dots', 'aria-hidden': 'true' }, el('i'), el('i'), el('i')),
        el('span', { class: 'code-lang', text: 'JSON · 日志记录' })),
      el('pre', { class: 'term-pre', html: highlightJson(JSON.stringify(record, null, 2)) }),
      el('div', { class: 'term-foot' },
        el('span', { text: String(it.endpoint || '/v1/index/check') }),
        el('span', { class: statusCls }, el('i'), el('span', { text: isNaN(n) ? '—' : n + ' ' + httpReason(n) }))));
  }

  function httpReason(n) {
    if (n >= 200 && n < 300) return 'OK';
    if (n === 400) return 'Bad Request';
    if (n === 401) return 'Unauthorized';
    if (n === 403) return 'Blocked';
    if (n === 404) return 'Not Found';
    if (n === 429) return 'Too Many Requests';
    if (n === 500) return 'Internal Server Error';
    if (n === 502) return 'Bad Gateway';
    if (n === 503) return 'Service Unavailable';
    if (n === 504) return 'Gateway Timeout';
    return '';
  }

  function statusPill(v) {
    if (v === null || v === undefined || v === '' || isNaN(Number(v))) {
      return el('span', { class: 'muted', text: '—' });
    }
    var n = Number(v);
    var cls = 'pill pill-gray';
    if (n >= 200 && n < 300) cls = 'pill pill-green';
    else if (n >= 400 && n < 500) cls = 'pill pill-amber';
    else if (n >= 500) cls = 'pill pill-red';
    return el('span', { class: cls, text: String(n) });
  }

  function buildPagination(total, count, reload) {
    var st = state.usage;
    var from = count > 0 ? st.offset + 1 : st.offset;
    var to = st.offset + count;

    var prevBtn = el('button', { class: 'btn btn-secondary btn-sm', type: 'button', text: '上一页' });
    prevBtn.disabled = st.offset <= 0;
    prevBtn.addEventListener('click', function () {
      st.offset = Math.max(0, st.offset - st.limit);
      reload();
    });
    var nextBtn = el('button', { class: 'btn btn-secondary btn-sm', type: 'button', text: '下一页' });
    nextBtn.disabled = to >= total;
    nextBtn.addEventListener('click', function () {
      st.offset = st.offset + st.limit;
      reload();
    });

    return el('div', { class: 'pagination' },
      el('span', { class: 'page-info', text: '第 ' + from + '–' + to + ' 条,共 ' + fmtNumber(total) + ' 条' }),
      prevBtn,
      nextBtn);
  }

  /* JSON 语法高亮:先转义 HTML 再包 span(数据来自 JSON.stringify,无注入面) */
  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }

  function highlightJson(json) {
    var escaped = escapeHtml(json);
    // 字符串(键名后置冒号区分 key/value)/ 数字 / 字面量
    escaped = escaped.replace(/&quot;(?:[^&]|&(?!quot;))*&quot;(\s*:)?/g, function (m, colon) {
      if (colon) return '<span class="jx-key">' + m + '</span>';
      return '<span class="jx-str">' + m + '</span>';
    });
    escaped = escaped.replace(/\b(-?\d+(?:\.\d+)?)\b/g, '<span class="jx-num">$1</span>');
    escaped = escaped.replace(/\b(true|false)\b/g, '<span class="jx-bool">$1</span>');
    escaped = escaped.replace(/\bnull\b/g, '<span class="jx-null">null</span>');
    return escaped;
  }

  /* ================= 8. 页面:在线调试 ================= */

  function pagePlayground(root) {
    var pgState = { view: 'table', lastData: null, lastReq: null, lastStatus: null };

    /* 左:参数表单(仅真实控件:urls + engines,请求体 { urls, engines }) */
    var urlsTa = el('textarea', {
      class: 'input pg-textarea',
      'aria-label': 'URL 列表',
      placeholder: '每行一个 URL,最多 20 条\nhttps://example.com/blog/post-1\nhttps://example.com/blog/post-2'
    });
    var urlCount = el('span', { class: 'pg-count-num', text: '0 / 20' });
    var countHint = el('span', { text: '0 URL × 4 引擎 = 0 unit' });
    var countRow = el('div', { class: 'pg-count' }, countHint, urlCount);

    function checkedEngineCount() {
      var n = 0;
      checksBox.querySelectorAll('input[type="checkbox"]').forEach(function (cb) {
        if (cb.checked) n++;
      });
      return n;
    }
    function updateCount() {
      var lines = urlsTa.value.split('\n').filter(function (l) { return l.trim() !== ''; });
      var ec = checkedEngineCount();
      urlCount.textContent = lines.length + ' / 20';
      var over = lines.length > 20;
      urlCount.className = 'pg-count-num' + (over ? ' over' : '');
      urlCount.parentElement.classList.toggle('over', over);
      countHint.textContent = lines.length + ' URL × ' + ec + ' 引擎 = ' + (lines.length * ec) + ' unit';
    }
    urlsTa.addEventListener('input', updateCount);

    var checksBox = el('div', { class: 'pg-checks' });
    ENGINE_ORDER.forEach(function (name) {
      var cb = el('input', { type: 'checkbox', value: name });
      cb.checked = true;
      checksBox.appendChild(el('label', { class: 'pg-check' }, cb, el('span', { text: ENGINE_LABEL[name] })));
    });
    checksBox.addEventListener('change', updateCount);
    updateCount();

    var formErr = el('div', { class: 'form-err hidden', role: 'alert' });
    function showFormErr(msg) {
      formErr.textContent = msg;
      formErr.classList.remove('hidden');
    }
    function hideFormErr() {
      formErr.classList.add('hidden');
    }

    var submitBtn = el('button', {
      class: 'btn btn-primary btn-block', type: 'button', text: '开始检测'
    });
    var curlBtn = el('button', {
      class: 'btn btn-outline btn-block', type: 'button',
      html: ICONS.copy + '<span>复制等价 curl</span>'
    });

    var formCard = el('div', { class: 'card pg-form-card' },
      el('div', { class: 'card-head' }, el('h2', { class: 'card-title', text: '收录判定' })),
      el('div', { class: 'card-body' },
        el('div', { class: 'field' },
          el('label', { class: 'field-label', text: 'URL 列表(每行一个,1–20 条)' }),
          urlsTa,
          countRow),
        el('div', { class: 'field' },
          el('label', { class: 'field-label', text: '检测引擎' }),
          checksBox,
          el('div', { class: 'form-hint', text: '平台调试接口不计费;响应结构与 /v1/index/check 完全一致。' })),
        formErr,
        submitBtn,
        el('div', { style: 'height:8px;' }),
        curlBtn));

    /* 右:终端卡(§4.6:三圆点 + pill Tab + 主体 + 底部状态条) */
    var segTable = el('button', { class: 'term-tab on', type: 'button', text: '表格' });
    var segJson = el('button', { class: 'term-tab', type: 'button', text: 'JSON' });
    segTable.addEventListener('click', function () { switchView('table'); });
    segJson.addEventListener('click', function () { switchView('json'); });

    var termHead = el('div', { class: 'term-head' },
      el('div', { style: 'display:flex;align-items:center;gap:12px;' },
        el('div', { class: 'term-dots', 'aria-hidden': 'true' }, el('i'), el('i'), el('i')),
        el('span', { class: 'code-lang', text: '检测结果' })),
      el('div', { class: 'term-tabs' }, segTable, segJson));
    var termBody = el('div', { class: 'card-body' });
    var footPath = el('span', { text: 'POST /web-tool/api/playground/check' });
    var footStatus = el('span', { class: 'term-status' }, el('i'), el('span', { text: '待发起' }));
    var termFoot = el('div', { class: 'term-foot' }, footPath, footStatus);
    var resultCard = el('div', { class: 'term' }, termHead, termBody, termFoot);
    termBody.appendChild(emptyNode('尚未发起检测', '在左侧填写 URL 并勾选引擎,点击「开始检测」。查询会真实访问各引擎的搜索结果页,单次 1–20 条 URL。'));

    var layout = el('div', { class: 'pg-layout' }, formCard, resultCard);
    root.appendChild(layout);

    function setFootStatus(status) {
      var cls = 'term-status';
      if (status === 200) cls += ' ok';
      else if (typeof status === 'number') cls += ' err';
      footStatus.className = cls;
      footStatus.innerHTML = '';
      footStatus.appendChild(el('i'));
      footStatus.appendChild(el('span', {
        text: typeof status === 'number' ? (status + ' ' + httpReason(status)) : '待发起'
      }));
    }

    function switchView(v) {
      pgState.view = v;
      segTable.className = 'term-tab' + (v === 'table' ? ' on' : '');
      segJson.className = 'term-tab' + (v === 'json' ? ' on' : '');
      if (pgState.lastData) renderResult();
    }

    /* 复制等价 curl:与 /web-tool/v1/index/check 同参(X-API-Key 占位) */
    function buildCurl() {
      var req = pgState.lastReq || { urls: [], engines: [] };
      var origin = pageOrigin();
      return 'curl -X POST "' + origin + '/web-tool/v1/index/check" \\\n' +
        '  -H "X-API-Key: sk-w2a-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \\\n' +
        '  -H "Content-Type: application/json" \\\n' +
        '  -d \'' + JSON.stringify({ urls: req.urls, engines: req.engines }) + '\'';
    }
    curlBtn.addEventListener('click', function () {
      if (!pgState.lastReq) {
        showFormErr('请先发起一次检测,再复制对应的 curl。');
        return;
      }
      copyText(buildCurl());
    });

    function parseUrls() {
      return urlsTa.value.split('\n')
        .map(function (l) { return l.trim(); })
        .filter(function (l) { return l !== ''; });
    }

    function selectedEngines() {
      var out = [];
      checksBox.querySelectorAll('input[type="checkbox"]').forEach(function (cb) {
        if (cb.checked) out.push(cb.value);
      });
      return out;
    }

    submitBtn.addEventListener('click', function () {
      hideFormErr();
      var urls = parseUrls();
      var engines = selectedEngines();
      if (!urls.length) {
        showFormErr('请至少输入 1 条 URL(每行一个)。');
        urlsTa.focus();
        return;
      }
      if (urls.length > 20) {
        showFormErr('单次最多 20 条 URL(平台契约上限),当前 ' + urls.length + ' 条。');
        return;
      }
      if (!engines.length) {
        showFormErr('请至少勾选 1 个检测引擎。');
        return;
      }
      pgState.lastReq = { urls: urls, engines: engines.slice() };
      termBody.innerHTML = '';
      termBody.appendChild(skeletonBlock([{ w: 'w30' }, { big: true, w: 'w50' }, { w: 'w80' }, { w: 'w70' }]));
      setFootStatus(null);
      setBtnLoading(submitBtn, true, '检测中…');
      api('POST', '/api/playground/check', { urls: urls, engines: engines })
        .then(function (data) {
          pgState.lastData = data || {};
          pgState.lastStatus = 200;
          setFootStatus(200);
          renderResult();
        })
        .catch(function (e) {
          pgState.lastStatus = typeof e.status === 'number' ? e.status : 0;
          setFootStatus(pgState.lastStatus);
          termBody.innerHTML = '';
          termBody.appendChild(errorNode(e, function () { submitBtn.click(); }));
        })
        .then(function () {
          setBtnLoading(submitBtn, false);
        });
    });

    /* 结果渲染:表格 / JSON 双视图 */
    function renderResult() {
      var data = pgState.lastData || {};
      termBody.innerHTML = '';
      termBody.style.padding = '';

      var metaBits = [];
      if (data.meta) {
        if (data.meta.request_id) metaBits.push('request_id ' + data.meta.request_id);
        if (data.meta.took_ms !== undefined) metaBits.push('耗时 ' + fmtDuration(data.meta.took_ms));
        if (data.meta.units !== undefined) metaBits.push('units ' + data.meta.units);
      }
      var metaRow = el('div', { class: 'pg-meta', style: 'margin-bottom:14px;' });
      metaBits.forEach(function (b) {
        metaRow.appendChild(el('span', { class: 'mchip', text: b }));
      });
      var cdList = (data.meta && Array.isArray(data.meta.engines_cooldown)) ? data.meta.engines_cooldown : [];
      cdList.forEach(function (cd) {
        metaRow.appendChild(el('span', {
          class: 'mchip warn',
          title: '冷却至 ' + fmtDateTime(cd.until),
          text: (ENGINE_LABEL[cd.engine] || cd.engine) + ' 冷却中 · 至 ' + fmtDateTime(cd.until)
        }));
      });
      if (metaBits.length || cdList.length) termBody.appendChild(metaRow);

      var results = Array.isArray(data.results) ? data.results : [];
      if (pgState.view === 'json') {
        termBody.innerHTML = '';
        termBody.style.padding = '0';
        termBody.appendChild(el('pre', {
          class: 'term-pre', style: 'max-height:560px;border-radius:0 0 var(--radius-xl) var(--radius-xl);',
          html: highlightJson(JSON.stringify(data, null, 2))
        }));
        return;
      }
      if (!results.length) {
        termBody.appendChild(emptyNode('无结果', '服务端返回了空结果列表。'));
        return;
      }

      var thead = el('thead', null, el('tr', null,
        el('th', { text: 'URL' }),
        el('th', { text: '引擎' }),
        el('th', { text: '收录' }),
        el('th', { class: 'td-nowrap', text: '匹配' }),
        el('th', { text: '命中地址' }),
        el('th', { class: 'td-num-h', text: '结果数' }),
        el('th', { class: 'td-num-h', text: '耗时' })));
      var tbody = el('tbody');
      results.forEach(function (r) {
        var enginesMap = (r && r.engines && typeof r.engines === 'object') ? r.engines : {};
        var names = Object.keys(enginesMap).sort(function (a, b) {
          var ia = ENGINE_ORDER.indexOf(a); if (ia === -1) ia = 99;
          var ib = ENGINE_ORDER.indexOf(b); if (ib === -1) ib = 99;
          return ia - ib;
        });
        names.forEach(function (name, idx) {
          var o = enginesMap[name] || {};
          var urlTd = el('td', { class: 'td-mono', title: r.url || '', text: truncate(r.url || '', 46) });
          if (idx > 0) urlTd.setAttribute('style', 'color:transparent;user-select:none;');
          var engineTd = el('td', null, el('span', { class: 'chip', text: name }));

          var statusTd, matchTd, hitTd, totalTd, tookTd;
          var errObj = (o.error && typeof o.error === 'object') ? o.error : null;
          if (errObj) {
            var code = String(errObj.code || '');
            var msg = String(errObj.message || '');
            if (code === 'engine_cooldown') {
              statusTd = el('td', null, el('div', { class: 'cell-cd' },
                el('div', null, el('span', { class: 'ecode', text: '引擎冷却中' })),
                msg ? el('div', { class: 'emsg', text: msg }) : null));
            } else {
              statusTd = el('td', null, el('div', { class: 'cell-err' },
                el('div', null, el('span', { class: 'ecode', text: code || 'error' })),
                msg ? el('div', { class: 'emsg', text: truncate(msg, 60) }) : null));
            }
            matchTd = el('td', { class: 'muted', text: '—' });
            hitTd = el('td', { class: 'muted', text: '—' });
            totalTd = el('td', { class: 'td-num muted', text: '—' });
            tookTd = el('td', { class: 'td-num muted', text: '—' });
          } else {
            var indexed = !!o.indexed;
            statusTd = el('td', null, el('span', {
              class: indexed ? 'pill pill-green' : 'pill pill-gray',
              text: indexed ? '已收录' : '未收录'
            }));
            matchTd = el('td', { class: 'td-nowrap', text: o.match === 'exact' ? '精确' : (o.match === 'none' ? '未命中' : String(o.match || '—')) });
            hitTd = el('td', { class: 'td-mono', title: o.matched_result_url || '', text: o.matched_result_url ? truncate(o.matched_result_url, 34) : '—' });
            totalTd = el('td', { class: 'td-num', text: (typeof o.total_results === 'number') ? String(o.total_results) : '—' });
            tookTd = el('td', { class: 'td-num', text: fmtDuration(o.took_ms) });
          }
          tbody.appendChild(el('tr', null, urlTd, engineTd, statusTd, matchTd, hitTd, totalTd, tookTd));
        });
      });
      termBody.appendChild(el('div', { class: 'table-wrap' }, el('table', { class: 'table' }, thead, tbody)));
    }

    function truncate(s, n) {
      s = String(s || '');
      return s.length > n ? s.slice(0, n - 1) + '…' : s;
    }
  }

  /* ================= 8b. 页面:Agent 集成 ================= */

  function pageOrigin() {
    return (location.origin && location.origin.indexOf('http') === 0)
      ? location.origin
      : 'http://localhost:7000';
  }

  function pageAgents(root) {
    var ORIGIN = pageOrigin();
    var BASE = ORIGIN + '/web-tool';
    var DEMO_KEY = 'sk-w2a-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx';
    var MCP_URL = ORIGIN + '/web-tool/mcp';
    var OPENAPI_URL = ORIGIN + '/web-tool/openapi.yaml';
    var LLMS_URL = ORIGIN + '/web-tool/llms.txt';
    var HOST_NOTE = '若 Agent 与面板不在同一台机器,请将地址中的主机名替换为本服务器的公网地址(域名或 IP)。';

    /* 卡头 ghost 钮:复制该卡接入所需的 base_url */
    function copyUrlBtn(label, url) {
      return el('button', {
        class: 'btn btn-ghost btn-sm', type: 'button',
        html: ICONS.copy + '<span>' + label + '</span>',
        onclick: function () { copyText(url); }
      });
    }

    /* ---- 概述与鉴权 ---- */
    root.appendChild(el('div', { class: 'card' },
      el('div', { class: 'card-head' },
        el('h2', { class: 'card-title' }, 'MCP 接入'),
        copyUrlBtn('复制 base_url', BASE)),
      el('div', { class: 'card-body' },
        p('Web2API 内置 <strong>MCP(Model Context Protocol)Streamable HTTP</strong> 端点,AI Agent(Claude Desktop / Claude Code / Dify / Coze / n8n / LangChain 等)可作为一等公民直接调用收录查询与搜索能力,无需自行封装 HTTP。'),
        p('端点:<code>' + MCP_URL + '</code>(JSON-RPC 2.0;协议版本 <code>2025-06-18</code>;支持 <code>initialize / notifications/initialized / tools/list / tools/call / ping</code>,服务端返回的 <code>Mcp-Session-Id</code> 响应头需在后续请求中原样带回)。'),
        el('div', { class: 'auth-warn', html: ICONS.warn + '<span><b>鉴权必须显式配置:</b>所有请求需携带 <code>X-API-Key</code> 请求头(计费,1 次 web_search = 1 unit;check_indexed = URL 数 × 引擎数 units)或 <code>Authorization: Bearer &lt;管理员 JWT&gt;</code>(管理员不计费)。<b>缺省密钥的调用一律返回 401 {"error":"unauthorized"}</b>。请先在「API 密钥」页创建密钥。</span>' }),
        el('div', { class: 'agent-links' },
          el('a', { href: OPENAPI_URL, target: '_blank', rel: 'noopener noreferrer', html: ICONS.external + '<span>openapi.yaml(OpenAPI 3.1 全端点)</span>' }),
          el('a', { href: LLMS_URL, target: '_blank', rel: 'noopener noreferrer', html: ICONS.external + '<span>llms.txt(Agent 友好简介)</span>' })),
        el('div', { class: 'docs-note', text: HOST_NOTE }))));

    /* ---- 工具清单 ---- */
    root.appendChild(el('div', { class: 'card' },
      el('div', { class: 'card-head' }, el('h2', { class: 'card-title', text: 'MCP 工具清单' })),
      el('div', { class: 'card-body' },
        paramTable([
          ['web_search', 'object', 'query 必填', '{ query, engine?("baidu|sogou|so360|bing",默认 baidu), limit?(默认 10) } → { results:[{rank,title,url,snippet}] };计费 1 unit/次'],
          ['check_indexed', 'object', 'urls 必填', '{ urls[1..20], engines? } → 与 /v1/index/check 相同的 results 结构;计费 = URL 数 × 引擎数 units'],
          ['get_quota', 'object', '—', '{} → 当前密钥的配额与用量'],
          ['list_engines', 'object', '—', '{} → 引擎清单与冷却 / 熔断状态']
        ]),
        p('提示:引擎冷却期间,check_indexed 对应引擎单元会返回 <code>engine_cooldown</code> 错误;可先用 <code>list_engines</code> 判断可用性。'))));

    /* ---- Claude Desktop / Claude Code ---- */
    root.appendChild(agentCard('Claude Desktop / Claude Code', [
      '打开 Claude Desktop「设置 → 开发者 → 编辑配置」(或项目根目录的 <code>.mcp.json</code>)。',
      '将下方 JSON 合并进 <code>mcpServers</code>;headers 中的 <code>X-API-Key</code> 替换为你的密钥。',
      '重启 Claude Desktop,工具面板出现 web_search / check_indexed / get_quota / list_engines 即接入成功。',
      '注意:未配置 <code>X-API-Key</code> 的调用一律返回 401 <code>{"error":"unauthorized"}</code>(JSON 不支持注释,勿在配置文件内加注释)。'
    ], 'JSON · claude_desktop_config.json', `{
  "mcpServers": {
    "web2api": {
      "url": "${MCP_URL}",
      "headers": {
        "X-API-Key": "${DEMO_KEY}"
      }
    }
  }
}`, MCP_URL));

    /* ---- Dify ---- */
    root.appendChild(agentCard('Dify', [
      '进入「工具 → 自定义 → 创建自定义工具」。'
    ], '配置 · Dify 自定义工具', `# Dify → 工具 → 创建自定义工具 → 导入 OpenAPI Schema
# 1. Schema:打开 ${ORIGIN}/web-tool/openapi.yaml 全选复制粘贴,
#    或直接填写 Schema URL:${ORIGIN}/web-tool/openapi.yaml
# 2. 鉴权方式:API Key
#    类型(Type):Header
#    名称(Key):X-API-Key
#    值(Value):${DEMO_KEY}
# 3. 导入后可用工具:web_search / check_indexed / get_quota / list_engines
# 注意:未配置 X-API-Key 的调用一律返回 401 {"error":"unauthorized"}`, OPENAPI_URL));

    /* ---- Coze ---- */
    root.appendChild(agentCard('Coze', [
      '进入「插件 → 创建插件」,选择「通过 URL 导入 OpenAPI 3.1」。'
    ], '配置 · Coze 插件', `# Coze → 插件 → 创建插件 → 导入 OpenAPI 3.1
# 1. 导入方式:URL 导入,填写 ${ORIGIN}/web-tool/openapi.yaml
# 2. 硬性要求:每个接口必须有唯一 operationId;servers 必须是完整域名
#    (本平台 openapi.yaml 的 servers 为 ${ORIGIN},接口路径统一带 /web-tool 前缀)
# 3. 鉴权:插件「授权方式」选择 API Key
#    位置:Header;名称:X-API-Key;值:${DEMO_KEY}
# 注意:未配置 X-API-Key 的调用一律返回 401 {"error":"unauthorized"}`, OPENAPI_URL));

    /* ---- n8n ---- */
    root.appendChild(agentCard('n8n', [
      '工作流中添加「HTTP Request」节点(或「MCP Client Tool」节点)。'
    ], '配置 · n8n HTTP Request', `# n8n → HTTP Request 节点
Method:          POST
URL:             ${ORIGIN}/web-tool/v1/index/check
Authentication:  Generic Credential Type → Header Auth
  Name:          X-API-Key
  Value:         ${DEMO_KEY}
Body Content Type: JSON
JSON Body:
  {
    "urls": ["https://example.com/blog/post-1"],
    "engines": ["baidu", "bing"]
  }

# 或使用 MCP Client Tool 节点直连 MCP 端点:
#   Endpoint:      ${MCP_URL}
#   Transport:     HTTP Streamable
#   Authentication: Header Auth → Name: X-API-Key,Value: 同上
# 注意:未配置 X-API-Key 的调用一律返回 401 {"error":"unauthorized"}`, BASE));

    /* ---- LangChain Python ---- */
    root.appendChild(agentCard('LangChain / Python', [
      'HTTP 方式:用 requests 直接调用 REST 端点;MCP 方式:用官方 <code>mcp</code> SDK 连接 Streamable HTTP。'
    ], 'Python · requests + MCP SDK', `import requests

BASE = "${ORIGIN}/web-tool"  # 注意:所有端点都带 /web-tool 前缀
HEADERS = {"X-API-Key": "${DEMO_KEY}"}  # 未携带将返回 401 {"error":"unauthorized"}

resp = requests.post(
    BASE + "/v1/index/check",
    headers=HEADERS,
    json={"urls": ["https://example.com/blog/post-1"], "engines": ["baidu", "bing"]},
)
print(resp.json()["results"][0]["engines"]["baidu"])

# MCP 方式(mcp 官方 Python SDK):
from mcp import ClientSession
from mcp.client.streamable_http import streamablehttp_client


async def run():
    async with streamablehttp_client(
        "${MCP_URL}",
        headers=HEADERS,  # MCP 端点同样要求 X-API-Key
    ) as (read, write, _):
        async with ClientSession(read, write) as session:
            await session.initialize()
            tools = await session.list_tools()
            print(tools)
            result = await session.call_tool(
                "check_indexed",
                {"urls": ["https://example.com/blog/post-1"], "engines": ["baidu"]},
            )
            print(result)`, BASE));

    function agentCard(title, steps, lang, code, copyUrl) {
      var list = el('ol', { class: 'agent-steps' });
      steps.forEach(function (s) {
        var li = el('li');
        li.innerHTML = s; // 受控静态文案
        list.appendChild(li);
      });
      var head = el('div', { class: 'card-head' }, el('h2', { class: 'card-title', text: title }));
      if (copyUrl) head.appendChild(copyUrlBtn('复制 base_url', copyUrl));
      return el('div', { class: 'card' }, head, el('div', { class: 'card-body' }, list, codeBlock(lang, code)));
    }
  }

  /* ================= 8c. 页面:API 文档 ================= */

  function docSection(opts) {
    var head = el('div', { class: 'card-head' });
    var h2 = el('h2', { class: 'docs-h2' });
    if (opts.method) h2.appendChild(methodBadge(opts.method));
    h2.appendChild(el('span', { text: opts.title }));
    head.appendChild(h2);
    if (opts.sub) head.appendChild(el('span', { class: 'card-head-sub', text: opts.sub }));
    if (opts.id) head.setAttribute('id', opts.id);
    var body = el('div', { class: 'card-body' });
    (opts.children || []).forEach(function (c) { if (c) body.appendChild(c); });
    return el('div', { class: 'card docs-section' }, head, body);
  }

  function methodBadge(m) {
    var up = String(m).toUpperCase();
    return el('span', { class: 'method-tag' + (up === 'GET' ? ' get' : ''), text: up });
  }

  function p(html) {
    var d = el('div', { class: 'docs-p' });
    d.innerHTML = html; // 仅受控静态文案
    return d;
  }

  function ulHtml(items) {
    var u = el('ul', { class: 'docs-ul' });
    items.forEach(function (t) {
      u.appendChild(el('li', { class: 'docs-li' }));
      u.lastChild.innerHTML = t; // 受控静态文案
    });
    return u;
  }

  function paramTable(rows) {
    var tbody = el('tbody');
    rows.forEach(function (r) {
      var tdDesc = el('td');
      tdDesc.innerHTML = r[3]; // 受控静态文案
      tbody.appendChild(el('tr', null,
        el('td', null, el('code', { text: r[0] })),
        el('td', { text: r[1] }),
        el('td', { text: r[2] }),
        tdDesc));
    });
    return el('table', { class: 'param-table' },
      el('thead', null, el('tr', null,
        el('th', { text: '参数' }), el('th', { text: '类型' }), el('th', { text: '必填' }), el('th', { text: '说明' }))),
      tbody);
  }

  function fieldTable(rows) {
    var tbody = el('tbody');
    rows.forEach(function (r) {
      var tdDesc = el('td');
      tdDesc.innerHTML = r[1]; // 受控静态文案
      tbody.appendChild(el('tr', null, el('td', null, el('code', { text: r[0] })), tdDesc));
    });
    return el('table', { class: 'param-table' },
      el('thead', null, el('tr', null, el('th', { text: '字段' }), el('th', { text: '说明' }))),
      tbody);
  }

  /* 错误码表(HTTP / error / 说明,三列) */
  function errTable(rows) {
    var tbody = el('tbody');
    rows.forEach(function (r) {
      var tdDesc = el('td');
      tdDesc.innerHTML = r[2]; // 受控静态文案
      tbody.appendChild(el('tr', null,
        el('td', null, el('code', { text: r[0] })),
        el('td', null, el('code', { text: r[1] })),
        tdDesc));
    });
    return el('table', { class: 'param-table' },
      el('thead', null, el('tr', null,
        el('th', { text: 'HTTP' }), el('th', { text: 'error / code' }), el('th', { text: '说明' }))),
      tbody);
  }

  function codeBlock(lang, code) {
    var pre = el('pre', { class: 'code-pre' });
    pre.textContent = code;
    var copy = el('button', {
      class: 'code-copy', type: 'button',
      html: ICONS.copy + '<span>复制</span>',
      onclick: function () { copyText(code); }
    });
    return el('div', { class: 'code-block' },
      el('div', { class: 'code-head' },
        el('span', { class: 'code-lang' },
          el('span', { class: 'term-dots', 'aria-hidden': 'true' }, el('i'), el('i'), el('i')),
          el('span', { text: lang })),
        copy),
      pre);
  }

  function pageDocs(pageRoot) {
    var ORIGIN = pageOrigin();
    var BASE = ORIGIN + '/web-tool';
    var DEMO_KEY = 'sk-w2a-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx';

    /* 两栏:左目录(激活=--fg + --accent 底)+ 右 prose;目录锚点滚动 + scrollspy */
    var NAVS = [
      ['docs-overview', '概述'],
      ['docs-auth', '鉴权'],
      ['docs-index-post', 'POST /v1/index/check'],
      ['docs-index-get', 'GET /v1/index/check'],
      ['docs-quota', 'GET /v1/quota'],
      ['docs-engines', 'GET /v1/engines'],
      ['docs-mcp', 'MCP /web-tool/mcp'],
      ['docs-admin', '管理 API 一览'],
      ['docs-ratelimit', '限流与冷却'],
      ['docs-errors', '错误码'],
      ['docs-assets', 'Agent 静态资源']
    ];
    var tocLinks = [];
    var toc = el('nav', { class: 'docs-toc', 'aria-label': '文档目录' });
    NAVS.forEach(function (n) {
      var a = el('a', {
        href: 'javascript:void(0)',
        text: n[1],
        onclick: function () {
          setActiveToc(a);
          var target = document.getElementById(n[0]);
          // 瞬时定位:平滑滚动在部分环境/长文档下会被截断,锚点跳转取即时语义
          if (target) target.scrollIntoView({ behavior: 'auto', block: 'start' });
        }
      });
      tocLinks.push({ link: a, id: n[0] });
      toc.appendChild(a);
    });

    function setActiveToc(active) {
      tocLinks.forEach(function (t) { t.link.classList.toggle('on', t.link === active); });
    }
    function syncTocScroll() {
      var best = null;
      var bestTop = -Infinity;
      tocLinks.forEach(function (t) {
        var node = document.getElementById(t.id);
        if (!node) return;
        var top = node.getBoundingClientRect().top;
        if (top <= 120 && top > bestTop) {
          bestTop = top;
          best = t;
        }
      });
      if (best) best.link.classList.add('on');
      tocLinks.forEach(function (t) {
        if (t !== best) t.link.classList.remove('on');
      });
    }
    setPageInterval(syncTocScroll, 400); // 轻量 scrollspy,离开页面时随 pageTimers 清理

    var main = el('div', { class: 'docs-main' });
    pageRoot.appendChild(el('div', { class: 'docs-layout' }, toc, main));
    var root = main; // 章节统一注入右栏

    /* ---- 概述 ---- */
    root.appendChild(docSection({
      id: 'docs-overview',
      title: '概述',
      children: [
        p('Web2API 是<strong>单二进制自托管的「网站收录查询开放平台」</strong>:提交文章 / 页面 URL 列表,查询百度(baidu)、搜狗(sogou)、360(so360)、必应(bing)四大搜索引擎是否已收录对应页面;同时提供国内四引擎通用搜索与 MCP Agent 端点。引擎级失败(反爬拦截 / 验证码 / 熔断 / 冷却)不会拖垮整体响应,对应引擎项返回 error,HTTP 仍为 200。'),
        p('Base URL(固定前缀,不可配置):<code>' + BASE + '</code>。下文示例均以该地址为例,请替换为你的实际地址与密钥。所有端点清单与可导入的 OpenAPI 3.1 描述见文末「Agent 静态资源」。')
      ]
    }));

    /* ---- 鉴权 ---- */
    root.appendChild(docSection({
      id: 'docs-auth',
      title: '鉴权(X-API-Key / JWT)',
      children: [
        p('公网 API(/v1/*)与 MCP 使用 API 密钥(在「API 密钥」页创建);管理 API(/api/*)使用管理员 JWT(面板登录获得)。'),
        ulHtml([
          '推荐:<code>X-API-Key: sk-w2a-…</code>',
          '兼容:<code>Authorization: Bearer sk-w2a-…</code>',
          '无效或被禁用的密钥返回 <code>401 {"error":"unauthorized"}</code>;',
          '当日配额用尽返回 <code>429 {"error":"quota_exceeded"}</code>,响应头携带 <code>Retry-After</code>(次日零点自动恢复)。'
        ]),
        codeBlock('cURL', 'curl "' + BASE + '/v1/quota" \\\n  -H "X-API-Key: ' + DEMO_KEY + '"')
      ]
    }));

    /* ---- POST /v1/index/check ---- */
    root.appendChild(docSection({
      id: 'docs-index-post',
      method: 'POST',
      title: '/v1/index/check',
      sub: '批量查询收录状态',
      children: [
        p('提交 1–20 条 URL,查询各引擎收录情况。计费按 unit:1 URL × 1 引擎 = 1 unit。'),
        paramTable([
          ['urls', 'array[string]', '是', '待查询 URL 列表,1–20 条'],
          ['engines', 'array[string]', '否', '引擎列表,默认 <code>["baidu","sogou","so360","bing"]</code>;接受别名 <code>"360"</code>(等同 so360)']
        ]),
        codeBlock('cURL',
          'curl -X POST "' + BASE + '/v1/index/check" \\\n' +
          '  -H "X-API-Key: ' + DEMO_KEY + '" \\\n' +
          '  -H "Content-Type: application/json" \\\n' +
          "  -d '{\n" +
          '    "urls": [\n' +
          '      "https://example.com/blog/post-1",\n' +
          '      "https://example.com/blog/post-2"\n' +
          '    ],\n' +
          '    "engines": ["baidu", "sogou", "so360", "bing"]\n' +
          "  }'"),
        codeBlock('JavaScript · fetch',
          'const resp = await fetch("' + BASE + '/v1/index/check", {\n' +
          '  method: "POST",\n' +
          '  headers: {\n' +
          '    "X-API-Key": "' + DEMO_KEY + '",\n' +
          '    "Content-Type": "application/json"\n' +
          '  },\n' +
          '  body: JSON.stringify({\n' +
          '    urls: ["https://example.com/blog/post-1"],\n' +
          '    engines: ["baidu", "bing"] // 可选,默认四引擎\n' +
          '  })\n' +
          '});\n' +
          'const data = await resp.json();\n' +
          'console.log(data.results[0].engines.baidu.indexed); // true / false'),
        p('<strong>响应示例</strong>'),
        codeBlock('JSON · 200', '{\n' +
          '  "id": "b7e2f9a0-3c4d-4e5f-8a1b-2c3d4e5f6a7b",\n' +
          '  "created_at": "2026-11-15T10:30:03+08:00",\n' +
          '  "results": [\n' +
          '    {\n' +
          '      "url": "https://example.com/blog/post-1",\n' +
          '      "engines": {\n' +
          '        "baidu": {\n' +
          '          "indexed": true,\n' +
          '          "match": "exact",\n' +
          '          "matched_result_url": "https://example.com/blog/post-1",\n' +
          '          "total_results": 10,\n' +
          '          "took_ms": 1820\n' +
          '        },\n' +
          '        "bing": {\n' +
          '          "error": { "code": "blocked", "message": "search engine blocked the request" }\n' +
          '        }\n' +
          '      }\n' +
          '    }\n' +
          '  ],\n' +
          '  "meta": {\n' +
          '    "request_id": "9f8b3c2a-1d4e-4f6a-b7c8-9d0e1f2a3b4c",\n' +
          '    "took_ms": 3421,\n' +
          '    "units": 8,\n' +
          '    "engines_cooldown": [\n' +
          '      { "engine": "sogou", "until": "2026-11-15T10:40:00+08:00", "reason": "COOLDOWN" }\n' +
          '    ]\n' +
          '  }\n' +
          '}'),
        fieldTable([
          ['id', '本次查询 ID(UUID)'],
          ['created_at', '查询创建时间'],
          ['results[].url', '对应请求中的 URL'],
          ['results[].engines.&lt;engine&gt;.indexed', '是否已收录(true / false)'],
          ['results[].engines.&lt;engine&gt;.match', '<code>exact</code>(精确命中)或 <code>none</code>(未命中)'],
          ['results[].engines.&lt;engine&gt;.matched_result_url', '命中的 SERP 结果地址(未命中为空)'],
          ['results[].engines.&lt;engine&gt;.total_results', 'SERP 返回的结果总数'],
          ['results[].engines.&lt;engine&gt;.took_ms', '该引擎耗时(毫秒)'],
          ['results[].engines.&lt;engine&gt;.error', '引擎级失败信息,如 <code>{"code":"blocked","message":"…"}</code>;不影响整体,HTTP 仍为 200'],
          ['meta.request_id / meta.took_ms / meta.units', '请求 ID(UUID)/ 总耗时(ms)/ 计费 units(URL 数 × 引擎数)'],
          ['meta.engines_cooldown', '处于冷却期的引擎列表:<code>engine / until(恢复时间)/ reason</code>;冷却引擎的单元快速失败并返回 <code>engine_cooldown</code>']
        ])
      ]
    }));

    /* ---- GET /v1/index/check ---- */
    root.appendChild(docSection({
      id: 'docs-index-get',
      method: 'GET',
      title: '/v1/index/check',
      sub: '单 URL 便捷版',
      children: [
        p('单 URL 便捷查询,响应结构与 POST 版完全一致。'),
        paramTable([
          ['url', 'string', '是', '目标 URL(URL 编码由 HTTP 客户端处理)'],
          ['engines', 'string', '否', '逗号分隔,如 <code>baidu,bing</code>;缺省为四引擎']
        ]),
        codeBlock('cURL',
          'curl -G "' + BASE + '/v1/index/check" \\\n' +
          '  -H "X-API-Key: ' + DEMO_KEY + '" \\\n' +
          '  --data-urlencode "url=https://example.com/blog/post-1" \\\n' +
          '  --data-urlencode "engines=baidu,bing"'),
        codeBlock('JavaScript · fetch',
          'const qs = new URLSearchParams({\n' +
          '  url: "https://example.com/blog/post-1",\n' +
          '  engines: "baidu,bing"\n' +
          '});\n' +
          'const resp = await fetch("' + BASE + '/v1/index/check?" + qs.toString(), {\n' +
          '  headers: { "X-API-Key": "' + DEMO_KEY + '" }\n' +
          '});\n' +
          'const data = await resp.json();')
      ]
    }));

    /* ---- GET /v1/quota ---- */
    root.appendChild(docSection({
      id: 'docs-quota',
      method: 'GET',
      title: '/v1/quota',
      sub: '查询当前密钥配额与用量',
      children: [
        p('返回调用方密钥的配额与用量,<code>daily_quota = 0</code> 表示不限量。'),
        codeBlock('cURL', 'curl "' + BASE + '/v1/quota" \\\n  -H "X-API-Key: ' + DEMO_KEY + '"'),
        codeBlock('JavaScript · fetch',
          'const resp = await fetch("' + BASE + '/v1/quota", {\n' +
          '  headers: { "X-API-Key": "' + DEMO_KEY + '" }\n' +
          '});\n' +
          'const quota = await resp.json();\n' +
          'console.log(quota.used_today, quota.daily_quota);'),
        codeBlock('JSON · 200', '{\n' +
          '  "key_id": "3f9c2a1b8d4e5f60",\n' +
          '  "name": "生产服务",\n' +
          '  "daily_quota": 0,\n' +
          '  "used_today": 128,\n' +
          '  "unlimited": true,\n' +
          '  "used_total": 2041\n' +
          '}')
      ]
    }));

    /* ---- GET /v1/engines ---- */
    root.appendChild(docSection({
      id: 'docs-engines',
      method: 'GET',
      title: '/v1/engines',
      sub: '引擎清单与熔断状态',
      children: [
        p('返回当前可用的引擎清单与各引擎熔断 / 冷却状态,便于在调用前判断引擎可用性。'),
        codeBlock('cURL', 'curl "' + BASE + '/v1/engines" \\\n  -H "X-API-Key: ' + DEMO_KEY + '"')
      ]
    }));

    /* ---- MCP ---- */
    root.appendChild(docSection({
      id: 'docs-mcp',
      method: 'POST',
      title: '/web-tool/mcp',
      sub: 'MCP Streamable HTTP(JSON-RPC 2.0)',
      children: [
        p('AI Agent 一等公民端点。方法:<code>initialize</code>(返回 protocolVersion「2025-06-18」与 serverInfo{name:"web2api"})、<code>notifications/initialized</code>(204)、<code>tools/list</code>、<code>tools/call</code>、<code>ping</code>。服务端通过 <code>Mcp-Session-Id</code> 响应头下发会话 ID,后续请求需原样带回。鉴权同公网 API:<code>X-API-Key</code>(计费)或管理员 JWT(不计费)。'),
        paramTable([
          ['web_search', 'object', 'query 必填', '{ query, engine?, limit? } → { results:[{rank,title,url,snippet}] };计费 1 unit/次'],
          ['check_indexed', 'object', 'urls 必填', '{ urls[1..20], engines? } → /v1/index/check 同构 results;计费 URL 数 × 引擎数 units'],
          ['get_quota', 'object', '—', '{} → 当前密钥配额与用量'],
          ['list_engines', 'object', '—', '{} → 引擎清单与冷却 / 熔断状态']
        ]),
        codeBlock('cURL · tools/call',
          'curl -X POST "' + BASE + '/mcp" \\\n' +
          '  -H "X-API-Key: ' + DEMO_KEY + '" \\\n' +
          '  -H "Content-Type: application/json" \\\n' +
          '  -d \'{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"check_indexed","arguments":{"urls":["https://example.com/blog/post-1"],"engines":["baidu","bing"]}}}\'')
      ]
    }));

    /* ---- 管理 API 一览 ---- */
    root.appendChild(docSection({
      id: 'docs-admin',
      title: '管理 API 一览(JWT Bearer)',
      children: [
        p('除 <code>POST /api/auth/login</code> 外均需 <code>Authorization: Bearer &lt;JWT&gt;</code>(面板登录获得)。管理 API 全局限速默认 60 次/分钟,超限返回 429 与 <code>Retry-After</code>。'),
        paramTable([
          ['POST /api/auth/login', '—', '—', '管理员登录,返回 {"token","expires_in","user"};同一用户名+IP 连续 5 次失败锁定 15 分钟(429 too_many_attempts)'],
          ['POST /api/auth/password', 'JSON', '—', '修改管理员密码 { old_password, new_password(≥8 位) };成功后全部旧 token 立即失效'],
          ['GET /api/me', '—', '—', '当前管理员与调用概况'],
          ['POST /api/keys', 'JSON', '—', '创建 API 密钥 { name, daily_quota? };完整密钥仅返回一次'],
          ['GET /api/keys', '—', '—', '密钥列表(新→旧)'],
          ['PATCH /api/keys/{id}', 'JSON', '—', '更新名称 / 状态 / 日配额'],
          ['DELETE /api/keys/{id}', '—', '—', '删除密钥'],
          ['GET /api/stats/overview', '—', '—', '总览统计(今日 / 累计 / 24h 成功率)'],
          ['GET /api/stats/timeseries', 'Query', '—', '按日时间序列 ?days=1..90,默认 14'],
          ['GET /api/usage', 'Query', '—', '使用日志 ?limit(≤200)&offset&key_id,分页返回 {total, items[]}'],
          ['GET /api/engines', '—', '—', '每引擎状态:{ state: ok|cooldown|circuit_open, until, reason, consecutive_blocks, last_error_at }'],
          ['POST /api/playground/check', 'JSON', '—', '面板在线调试 { urls[1..20], engines? };响应结构与 /v1/index/check 完全一致;JWT 鉴权,不计费'],
          ['POST /api/engines/{name}/cooldown/reset', '—', '—', '强制恢复引擎:清除冷却与连续拦截计数'],
          ['POST /api/engines/{name}/cooldown/force', 'JSON', '—', '强制冷却引擎 { minutes: 1–720 }(维护窗口用)']
        ].map(function (r) {
          // 管理端点表复用参数表的 4 列结构:端点/类型/必填→—/说明
          return r;
        }))
      ]
    }));

    /* ---- 限流与冷却 ---- */
    root.appendChild(docSection({
      id: 'docs-ratelimit',
      title: '限流与引擎冷却',
      children: [
        ulHtml([
          '<strong>管理 API 限速</strong>:默认 60 次/分钟,超限返回 <code>429</code> 并携带 <code>Retry-After</code> 响应头(配置:<code>security.admin_rate_per_minute</code>)。',
          '<strong>登录防爆破</strong>:同一「用户名 + IP」连续 5 次密码失败锁定 15 分钟,期间登录一律 <code>429 {"error":"too_many_attempts"}</code> + <code>Retry-After</code>(配置:<code>security.login_lockout.{fails,minutes}</code>)。',
          '<strong>引擎冷却状态机</strong>(每引擎独立):触发 = 引擎返回 blocked(403)/ 429 / captcha。连续第 1 次冷却 10 分钟、第 2 次 30 分钟、第 3 次及以上 120 分钟;连续 240 分钟无拦截则计数归零(配置:<code>stability.engine_cooldown.{steps_minutes:[10,30,120], reset_after_minutes:240}</code>)。',
          '<strong>冷却期内</strong>:该引擎所有 search / check 请求快速失败、不发真实请求,返回 <code>503 {"error":"engine_cooldown","reason":"COOLDOWN","retry_after_seconds":N}</code> 并携带 <code>Retry-After</code>;/v1 与 /{engine}/search 的 <code>meta.engines_cooldown</code> 同步列出冷却引擎。',
          '<strong>到期恢复</strong>:冷却到期后放行一个探测请求,成功即恢复、失败进下一档。管理员可在「引擎状态」页强制恢复或强制冷却。'
        ])
      ]
    }));

    /* ---- 错误码 ---- */
    root.appendChild(docSection({
      id: 'docs-errors',
      title: '错误码',
      children: [
        p('<strong>HTTP 级错误(整请求失败)</strong>'),
        errTable([
          ['400', 'bad_request', '参数错误;reason 取值:<code>EMPTY_QUERY</code>(URL 列表为空)/ <code>TOO_MANY_URLS</code>(超过 20 条)/ <code>INVALID_PARAM</code>(参数非法)'],
          ['401', 'unauthorized', '密钥无效 / 已禁用,或管理员 JWT 缺失、过期'],
          ['404', 'not_found', '接口或资源不存在(公网 API 挂在 /web-tool 前缀下,根路径 404)'],
          ['403', 'blocked', '搜索引擎反爬拦截了请求(引擎级错误同样会以 code=blocked 出现在单元结果中)'],
          ['429', 'quota_exceeded', '当日配额已用尽;带 <code>Retry-After</code>'],
          ['429', 'too_many_attempts', '登录失败次数过多,已锁定;带 <code>Retry-After</code>'],
          ['429', 'captcha_detected', '引擎返回验证码(引擎级 429 不影响批量其它引擎)'],
          ['503', 'engine_cooldown', '引擎冷却中:<code>{"error":"engine_cooldown","reason":"COOLDOWN","retry_after_seconds":N}</code>;带 <code>Retry-After</code>'],
          ['503', 'circuit_open / proxy_*', '熔断开启或代理类失败(auth / connect / timeout / unavailable)'],
          ['504', 'search_timeout / request_timeout', '引擎搜索超时或整体请求超时'],
          ['501', 'not_implemented', '该引擎未实现所请求的能力'],
          ['502', 'parser_failure / engine_internal / all_engines_failed', '解析失败 / 引擎内部错误 / 全部引擎失败'],
          ['500', 'server_error', '服务端内部错误']
        ]),
        p('注:429 / 503 响应均携带 <code>Retry-After</code>(秒)。'),
        p('<strong>引擎级错误(单元失败,HTTP 仍为 200)</strong> —— 出现在 <code>results[].engines.&lt;engine&gt;.error.code</code>:'),
        errTable([
          ['200', 'blocked', '搜索引擎反爬拦截(403 / 429)'],
          ['200', 'captcha_detected', '引擎返回验证码'],
          ['200', 'engine_cooldown', '该引擎处于冷却窗口,快速失败'],
          ['200', 'circuit_open', '熔断器开启,未发起真实请求'],
          ['200', 'search_timeout', '引擎搜索超时'],
          ['200', 'not_implemented', '引擎未实现该能力'],
          ['200', 'proxy_auth / proxy_connect / proxy_timeout / proxy_unavailable', '代理链路失败'],
          ['200', 'parser_failure', 'SERP 解析失败'],
          ['200', 'engine_internal', '引擎内部错误']
        ]),
        p('引擎级失败不改变整体响应:HTTP 仍为 200,其余引擎结果正常返回。')
      ]
    }));

    /* ---- Agent 静态资源 ---- */
    root.appendChild(docSection({
      id: 'docs-assets',
      title: 'Agent 静态资源',
      children: [
        p('面向 Agent / 客户端的全量机器可读描述,随服务自动提供:'),
        ulHtml([
          '<a href="' + BASE + '/openapi.yaml" target="_blank" rel="noopener noreferrer">openapi.yaml</a> — OpenAPI 3.1 全端点描述(可直接导入 Dify / Coze / Apifox 等)',
          '<a href="' + BASE + '/llms.txt" target="_blank" rel="noopener noreferrer">llms.txt</a> — 平台简介 + 端点清单(Agent 友好格式)',
          'MCP 接入配置示例见「<a href="#/agents">Agent 集成</a>」页(Claude / Dify / Coze / n8n / LangChain)。'
        ])
      ]
    }));
  }

  /* ================= 8d. 页面:系统设置 ================= */

  function pageSettings(root) {
    /* ---- 修改密码 ---- */
    var oldPwd = el('input', { class: 'input', type: 'password', autocomplete: 'current-password', placeholder: '当前密码' });
    var newPwd = el('input', { class: 'input', type: 'password', autocomplete: 'new-password', placeholder: '至少 8 位,建议混合字母 / 数字 / 符号' });
    var confirmPwd = el('input', { class: 'input', type: 'password', autocomplete: 'new-password', placeholder: '再次输入新密码' });

    var bar = el('div', { class: 'strength-bar' });
    var strengthLabel = el('div', { class: 'strength-label', text: '密码强度:—' });
    var strength = el('div', null,
      el('div', { class: 'strength-track' }, bar),
      strengthLabel);

    function scorePwd(v) {
      // 0-3:长度 + 字符类别;仅用于面板提示,强校验在服务端
      var score = 0;
      if (v.length >= 8) score++;
      if (v.length >= 12) score++;
      var classes = 0;
      if (/[a-z]/.test(v) || /[A-Z]/.test(v)) classes++;
      if (/\d/.test(v)) classes++;
      if (/[^A-Za-z0-9]/.test(v)) classes++;
      if (v.length >= 8 && classes >= 2) score++;
      if (score > 3) score = 3;
      return score;
    }

    newPwd.addEventListener('input', function () {
      var v = newPwd.value;
      var s = scorePwd(v);
      bar.className = 'strength-bar' + (s > 0 ? ' s' + s : '');
      bar.style.width = v ? (s === 0 ? 25 : (s === 1 ? 40 : (s === 2 ? 70 : 100))) + '%' : '0';
      strengthLabel.textContent = v ? ('密码强度:' + (s === 0 ? '过弱(不足 8 位或过于简单)' : s === 1 ? '弱' : s === 2 ? '中' : '强')) : '密码强度:—';
    });

    var err = el('div', { class: 'form-err hidden', role: 'alert' });
    function showErr(msg) {
      err.textContent = msg;
      err.classList.remove('hidden');
    }

    var submitBtn = el('button', { class: 'btn btn-primary', type: 'button', text: '修改密码' });
    submitBtn.addEventListener('click', function () {
      err.classList.add('hidden');
      if (!oldPwd.value) { showErr('请输入当前密码'); oldPwd.focus(); return; }
      if (newPwd.value.length < 8) { showErr('新密码至少 8 位'); newPwd.focus(); return; }
      if (newPwd.value === oldPwd.value) { showErr('新密码不能与当前密码相同'); newPwd.focus(); return; }
      if (confirmPwd.value !== newPwd.value) { showErr('两次输入的新密码不一致'); confirmPwd.focus(); return; }
      setBtnLoading(submitBtn, true, '提交中…');
      api('POST', '/api/auth/password', {
        old_password: oldPwd.value,
        new_password: newPwd.value
      })
        .then(function () {
          // 契约:成功后 token_version+1,全部旧 token(含当前)立即失效 → 回登录页
          clearAuth();
          toast('密码已修改,请使用新密码重新登录', 'success');
          showLogin();
        })
        .catch(function (e) {
          showErr(e.message);
        })
        .then(function () {
          setBtnLoading(submitBtn, false);
        });
    });
    confirmPwd.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') {
        e.preventDefault();
        submitBtn.click();
      }
    });

    var pwdCard = el('div', { class: 'card' },
      el('div', { class: 'card-head' }, el('h2', { class: 'card-title', text: '修改管理员密码' })),
      el('div', { class: 'card-body' },
        el('div', { class: 'field' },
          el('label', { class: 'field-label', text: '当前密码' }),
          oldPwd),
        el('div', { class: 'field' },
          el('label', { class: 'field-label', text: '新密码' }),
          newPwd,
          strength),
        el('div', { class: 'field' },
          el('label', { class: 'field-label', text: '确认新密码' }),
          confirmPwd),
        err,
        submitBtn,
        el('div', { class: 'form-hint', style: 'margin-top:12px;', text: '密码以 bcrypt 哈希存储;修改成功后全部已登录会话立即失效,需要重新登录。' })));

    /* ---- 安全机制说明 ---- */
    var infoCard = el('div', { class: 'card' },
      el('div', { class: 'card-head' }, el('h2', { class: 'card-title', text: '安全机制' })),
      el('div', { class: 'card-body' },
        el('ul', { class: 'info-list' },
          el('li', null, el('b', { text: '登录锁定:' }), '同一「用户名 + IP」连续 5 次密码错误后锁定 15 分钟;期间登录一律返回 429 {"error":"too_many_attempts"} 与 Retry-After。'),
          el('li', null, el('b', { text: '管理 API 限速:' }), '全部 /api/* 默认 60 次 / 分钟,超限返回 429 与 Retry-After。'),
          el('li', null, el('b', { text: '会话失效:' }), '修改密码后全部已登录 token(含当前浏览器)立即失效,需要重新登录。'),
          el('li', null, el('b', { text: '公网鉴权:' }), '/v1/* 与 MCP 使用 X-API-Key(哈希存储,面板仅可见前缀);引擎冷却期间请求快速失败,不产生上游流量。'))));

    var grid = el('div', { class: 'settings-grid' }, pwdCard, infoCard);
    root.appendChild(grid);
  }

  /* ================= 9. 启动 ================= */

  function setLoginMsg(msg, warn) {
    var box = $('#login-error');
    if (!box) return;
    box.textContent = msg;
    box.className = warn ? 'login-hint' : 'login-error';
  }

  function bindLogin() {
    var form = $('#login-form');
    if (!form) return;
    var userInput = $('#login-username');
    var pwdInput = $('#login-password');
    var errBox = $('#login-error');
    var submitBtn = $('#login-submit');

    /* 错误态输入框:aria-invalid=true 命中样式表 .input[aria-invalid="true"] 的 destructive 边框;
     * 用户重新输入或重新提交时清除。 */
    function setFieldInvalid(input, bad) {
      if (!input) return;
      if (bad) input.setAttribute('aria-invalid', 'true');
      else input.removeAttribute('aria-invalid');
    }
    function clearFieldInvalid() {
      setFieldInvalid(userInput, false);
      setFieldInvalid(pwdInput, false);
    }
    [userInput, pwdInput].forEach(function (inp) {
      if (inp) inp.addEventListener('input', function () { setFieldInvalid(inp, false); });
    });

    function showErr(msg) {
      if (!errBox) return;
      errBox.textContent = msg;
      errBox.className = 'login-error';
      errBox.classList.remove('hidden');
    }

    form.addEventListener('submit', function (ev) {
      ev.preventDefault();
      var u = userInput.value.trim();
      var pwd = pwdInput.value;
      if (errBox) errBox.classList.add('hidden');
      clearFieldInvalid();
      if (!u || !pwd) {
        setFieldInvalid(userInput, !u);
        setFieldInvalid(pwdInput, !pwd);
        showErr('请输入用户名和密码');
        return;
      }
      setBtnLoading(submitBtn, true, '登录中…');
      api('POST', '/api/auth/login', { username: u, password: pwd }, { skipAuthRedirect: true })
        .then(function (data) {
          data = data || {};
          var uname = (data.user && data.user.username) ? data.user.username : u;
          setAuth(data.token, data.expires_in, uname);
          toast('登录成功,欢迎回来', 'success');
          var target = pendingHash || '#/overview';
          pendingHash = null;
          if (location.hash === target) renderRoute();
          else location.hash = target;
        })
        .catch(function (e) {
          if (e.status === 401) {
            /* 凭据错误:用户名与密码成对校验,两个输入框都标记错误态(destructive 边框) */
            setFieldInvalid(userInput, true);
            setFieldInvalid(pwdInput, true);
            showErr('用户名或密码错误(同一用户名+IP 连续 5 次失败将锁定 15 分钟)');
          } else if (e.status === 429 && e.data && e.data.error === 'too_many_attempts') {
            showErr('尝试次数过多,登录已被临时锁定' +
              (e.retryAfter ? ',约 ' + e.retryAfter + ' 秒后可重试' : '(15 分钟)') + '。');
          } else if (e.status === 404) {
            // 平台停用:/api/* 未挂载,静态面板仍可访问
            showErr('平台 API 未启用:面板页面可以打开,但登录接口不存在(HTTP 404)。请检查 config.yaml 的 platform.enabled 是否为 true,修改后重启服务。');
          } else {
            showErr(e.message);
          }
        })
        .then(function () {
          setBtnLoading(submitBtn, false);
        });
    });
  }

  function bindLogout() {
    var btn = $('#btn-logout');
    if (!btn) return;
    btn.addEventListener('click', function () {
      clearAuth();
      toast('已退出登录', 'info');
      if (location.hash === '#/login') showLogin();
      else location.hash = '#/login';
    });
  }

  function boot() {
    initTheme();
    bindThemeButtons();
    bindLogin();
    bindLogout();
    bindSearchHotkey();
    window.addEventListener('hashchange', renderRoute);

    if (!isLoggedIn()) {
      showLogin();
      return;
    }
    // 恢复缓存的用户名,再校验 token
    try {
      state.username = localStorage.getItem(LS_USERNAME) || '';
    } catch (e) { /* 忽略 */ }

    api('GET', '/api/me')
      .then(function (me) {
        if (me && me.username) {
          state.username = me.username;
          try { localStorage.setItem(LS_USERNAME, me.username); } catch (e) { /* 忽略 */ }
        }
        renderRoute();
      })
      .catch(function (e) {
        if (e.status === 401) return; // onUnauthorized 已处理跳转
        showLogin();
        if (e.status === 404) {
          setLoginMsg('平台 API 未启用:面板页面可以打开,但 /api/* 管理接口未挂载。请检查 config.yaml 的 platform.enabled 是否为 true,修改后重启服务。', true);
        } else {
          toast('无法连接服务:' + e.message, 'error');
        }
      });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
