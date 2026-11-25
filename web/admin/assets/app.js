/* ============================================================
 * Web2API 网站收录查询开放平台 · 管理控制台
 * 原生 ES2018 JavaScript,零依赖、零构建。
 * 设计语言:docs/design/STYLE-v4.md(取代 STYLE-v2.md)。
 *
 * 结构:
 *   1. 常量与内联图标(API_BASE 与 core.BasePath 对齐;PAGE_META 页头块)
 *   2. DOM 工具 / 格式化工具 / 骨架屏
 *   3. Toast / 剪贴板 / 按钮三态
 *   3b. 三态主题(html.dark + localStorage w2a_theme:light/dark/system,
 *       预置逻辑在 assets/theme-boot.js,此处负责菜单与运行时切换)
 *   3c. 全局快捷键(/ 或 ⌘K 聚焦搜索,带输入守卫)
 *   3d. 侧栏折叠(256px ↔ icon 列,localStorage w2a_sidebar_collapsed)
 *   4. 登录态与 fetch 统一封装(统一 /web-tool 前缀,401 自动回登录页)
 *   5. 弹窗 / 确认框
 *   6. Hash 路由与视图切换(页头块渲染进内容区;未知路由渲染 404 视图;
 *      渲染完成后滚动复位)
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
  var LS_THEME = 'w2a_theme'; // V4 三态:'light' | 'dark' | 'system'(缺省 light)

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
    '#/playground': { title: '在线调试', desc: '在面板内发起一次真实收录查询,结果与 POST /v1/index/check 完全一致。' },
    '#/agents': { title: 'Agent 集成', desc: 'MCP 端点与 Claude / Dify / n8n / LangChain 的接入配置。' },
    '#/docs': { title: 'API 文档', desc: '全部 REST 端点、MCP 工具与错误码的完整说明。' },
    '#/settings': { title: '系统设置', desc: '管理员密码修改与平台安全机制说明。' },
    '#/users': { title: '用户管理', desc: '管理员与运维账号的角色、启停、密码重置与删除。' },
    '#/logs': { title: '系统日志', desc: '服务与审计日志检索、日志配置查看及过期日志清理。' },
    '#/blacklist': { title: '黑名单', desc: '按 IP / CIDR 屏蔽来源,支持到期时间与命中统计。' }
  };
  var ROUTE_RE = /^#\/(overview|engines|keys|usage|playground|agents|docs|settings|users|logs|blacklist)$/;

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
    /* 角色来自 /api/me;旧后端不返回 role 时按 admin 处理(向后兼容) */
    role: 'viewer',
    userId: '',
    keysCache: [],
    usage: { limit: 50, offset: 0, keyId: '' }
  };
  var pendingHash = null;
  var pageTimers = [];
  /* T-07: boot 是否已完成首屏渲染(用于 /api/me 回来后避免整页二次渲染) */
  var bootRendered = false;

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
    /* is-skeleton 为哨兵类(无样式):防止 renderGrid 把骨架网格误判为真网格 */
    var grid = el('div', { class: 'engine-grid is-skeleton' });
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
    var text = String(msg);
    /* T-06:同文案去重合并——重复触发时给已有 toast 计数(×N)并重新计时,
     * 不再堆叠多条一模一样的提示(如 401 风暴下的「登录已过期」)。 */
    for (var i = 0; i < box.children.length; i++) {
      var existing = box.children[i];
      var msgEl = existing ? existing.querySelector('.toast-msg') : null;
      if (msgEl && msgEl.getAttribute('data-text') === text) {
        var count = (Number(existing.getAttribute('data-count')) || 1) + 1;
        existing.setAttribute('data-count', String(count));
        msgEl.textContent = count > 1 ? text + '(×' + count + ')' : text;
        existing.classList.remove('toast-out');
        if (existing._toastTimer) clearTimeout(existing._toastTimer);
        existing._toastTimer = toastAutoDismiss(existing);
        return;
      }
    }
    while (box.children.length >= 4) {
      if (box.firstChild) box.removeChild(box.firstChild);
    }
    var icon = type === 'success' ? ICONS.toastOk : (type === 'error' ? ICONS.toastErr : ICONS.toastInfo);
    var t = el('div', { class: 'toast toast-' + (type || 'info') });
    t.appendChild(el('span', { html: icon }));
    t.appendChild(el('span', { class: 'toast-msg', 'data-text': text, text: text }));
    /* V4 §9.12:右上 20×20 关闭钮 */
    t.appendChild(el('button', {
      class: 'toast-close', type: 'button', 'aria-label': '关闭提示', text: '×',
      onclick: function () {
        t.classList.add('toast-out');
        setTimeout(function () { if (t.parentNode) t.parentNode.removeChild(t); }, 200);
      }
    }));
    box.appendChild(t);
    /* V4 §9.12:4s 自动消失 */
    t._toastTimer = toastAutoDismiss(t);
  }

  function toastAutoDismiss(node) {
    return setTimeout(function () {
      node.classList.add('toast-out');
      setTimeout(function () {
        if (node.parentNode) node.parentNode.removeChild(node);
      }, 220);
    }, 4000);
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

  /* ================= 3b. 三态主题(html.dark + localStorage w2a_theme) =================
   * V4 §2:用户入口=右上角图标钮 → 三态菜单「浅色 / 深色 / 系统」,当前项右侧 ✓。
   * 预置(paint 前同步)由 assets/theme-boot.js 完成;此处负责菜单、运行时
   * 切换与 system 模式下的跟随。缺省 light(与 V4「浅色默认」一致)。 */

  var THEME_MQ = (typeof window.matchMedia === 'function') ? window.matchMedia('(prefers-color-scheme: dark)') : null;
  var THEME_MODES = [['light', '浅色'], ['dark', '深色'], ['system', '系统']];
  var themeMenuEl = null;
  var themeMenuTrigger = null;

  function storedThemeMode() {
    try {
      var v = localStorage.getItem(LS_THEME);
      return (v === 'dark' || v === 'light' || v === 'system') ? v : 'light';
    } catch (e) {
      return 'light';
    }
  }

  function systemPrefersDark() {
    return !!(THEME_MQ && THEME_MQ.matches);
  }

  function themeResolvesDark(mode) {
    return mode === 'dark' || (mode === 'system' && systemPrefersDark());
  }

  function applyTheme(mode) {
    var dark = themeResolvesDark(mode);
    document.documentElement.classList.toggle('dark', dark);
    var meta = document.querySelector('meta[name="color-scheme"]');
    if (meta) meta.setAttribute('content', dark ? 'dark' : 'light');
    syncThemeMenus(mode);
  }

  function initTheme() {
    // head 预置脚本已按存储值预置 class;此处统一兜底并同步菜单勾选态
    applyTheme(storedThemeMode());
    if (THEME_MQ) {
      var onChange = function () {
        if (storedThemeMode() === 'system') applyTheme('system');
      };
      if (typeof THEME_MQ.addEventListener === 'function') THEME_MQ.addEventListener('change', onChange);
      else if (typeof THEME_MQ.addListener === 'function') THEME_MQ.addListener(onChange);
    }
  }

  function setThemeMode(mode) {
    if (mode !== 'light' && mode !== 'dark' && mode !== 'system') mode = 'light';
    try { localStorage.setItem(LS_THEME, mode); } catch (e) { /* 忽略 */ }
    applyTheme(mode);
  }

  function syncThemeMenus(mode) {
    var items = document.querySelectorAll('.theme-menu .menu-item[data-mode]');
    for (var i = 0; i < items.length; i++) {
      items[i].classList.toggle('on', items[i].getAttribute('data-mode') === mode);
    }
  }

  function closeThemeMenu() {
    if (themeMenuEl && themeMenuEl.parentNode) themeMenuEl.parentNode.removeChild(themeMenuEl);
    if (themeMenuTrigger) themeMenuTrigger.setAttribute('aria-expanded', 'false');
    themeMenuEl = null;
    themeMenuTrigger = null;
    document.removeEventListener('mousedown', onMenuOutside, true);
    document.removeEventListener('keydown', onMenuKeydown, true);
  }

  function onMenuOutside(e) {
    if (!themeMenuEl) return;
    if (themeMenuEl.contains(e.target) || (themeMenuTrigger && themeMenuTrigger.contains(e.target))) return;
    closeThemeMenu();
  }

  function onMenuKeydown(e) {
    if (!themeMenuEl) return;
    if (e.key === 'Escape') {
      e.preventDefault();
      closeThemeMenu();
      if (themeMenuTrigger) themeMenuTrigger.focus();
    } else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      var items = themeMenuEl.querySelectorAll('.menu-item');
      if (!items.length) return;
      var idx = Array.prototype.indexOf.call(items, document.activeElement);
      var next = e.key === 'ArrowDown' ? (idx + 1) % items.length : (idx - 1 + items.length) % items.length;
      items[next === -1 ? 0 : next].focus();
    }
  }

  /* 三态菜单 popover:fixed 定位锚定触发钮右下角;150ms zoom-in 入场(§9.10) */
  function openThemeMenu(trigger) {
    closeThemeMenu();
    var mode = storedThemeMode();
    var menu = el('div', { class: 'theme-menu', role: 'menu', 'aria-label': '主题' });
    THEME_MODES.forEach(function (pair) {
      menu.appendChild(el('button', {
        class: 'menu-item' + (pair[0] === mode ? ' on' : ''),
        type: 'button', role: 'menuitemradio',
        'data-mode': pair[0],
        'aria-checked': pair[0] === mode ? 'true' : 'false',
        onclick: function () {
          setThemeMode(pair[0]);
          closeThemeMenu();
          if (trigger) trigger.focus();
        }
      },
        el('span', { text: pair[1] }),
        el('span', { class: 'menu-check', 'aria-hidden': 'true', text: '✓' })));
    });
    document.body.appendChild(menu);
    var rect = trigger.getBoundingClientRect();
    var mw = menu.offsetWidth;
    var left = Math.min(Math.max(8, rect.right - mw), window.innerWidth - mw - 8);
    var top = rect.bottom + 6;
    if (top + menu.offsetHeight > window.innerHeight - 8) top = Math.max(8, rect.top - menu.offsetHeight - 6);
    menu.style.left = left + 'px';
    menu.style.top = top + 'px';
    themeMenuEl = menu;
    themeMenuTrigger = trigger;
    trigger.setAttribute('aria-expanded', 'true');
    document.addEventListener('mousedown', onMenuOutside, true);
    document.addEventListener('keydown', onMenuKeydown, true);
    var first = menu.querySelector('.menu-item.on') || menu.querySelector('.menu-item');
    if (first) first.focus();
  }

  function bindThemeButtons() {
    ['btn-theme', 'btn-login-theme'].forEach(function (id) {
      var b = document.getElementById(id);
      if (b) b.addEventListener('click', function () {
        if (themeMenuEl && themeMenuTrigger === b) closeThemeMenu();
        else openThemeMenu(b);
      });
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

  /* ================= 3d. 侧栏折叠(256px ↔ icon 列,V4 §9.8 推断项) =================
   * 桌面端顶栏左侧折叠钮;状态持久化到 localStorage。折叠态仅图标,
   * 项上挂 title 作悬停提示。移动端(≤960px)侧栏为底部横条,按钮隐藏。 */
  var LS_SIDEBAR = 'w2a_sidebar_collapsed';

  function updateNavTitles(collapsed) {
    var items = document.querySelectorAll('.nav-item');
    for (var i = 0; i < items.length; i++) {
      var a = items[i];
      if (collapsed) a.setAttribute('title', (a.textContent || '').trim());
      else a.removeAttribute('title');
    }
  }

  function bindSidebarToggle() {
    var btn = document.getElementById('btn-sidebar');
    var appEl = document.getElementById('app');
    if (!btn || !appEl) return;
    var collapsed = false;
    try { collapsed = localStorage.getItem(LS_SIDEBAR) === '1'; } catch (e) { /* 忽略 */ }
    appEl.classList.toggle('sidebar-collapsed', collapsed);
    btn.setAttribute('aria-expanded', collapsed ? 'false' : 'true');
    updateNavTitles(collapsed);
    btn.addEventListener('click', function () {
      collapsed = !collapsed;
      appEl.classList.toggle('sidebar-collapsed', collapsed);
      try { localStorage.setItem(LS_SIDEBAR, collapsed ? '1' : '0'); } catch (e) { /* 忽略 */ }
      btn.setAttribute('aria-expanded', collapsed ? 'false' : 'true');
      updateNavTitles(collapsed);
    });
  }

  /* ================= 4. 登录态与 fetch 统一封装 =================
   * B-12 迁移: 管理员 JWT 不再写入 localStorage(XSS 可窃取),登录后持久化
   * 由后端下发的 httpOnly 会话 cookie 承担(服务端 POST /api/auth/login 同时
   * Set-Cookie)。token 仅存本页内存;老版本遗留的 localStorage token 做
   * 一次性迁移(读入内存后立即清除),之后与 cookie 双轨并存。 */

  var memToken = '';
  var memTokenExp = 0;
  /* /api/me 验证通过的会话标记:cookie 会话对前端不可读,F5 后内存 token 必然
   * 为空,登录态以服务端 /api/me 结果为准(见 boot)。由 boot 成功分支置位,
   * clearAuth 统一清除(登出 / 401 拦截 / boot 401 全部经过 clearAuth)。 */
  var meVerified = false;

  function getToken() {
    return memToken;
  }

  function tokenExpired() {
    return memTokenExp > 0 && Date.now() >= memTokenExp;
  }

  function setAuth(token, expiresIn, username) {
    memToken = token || '';
    var ms = Number(expiresIn);
    memTokenExp = (!isNaN(ms) && ms > 0) ? Date.now() + ms * 1000 - 60000 : 0; // 预留 60s 偏差
    try {
      localStorage.removeItem(LS_TOKEN);
      localStorage.removeItem(LS_TOKEN_EXP);
      if (username) localStorage.setItem(LS_USERNAME, username);
    } catch (e) { /* 忽略 */ }
    state.username = username || '';
  }

  function clearAuth() {
    memToken = '';
    memTokenExp = 0;
    meVerified = false;
    try {
      localStorage.removeItem(LS_TOKEN);
      localStorage.removeItem(LS_TOKEN_EXP);
      localStorage.removeItem(LS_USERNAME);
    } catch (e) { /* 忽略 */ }
    state.username = '';
    state.role = 'viewer'; // 未取得权威角色前按最小权限渲染，下次 /api/me 会重新拉取
    state.userId = '';
  }

  /* 把 /api/me(或登录响应中的 user)的角色落到全局;缺失/未知 role 兼容为 admin */
  function applyMe(me) {
    me = me || {};
    if (me.username) {
      state.username = me.username;
      try { localStorage.setItem(LS_USERNAME, me.username); } catch (e) { /* 忽略 */ }
    }
    var r = me.role;
    state.role = (r === 'admin' || r === 'operator' || r === 'viewer') ? r : 'viewer'; // 未知角色一律最小权限
    state.userId = (me.user_id === undefined || me.user_id === null) ? '' : me.user_id;
  }

  /* 一次性迁移: 老登录态的 token 只进内存,localStorage 立即清空。 */
  (function migrateLegacyToken() {
    try {
      var legacy = localStorage.getItem(LS_TOKEN);
      if (legacy) {
        memToken = legacy;
        var exp = Number(localStorage.getItem(LS_TOKEN_EXP) || 0);
        memTokenExp = exp > 0 ? exp : 0;
        localStorage.removeItem(LS_TOKEN);
        localStorage.removeItem(LS_TOKEN_EXP);
      }
    } catch (e) { /* 忽略 */ }
  })();

  /* 登录态判定改为服务端权威: cookie 对前端不可读,登录与否以 /api/me 结果
   * 为准(见 boot)。内存 token 仅用于提前渲染与 Bearer 双轨。 */
  function isLoggedIn() {
    return meVerified || (!!memToken && !tokenExpired());
  }

  /* 机器可读错误 → 面向管理员的中文提示。
   * B-09 适配:管理面 500 改为 {"error":"server_error","request_id":N}(不带
   * message,原始错误只进服务端日志),此处不再拼接 message,仅展示请求 ID。 */
  function extractErrMsg(data, status) {
    if (data && typeof data === 'object') {
      if (typeof data.error === 'string' && data.error) {
        if (data.error === 'unauthorized') return '未授权或登录已过期';
        if (data.error === 'forbidden') {
          if (data.reason === 'blacklisted') return '访问被拒绝:当前 IP 或网段已被列入黑名单';
          if (data.code === 'insufficient_role') return '权限不足:当前角色无权执行该操作';
          return '该请求被拒绝访问';
        }
        if (data.reason === 'blacklisted') return '访问被拒绝:当前 IP 或网段已被列入黑名单';
        if (data.code === 'insufficient_role' || data.reason === 'insufficient_role') return '权限不足:当前角色无权执行该操作';
        if (data.error === 'quota_exceeded') return '调用配额已用尽';
        if (data.error === 'rate_limited') return '请求过于频繁,请稍后再试';
        if (data.error === 'method_not_allowed') return '该接口不支持此 HTTP 方法';
        if (data.error === 'too_many_attempts') return '登录尝试次数过多,已临时锁定,请稍后再试';
        if (data.error === 'engine_cooldown') return '引擎冷却中,请稍后重试';
        if (data.error === 'not_found') return '接口或资源不存在(若刚部署,请确认平台 API 已启用)';
        if (data.error === 'server_error') {
          return '服务端内部错误' + (data.request_id ? '(请求 ID ' + data.request_id + ')' : '');
        }
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
      credentials: 'same-origin', // B-12: httpOnly 会话 cookie 与 Bearer 双轨
      body: (body !== undefined && body !== null) ? JSON.stringify(body) : undefined
    }).catch(function (fetchErr) {
      /* D-03: 网络层失败(fetch reject, 典型: 服务端 IdleTimeout 关闭 keep-alive
       * 连接后的 TypeError: Failed to fetch)不会经过下方 extractErrMsg——它只在
       * 拿到 HTTP 响应时执行。这里统一映射为中文提示, 英文原文不进全中文界面;
       * err.status=0 与 HTTP 状态码区分, err.network 供调用方识别网络类失败。 */
      var e = new Error('网络异常或服务已重启,请稍后重试');
      e.status = 0;
      e.network = true;
      if (fetchErr && fetchErr.name) e.causeName = fetchErr.name;
      throw e;
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
          /* 管理面 429/503 且 Retry-After 较短时,只读 GET 自动重试一次,
             避免快速切换面板页面时偶发「加载失败」;写操作不重试。 */
          if ((res.status === 429 || res.status === 503) && String(method).toUpperCase() === 'GET' &&
              (opts._retry || 0) < 2 && !isNaN(retryAfter) && retryAfter > 0 && retryAfter <= 5) {
            var nextOpts = {};
            for (var k in opts) { if (Object.prototype.hasOwnProperty.call(opts, k)) nextOpts[k] = opts[k]; }
            nextOpts._retry = (opts._retry || 0) + 1;
            return new Promise(function (resolve, reject) {
              setTimeout(function () { api(method, path, body, nextOpts).then(resolve, reject); }, retryAfter * 1000);
            });
          }
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

  var lastUnauthorizedToastAt = 0;

  function onUnauthorized() {
    clearAuth();
    showLogin();
    /* T-06:会话失效后并发请求触发 401 风暴时,全局拦截只弹一次提示;
     * 时间窗内重复的 401 不再逐条 toast(toast 合并计数为兜底)。 */
    var now = Date.now();
    if (now - lastUnauthorizedToastAt > 4000) {
      lastUnauthorizedToastAt = now;
      toast('登录已过期,请重新登录', 'error');
    }
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
      window.scrollTo(0, 0); // P-06:切页滚动复位(移动端不再落在页底)
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
    else if (hash === '#/users') pageUsers(root, actions);
    else if (hash === '#/logs') pageLogs(root, actions);
    else if (hash === '#/blacklist') pageBlacklist(root, actions);
    else if (hash === '#/playground') pagePlayground(root);
    else if (hash === '#/agents') pageAgents(root);
    else if (hash === '#/settings') pageSettings(root);
    else pageDocs(root);
    animatePage(root);
    window.scrollTo(0, 0); // P-06:切页滚动复位(移动端不再落在页底)
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

    var firstStatsLoad = true;
    function loadAll() {
      /* 首进骨架;此后旧内容原位降透明,避免「清空→骨架→重建」白浪 */
      if (firstStatsLoad) {
        firstStatsLoad = false;
        wrap.innerHTML = '';
        wrap.appendChild(skeletonBlock([{ w: 'w30' }, { big: true, w: 'w60' }, { w: 'w40' }]));
      } else {
        wrap.classList.add('is-busy');
      }
      Promise.all([
        api('GET', '/api/stats/overview'),
        api('GET', '/api/stats/timeseries?days=14'),
        api('GET', '/api/engines')
      ]).then(function (res) {
        wrap.classList.remove('is-busy');
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
        wrap.classList.remove('is-busy');
        wrap.innerHTML = '';
        wrap.appendChild(errorNode(e, function () { loadAll(); }));
      });
    }

    var recentBody = null;
    function loadRecent() {
      /* 卡头只建一次;重载时仅表体降透明 */
      recentBody = recentCard.querySelector('.card-body');
      if (!recentBody) {
        recentCard.innerHTML = '';
        recentCard.appendChild(el('div', { class: 'card-head' },
          el('h2', { class: 'card-title' }, '最近日志',
            el('span', { class: 'card-head-sub', text: '最近 5 次调用记录' })),
          el('a', { class: 'link-more', href: '#/usage', text: '查看全部 >' })));
        recentBody = el('div', { class: 'card-body flush' });
        recentCard.appendChild(recentBody);
        recentBody.appendChild(skeletonBlock([{ w: 'w40' }, { w: 'w80' }, { w: 'w70' }]));
      } else {
        recentBody.classList.add('is-busy');
      }
      var body = recentBody;
      api('GET', '/api/usage?limit=5').then(function (data) {
        data = data || {};
        var items = Array.isArray(data.items) ? data.items : [];
        body.classList.remove('is-busy');
        body.innerHTML = '';
        if (!items.length) {
          body.appendChild(el('div', { class: 'empty' },
            el('div', { class: 'empty-dot' },
              el('div', { class: 'empty-title', text: '暂无调用记录' }),
              el('div', { class: 'empty-desc', text: '完成一次收录查询后,最近 5 条日志会显示在这里。' }))));
          return;
        }
        var tbody = el('tbody');
        items.forEach(function (it) {
          it = it || {};
          tbody.appendChild(el('tr', null,
            el('td', { class: 'td-mono', text: fmtDateTime(it.time) }),
            el('td', { class: 'td-strong', text: it.key_name || ('#' + it.key_id) }),
            el('td', null, el('span', { class: 'chip', text: it.endpoint || '—' })),
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
        body.classList.remove('is-busy');
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

  /* 统计带:一个 hairline 容器内 4 格竖线分隔(V4 §9.6),非独立卡。
   * P-01:KPI 口径=「有效调用」(到达引擎、进入业务逻辑的请求);
   * 参数错误(400)、配额与限速拒绝(429)、冷却拒绝(503)不计入调用与成功率。
   * 依赖后端 additive 字段 today_calls_all / rejected_today_24h(B-01);
   * 字段缺失时回退现口径(标签维持原表述,不加脚注,避免误导)。 */
  function statsHasRejectedFields(stats) {
    return stats && typeof stats.today_calls_all === 'number' && typeof stats.rejected_today_24h === 'number';
  }

  function buildStatBand(stats) {
    var hasNew = statsHasRejectedFields(stats);
    var defs;
    if (hasNew) {
      defs = [
        { label: '今日有效调用', value: fmtNumber(stats.today_calls), sub: '计费 ' + fmtNumber(stats.today_units) + ' 次' },
        { label: '累计调用', value: fmtNumber(stats.total_calls), sub: '计费 ' + fmtNumber(stats.total_units) + ' 次' },
        { label: '活跃密钥', value: fmtNumber(stats.active_keys), sub: '状态为启用的 API 密钥' },
        { label: '有效成功率(24h)', value: fmtPercent(stats.success_rate_24h), sub: '全部请求 ' + fmtNumber(stats.today_calls_all) + ' 次,拒绝 ' + fmtNumber(stats.rejected_today_24h) + ' 次' }
      ];
    } else {
      defs = [
        { label: '今日调用', value: fmtNumber(stats.today_calls), sub: '计费 ' + fmtNumber(stats.today_units) + ' 次' },
        { label: '累计调用', value: fmtNumber(stats.total_calls), sub: '计费 ' + fmtNumber(stats.total_units) + ' 次' },
        { label: '活跃密钥', value: fmtNumber(stats.active_keys), sub: '状态为启用的 API 密钥' },
        { label: '24h 成功率', value: fmtPercent(stats.success_rate_24h), sub: '近 24 小时请求成功率' }
      ];
    }
    var band = el('div', { class: 'stat-band' });
    defs.forEach(function (d) {
      band.appendChild(el('div', { class: 'stat-cell' },
        el('div', { class: 'stat-value', text: d.value }),
        el('div', { class: 'stat-label', text: d.label }),
        el('div', { class: 'stat-sub', text: d.sub })));
    });
    if (hasNew) {
      band.appendChild(el('div', { class: 'stat-foot', text: '参数错误、配额与限速拒绝的请求不计入调用与成功率。' }));
    }
    return band;
  }

  function buildChartCard(series) {
    var body = el('div', { class: 'card-body' });
    var card = el('div', { class: 'card' },
      el('div', { class: 'card-head' },
        el('h2', { class: 'card-title' }, '近 14 日调用', el('span', { class: 'card-head-sub', text: '全部调用按天统计' }))),
      body);
    if (!series.length) {
      body.appendChild(emptyNode('暂无调用数据', '完成一次收录查询后,这里会展示近 14 日的调用柱状图。'));
      return card;
    }
    body.appendChild(barChart(series));
    var legend = el('div', { class: 'chart-legend' },
      el('span', null, el('span', { class: 'dot' }), '调用次数'),
      el('span', { class: 'muted', text: '悬停柱形可查看次数与成功数' }));
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
      var yt = s('text', { x: m.left - 8, y: y + 4, 'text-anchor': 'end', 'font-size': 10, style: 'fill:var(--muted-fg);font-family:var(--font-mono)' });
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
      title.textContent = dateStr + ' · 调用 ' + v + ' · ' + (Number(d.units) || 0) + ' 次 · 成功 ' + (Number(d.ok) || 0);

      var rect;
      if (h > 0) {
        rect = s('rect', { x: x, y: m.top + ih - h, width: bw, height: h, rx: 2, style: 'fill:var(--chart-1)' });
      } else {
        rect = s('rect', { x: x, y: m.top + ih - 2, width: bw, height: 2, rx: 1, style: 'fill:var(--border)' }); // 零值基线占位
      }
      rect.appendChild(title);
      svg.appendChild(rect);

      if (v > 0 && slot >= 30) {
        var vt = s('text', { x: x + bw / 2, y: m.top + ih - h - 6, 'text-anchor': 'middle', 'font-size': 10, style: 'fill:var(--fg);font-family:var(--font-mono)' });
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

  /* ================= 引擎可视化(共享:总览迷你行 + 冷却看板大卡) =================
   * 仅消费 GET /api/engines 快照字段(state/until/reason/consecutive_blocks/
   * last_error_at);四态 = 运行(满绿+流光)/ 冷却(until 驱动倒计时条,分母按
   * 10/30/120 档前端估算,真值始终 until)/ 熔断(红满格)/ 停用(灰)。
   * 契约若将来补充 calls/ok 两字段,占比分支自动生效,前端零改动升级。 */
  function engineVizState(eng) {
    if (eng.enabled === false || eng.state === 'disabled') return 'disabled';
    if (eng.state === 'cooldown') return 'cooldown';
    if (eng.state === 'circuit_open' || eng.breaker === 'open') return 'circuit';
    return 'ok';
  }
  var VIZ_CLS = { ok: 'is-ok', cooldown: 'is-cooldown', circuit: 'is-circuit', disabled: 'is-disabled' };

  function stateSegBar(eng, size) {
    var SEGS = 16, vs = engineVizState(eng);
    var bar = el('div', { class: 'segbar' + (size ? ' ' + size : '') + (vs === 'ok' ? ' is-live' : ''), role: 'img' });
    var ratio = null;
    if (typeof eng.calls === 'number' && eng.calls > 0 && typeof eng.ok === 'number' && eng.ok >= 0) {
      ratio = Math.max(0, Math.min(1, eng.ok / eng.calls));
    }
    var mode = vs === 'circuit' ? 'bad' : (vs === 'cooldown' ? 'warn' : (vs === 'disabled' ? 'plain' : 'ok'));
    var lit = ratio === null ? SEGS : Math.round(ratio * SEGS);
    if (mode === 'ok' && ratio !== null) mode = 'mix';
    if (vs === 'circuit') bar.setAttribute('aria-label', '熔断开启');
    else if (vs === 'cooldown') bar.setAttribute('aria-label', '冷却中');
    else if (vs === 'disabled') bar.setAttribute('aria-label', '已停用');
    else if (ratio !== null) bar.setAttribute('aria-label', '健康度 ' + Math.round(ratio * 100) + '%');
    else bar.setAttribute('aria-label', '运行正常');
    for (var i = 0; i < SEGS; i++) {
      var cls = '';
      if (mode === 'ok') cls = 'ok';
      else if (mode === 'warn') cls = 'warn';
      else if (mode === 'bad') cls = 'bad';
      else if (mode === 'mix') cls = (i < lit) ? 'ok' : 'bad';
      bar.appendChild(el('i', { class: cls }));
    }
    return bar;
  }

  /* 冷却档位与 core/engine_cooldown.go 一致:10/30/120 分钟;真值始终 until */
  var CD_TIERS = [10, 30, 120];
  function estimateCdSpan(untilMs, now) {
    var left = untilMs - now;
    if (left <= 0) return 60000;
    for (var i = 0; i < CD_TIERS.length; i++) {
      var t = CD_TIERS[i] * 60000;
      if (left <= t) return t;
    }
    return left;
  }

  function cdBlockNode(eng, size) {
    var untilMs = eng.until.getTime(), now = Date.now(), left = untilMs - now, total = estimateCdSpan(untilMs, now);
    var block = el('div', { class: 'cd-block' + (size ? ' ' + size : ''), 'data-cd-until': String(untilMs), 'data-cd-total': String(total) });
    var pct = Math.max(0, Math.min(100, total > 0 ? (left / total) * 100 : 0));
    var bar = el('div', { class: 'cd-bar', role: 'progressbar', 'aria-valuemin': '0', 'aria-valuemax': '100', 'aria-valuenow': String(Math.round(pct)), 'aria-label': '冷却剩余' },
      el('div', { class: 'cd-bar-fill' }));
    if (left > 0) bar.firstChild.style.width = pct.toFixed(2) + '%';
    else block.classList.add('is-done');
    block.appendChild(bar);
    block.appendChild(el('div', { class: 'cd-row' },
      el('span', { class: 'cd-value', text: left > 0 ? fmtRemaining(left) : '等待恢复探测…' }),
      el('span', { class: 'cd-label', text: '冷却剩余(至 ' + fmtDateTime(eng.until) + ')' })));
    return block;
  }

  function engineViz(eng, size) {
    var big = size === 'viz-lg', vs = engineVizState(eng);
    if (vs === 'cooldown' && eng.until) return cdBlockNode(eng, big ? '' : 'cd-sm');
    if (vs === 'cooldown') {
      return el('div', { class: 'cd-block' + (big ? '' : ' cd-sm') },
        el('div', { class: 'cd-bar is-stalled', role: 'img', 'aria-label': '冷却中' }, el('div', { class: 'cd-bar-fill' })),
        big ? el('div', { class: 'cd-row' },
          el('span', { class: 'cd-value done', text: '冷却' }),
          el('span', { class: 'cd-label', text: '冷却中(服务端未返回恢复时间,等待下次刷新)' })) : null);
    }
    var frag = el('div', null, stateSegBar(eng, big ? 'segbar-lg' : ''));
    if (big) {
      var copy = vs === 'circuit'
        ? '熔断开启:连续失败达阈值,等待半开探测' + ((eng.breaker === 'half-open' || eng.breaker === 'halfopen' || eng.breaker === 'half_open') ? '(当前半开)' : '')
        : (vs === 'disabled' ? '该引擎已在配置中停用' : '运行正常,请求正常放行');
      frag.appendChild(el('div', { class: 'cd-label' + (vs === 'circuit' ? ' is-alarm-text' : ''), text: copy }));
    }
    return frag;
  }

  function buildEngineCard(engines) {
    var body = el('div', { class: 'card-body' });
    var card = el('div', { class: 'card' },
      el('div', { class: 'card-head' },
        el('h2', { class: 'card-title' }, '引擎状态', el('span', { class: 'card-head-sub', text: '四引擎运行与冷却实时状态' }))),
      body);
    if (!engines.length) {
      body.appendChild(emptyNode('暂无引擎状态', '服务端未返回引擎数据。'));
      return card;
    }
    var list = el('div', { class: 'engine-list' });
    engines.forEach(function (eng) {
      var info = engineStateInfo(eng), vs = engineVizState(eng);
      var meta = el('div', { class: 'engine-meta' });
      if (vs === 'cooldown') meta.appendChild(engineViz(eng, ''));
      else meta.appendChild(stateSegBar(eng, ''));
      meta.appendChild(el('span', {
        class: 'pill ' + info.pill + (vs === 'ok' ? ' is-live' : '') + (vs === 'circuit' ? ' is-alarm' : ''),
        text: info.label
      }));
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
      text: '连续拦截触发冷却:第 1 次 10 分钟、第 2 次 30 分钟、第 3 次及以上 120 分钟;连续 240 分钟无拦截则归零。到期后放行一个探测请求:成功即恢复,再次被拦截进下一档,其他失败(如超时)按当前档顺延重试。每 10 秒自动刷新。'
    }));

    var wrap = el('div', null);
    root.appendChild(wrap);
    var lastList = [];
    var reloadQueued = false;
    /* 键控 patch:引擎名 → 卡片记录(节点/按钮/状态键),10s 静默刷新零重建 */
    var cardRecs = {};

    function switchView(v) {
      if (viewState.view === v) return;
      viewState.view = v;
      segCards.className = v === 'cards' ? 'on' : '';
      segTable.className = v === 'table' ? 'on' : '';
      renderGrid(lastList);
      tickCountdowns();
    }

    function renderGrid(list) {
      var scoped = (groupState === 'all') ? list : list.filter(function (eng) {
        return eng && eng.name === groupState;
      });
      wrap.classList.remove('is-busy');
      if (!scoped.length) {
        cardRecs = {};
        wrap.innerHTML = '';
        if (groupState !== 'all' && list.length) {
          wrap.appendChild(el('div', { class: 'card' },
            emptyNode('该分组暂无引擎',
              '当前分组「' + (ENGINE_LABEL[groupState] || groupState) + '」没有返回引擎数据,可切换分组或确认引擎已在配置中启用。')));
        } else {
          wrap.appendChild(el('div', { class: 'card' },
            emptyNode('暂无引擎状态', '服务端未返回引擎数据。请确认引擎已在配置中启用。')));
        }
        return;
      }
      if (viewState.view === 'table') {
        cardRecs = {};
        wrap.innerHTML = '';
        wrap.appendChild(buildEnginesTable(scoped));
        return;
      }
      /* 卡片视图:真网格则键控复用(状态键未变时零 DOM 写入,动画不被 10s 轮询打断);
       * 骨架网格/空网格/其他内容仍整建。 */
      var grid = wrap.firstElementChild;
      var realGrid = grid && grid.classList.contains('engine-grid') && !grid.classList.contains('is-skeleton') &&
        (grid.children.length === 0 || grid.firstElementChild.hasAttribute('data-engine'));
      if (!realGrid) {
        cardRecs = {};
        wrap.innerHTML = '';
        grid = el('div', { class: 'engine-grid' });
        wrap.appendChild(grid);
      }
      var seen = {};
      scoped.forEach(function (eng, idx) {
        seen[eng.name] = true;
        var rec = cardRecs[eng.name];
        if (rec) {
          patchEngineCard(rec, eng);
          if (grid.children[idx] !== rec.node) grid.insertBefore(rec.node, grid.children[idx] || null);
        } else {
          rec = engineCardNode(eng, idx);
          cardRecs[eng.name] = rec;
          grid.appendChild(rec.node);
        }
      });
      Object.keys(cardRecs).forEach(function (name) {
        if (!seen[name]) {
          cardRecs[name].node.remove();
          delete cardRecs[name];
        }
      });
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

    /* 单秒倒计时:刷新所有 data-cd-until 载体(表格纯 span / 冷却看板 cd-block 两种);
     * cd-bar-fill 宽度 1s linear 过渡把每秒一跳内插成连续收缩;到期触发一次静默刷新 */
    function tickCountdowns() {
      var nodes = wrap.querySelectorAll('[data-cd-until]');
      var expired = false;
      var now = Date.now();
      for (var i = 0; i < nodes.length; i++) {
        var node = nodes[i];
        var left = Number(node.getAttribute('data-cd-until')) - now;
        var isBlock = node.classList.contains('cd-block');
        var val = isBlock ? node.querySelector('.cd-value') : node;
        var bar = isBlock ? node.querySelector('.cd-bar') : null;
        if (left > 0) {
          val.textContent = fmtRemaining(left);
          val.classList.remove('done');
          node.classList.remove('is-done');
          if (bar) {
            var total = Number(node.getAttribute('data-cd-total')) || left;
            var pct = Math.max(0, Math.min(100, (left / total) * 100));
            bar.querySelector('.cd-bar-fill').style.width = pct.toFixed(2) + '%';
            bar.setAttribute('aria-valuenow', String(Math.round(pct)));
            bar.setAttribute('aria-label', '冷却剩余 ' + fmtRemaining(left));
          }
        } else {
          val.textContent = '等待恢复探测…';
          val.classList.add('done');
          if (bar) bar.classList.add('is-done');
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
        /* 首进骨架;此后旧内容原位降透明(renderGrid 返回后恢复) */
        if (!wrap.childElementCount) {
          wrap.innerHTML = '';
          wrap.appendChild(skeletonCards(4));
        } else {
          wrap.classList.add('is-busy');
        }
      }
      api('GET', '/api/engines').then(function (res) {
        lastList = normalizeEngines(res);
        renderGrid(lastList);
        tickCountdowns();
      }).catch(function (e) {
        cardRecs = {};
        wrap.classList.remove('is-busy');
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
    function stateKey(eng) {
      return [eng.state, eng.enabled === false ? 'd' : 'e', eng.breaker,
        eng.until ? eng.until.getTime() : 0, eng.reason, eng.consecutiveBlocks,
        eng.lastErrorAt ? eng.lastErrorAt.getTime() : 0, eng.endpoint].join('|');
    }

    function engineCardNode(eng, idx) {
      var vs = engineVizState(eng), info = engineStateInfo(eng);
      var node = el('div', { class: 'engine-card ' + (VIZ_CLS[vs] || 'is-ok'), 'data-engine': eng.name });
      node.style.animationDelay = Math.min((idx || 0) * 45, 360) + 'ms';
      var head = el('div', { class: 'engine-card-head' },
        el('div', { class: 'engine-card-title' },
          el('b', { text: ENGINE_LABEL[eng.name] || eng.name }),
          el('span', { class: 'chip', text: eng.name })),
        el('span', { class: 'pill ' + info.pill + (vs === 'ok' ? ' is-live' : '') + (vs === 'circuit' ? ' is-alarm' : ''), text: info.label }));
      var viz = el('div', { class: 'engine-viz' }, engineViz(eng, 'viz-lg'));
      var btns = buildEngineButtons(eng);
      var facts = el('div', { class: 'engine-facts' });
      facts.appendChild(el('div', { class: 'fact' }, el('b', { text: '端点' }), el('span', { text: eng.endpoint || '—' })));
      facts.appendChild(el('div', { class: 'fact' }, el('b', { text: '冷却原因' }), el('span', { text: eng.reason || '—' })));
      facts.appendChild(el('div', { class: 'fact' }, el('b', { text: '连续拦截' }), el('span', { text: eng.consecutiveBlocks !== null ? eng.consecutiveBlocks + ' 次' : '—' })));
      facts.appendChild(el('div', { class: 'fact' }, el('b', { text: '最近错误' }), el('span', { text: eng.lastErrorAt ? fmtDateTime(eng.lastErrorAt) : '—' })));
      node.appendChild(head);
      node.appendChild(viz);
      node.appendChild(facts);
      node.appendChild(el('div', { class: 'engine-actions' }, btns.resetBtn, btns.forceBtn));
      return {
        node: node, btns: btns, key: stateKey(eng), viz: vs,
        untilKey: String(eng.until ? eng.until.getTime() : 0)
      };
    }

    /* 就地更新已渲染卡片:状态键未变时零 DOM 写入;变化时仅 patch 受影响部分 */
    function patchEngineCard(rec, eng) {
      var key = stateKey(eng);
      if (rec.key === key) return;
      var vs = engineVizState(eng), node = rec.node;
      node.className = 'engine-card ' + (VIZ_CLS[vs] || 'is-ok');
      var info = engineStateInfo(eng);
      var pill = node.querySelector('.engine-card-head .pill');
      pill.className = 'pill ' + info.pill + (vs === 'ok' ? ' is-live' : '') + (vs === 'circuit' ? ' is-alarm' : '');
      pill.textContent = info.label;
      var untilKey = String(eng.until ? eng.until.getTime() : 0);
      if (vs !== rec.viz || untilKey !== rec.untilKey) {
        node.querySelector('.engine-viz').replaceWith(el('div', { class: 'engine-viz' }, engineViz(eng, 'viz-lg')));
        rec.viz = vs;
        rec.untilKey = untilKey;
      }
      var spans = node.querySelectorAll('.engine-facts .fact span');
      if (spans.length >= 4) {
        spans[0].textContent = eng.endpoint || '—';
        spans[1].textContent = eng.reason || '—';
        spans[2].textContent = eng.consecutiveBlocks !== null ? eng.consecutiveBlocks + ' 次' : '—';
        spans[3].textContent = eng.lastErrorAt ? fmtDateTime(eng.lastErrorAt) : '—';
      }
      var needReset = eng.state === 'cooldown' || eng.state === 'circuit_open';
      rec.btns.resetBtn.disabled = !needReset;
      rec.btns.resetBtn.title = needReset ? '' : '引擎当前无需强制恢复';
      rec.btns.forceBtn.disabled = eng.state === 'cooldown';
      rec.btns.forceBtn.title = eng.state === 'cooldown' ? '冷却中,无需再次强制冷却' : '';
      rec.key = key;
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
      function hideErr() {
        err.classList.add('hidden');
      }
      /* P-05:输入即清除既有错误(与登录页范式一致) */
      minutesInput.addEventListener('input', hideErr);

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
    var firstKeysLoad = true;

    function loadAll() {
      /* 首进骨架;此后旧内容原位降透明 */
      if (firstKeysLoad) {
        firstKeysLoad = false;
        card.innerHTML = '';
        card.appendChild(skeletonBlock([{ w: 'w30' }, { w: 'w80' }, { w: 'w80' }, { w: 'w60' }]));
      } else {
        card.classList.add('is-busy');
      }
      api('GET', '/api/keys').then(function (data) {
        keys = Array.isArray(data) ? data : (data && Array.isArray(data.items) ? data.items : []);
        state.keysCache = keys;
        card.classList.remove('is-busy');
        card.innerHTML = '';
        renderTable(true);
      }).catch(function (e) {
        card.classList.remove('is-busy');
        card.innerHTML = '';
        card.appendChild(errorNode(e, loadAll));
      });
    }

    /* 搜索为前端过滤:GET /api/keys 为全量返回,只在已加载列表内筛选 */
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
      card.classList.remove('is-busy');
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
          el('span', { class: 'page-info', text: '在当前结果中筛选 ' + list.length + ' / 共 ' + keys.length + ' 条' }));
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

      // 编辑(P-03:名称 / 日配额,复用既有 PATCH /api/keys/{id})
      var editBtn = el('button', { class: 'btn btn-ghost btn-sm', type: 'button', text: '编辑' });
      editBtn.addEventListener('click', function () {
        openEditKeyModal(k, reload);
      });

      // 轮换(P-03;POST /api/keys/{id}/rotate 为新端点,旧密钥即时失效)
      var rotateBtn = el('button', { class: 'btn btn-ghost btn-sm', type: 'button', text: '轮换' });
      rotateBtn.addEventListener('click', function () {
        confirmDialog({
          title: '轮换密钥',
          message: '确定轮换密钥「' + (k.name || '未命名') + '(' + (k.prefix || '—') + ')」吗?将生成新密钥并立即替换,旧密钥即时失效,已接入的调用方需同步更新。名称、配额与用量计数保留。',
          confirmText: '轮换'
        }).then(function (yes) {
          if (!yes) return;
          setBtnLoading(rotateBtn, true);
          api('POST', '/api/keys/' + encodeURIComponent(String(k.id)) + '/rotate')
            .then(function (created) {
              toast('密钥已轮换,请立即保存新密钥', 'success');
              if (typeof reload === 'function') reload();
              openKeySuccessModal(created || {});
            })
            .catch(function (e) {
              if (e.status === 404) toast('轮换接口需要服务端支持,当前实例尚未提供', 'error');
              else toast(e.message, 'error');
            })
            .then(function () { setBtnLoading(rotateBtn, false); });
        });
      });

      // 删除(确认框)
      var del = el('button', { class: 'btn btn-text-danger', type: 'button', text: '删除' });
      del.addEventListener('click', function () {
        confirmDialog({
          title: '删除密钥',
          message: '确定删除密钥「' + (k.name || '未命名') + '(' + (k.prefix || '—') + ')」吗?删除后使用该密钥的调用将立即失效,且不可恢复。',
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

      // 今日用量 + 配额进度条(P-16:数值常显小字;daily_quota=0 不限,不渲染进度)
      var usedNum = Number(k.used_today) || 0;
      var quotaNum = Number(k.daily_quota) || 0;
      var usedCell;
      if (quotaNum > 0) {
        var pct = Math.min(100, Math.round(usedNum / quotaNum * 100));
        var barCls = 'qbar' + (pct >= 100 ? ' full' : (pct >= 80 ? ' hot' : (pct > 0 ? ' low' : '')));
        usedCell = el('td', { class: 'td-num' },
          el('div', { class: 'qbar-wrap' },
            el('span', { class: 'qbar-num', text: fmtNumber(usedNum) + ' / ' + fmtNumber(quotaNum) + ' · ' + pct + '%' }),
            el('div', { class: barCls, title: '今日 ' + fmtNumber(usedNum) + ' / ' + fmtNumber(quotaNum) + ' 次(' + pct + '%)' },
              el('i', { style: 'width:' + pct + '%' }))));
      } else {
        usedCell = el('td', { class: 'td-num', text: fmtNumber(k.used_today) });
      }

      tbody.appendChild(el('tr', null,
        el('td', { class: 'td-strong', text: k.name || '未命名' }),
        el('td', null, el('span', { class: 'chip', text: k.prefix || '—' })),
        el('td', null, el('span', { class: 'pill ' + (isActive ? 'pill-green' : 'pill-gray'), text: isActive ? '启用' : '停用' }),
          keyExtraChips(k).map(function (c) { return el('span', { class: 'td-chip-gap' }, c); })),
        usedCell,
        el('td', { class: 'td-num', text: fmtNumber(k.used_total) }),
        el('td', { class: 'td-num', text: fmtQuota(k.daily_quota) }),
        el('td', { class: 'td-mono', text: fmtDateTime(k.created_at) }),
        el('td', { class: 'td-mono', text: (k.last_used_at === null || k.last_used_at === undefined || k.last_used_at === '') ? '从未使用' : fmtDateTime(k.last_used_at) }),
        el('td', null, el('div', { class: 'row-actions' }, sw, editBtn, rotateBtn, del))));
    });

    // keys 页无分页:GET /api/keys 全量返回;行多时容器内纵向滚动,不伪造分页控件
    return el('div', { class: 'table-scroll' }, el('table', { class: 'table' }, thead, tbody));
  }

  function fmtQuota(v) {
    if (v === null || v === undefined || v === '' || isNaN(Number(v)) || Number(v) === 0) return '不限';
    return fmtNumber(Number(v));
  }

  /* S4-GAP 密钥增强辅助: 本地时间 ↔ datetime-local 输入值,RFC3339 提交。 */
  function toLocalInputValue(iso) {
    if (!iso) return '';
    var d = new Date(iso);
    if (isNaN(d.getTime())) return '';
    var p2 = function (n) { return (n < 10 ? '0' : '') + n; };
    return d.getFullYear() + '-' + p2(d.getMonth() + 1) + '-' + p2(d.getDate()) + 'T' + p2(d.getHours()) + ':' + p2(d.getMinutes());
  }

  function fromLocalInputValue(v) {
    if (!v) return null; // 空 = 清除过期时间
    var d = new Date(v);
    if (isNaN(d.getTime())) return undefined; // 无效值由调用方提示
    return d.toISOString();
  }

  function parseAllowedIPs(raw) {
    var parts = String(raw || '').split(/[\s,;]+/).filter(function (x) { return x !== ''; });
    return parts.length ? parts : null;
  }

  /* 密钥过期/IP 白名单小徽标(表格状态列,S4-GAP)。 */
  function keyExtraChips(k) {
    var chips = [];
    var exp = k && k.expires_at ? new Date(k.expires_at) : null;
    if (exp && !isNaN(exp.getTime())) {
      var expired = exp.getTime() <= Date.now();
      chips.push(el('span', {
        class: 'chip ' + (expired ? 'chip-red' : 'chip'),
        title: expired ? '该密钥已过期(调用一律 401)' : '过期时间:' + fmtDateTime(k.expires_at),
        text: (expired ? '已过期 ' : '至 ') + fmtDateTime(k.expires_at)
      }));
    }
    var ips = (k && Array.isArray(k.allowed_ips)) ? k.allowed_ips.length : 0;
    if (ips > 0) {
      chips.push(el('span', { class: 'chip', title: 'IP 白名单:' + k.allowed_ips.join(', '), text: ips + ' IP 限制' }));
    }
    return chips;
  }

  /* 创建密钥弹窗 */
  function openCreateKeyModal(onCreated) {
    var nameInput = el('input', { class: 'input', type: 'text', maxlength: '64', placeholder: '例如:生产服务 / 测试环境' });
    var quotaInput = el('input', { class: 'input', type: 'number', min: '0', step: '1', placeholder: '0 表示不限' });
    var expiryInput = el('input', { class: 'input', type: 'datetime-local' });
    var ipsInput = el('input', { class: 'input', type: 'text', placeholder: '留空不限制;多个用逗号分隔' });
    var err = el('div', { class: 'form-err hidden', role: 'alert' });
    var body = el('div', { class: 'modal-body' },
      el('div', { class: 'field' },
        el('label', { class: 'field-label', text: '名称' }),
        nameInput),
      el('div', { class: 'field' },
        el('label', { class: 'field-label', text: '日配额(次 / 天)' }),
        quotaInput,
        el('div', { class: 'form-hint', text: '1 条 URL × 1 个引擎 = 1 次;留空或 0 表示不限量。' })),
      el('div', { class: 'field' },
        el('label', { class: 'field-label', text: '过期时间(可选)' }),
        expiryInput,
        el('div', { class: 'form-hint', text: '留空表示长期有效;到期后调用一律返回 401。' })),
      el('div', { class: 'field' },
        el('label', { class: 'field-label', text: 'IP 白名单(可选)' }),
        ipsInput,
        el('div', { class: 'form-hint', text: '仅允许列表内 IP 调用(单个 IP 或 CIDR 网段);留空不限。' })),
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
    function hideErr() {
      err.classList.add('hidden');
    }

    /* P-05:同表单内输入变化即清除既有错误(与登录页范式一致) */
    [nameInput, quotaInput, expiryInput, ipsInput].forEach(function (inp) { inp.addEventListener('input', hideErr); });

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
      var iso = fromLocalInputValue(expiryInput.value);
      if (iso === undefined) { showErr('过期时间无效'); return; }
      if (iso) payload.expires_at = iso;
      var ips = parseAllowedIPs(ipsInput.value);
      if (ips) payload.allowed_ips = ips;
      hideErr();
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

  /* 编辑密钥弹窗(P-03 名称/日配额 + S4-GAP 过期时间/IP 白名单,复用 PATCH /api/keys/{id}) */
  function openEditKeyModal(k, onSaved) {
    k = k || {};
    var nameInput = el('input', { class: 'input', type: 'text', maxlength: '64', value: String(k.name || '') });
    var quotaInput = el('input', { class: 'input', type: 'number', min: '0', step: '1', placeholder: '0 表示不限', value: (k.daily_quota === null || k.daily_quota === undefined || Number(k.daily_quota) === 0) ? '' : String(k.daily_quota) });
    var expiryInput = el('input', { class: 'input', type: 'datetime-local', value: toLocalInputValue(k.expires_at) });
    var ipsInput = el('input', { class: 'input', type: 'text', placeholder: '留空不限制;多个用逗号分隔', value: (Array.isArray(k.allowed_ips) ? k.allowed_ips.join(', ') : '') });
    var err = el('div', { class: 'form-err hidden', role: 'alert' });
    var body = el('div', { class: 'modal-body' },
      el('div', { class: 'field' },
        el('label', { class: 'field-label', text: '名称' }),
        nameInput),
      el('div', { class: 'field' },
        el('label', { class: 'field-label', text: '日配额(次 / 天)' }),
        quotaInput,
        el('div', { class: 'form-hint', text: '1 条 URL × 1 个引擎 = 1 次;留空或 0 表示不限量。' })),
      el('div', { class: 'field' },
        el('label', { class: 'field-label', text: '过期时间' }),
        expiryInput,
        el('div', { class: 'form-hint', text: '清空并保存表示取消过期(长期有效);到期后调用一律返回 401。' })),
      el('div', { class: 'field' },
        el('label', { class: 'field-label', text: 'IP 白名单' }),
        ipsInput,
        el('div', { class: 'form-hint', text: '单个 IP 或 CIDR 网段,逗号分隔;清空并保存表示不限制。' })),
      err);
    var submitBtn = el('button', { class: 'btn btn-primary', type: 'button', text: '保存' });
    var cancelBtn = el('button', {
      class: 'btn btn-secondary', type: 'button', text: '取消',
      onclick: function () { handle.close(); }
    });
    var foot = el('div', { class: 'modal-foot' }, cancelBtn, submitBtn);
    var handle = openModal({ title: '编辑密钥「' + (k.name || '未命名') + '」', body: body, footer: foot, dismissable: false });

    function showErr(msg) {
      err.textContent = msg;
      err.classList.remove('hidden');
    }
    function hideErr() {
      err.classList.add('hidden');
    }

    /* P-05:输入即清错 */
    [nameInput, quotaInput, expiryInput, ipsInput].forEach(function (inp) { inp.addEventListener('input', hideErr); });

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
      var iso = fromLocalInputValue(expiryInput.value);
      if (iso === undefined) { showErr('过期时间无效'); return; }
      payload.expires_at = iso; // ISO 字符串或 null(显式清除)
      payload.allowed_ips = parseAllowedIPs(ipsInput.value) || []; // 空数组 = 清除限制
      hideErr();
      setBtnLoading(submitBtn, true, '保存中…');
      api('PATCH', '/api/keys/' + encodeURIComponent(String(k.id)), payload)
        .then(function () {
          handle.close();
          toast('密钥已更新', 'success');
          if (typeof onSaved === 'function') onSaved();
        })
        .catch(function (e) {
          showErr(e.message);
        })
        .then(function () {
          setBtnLoading(submitBtn, false);
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
        st.offset = 0;
        loadList(); // S4: 时间范围走服务端 from 参数,需重新拉取
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
    root.appendChild(el('p', { class: 'page-note', text: '时间范围在服务端过滤(翻页总数随之准确);级别筛选作用于当前页记录。' }));

    var card = el('div', { class: 'card' });
    root.appendChild(card);

    var lastItems = [];
    var lastTotal = 0;
    /* 闪屏守卫:首进骨架、此后降透明;listSeq 只采纳最后一次请求(防 tab 快速切换竞态) */
    var firstListLoad = true;
    var listSeq = 0;

    /* S4: GET /api/usage 支持 from/to 服务端时间过滤(时间 tabs 改服务端参数,
     * 分页总数随之准确);级别 chips 仍作用于当前页数据。 */
    function currentUrl() {
      var q = '?limit=' + st.limit + '&offset=' + st.offset;
      if (st.keyId !== '') q += '&key_id=' + encodeURIComponent(st.keyId);
      var range = localFilter.range;
      if (range === 'today') {
        var now = new Date();
        var p2 = function (n) { return (n < 10 ? '0' : '') + n; };
        q += '&from=' + encodeURIComponent(now.getFullYear() + '-' + p2(now.getMonth() + 1) + '-' + p2(now.getDate()));
      } else if (range === '7d' || range === '30d') {
        var days = range === '7d' ? 7 : 30;
        var from = new Date(Date.now() - days * 86400000);
        var p2b = function (n) { return (n < 10 ? '0' : '') + n; };
        q += '&from=' + encodeURIComponent(from.getFullYear() + '-' + p2b(from.getMonth() + 1) + '-' + p2b(from.getDate()));
      }
      return '/api/usage' + q;
    }

    /* 时间过滤已上移服务端(from=);此处保留级别过滤与防御性时间过滤。 */
    function inRange(timeStr) {
      if (localFilter.range === 'all') return true;
      var d = parseWhen(timeStr);
      if (!d) return false;
      var now = new Date();
      if (localFilter.range === 'today') {
        return d.getTime() >= new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
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
      if (firstListLoad) {
        firstListLoad = false;
        card.innerHTML = '';
        card.appendChild(skeletonBlock([{ w: 'w40' }, { w: 'w80' }, { w: 'w80' }, { w: 'w60' }]));
      } else {
        card.classList.add('is-busy');
      }
      var seq = ++listSeq;
      api('GET', currentUrl()).then(function (data) {
        if (seq !== listSeq) return; // 已有更新一次的请求,丢弃过期响应
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
        if (seq !== listSeq) return;
        card.classList.remove('is-busy');
        card.innerHTML = '';
        card.appendChild(errorNode(e, loadList));
      });
    }

    function renderTable() {
      card.classList.remove('is-busy');
      card.innerHTML = '';
      if (!lastItems.length) {
        card.appendChild(emptyNode('暂无调用记录',
          st.keyId ? '当前筛选条件下还没有日志,试试切换密钥或点击刷新。' : '完成一次收录查询后,这里会出现日志。'));
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
            el('div', { class: 'empty-desc', text: '可切换级别或时间范围后重试,或清除密钥筛选再查看。' }))));
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

  /* 使用日志表:全部列保留(时间/密钥/端点/引擎/URL 数/次数/HTTP 状态/耗时);行点击展开详情 */
  /* 使用日志列定义:8 列全部可排序(§4.5 可排序列名后置 ⇅ 图标)。
   * val() 取排序键:时间/数字取数值,文本列取展示字符串。 */
  var USAGE_COLS = [
    { key: 'time', label: '时间', num: false, val: function (it) { var d = parseWhen(it.time); return d ? d.getTime() : 0; } },
    { key: 'key', label: '密钥', num: false, val: function (it) { return String(it.key_name || ('#' + it.key_id) || ''); } },
    { key: 'endpoint', label: '端点', num: false, val: function (it) { return String(it.endpoint || ''); } },
    { key: 'engines', label: '引擎', num: false, val: function (it) { return fmtEngines(it.engines); } },
    { key: 'urls', label: 'URL 数', num: true, val: function (it) { return Number(it.urls) || 0; } },
    { key: 'units', label: '次数', num: true, val: function (it) { return Number(it.units) || 0; } },
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
        el('td', null, el('span', { class: 'chip', text: it.endpoint || '—' })),
        el('td', { text: fmtEngines(it.engines) }),
        el('td', { class: 'td-num', text: fmtNumber(it.urls) }),
        unitsCell(it),
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
      took_ms: Number(it.took_ms) || 0,
      outcome: it.outcome || ''
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

  /* 次数列:0 次说明本次没有任何单元成功(计费口径为实扣,带 error 的单元不
   * 计费)。界面上单看 "HTTP 200 + 次数 0" 会以为是"成功了但没记数",所以对 0
   * 加一条弱化样式与说明,让"这次没产生有效结果"这件事自己说出来。
   * 注意:outcome=billed 只表示请求到达引擎且 HTTP 200,不代表每个单元成功,
   * 因此不能拿它当"成功"标记。 */
  function unitsCell(it) {
    var n = Number(it.units) || 0;
    if (n > 0) return el('td', { class: 'td-num', text: fmtNumber(n) });
    var note = '本次未计费:所有单元都返回 error(或引擎侧失败)';
    return el('td', { class: 'td-num', title: note },
      el('span', { class: 'muted', text: '0' }),
      el('span', { class: 'muted', text: ' · 未计费' }));
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
    var countHint = el('span', { text: '0 条 URL × 4 个引擎 = 0 组检测' });
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
      countHint.textContent = lines.length + ' 条 URL × ' + ec + ' 个引擎 = ' + (lines.length * ec) + ' 组检测';
    }
    urlsTa.addEventListener('input', function () {
      updateCount();
      hideFormErr(); // P-05:输入即清除既有错误(与登录页范式一致)
    });

    var checksBox = el('div', { class: 'pg-checks' });
    ENGINE_ORDER.forEach(function (name) {
      var cb = el('input', { type: 'checkbox', value: name });
      cb.checked = true;
      checksBox.appendChild(el('label', { class: 'pg-check' }, cb, el('span', { text: ENGINE_LABEL[name] })));
    });
    /* P-04:选中态=实心 brand 底 + 勾选标记(样式表);行尾补全选/清空文字钮 */
    var checkActions = el('div', { class: 'pg-check-actions' },
      el('button', {
        class: 'btn btn-text', type: 'button', text: '全选',
        onclick: function () { setAllEngines(true); }
      }),
      el('button', {
        class: 'btn btn-text', type: 'button', text: '清空',
        onclick: function () { setAllEngines(false); }
      }));
    function setAllEngines(checked) {
      checksBox.querySelectorAll('input[type="checkbox"]').forEach(function (cb) { cb.checked = checked; });
      updateCount();
    }
    checksBox.appendChild(checkActions);
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
          el('div', { class: 'form-hint', text: '在线调试不计费。「复制等价 curl」对应 POST /v1/index/check,用 API 密钥发起等价调用时按 URL 数 × 引擎数计次。' })),
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
        showFormErr('单次最多 20 条 URL,当前输入了 ' + urls.length + ' 条,请删减后重试。');
        return;
      }
      if (!engines.length) {
        showFormErr('请至少勾选 1 个检测引擎。');
        return;
      }
      pgState.lastReq = { urls: urls, engines: engines.slice() };
      /* 首次检测显示骨架屏(空态节点不占位);此后旧结果原位降透明 */
      if (!pgState.lastData) {
        termBody.innerHTML = '';
        termBody.style.padding = '';
        termBody.appendChild(skeletonBlock([{ w: 'w30' }, { big: true, w: 'w50' }, { w: 'w80' }, { w: 'w70' }]));
      } else {
        termBody.classList.add('is-busy');
      }
      setFootStatus(null);
      setBtnLoading(submitBtn, true, '检测中…');
      runBatchCheck(urls, engines, Date.now());
    });

    /* 分块串行检测:结果按块渐进上表,不再整包等待大转圈。
     * 引擎限流(如百度 12 次/分钟)是批量耗时的瓶颈,块间串行让令牌
     * 在块与块之间连续累积,块内排队时间被请求预算覆盖;任一块失败
     * 即停,保留已出的结果并标注失败批次。 */
    var PG_CHUNK_SIZE = 6;

    function runBatchCheck(urls, engines, startedAt) {
      var chunks = [];
      for (var i = 0; i < urls.length; i += PG_CHUNK_SIZE) {
        chunks.push(urls.slice(i, i + PG_CHUNK_SIZE));
      }
      var acc = { id: '', cooldowns: null, results: [], done: 0, total: chunks.length };

      function merge(chunkData) {
        chunkData = chunkData || {};
        var meta = chunkData.meta || {};
        if (!acc.id && meta.request_id) acc.id = meta.request_id;
        if (meta.engines_cooldown) acc.cooldowns = meta.engines_cooldown;
        if (Array.isArray(chunkData.results)) acc.results = acc.results.concat(chunkData.results);
        acc.done += 1;
      }

      function currentData(final) {
        var meta = {
          request_id: acc.id,
          took_ms: Date.now() - startedAt,
          units: urls.length * engines.length
        };
        if (acc.cooldowns) meta.engines_cooldown = acc.cooldowns;
        if (!final) meta.progress = { chunk: acc.done, chunks: acc.total };
        if (acc.err) meta.error = { chunk: acc.failed, message: acc.err.message || '' };
        return { id: acc.id, results: acc.results, meta: meta };
      }

      function renderProgress() {
        pgState.lastData = currentData(false);
        pgState.lastStatus = 200;
        setFootStatus(200);
        renderResult();
      }

      var chain = Promise.resolve();
      chunks.forEach(function (chunk, idx) {
        chain = chain.then(function () {
          return api('POST', '/api/playground/check', { urls: chunk, engines: engines })
            .then(function (data) {
              merge(data);
              renderProgress();
            })
            .catch(function (e) {
              acc.err = e;
              acc.failed = idx + 1;
              throw e;
            });
        });
      });

      chain.then(function () {
        pgState.lastData = currentData(true);
        pgState.lastStatus = 200;
        setFootStatus(200);
        renderResult();
      }).catch(function () {
        pgState.lastStatus = typeof acc.err.status === 'number' ? acc.err.status : 0;
        setFootStatus(pgState.lastStatus);
        if (acc.results.length) {
          /* 部分批次已出结果:保留已有行,失败批次以警示条标注 */
          pgState.lastData = currentData(true);
          renderResult();
        } else {
          termBody.classList.remove('is-busy');
          termBody.innerHTML = '';
          termBody.appendChild(errorNode(acc.err, function () { submitBtn.click(); }));
        }
      }).then(function () {
        setBtnLoading(submitBtn, false);
      });
    }

    /* 结果渲染:表格 / JSON 双视图 */
    function renderResult() {
      var data = pgState.lastData || {};
      termBody.classList.remove('is-busy');
      termBody.innerHTML = '';
      termBody.style.padding = '';

      var results = Array.isArray(data.results) ? data.results : [];
      var metaBits = [];
      if (data.meta) {
        if (data.meta.request_id) metaBits.push('request_id ' + data.meta.request_id);
        if (data.meta.took_ms !== undefined) metaBits.push('耗时 ' + fmtDuration(data.meta.took_ms));
        /* 计量口径 B-01:只实扣无错误的单元;分块进行中显示批次进度 */
        if (data.meta.progress) {
          metaBits.push('第 ' + data.meta.progress.chunk + '/' + data.meta.progress.chunks + ' 批 · 已出 ' + okUnitCount(results) + '/' + data.meta.units + ' 条');
        } else if (data.meta.units !== undefined) {
          metaBits.push('计费 ' + okUnitCount(results) + ' / ' + data.meta.units + ' 次');
        }
      }
      var metaRow = el('div', { class: 'pg-meta', style: 'margin-bottom:14px;' });
      metaBits.forEach(function (b) {
        metaRow.appendChild(el('span', { class: 'mchip', text: b }));
      });
      if (data.meta && data.meta.error) {
        metaRow.appendChild(el('span', {
          class: 'mchip warn',
          text: '第 ' + data.meta.error.chunk + ' 批失败' + (data.meta.error.message ? ':' + truncate(data.meta.error.message, 48) : '')
        }));
      }
      var cdList = (data.meta && Array.isArray(data.meta.engines_cooldown)) ? data.meta.engines_cooldown : [];
      cdList.forEach(function (cd) {
        metaRow.appendChild(el('span', {
          class: 'mchip warn',
          title: '冷却至 ' + fmtDateTime(cd.until),
          text: (ENGINE_LABEL[cd.engine] || cd.engine) + ' 冷却中 · 至 ' + fmtDateTime(cd.until)
        }));
      });
      if (metaBits.length || cdList.length) termBody.appendChild(metaRow);

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

    /* okUnitCount 统计无错误的 (URL × 引擎) 单元数,与 B-01 实扣口径一致 */
    function okUnitCount(results) {
      var n = 0;
      (results || []).forEach(function (r) {
        var m = (r && r.engines && typeof r.engines === 'object') ? r.engines : {};
        Object.keys(m).forEach(function (k) {
          if (!(m[k] && m[k].error)) n += 1;
        });
      });
      return n;
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
        p('Web2API 内置 MCP(Model Context Protocol)端点。传输方式为 Streamable HTTP,协议为 JSON-RPC 2.0。'),
        p('Claude Desktop、Claude Code、Dify、n8n、LangChain 等 AI Agent 可直接接入,无需自行封装 HTTP。'),
        el('dl', { class: 'spec-list' },
          el('div', null, el('dt', { text: '端点地址' }), el('dd', null, el('code', { text: MCP_URL }))),
          el('div', null, el('dt', { text: '协议' }), el('dd', null, 'JSON-RPC 2.0(Streamable HTTP)')),
          el('div', null, el('dt', { text: '协议版本' }), el('dd', null, el('code', { text: '2025-06-18' }))),
          el('div', null, el('dt', { text: '支持方法' }), el('dd', null, 'initialize、notifications/initialized、tools/list、tools/call、ping')),
          el('div', null, el('dt', { text: '会话机制' }), el('dd', null, '服务端通过 Mcp-Session-Id 响应头下发会话 ID,后续请求需原样带回'))),
        el('div', { class: 'auth-warn', html: ICONS.warn + '<span><b>调用前必须配置鉴权。</b>请先在「API 密钥」页创建密钥,并为每个请求携带 X-API-Key 请求头。未携带有效密钥的调用会返回 401 {"error":"unauthorized"}。</span>' }),
        codeBlock('HTTP · 请求头', 'X-API-Key: ' + DEMO_KEY),
        el('div', { class: 'docs-note', text: '管理员也可改用 Authorization: Bearer <JWT>(面板登录获得)。管理员发起的 MCP 调用不计费。' }),
        p('计费口径(仅 API 密钥调用计费;管理员 JWT 调用不计费):'),
        el('dl', { class: 'spec-list' },
          el('div', null, el('dt', { text: 'web_search' }), el('dd', null, '每次调用计 1 次')),
          el('div', null, el('dt', { text: 'check_indexed' }), el('dd', null, '按 URL 数 × 引擎数计次')),
          el('div', null, el('dt', { text: 'get_quota、list_engines' }), el('dd', null, '不计费'))),
        el('div', { class: 'agent-links' },
          el('a', { href: OPENAPI_URL, target: '_blank', rel: 'noopener noreferrer', html: ICONS.external + '<span>openapi.yaml(OpenAPI 3.1 全端点)</span>' }),
          el('a', { href: LLMS_URL, target: '_blank', rel: 'noopener noreferrer', html: ICONS.external + '<span>llms.txt(Agent 友好简介)</span>' })),
        el('div', { class: 'docs-note', text: HOST_NOTE }))));

    /* ---- 工具清单 ---- */
    root.appendChild(el('div', { class: 'card' },
      el('div', { class: 'card-head' }, el('h2', { class: 'card-title', text: 'MCP 工具清单' })),
      el('div', { class: 'card-body' },
        paramTable([
          ['web_search', 'object', 'query 必填', '按关键词搜索,返回 rank、title、url、snippet。engine 可选 baidu|sogou|so360|bing,默认 baidu;limit 默认 10'],
          ['check_indexed', 'object', 'urls 必填', '批量查询收录状态,1–5000 条 URL;≤20 条同步返回,>20 条转异步任务并返回 job_id'],
['get_index_job', 'object', 'job_id 必填', '查询异步收录任务进度;include_results=true 时附带已完成的结果行(可分页)'],
['cancel_index_job', 'object', 'job_id 必填', '取消排队中/运行中的收录任务'],
          ['get_quota', 'object', '—', '返回当前密钥的配额与用量'],
          ['list_engines', 'object', '—', '返回引擎清单与冷却、熔断状态']
        ]),
        p('计费:web_search 每次调用计 1 次;check_indexed 按 URL 数 × 引擎数计次;get_quota 与 list_engines 不计费。'),
        p('提示:引擎冷却期间,check_indexed 中对应引擎的查询会返回 <code>engine_cooldown</code> 错误。可先调用 <code>list_engines</code> 确认可用性。'))));

    /* ---- Claude Desktop / Claude Code ---- */
    root.appendChild(agentCard('Claude Desktop / Claude Code', [
      '打开 Claude Desktop「设置 → 开发者 → 编辑配置」(或项目根目录的 <code>.mcp.json</code>)。',
      '将下方 JSON 合并进 <code>mcpServers</code>;headers 中的 <code>X-API-Key</code> 替换为你的密钥。',
      '重启 Claude Desktop,工具面板出现 web_search / check_indexed / get_quota / list_engines 即接入成功。',
      '注意:未配置 <code>X-API-Key</code> 的调用会返回 401 <code>{"error":"unauthorized"}</code>。',
      '配置文件为 JSON,不支持注释,请勿在文件内添加注释。'
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
# 注意:未携带 X-API-Key 的调用会返回 401 {"error":"unauthorized"}`, OPENAPI_URL));

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
#   Authentication: Header Auth(Name: X-API-Key,Value 同上)
# 注意:未携带 X-API-Key 的调用会返回 401 {"error":"unauthorized"}`, BASE));

    /* ---- LangChain Python ---- */
    root.appendChild(agentCard('LangChain / Python', [
      'HTTP 方式:用 <code>requests</code> 直接调用 REST 端点。',
      'MCP 方式:用官方 <code>mcp</code> SDK 连接 Streamable HTTP。'
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
      ['docs-overview', '第一次调用'],
      ['docs-auth', '鉴权'],
      ['docs-index-post', 'POST /v1/index/check'],
      ['docs-index-get', 'GET /v1/index/check'],
      ['docs-index-jobs', '异步任务与 CSV'],
      ['docs-quota', 'GET /v1/quota'],
      ['docs-engines', 'GET /v1/engines'],
      ['docs-mcp', 'MCP /web-tool/mcp'],
      ['docs-admin', '管理 API 与权限'],
      ['docs-health', '健康与引擎状态'],
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

    /* ---- 第一次调用 ---- */
    root.appendChild(docSection({
      id: 'docs-overview',
      title: '第一次调用',
      children: [
        p('服务地址为 <code>' + BASE + '</code>。从其他机器访问时，使用部署后的域名或 IP，并保留 <code>/web-tool</code> 前缀。'),
        ulHtml([
          '在「API 密钥」页创建并保存完整密钥。下方示例中的密钥需要替换。',
          '调用 <code>GET /v1/quota</code> 验证密钥。这个请求不会访问搜索引擎。',
          '用 <code>POST /v1/index/check</code> 提交 URL。只查百度时，传入 <code>"engines":["baidu"]</code>。'
        ]),
        p('1–20 条会在整批结束后返回 <code>200</code>。超过 20 条且任务队列开启时返回 <code>202</code>，可轮询任务并下载 CSV。')
      ]
    }));

    /* ---- 鉴权 ---- */
    root.appendChild(docSection({
      id: 'docs-auth',
      title: '鉴权(X-API-Key / JWT)',
      children: [
        p('公网 API(/v1/*)与 MCP 使用 API 密钥,在「API 密钥」页创建。管理 API(/api/*)使用管理员 JWT,由面板登录获得。'),
        ulHtml([
          '推荐请求头:<code>X-API-Key: sk-w2a-…</code>',
          '兼容写法:<code>Authorization: Bearer sk-w2a-…</code>'
        ]),
        p('密钥无效或已禁用时,返回 <code>401 {"error":"unauthorized"}</code>。当日配额用尽时,返回 <code>429 {"error":"quota_exceeded"}</code>,响应携带 <code>Retry-After</code>,次日零点自动恢复。'),
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
        p('提交 ≤20 条 URL 时，同一引擎按顺序逐条查，不同引擎可以并行。接口等整批结束后一次性返回 JSON。百度默认每分钟放行 5 次、突发额度为 1，20 条约需 4 分钟。'),
        p('超过 20 条且已开启任务队列时，返回 <code>202</code> 和任务 ID。同步按成功单元扣费，未命中也计费。异步按提交的 URL 条数 × 引擎数扣费，重复 URL 也计入。提交前可自行去重。'),
        paramTable([
          ['urls', 'array[string]', '是', '待查询 URL 列表,1..<code>index_jobs.max_urls</code>(默认 5000);&gt;20 条转异步任务'],
          ['engines', 'array[string]', '否', '省略时使用全部可用引擎。只查百度填 <code>["baidu"]</code>。<code>"360"</code> 是 <code>"so360"</code> 的别名。']
        ]),
        codeBlock('cURL',
          'curl -X POST "' + BASE + '/v1/index/check" \\\n' +
          '  -H "X-API-Key: ' + DEMO_KEY + '" \\\n' +
          '  -H "Content-Type: application/json" \\\n' +
          "  -d '{\n" +
          '    "urls": [\n' +
          '      "https://www.onlinedown.net/article/10049071.htm"\n' +
          '    ],\n' +
          '    "engines": ["baidu"]\n' +
          "  }'"),
        codeBlock('JavaScript · fetch',
          'const resp = await fetch("' + BASE + '/v1/index/check", {\n' +
          '  method: "POST",\n' +
          '  headers: {\n' +
          '    "X-API-Key": "' + DEMO_KEY + '",\n' +
          '    "Content-Type": "application/json"\n' +
          '  },\n' +
          '  body: JSON.stringify({\n' +
          '    urls: ["https://www.onlinedown.net/article/10049071.htm"],\n' +
          '    engines: ["baidu"]\n' +
          '  })\n' +
          '});\n' +
          'const data = await resp.json();\n' +
          'if (resp.status !== 200) throw new Error("HTTP " + resp.status + ": " + data.error);\n' +
          'const baidu = data.results[0].engines.baidu;\n' +
          'console.log(baidu.error ? baidu.error.code : baidu.indexed);'),
        p('先判断 HTTP 状态。收到 <code>200</code> 后逐条查看对应引擎结果。含 <code>error</code> 表示查询失败，<code>indexed: false</code> 表示本次查询完成但未命中。'),
        codeBlock('JSON · 200', '{\n' +
          '  "id": "b7e2f9a0-3c4d-4e5f-8a1b-2c3d4e5f6a7b",\n' +
          '  "created_at": "2026-11-25T07:05:39Z",\n' +
          '  "results": [\n' +
          '    {\n' +
          '      "url": "https://www.onlinedown.net/article/10049071.htm",\n' +
          '      "engines": {\n' +
          '        "baidu": {\n' +
          '          "indexed": true,\n' +
          '          "match": "exact",\n' +
          '          "matched_result_url": "http://www.onlinedown.net/article/10049071.htm",\n' +
          '          "total_results": 8,\n' +
          '          "took_ms": 1820\n' +
          '        }\n' +
          '      }\n' +
          '    }\n' +
          '  ],\n' +
          '  "meta": {\n' +
          '    "request_id": "b7e2f9a0-3c4d-4e5f-8a1b-2c3d4e5f6a7b",\n' +
          '    "took_ms": 1830,\n' +
          '    "units": 1\n' +
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
          ['meta.request_id / meta.took_ms / meta.units', '请求 ID / 总耗时（毫秒）/ 请求单元数（URL 数 × 引擎数）'],
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

    /* ---- 异步批量任务 ---- */
    root.appendChild(docSection({
      id: 'docs-index-jobs',
      method: 'GET',
      title: '/v1/index/check/jobs/{id}',
      sub: '批量任务进度 / 结果 / 取消(不计费)',
      children: [
        p('收到 <code>202</code> 后保存 <code>job_id</code>。使用提交时的密钥请求 <code>poll_url</code>，任务进入 <code>done</code> 后用 <code>results_url</code> 获取 JSON，或用 <code>csv_url</code> 下载 CSV。链接已含 <code>/web-tool</code>。'),
        p('所选引擎全部冷却时，任务进入 <code>waiting</code>，可按 <code>wait_seconds</code> 延长轮询间隔。<code>cancelled</code> 和 <code>failed</code> 也是终态，可读取已完成的结果。若提交时连接中断，先查最近任务，重复提交可能再次扣费。'),
        paramTable([
          ['GET …/jobs', '—', '否', '最近任务列表,<code>?limit=</code>(默认 50,最大 500)'],
          ['GET …/jobs/{id}', '—', '—', '进度:<code>status</code>、<code>progress</code>、<code>indexed/not_indexed/failed/retrying</code>、<code>eta_seconds</code>、<code>interval_ms</code>'],
          ['GET …/jobs/{id}/results', '—', '否', '结果行:<code>?offset=&amp;limit=</code> 分页,<code>?format=csv</code> 直接下载表格(UTF-8 BOM)'],
          ['DELETE …/jobs/{id}', '—', '—', '取消排队中/运行中的任务;已结束返回 <code>409 conflict</code>']
        ]),
        codeBlock('cURL',
          'curl "' + BASE + '/v1/index/check/jobs/job_20261124T054613_379221272c" \\\n' +
          '  -H "X-API-Key: ' + DEMO_KEY + '"\n\n' +
          '# 结果下载(CSV,Excel 可直接打开)\n' +
          'curl -OJ "' + BASE + '/v1/index/check/jobs/job_20261124T054613_379221272c/results?format=csv" \\\n' +
          '  -H "X-API-Key: ' + DEMO_KEY + '"'),
        codeBlock('JSON · 200(进度)',
          '{\n' +
          '  "job_id": "job_20261124T054613_379221272c",\n' +
          '  "status": "running",\n' +
          '  "progress": 0.66,\n' +
          '  "total": 800,\n' +
          '  "completed": 530,\n' +
          '  "indexed": 210,\n' +
          '  "not_indexed": 320,\n' +
          '  "failed": 0,\n' +
          '  "retrying": 0,\n' +
          '  "eta_seconds": 3240,\n' +
          '  "interval_ms": 12000,\n' +
          '  "engines": ["baidu"]\n' +
          '}')
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
        p('MCP 端点供 AI Agent 直接调用平台能力。鉴权与计费规则与公网 API 相同(见「鉴权」)。'),
        ulHtml([
          '<code>initialize</code> — 握手,返回协议版本 <code>2025-06-18</code> 与 serverInfo(name 为 <code>web2api</code>)。',
          '<code>notifications/initialized</code> — 握手确认,服务端返回 204。',
          '<code>tools/list</code> — 列出可用工具。',
          '<code>tools/call</code> — 调用工具。',
          '<code>ping</code> — 连通性探测。'
        ]),
        p('服务端通过 <code>Mcp-Session-Id</code> 响应头下发会话 ID,后续请求需原样带回。'),
        paramTable([
          ['web_search', 'object', 'query 必填', '按关键词搜索,返回 rank、title、url、snippet。engine 可选 baidu|sogou|so360|bing,默认 baidu;limit 默认 10'],
          ['check_indexed', 'object', 'urls 必填', '批量查询收录状态。≤20 条同步返回，>20 条转异步任务并返回 job_id。同步按成功单元计费，异步按提交单元计费。'],
['get_index_job', 'object', 'job_id 必填', '查询异步收录任务进度(status/progress/eta_seconds);include_results=true 时附带结果行,不计费'],
['cancel_index_job', 'object', 'job_id 必填', '取消排队中/运行中的收录任务,不计费'],
          ['get_quota', 'object', '—', '返回当前密钥的配额与用量'],
          ['list_engines', 'object', '—', '返回引擎清单与冷却、熔断状态']
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
      title: '管理 API 与权限',
      children: [
        p('登录后，程序可使用 <code>Authorization: Bearer &lt;JWT&gt;</code>，浏览器控制台使用会话 Cookie。角色权限依次为 viewer、operator、admin。角色不足返回 <code>403 insufficient_role</code>。'),
        p('读取状态、统计、审计、用量及在线调试需要 viewer。密钥写操作、引擎冷却操作和黑名单管理需要 operator。用户管理、撤销指定会话、下载备份和日志清理需要 admin。'),
        paramTable([
          ['POST /api/auth/login', '—', '—', '管理员登录,返回 token、expires_in 与 user。同一用户名连续 5 次失败将锁定 15 分钟(换 IP 不重置),期间返回 429 too_many_attempts'],
          ['POST /api/auth/password', 'JSON', '—', '用 old_password 与 new_password 修改当前账号密码。新密码按服务配置校验。配置中的初始管理员改密会使全局旧 JWT 失效，其他账号改密撤销本人会话。'],
          ['GET /api/me', '—', '—', '当前管理员与调用概况'],
          ['POST /api/keys', 'JSON', '—', '创建密钥。name 必填，可传 daily_quota、expires_at 和 allowed_ips。完整密钥仅在创建或轮换时返回。'],
          ['GET /api/keys', '—', '—', '密钥列表(新→旧)'],
          ['PATCH /api/keys/{id}', 'JSON', '—', '更新名称、状态、日配额、过期时间或允许访问的 IP / CIDR。'],
          ['DELETE /api/keys/{id}', '—', '—', '删除密钥'],
          ['GET /api/stats/overview', '—', '—', '总览统计(今日 / 累计 / 24h 成功率)'],
          ['GET /api/stats/timeseries', 'Query', '—', '按日时间序列 ?days=1..90,默认 14'],
          ['GET /api/usage', 'Query', '—', '使用日志 ?limit(≤200)&offset&key_id,分页返回总条数与 items 数组'],
          ['GET /api/engines', '—', '—', '每引擎状态:{ state: ok|cooldown|circuit_open, until, reason, consecutive_blocks, last_error_at }'],
          ['POST /api/playground/check', 'JSON', '—', '面板在线调试。请求体为 urls(1–20 条)与可选 engines;响应结构与 /v1/index/check 完全一致;JWT 鉴权,不计费'],
          ['POST /api/engines/{name}/cooldown/reset', '—', '—', '强制恢复引擎:清除冷却与连续拦截计数'],
          ['POST /api/engines/{name}/cooldown/force', 'JSON', '—', '强制冷却引擎。请求体为 minutes(1–720),供维护窗口使用'],
          ['POST /api/keys/{id}/rotate', '—', '—', '轮换密钥:保留名称 / 配额 / 计数,一次性返回新密钥;旧密钥即时失效'],
          ['GET /api/sessions', '—', '—', '管理员会话列表(IP / 最后活跃 / 当前会话标记)'],
          ['DELETE /api/sessions/{id}', '—', '—', '撤销指定会话;撤销当前会话等同退出登录'],
          ['POST /api/auth/refresh', '—', '—', '滑动续期当前会话'],
          ['POST /api/auth/logout', '—', '—', '退出登录:清除会话凭证'],
          ['GET /api/audit', 'Query', '—', '安全审计轨迹 ?limit&offset,分页返回总条数与 items 数组(动作 / 对象 / 时间 / IP)'],
          ['GET /api/usage/export', 'Query', '—', '用量导出 ?format=csv|json&key_id&from&to'],
          ['GET /api/reconcile', 'Query', '—', '记账对账 ?date=YYYY-MM-DD,返回扣费与流水差异数'],
          ['GET /api/admin/backup', '—', '—', '下载当前存储的 .db 或 .json 快照。'],
          ['GET /api/runtime', '—', '—', '查看内存、goroutine 数量、Go 版本及运行时长。'],
          ['GET /api/users', '—', '—', '查看用户、角色和状态。'],
          ['POST /api/users', 'JSON', '—', '提交 username、password 和 role 创建用户。'],
          ['PATCH /api/users/{id}', 'JSON', '—', '更新角色或状态。'],
          ['DELETE /api/users/{id}', '—', '—', '删除用户。'],
          ['POST /api/users/{id}/password', 'JSON', '—', '提交 new_password 重置用户密码。'],
          ['GET /api/blacklist', '—', '—', '查看 IP 和 CIDR 规则。'],
          ['POST /api/blacklist', 'JSON', '—', '提交 value 创建规则，可设置备注和过期时间。'],
          ['PATCH /api/blacklist/{id}', 'JSON', '—', '修改备注或过期时间。'],
          ['DELETE /api/blacklist/{id}', '—', '—', '删除规则。'],
          ['GET /api/logs', 'Query', '—', '使用 level、q、since、limit 和 offset 查询服务日志。'],
          ['GET /api/logs/config', '—', '—', '查看日志轮转和保留设置。'],
          ['POST /api/logs/cleanup', '—', '—', '按当前保留策略清理过期归档日志。']
        ])
      ]
    }));

    root.appendChild(docSection({
      id: 'docs-health',
      title: '健康与引擎状态',
      children: [
        fieldTable([
          ['GET /health', '公开。整体健康或部分引擎可用时返回 200，全部不可用时返回 503。'],
          ['GET /ready', '公开。返回 ready，服务停机排水时返回 503 和 draining。'],
          ['GET /engines/status', '公开。返回冷却与熔断状态，冷却时可读取 <code>until</code>。该请求不会访问搜索引擎。'],
          ['GET /stats、/stats/cache、/stats/proxy、/stats/cb', '配置模板默认需要 API Key，由 <code>security.data_plane_auth</code> 控制。'],
          ['GET /metrics', '配置了指标专用 token 时使用 Bearer 鉴权，否则只允许回环来源。']
        ])
      ]
    }));

    /* ---- 限流与冷却 ---- */
    root.appendChild(docSection({
      id: 'docs-ratelimit',
      title: '限流与引擎冷却',
      children: [
        ulHtml([
          '<strong>管理 API 限速</strong>:默认 60 次/分钟。超限返回 429,响应携带 <code>Retry-After</code>。',
          '<strong>登录防爆破</strong>:登录失败按用户名在不同来源间累计。达到实例配置的阈值后，登录返回 <code>429 {"error":"too_many_attempts"}</code> 与 <code>Retry-After</code>。',
          '<strong>引擎冷却</strong>由拦截（403）、429 或验证码触发。默认三档为 10、30、120 分钟，随仓库提供的配置将百度覆盖为 2、10、30 分钟。正常查完一批没有额外冷却，仍按引擎限流。',
          '同步收录请求开始时，若所选引擎全部已冷却，返回 <code>503 engine_cooldown</code> 和 <code>Retry-After</code>。若部分已冷却或批内触发冷却，被跳过的单元在 200 响应内给出 <code>engine_cooldown</code> 和剩余秒数。异步任务可以等待冷却结束。',
          '<strong>到期恢复</strong>:冷却到期后放行一个探测请求,成功即恢复,再次被拦截进下一档,其他失败(如超时)按当前档顺延重试。管理员可在「引擎状态」页强制恢复或强制冷却。'
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
          ['400', 'bad_request', '参数错误。reason:EMPTY_QUERY(URL 列表为空)、TOO_MANY_URLS(未开启 index_jobs 时 >20 条,或超过 index_jobs.max_urls)、INVALID_PARAM(参数非法)'],
          ['401', 'unauthorized', '密钥无效 / 已禁用,或管理员 JWT 缺失、过期'],
          ['403', 'blocked', '搜索引擎反爬拦截了请求(引擎级错误同样会以 code=blocked 出现在单元结果中)'],
          ['403', 'forbidden', '该请求被拒绝访问(如来源不在允许范围内)'],
          ['404', 'not_found', '接口或资源不存在(公网 API 挂在 /web-tool 前缀下,根路径 404)'],
          ['405', 'method_not_allowed', '该接口不支持此 HTTP 方法(如对 /v1/engines 使用 POST)'],
          ['429', 'quota_exceeded', '当日配额已用尽;带 <code>Retry-After</code>'],
          ['429', 'rate_limited', '请求过于频繁(公网或管理面限速);带 <code>Retry-After</code>'],
          ['429', 'too_many_attempts', '登录失败次数过多,已锁定;带 <code>Retry-After</code>'],
          ['429', 'captcha_detected', '引擎返回验证码(引擎级 429 不影响批量其它引擎)'],
          ['503', 'engine_cooldown', '同步查询开始时所选引擎全部冷却。按 <code>Retry-After</code> 或 <code>retry_after_seconds</code> 等待。'],
          ['503', 'job_queue_full / server_draining', '异步提交未入队也未扣费。按 <code>Retry-After</code> 等待后再提交。'],
          ['503', 'circuit_open / proxy_*', '熔断开启或代理类失败(auth / connect / timeout / unavailable)'],
          ['504', 'search_timeout / request_timeout / engine_queue_exceeded', '引擎搜索超时 / 整体请求超时 / 批量限流排队超出请求预算'],
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
          ['200', 'engine_queue_exceeded', '该单元在引擎限流队列中等待超预算,未发起真实查询'],
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
          '<a href="' + BASE + '/openapi.yaml" target="_blank" rel="noopener noreferrer">openapi.yaml</a> — 公开 API 的 OpenAPI 3.1 规范，导入工具时可加 <code>?raw=1</code> 获取 YAML',
          '<a href="' + BASE + '/llms.txt" target="_blank" rel="noopener noreferrer">llms.txt</a> — 平台简介 + 端点清单(Agent 友好格式)',
          'MCP 接入配置示例见「<a href="#/agents">Agent 集成</a>」页(Claude / Dify / n8n / LangChain)。'
        ])
      ]
    }));
  }

  /* ================= 8d. 页面:系统设置(V4 §10.6:左锚点 + 右表单卡) ================= */

  function pageSettings(root) {
    /* 左分组锚点(安全 / 数据 / 系统)+ scrollspy,复用文档页范式 */
    var NAVS = [
      ['st-account', '账户安全', '安全'],
      ['st-security', '安全机制', '安全'],
      ['st-sessions', '会话管理', '数据'],
      ['st-audit', '安全审计', '数据'],
      ['st-backup', '数据备份', '系统']
    ];
    var tocLinks = [];
    var nav = el('nav', { class: 'settings-nav', 'aria-label': '设置目录' });
    var lastGroup = '';
    NAVS.forEach(function (n) {
      if (n[2] !== lastGroup) {
        nav.appendChild(el('div', { class: 'nav-label', text: n[2] }));
        lastGroup = n[2];
      }
      var a = el('a', {
        href: 'javascript:void(0)',
        text: n[1],
        onclick: function () {
          setActiveToc(a);
          var target = document.getElementById(n[0]);
          if (target) target.scrollIntoView({ behavior: 'auto', block: 'start' });
        }
      });
      tocLinks.push({ link: a, id: n[0] });
      nav.appendChild(a);
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
    setPageInterval(syncTocScroll, 400);

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
          // 成功响应携带新 token,当前会话续期;仅配置管理员改密会全局失效旧 JWT
          clearAuth();
          toast('密码已修改,当前会话已续期', 'success');
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

    var pwdCard = el('form', { class: 'card', id: 'st-account', onsubmit: function (e) { e.preventDefault(); } },
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
        el('div', { class: 'form-hint', style: 'margin-top:12px;', text: '修改成功后当前会话自动使用新 token；其它会话与旧 token 立即失效。' }))); // P-12:脚注缩短,完整表述仅保留在「安全机制」卡

    /* ---- 安全机制说明 ---- */
    var infoCard = el('div', { class: 'card', id: 'st-security' },
      el('div', { class: 'card-head' }, el('h2', { class: 'card-title', text: '安全机制' })),
      el('div', { class: 'card-body' },
        el('ul', { class: 'info-list' },
          el('li', null, el('b', { text: '登录锁定:' }), '登录失败按用户名累计。达到配置的阈值后返回 429，响应中的 Retry-After 表示可重试时间。'),
          el('li', null, el('b', { text: '管理 API 限速:' }), '管理 API(/api/*)默认限速 60 次/分钟,超限返回 429,响应携带 Retry-After。'),
          el('li', null, el('b', { text: '会话失效:' }), '密码以 bcrypt 哈希存储。修改后当前浏览器自动换用新 token；配置管理员改密会使全部旧 token 与其它会话立即失效，其它账号只撤销本人会话。'),
          el('li', null, el('b', { text: '公网鉴权:' }), '公网 API 与 MCP 均使用 X-API-Key。密钥以哈希存储,面板中仅可见前缀。'))));

    /* ---- 会话管理(UI 壳:GET /api/sessions 随服务端新版本提供) ---- */
    var sessionBody = el('div', { class: 'card-body' });
    var sessionsCard = el('div', { class: 'card', id: 'st-sessions' },
      el('div', { class: 'card-head' },
        el('h2', { class: 'card-title' }, '会话管理', el('span', { class: 'card-head-sub', text: '当前登录中的管理员会话' })),
        el('button', {
          class: 'btn btn-secondary btn-sm', type: 'button', text: '撤销其他会话',
          onclick: function () { revokeOtherSessions(); }
        })),
      sessionBody);

    function revokeOtherSessions() {
      api('GET', '/api/sessions').then(function (data) {
        var items = (data && Array.isArray(data.items)) ? data.items : [];
        var others = items.filter(function (s) { return s && !s.current; });
        if (!others.length) {
          toast('没有其他活跃会话', 'info');
          return;
        }
        confirmDialog({
          title: '撤销其他会话',
          message: '确定撤销除当前浏览器以外的 ' + others.length + ' 个会话吗?对应设备下次请求时将被要求重新登录。',
          confirmText: '撤销'
        }).then(function (yes) {
          if (!yes) return;
          var jobs = others.map(function (s) {
            return api('DELETE', '/api/sessions/' + encodeURIComponent(String(s.id)));
          });
          Promise.all(jobs)
            .then(function () {
              toast('已撤销 ' + others.length + ' 个其他会话', 'success');
              loadSessions();
            })
            .catch(function (e) { toast(e.message, 'error'); });
        });
      }).catch(function (e) { toast(e.message, 'error'); });
    }

    function revokeSession(id) {
      api('DELETE', '/api/sessions/' + encodeURIComponent(String(id)))
        .then(function () {
          toast('会话已撤销', 'success');
          loadSessions();
        })
        .catch(function (e) { toast(e.message, 'error'); });
    }

    function shellUnavailable(body, what) {
      body.classList.remove('is-busy');
      body.innerHTML = '';
      body.appendChild(el('div', { class: 'shell-note', text: what + '需要服务端对应接口支持(随平台新版本提供),当前实例尚未启用。' }));
    }

    var firstSessionsLoad = true;
    function loadSessions() {
      /* 首进骨架;此后旧内容原位降透明 */
      if (firstSessionsLoad) {
        firstSessionsLoad = false;
        sessionBody.innerHTML = '';
        sessionBody.appendChild(skeletonBlock([{ w: 'w40' }, { w: 'w80' }, { w: 'w70' }]));
      } else {
        sessionBody.classList.add('is-busy');
      }
      api('GET', '/api/sessions').then(function (data) {
        var items = (data && Array.isArray(data.items)) ? data.items : [];
        sessionBody.classList.remove('is-busy');
        sessionBody.innerHTML = '';
        if (!items.length) {
          sessionBody.appendChild(el('div', { class: 'shell-note', text: '当前没有活跃会话记录。' }));
          return;
        }
        var tbody = el('tbody');
        items.forEach(function (s) {
          s = s || {};
          var isCur = !!s.current;
          var row = el('tr', { class: isCur ? 'row-open' : '' },
            el('td', { class: 'td-mono', text: s.ip || '—' }),
            el('td', { class: 'td-mono', text: (s.last_active_at || s.last_active) ? fmtDateTime(s.last_active_at || s.last_active) : '—' }),
            el('td', null, el('span', { class: 'pill ' + (isCur ? 'pill-green' : 'pill-gray'), text: isCur ? '当前会话' : '活跃' })),
            el('td', null, el('div', { class: 'row-actions' },
              el('button', {
                class: 'btn btn-ghost btn-sm', type: 'button', text: '撤销',
                onclick: function () {
                  confirmDialog({
                    title: '撤销会话',
                    message: isCur ? '这是当前浏览器会话,撤销后将立即退出登录。确定继续吗?' : '确定撤销该会话吗?对应设备将被要求重新登录。',
                    confirmText: '撤销'
                  }).then(function (yes) { if (yes) revokeSession(s.id); });
                }
              }))));
          tbody.appendChild(row);
        });
        sessionBody.appendChild(el('div', { class: 'table-wrap' }, el('table', { class: 'table' },
          el('thead', null, el('tr', null,
            el('th', { text: '来源 IP' }),
            el('th', { text: '最近活跃' }),
            el('th', { text: '状态' }),
            el('th', { text: '操作' }))),
          tbody)));
      }).catch(function (e) {
        sessionBody.classList.remove('is-busy');
        if (e.status === 404 || e.status === 401) shellUnavailable(sessionBody, '会话列表');
        else {
          sessionBody.innerHTML = '';
          sessionBody.appendChild(el('div', { class: 'shell-note', text: '会话列表加载失败:' + (e.message || '网络异常') }));
        }
      });
    }

    /* ---- 安全审计(UI 壳:GET /api/audit 随服务端新版本提供) ---- */
    var auditBody = el('div', { class: 'card-body' });
    var auditCard = el('div', { class: 'card', id: 'st-audit' },
      el('div', { class: 'card-head' },
        el('h2', { class: 'card-title' }, '安全审计', el('span', { class: 'card-head-sub', text: '管理员操作记录(最近 20 条)' })),
        el('button', {
          class: 'btn btn-secondary btn-sm', type: 'button', text: '刷新',
          onclick: function () { loadAudit(); }
        })),
      auditBody);

    var firstAuditLoad = true;
    function loadAudit() {
      /* 首进骨架;此后旧内容原位降透明 */
      if (firstAuditLoad) {
        firstAuditLoad = false;
        auditBody.innerHTML = '';
        auditBody.appendChild(skeletonBlock([{ w: 'w40' }, { w: 'w80' }, { w: 'w70' }]));
      } else {
        auditBody.classList.add('is-busy');
      }
      api('GET', '/api/audit?limit=20').then(function (data) {
        var items = (data && Array.isArray(data.items)) ? data.items : [];
        auditBody.classList.remove('is-busy');
        auditBody.innerHTML = '';
        if (!items.length) {
          auditBody.appendChild(el('div', { class: 'shell-note', text: '暂无审计记录。' }));
          return;
        }
        var tbody = el('tbody');
        items.forEach(function (a) {
          a = a || {};
          tbody.appendChild(el('tr', null,
            el('td', null, el('span', { class: 'chip', text: a.action || '—' })),
            el('td', { class: 'td-mono', text: a.target || '—' }),
            el('td', { class: 'td-mono', text: fmtDateTime(a.ts || a.time) }),
            el('td', { class: 'td-mono', text: a.ip || '—' })));
        });
        auditBody.appendChild(el('div', { class: 'table-wrap' }, el('table', { class: 'table' },
          el('thead', null, el('tr', null,
            el('th', { text: '动作' }),
            el('th', { text: '对象' }),
            el('th', { text: '时间' }),
            el('th', { text: '来源 IP' }))),
          tbody)));
      }).catch(function (e) {
        auditBody.classList.remove('is-busy');
        if (e.status === 404 || e.status === 401) shellUnavailable(auditBody, '审计轨迹');
        else {
          auditBody.innerHTML = '';
          auditBody.appendChild(el('div', { class: 'shell-note', text: '审计轨迹加载失败:' + (e.message || '网络异常') }));
        }
      });
    }

    /* ---- 数据备份(UI 壳:GET /api/admin/backup 随服务端新版本提供) ---- */
    var backupBody = el('div', { class: 'card-body' });
    var backupCard = el('div', { class: 'card', id: 'st-backup' },
      el('div', { class: 'card-head' }, el('h2', { class: 'card-title', text: '数据备份' })),
      backupBody);

    function downloadBackup() {
      toast('正在生成备份快照…', 'info');
      fetch(API_BASE + '/api/admin/backup', {
        credentials: 'same-origin',
        headers: { 'Authorization': 'Bearer ' + getToken() }
      }).then(function (res) {
        if (res.status === 404) throw new Error('备份接口需要服务端支持,当前实例尚未提供');
        if (!res.ok) throw new Error('下载失败(HTTP ' + res.status + ')');
        return res.blob().then(function (blob) {
          var d = new Date();
          var name = 'web2api-backup-' +
            d.getFullYear() + pad2(d.getMonth() + 1) + pad2(d.getDate()) +
            '-' + pad2(d.getHours()) + pad2(d.getMinutes()) + pad2(d.getSeconds()) + '.db';
          var url = URL.createObjectURL(blob);
          var a = document.createElement('a');
          a.href = url;
          a.download = name;
          document.body.appendChild(a);
          a.click();
          document.body.removeChild(a);
          setTimeout(function () { URL.revokeObjectURL(url); }, 4000);
          toast('备份已开始下载:' + name, 'success');
        });
      }).catch(function (e) {
        toast(e.message, 'error');
      });
    }

    backupBody.appendChild(el('div', null,
      el('p', { class: 'card-desc', text: '下载数据库的一致性快照(密钥、用量、审计与配置数据)。' }),
      el('div', { style: 'margin-top:12px;display:flex;gap:10px;flex-wrap:wrap;' },
        el('button', {
          class: 'btn btn-primary', type: 'button', text: '下载备份快照',
          onclick: downloadBackup
        })),
      el('div', { class: 'form-hint', style: 'margin-top:12px;', text: '建议定期下载备份,并在停服窗口演练恢复流程。' })));

    /* ---- 布局:左锚点 + 右卡片流 ---- */
    var cardsCol = el('div', { class: 'settings-cards' },
      el('div', { class: 'settings-grid' }, pwdCard, infoCard),
      sessionsCard,
      auditCard,
      backupCard);
    root.appendChild(el('div', { class: 'settings-layout' }, nav, cardsCol));

    loadSessions();
    loadAudit();
  }

  /* ================= 8b. 页面:用户管理 / 系统日志 / 黑名单 =================
   * 角色规则:admin 全部可写;operator 可管理黑名单与清理日志,用户列表只读;
   * viewer 三组页面均只读(隐藏写按钮 + 就地提示,不强制登出)。
   * 403 的 insufficient_role / blacklisted 由 extractErrMsg 转成中文提示。 */

  var ROLE_LABEL = { admin: '管理员', operator: '运维员', viewer: '只读用户' };

  function currentRole() { return state.role || 'viewer'; }
  function canWriteUsers() { return currentRole() === 'admin'; }
  function canWriteBlacklist() { return currentRole() !== 'viewer'; }
  function canCleanupLogs() { return currentRole() === 'admin'; }

  /* 只读提示:留在页面内,不弹窗、不跳转 */
  function roleHintNode(text) {
    return el('div', { class: 'role-hint', text: text });
  }

  /* 通用分页:不依赖 state.usage,供本组页面复用 */
  function buildAdminPagination(total, offset, limit, onChange) {
    total = Number(total) || 0;
    var from = total ? offset + 1 : offset;
    var to = Math.min(total, offset + limit);
    var prevBtn = el('button', { class: 'btn btn-secondary btn-sm', type: 'button', text: '上一页' });
    prevBtn.disabled = offset <= 0;
    prevBtn.addEventListener('click', function () { onChange(Math.max(0, offset - limit)); });
    var nextBtn = el('button', { class: 'btn btn-secondary btn-sm', type: 'button', text: '下一页' });
    nextBtn.disabled = to >= total;
    nextBtn.addEventListener('click', function () { onChange(offset + limit); });
    return el('div', { class: 'pagination' },
      el('span', { class: 'page-info', text: '第 ' + from + ' – ' + to + ' 条,共 ' + fmtNumber(total) + ' 条' }),
      prevBtn, nextBtn);
  }

  function rolePill(role) {
    var r = String(role || '');
    var cls = r === 'admin' ? 'pill-indigo' : (r === 'operator' ? 'pill-amber' : 'pill-gray');
    return el('span', { class: 'pill ' + cls, text: ROLE_LABEL[r] || (r || '未知') });
  }

  function userStatusPill(status) {
    var s = String(status || '').toLowerCase();
    var active = (s === 'active' || s === 'enabled' || s === '正常' || s === '1');
    return el('span', { class: 'pill ' + (active ? 'pill-green' : 'pill-gray'), text: active ? '启用' : '停用' });
  }

  function logLevelPill(level) {
    var lv = String(level || '').toLowerCase();
    var cls = 'pill-gray';
    if (lv === 'error' || lv === 'fatal') cls = 'pill-red';
    else if (lv === 'warn' || lv === 'warning') cls = 'pill-amber';
    else if (lv === 'info') cls = 'pill-indigo';
    return el('span', { class: 'pill ' + cls, text: lv ? lv.toUpperCase() : '未知' });
  }

  function buildRoleSelect(current) {
    var sel = el('select', { class: 'inline-select', 'aria-label': '角色' });
    [['admin', '管理员'], ['operator', '运维员'], ['viewer', '只读用户']].forEach(function (p) {
      sel.appendChild(el('option', { value: p[0], text: p[1] }));
    });
    sel.value = (current === 'admin' || current === 'operator' || current === 'viewer') ? current : 'viewer';
    return sel;
  }

  function buildUserStatusSelect(current) {
    var sel = el('select', { class: 'inline-select', 'aria-label': '状态' });
    sel.appendChild(el('option', { value: 'active', text: '启用' }));
    sel.appendChild(el('option', { value: 'disabled', text: '停用' }));
    sel.value = (current === 'active' || current === 'enabled') ? 'active' : 'disabled';
    return sel;
  }

  /* ---------- 用户管理 ---------- */

  function pageUsers(root, actions) {
    var st = { offset: 0, limit: 20 };
    var all = [];
    var first = true;
    var seq = 0;

    actions.appendChild(el('button', {
      class: 'btn btn-secondary btn-sm', type: 'button',
      html: ICONS.refresh + '<span>刷新</span>',
      onclick: function () { loadAll(); }
    }));
    if (canWriteUsers()) {
      actions.appendChild(el('button', {
        class: 'btn btn-primary btn-sm', type: 'button',
        html: ICONS.plus + '<span>新建用户</span>',
        onclick: function () { openCreateUserModal(loadAll); }
      }));
    } else {
      root.appendChild(roleHintNode('当前角色无权查看用户列表;请使用管理员账号登录。'));
    }

    var card = el('div', { class: 'card' });
    root.appendChild(card);

    function loadAll() {
      if (first) {
        first = false;
        card.innerHTML = '';
        card.appendChild(skeletonBlock([{ w: 'w30' }, { w: 'w80' }, { w: 'w60' }]));
      } else {
        card.classList.add('is-busy');
      }
      var cur = ++seq;
      api('GET', '/api/users').then(function (data) {
        if (cur !== seq) return;
        card.classList.remove('is-busy');
        data = data || {};
        all = Array.isArray(data.items) ? data.items : [];
        renderTable();
      }).catch(function (e) {
        if (cur !== seq) return;
        card.classList.remove('is-busy');
        card.innerHTML = '';
        card.appendChild(errorNode(e, loadAll));
      });
    }

    function renderTable() {
      card.classList.remove('is-busy');
      card.innerHTML = '';
      if (!all.length) {
        card.appendChild(emptyNode('暂无用户', '还没有用户记录,可由管理员创建管理员 / 运维 / 只读账号。'));
        return;
      }
      var page = all.slice(st.offset, st.offset + st.limit);
      if (!page.length && st.offset > 0) {
        st.offset = Math.max(0, Math.floor((all.length - 1) / st.limit) * st.limit);
        page = all.slice(st.offset, st.offset + st.limit);
      }
      card.appendChild(buildUsersTable(page, loadAll));
      card.appendChild(buildAdminPagination(all.length, st.offset, st.limit, function (next) {
        st.offset = next;
        renderTable();
      }));
    }
    loadAll();
  }

  function buildUsersTable(items, reload) {
    var thead = el('thead', null, el('tr', null,
      el('th', { text: '用户名' }),
      el('th', { text: '角色' }),
      el('th', { text: '状态' }),
      el('th', { text: '最后登录' }),
      el('th', { text: '创建时间' }),
      el('th', { text: '操作' })));
    var tbody = el('tbody');

    items.forEach(function (u) {
      u = u || {};
      var uid = encodeURIComponent(String(u.id));
      var uname = u.username || '—';
      var acts = [];
      if (canWriteUsers()) {
        acts.push(el('button', {
          class: 'btn btn-ghost btn-sm', type: 'button', text: '编辑',
          onclick: function () { openEditUserModal(u, reload); }
        }));
        acts.push(el('button', {
          class: 'btn btn-ghost btn-sm', type: 'button', text: '重置密码',
          onclick: function () { openResetUserPasswordModal(u, reload); }
        }));
        var delBtn = el('button', { class: 'btn btn-text-danger', type: 'button', text: '删除' });
        delBtn.addEventListener('click', function () {
          confirmDialog({
            title: '删除用户',
            message: '确定删除用户「' + uname + '」吗?该用户将无法再登录,操作不可恢复。',
            confirmText: '删除'
          }).then(function (yes) {
            if (!yes) return;
            setBtnLoading(delBtn, true);
            api('DELETE', '/api/users/' + uid)
              .then(function () { toast('用户已删除', 'success'); reload(); })
              .catch(function (e) { toast(e.message, 'error'); setBtnLoading(delBtn, false); });
          });
        });
        acts.push(delBtn);
      }
      tbody.appendChild(el('tr', null,
        el('td', { class: 'td-strong', text: uname }),
        el('td', null, rolePill(u.role)),
        el('td', null, userStatusPill(u.status)),
        el('td', { class: 'td-mono', text: u.last_login_at ? fmtDateTime(u.last_login_at) : '从未登录' }),
        el('td', { class: 'td-mono', text: fmtDateTime(u.created_at) }),
        el('td', null, acts.length ? el('div', { class: 'row-actions' }, acts) : el('span', { class: 'muted', text: '—' }))));
    });
    return el('div', { class: 'table-scroll' }, el('table', { class: 'table' }, thead, tbody));
  }

  function openCreateUserModal(onDone) {
    var nameInput = el('input', { class: 'input', type: 'text', maxlength: '64', autocomplete: 'off', placeholder: '请输入用户名' });
    var pwdInput = el('input', { class: 'input', type: 'password', maxlength: '128', autocomplete: 'new-password', placeholder: '至少 8 位' });
    var roleSelect = buildRoleSelect('viewer');
    var err = el('div', { class: 'form-err hidden', role: 'alert' });
    var body = el('div', { class: 'modal-body' },
      el('div', { class: 'field' }, el('label', { class: 'field-label', text: '用户名' }), nameInput),
      el('div', { class: 'field' },
        el('label', { class: 'field-label', text: '密码' }), pwdInput,
        el('div', { class: 'form-hint', text: '至少 8 位,建议混合字母 / 数字 / 符号。' })),
      el('div', { class: 'field' }, el('label', { class: 'field-label', text: '角色' }), roleSelect),
      err);
    var submitBtn = el('button', { class: 'btn btn-primary', type: 'button', text: '创建' });
    var cancelBtn = el('button', { class: 'btn btn-secondary', type: 'button', text: '取消', onclick: function () { handle.close(); } });
    var foot = el('div', { class: 'modal-foot' }, cancelBtn, submitBtn);
    var handle = openModal({ title: '新建用户', body: body, footer: foot, dismissable: false });

    function showErr(msg) { err.textContent = msg; err.classList.remove('hidden'); }
    [nameInput, pwdInput, roleSelect].forEach(function (inp) {
      inp.addEventListener('input', function () { err.classList.add('hidden'); });
      inp.addEventListener('change', function () { err.classList.add('hidden'); });
    });
    submitBtn.addEventListener('click', function () {
      var name = nameInput.value.trim();
      if (!name) { showErr('请输入用户名'); return; }
      if (pwdInput.value.length < 8) { showErr('密码至少 8 位'); return; }
      err.classList.add('hidden');
      setBtnLoading(submitBtn, true, '创建中…');
      cancelBtn.disabled = true;
      api('POST', '/api/users', { username: name, password: pwdInput.value, role: roleSelect.value })
        .then(function () { handle.close(); toast('用户已创建', 'success'); if (onDone) onDone(); })
        .catch(function (e) { showErr(e.message); })
        .then(function () { setBtnLoading(submitBtn, false); cancelBtn.disabled = false; });
    });
    nameInput.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') { e.preventDefault(); submitBtn.click(); }
    });
    setTimeout(function () { nameInput.focus(); }, 0);
  }

  function openEditUserModal(u, onDone) {
    u = u || {};
    var roleSelect = buildRoleSelect(u.role);
    var statusSelect = buildUserStatusSelect(u.status);
    var err = el('div', { class: 'form-err hidden', role: 'alert' });
    var body = el('div', { class: 'modal-body' },
      el('div', { class: 'field' }, el('label', { class: 'field-label', text: '角色' }), roleSelect),
      el('div', { class: 'field' }, el('label', { class: 'field-label', text: '状态' }), statusSelect),
      err);
    var submitBtn = el('button', { class: 'btn btn-primary', type: 'button', text: '保存' });
    var cancelBtn = el('button', { class: 'btn btn-secondary', type: 'button', text: '取消', onclick: function () { handle.close(); } });
    var foot = el('div', { class: 'modal-foot' }, cancelBtn, submitBtn);
    var handle = openModal({ title: '编辑用户「' + (u.username || '—') + '」', body: body, footer: foot, dismissable: false });

    [roleSelect, statusSelect].forEach(function (inp) {
      inp.addEventListener('change', function () { err.classList.add('hidden'); });
    });
    submitBtn.addEventListener('click', function () {
      err.classList.add('hidden');
      setBtnLoading(submitBtn, true, '保存中…');
      cancelBtn.disabled = true;
      api('PATCH', '/api/users/' + encodeURIComponent(String(u.id)), {
        role: roleSelect.value, status: statusSelect.value
      })
        .then(function () { handle.close(); toast('用户已更新', 'success'); if (onDone) onDone(); })
        .catch(function (e) {
          err.textContent = e.message;
          err.classList.remove('hidden');
        })
        .then(function () { setBtnLoading(submitBtn, false); cancelBtn.disabled = false; });
    });
    setTimeout(function () { roleSelect.focus(); }, 0);
  }

  function openResetUserPasswordModal(u, onDone) {
    u = u || {};
    var pwdInput = el('input', { class: 'input', type: 'password', maxlength: '128', autocomplete: 'new-password', placeholder: '新密码至少 8 位' });
    var confirmInput = el('input', { class: 'input', type: 'password', maxlength: '128', autocomplete: 'new-password', placeholder: '再次输入新密码' });
    var err = el('div', { class: 'form-err hidden', role: 'alert' });
    var body = el('div', { class: 'modal-body' },
      el('div', { class: 'field' },
        el('label', { class: 'field-label', text: '新密码' }), pwdInput,
        el('div', { class: 'form-hint', text: '重置后该用户旧密码立即失效,请线下告知本人。' })),
      el('div', { class: 'field' }, el('label', { class: 'field-label', text: '确认新密码' }), confirmInput),
      err);
    var submitBtn = el('button', { class: 'btn btn-primary', type: 'button', text: '重置' });
    var cancelBtn = el('button', { class: 'btn btn-secondary', type: 'button', text: '取消', onclick: function () { handle.close(); } });
    var foot = el('div', { class: 'modal-foot' }, cancelBtn, submitBtn);
    var handle = openModal({ title: '重置「' + (u.username || '—') + '」的密码', body: body, footer: foot, dismissable: false });

    function showErr(msg) { err.textContent = msg; err.classList.remove('hidden'); }
    [pwdInput, confirmInput].forEach(function (inp) {
      inp.addEventListener('input', function () { err.classList.add('hidden'); });
    });
    submitBtn.addEventListener('click', function () {
      if (pwdInput.value.length < 8) { showErr('新密码至少 8 位'); return; }
      if (pwdInput.value !== confirmInput.value) { showErr('两次输入的新密码不一致'); return; }
      err.classList.add('hidden');
      setBtnLoading(submitBtn, true, '重置中…');
      cancelBtn.disabled = true;
      api('POST', '/api/users/' + encodeURIComponent(String(u.id)) + '/password', { new_password: pwdInput.value })
        .then(function () { handle.close(); toast('密码已重置', 'success'); if (onDone) onDone(); })
        .catch(function (e) { showErr(e.message); })
        .then(function () { setBtnLoading(submitBtn, false); cancelBtn.disabled = false; });
    });
    setTimeout(function () { pwdInput.focus(); }, 0);
  }

  /* ---------- 系统日志 ---------- */

  function pageLogs(root, actions) {
    var st = { offset: 0, limit: 50 };
    var first = true;
    var seq = 0;

    actions.appendChild(el('button', {
      class: 'btn btn-secondary btn-sm', type: 'button',
      html: ICONS.refresh + '<span>刷新</span>',
      onclick: function () { loadList(); }
    }));
    var cleanupBtn = null;
    if (canCleanupLogs()) {
      cleanupBtn = el('button', {
        class: 'btn btn-ghost btn-sm', type: 'button', text: '清理日志',
        onclick: cleanupLogs
      });
      actions.appendChild(cleanupBtn);
    } else {
      root.appendChild(roleHintNode('当前为只读角色,仅可检索日志;过期日志清理操作已隐藏。'));
    }

    var levelSelect = el('select', { class: 'inline-select', 'aria-label': '日志级别' });
    [['', '全部级别'], ['debug', 'DEBUG'], ['info', 'INFO'], ['warn', 'WARN'], ['error', 'ERROR']].forEach(function (p) {
      levelSelect.appendChild(el('option', { value: p[0], text: p[1] }));
    });
    var qInput = el('input', { class: 'input', type: 'search', placeholder: '搜索日志内容…', 'aria-label': '搜索日志内容' });
    var sinceInput = el('input', { class: 'input', type: 'datetime-local', 'aria-label': '起始时间' });
    var applyBtn = el('button', {
      class: 'btn btn-primary btn-sm', type: 'button', text: '查询',
      onclick: function () { st.offset = 0; loadList(); }
    });
    var resetBtn = el('button', {
      class: 'btn btn-secondary btn-sm', type: 'button', text: '重置',
      onclick: function () {
        levelSelect.value = '';
        qInput.value = '';
        sinceInput.value = '';
        st.offset = 0;
        loadList();
      }
    });
    root.appendChild(el('div', { class: 'page-toolbar' },
      el('div', { class: 'toolbar-actions' },
        el('span', { class: 'muted', text: '级别' }), levelSelect,
        el('span', { class: 'muted', text: '关键词' }), qInput,
        el('span', { class: 'muted', text: '起始时间' }), sinceInput),
      el('div', { class: 'toolbar-actions' }, applyBtn, resetBtn)));
    root.appendChild(el('p', { class: 'page-note', text: '筛选条件在服务端生效;起始时间按本地时区换算后提交。' }));

    var card = el('div', { class: 'card' });
    root.appendChild(card);
    var configBody = el('div', { class: 'card-body' });
    var configCard = el('div', { class: 'card' },
      el('div', { class: 'card-head' }, el('h2', { class: 'card-title', text: '日志配置' })),
      configBody);
    root.appendChild(configCard);

    function currentUrl() {
      var q = '?limit=' + st.limit + '&offset=' + st.offset;
      if (levelSelect.value) q += '&level=' + encodeURIComponent(levelSelect.value);
      var kw = qInput.value.trim();
      if (kw) q += '&q=' + encodeURIComponent(kw);
      if (sinceInput.value) {
        var iso = fromLocalInputValue(sinceInput.value);
        if (iso === undefined) { toast('起始时间无效', 'error'); return null; }
        if (iso) q += '&since=' + encodeURIComponent(iso);
      }
      return '/api/logs' + q;
    }

    function loadList() {
      var url = currentUrl();
      if (!url) return;
      if (first) {
        first = false;
        card.innerHTML = '';
        card.appendChild(skeletonBlock([{ w: 'w40' }, { w: 'w80' }, { w: 'w80' }]));
      } else {
        card.classList.add('is-busy');
      }
      var cur = ++seq;
      api('GET', url).then(function (data) {
        if (cur !== seq) return;
        card.classList.remove('is-busy');
        card.innerHTML = '';
        data = data || {};
        var items = Array.isArray(data.items) ? data.items : [];
        var total = Number(data.total) || 0;
        if (!items.length && st.offset > 0 && total > 0) {
          st.offset = Math.max(0, Math.floor((total - 1) / st.limit) * st.limit);
          loadList();
          return;
        }
        if (!items.length) {
          card.appendChild(emptyNode('暂无日志记录', '当前筛选条件下没有日志;可调整级别、关键词或起始时间后重试。'));
          return;
        }
        card.appendChild(buildLogsTable(items));
        card.appendChild(buildAdminPagination(total, st.offset, st.limit, function (next) {
          st.offset = next;
          loadList();
        }));
      }).catch(function (e) {
        if (cur !== seq) return;
        card.classList.remove('is-busy');
        card.innerHTML = '';
        card.appendChild(errorNode(e, loadList));
      });
    }

    function loadConfig() {
      api('GET', '/api/logs/config').then(function (cfg) {
        cfg = cfg || {};
        configBody.innerHTML = '';
        configBody.appendChild(el('ul', { class: 'info-list' },
          el('li', null, el('b', { text: '单文件大小上限:' }), cfgVal(cfg.max_mb, ' MB')),
          el('li', null, el('b', { text: '保留备份数:' }), cfgVal(cfg.backups, ' 份')),
          el('li', null, el('b', { text: '保留天数:' }), cfgVal(cfg.retention_days, ' 天')),
          el('li', null, el('b', { text: '日志格式:' }), cfgVal(cfg.format)),
          el('li', null, el('b', { text: '文件落盘:' }), cfg.file_logging_enabled === true ? '开启' : (cfg.file_logging_enabled === false ? '关闭' : '—')),
          el('li', null, el('b', { text: '审计保留天数:' }), cfgVal(cfg.audit_retention_days, ' 天'))));
      }).catch(function (e) {
        configBody.innerHTML = '';
        configBody.appendChild(el('div', { class: 'muted', text: '日志配置加载失败:' + e.message }));
      });
    }
    function cfgVal(v, unit) {
      if (v === null || v === undefined || v === '') return '—';
      return String(v) + (unit || '');
    }

    function cleanupLogs() {
      confirmDialog({
        title: '清理日志',
        message: '确定清理过期的服务日志与审计日志吗?被清理的文件不可恢复。',
        confirmText: '清理'
      }).then(function (yes) {
        if (!yes) return;
        setBtnLoading(cleanupBtn, true, '清理中…');
        api('POST', '/api/logs/cleanup').then(function (res) {
          res = res || {};
          toast('已清理 ' + fmtNumber(res.removed_serve) + ' 个服务日志、' + fmtNumber(res.removed_audit) + ' 个审计日志', 'success');
          loadList();
        }).catch(function (e) {
          toast(e.message, 'error');
        }).then(function () { setBtnLoading(cleanupBtn, false); });
      });
    }

    loadList();
    loadConfig();
  }

  function buildLogsTable(items) {
    var thead = el('thead', null, el('tr', null,
      el('th', { text: '时间' }),
      el('th', { text: '级别' }),
      el('th', { text: '来源' }),
      el('th', { text: '内容' })));
    var tbody = el('tbody');
    items.forEach(function (it) {
      it = it || {};
      tbody.appendChild(el('tr', null,
        el('td', { class: 'td-mono', text: fmtDateTime(it.time) }),
        el('td', null, logLevelPill(it.level)),
        el('td', { class: 'td-mono', text: it.source || '—' }),
        el('td', { class: 'log-msg', text: String(it.message || '') })));
    });
    return el('div', { class: 'table-scroll' }, el('table', { class: 'table' }, thead, tbody));
  }

  /* ---------- 黑名单 ---------- */

  function pageBlacklist(root, actions) {
    var st = { offset: 0, limit: 20 };
    var all = [];
    var first = true;
    var seq = 0;
    var writable = canWriteBlacklist();

    actions.appendChild(el('button', {
      class: 'btn btn-secondary btn-sm', type: 'button',
      html: ICONS.refresh + '<span>刷新</span>',
      onclick: function () { loadAll(); }
    }));
    if (writable) {
      actions.appendChild(el('button', {
        class: 'btn btn-primary btn-sm', type: 'button',
        html: ICONS.plus + '<span>加入黑名单</span>',
        onclick: function () { openBlacklistModal(null, loadAll); }
      }));
    } else {
      root.appendChild(roleHintNode('当前为只读角色,仅可查看黑名单;新增、编辑与删除操作已隐藏。'));
    }

    var card = el('div', { class: 'card' });
    root.appendChild(card);

    function loadAll() {
      if (first) {
        first = false;
        card.innerHTML = '';
        card.appendChild(skeletonBlock([{ w: 'w30' }, { w: 'w80' }, { w: 'w60' }]));
      } else {
        card.classList.add('is-busy');
      }
      var cur = ++seq;
      api('GET', '/api/blacklist').then(function (data) {
        if (cur !== seq) return;
        card.classList.remove('is-busy');
        data = data || {};
        all = Array.isArray(data.items) ? data.items : [];
        renderTable();
      }).catch(function (e) {
        if (cur !== seq) return;
        card.classList.remove('is-busy');
        card.innerHTML = '';
        card.appendChild(errorNode(e, loadAll));
      });
    }

    function renderTable() {
      card.classList.remove('is-busy');
      card.innerHTML = '';
      if (!all.length) {
        card.appendChild(emptyNode('黑名单为空', '还没有被屏蔽的 IP / CIDR。',
          writable ? el('button', {
            class: 'btn btn-primary btn-sm', type: 'button', text: '加入黑名单',
            onclick: function () { openBlacklistModal(null, loadAll); }
          }) : null));
        return;
      }
      var page = all.slice(st.offset, st.offset + st.limit);
      if (!page.length && st.offset > 0) {
        st.offset = Math.max(0, Math.floor((all.length - 1) / st.limit) * st.limit);
        page = all.slice(st.offset, st.offset + st.limit);
      }
      card.appendChild(buildBlacklistTable(page, loadAll, writable));
      card.appendChild(buildAdminPagination(all.length, st.offset, st.limit, function (next) {
        st.offset = next;
        renderTable();
      }));
    }
    loadAll();
  }

  function buildBlacklistTable(items, reload, writable) {
    var thead = el('thead', null, el('tr', null,
      el('th', { text: '值' }),
      el('th', { text: '类型' }),
      el('th', { text: '原因' }),
      el('th', { text: '过期时间' }),
      el('th', { class: 'td-num-h', text: '命中' }),
      el('th', { text: '创建人' }),
      el('th', { text: '操作' })));
    var tbody = el('tbody');

    items.forEach(function (it) {
      it = it || {};
      var bid = encodeURIComponent(String(it.id));
      var expCell;
      if (it.expired) {
        expCell = el('td', null, el('span', {
          class: 'pill pill-gray', text: '已过期',
          title: '过期时间:' + fmtDateTime(it.expires_at)
        }));
      } else if (it.expires_at) {
        expCell = el('td', { class: 'td-mono', text: fmtDateTime(it.expires_at) });
      } else {
        expCell = el('td', null, el('span', { class: 'pill pill-green', text: '永久' }));
      }
      var acts = [];
      if (writable) {
        acts.push(el('button', {
          class: 'btn btn-ghost btn-sm', type: 'button', text: '编辑',
          onclick: function () { openBlacklistModal(it, reload); }
        }));
        var delBtn = el('button', { class: 'btn btn-text-danger', type: 'button', text: '删除' });
        delBtn.addEventListener('click', function () {
          confirmDialog({
            title: '移出黑名单',
            message: '确定将「' + (it.value || '—') + '」移出黑名单吗?该来源将立即恢复访问。',
            confirmText: '删除'
          }).then(function (yes) {
            if (!yes) return;
            setBtnLoading(delBtn, true);
            api('DELETE', '/api/blacklist/' + bid)
              .then(function () { toast('已移出黑名单', 'success'); reload(); })
              .catch(function (e) { toast(e.message, 'error'); setBtnLoading(delBtn, false); });
          });
        });
        acts.push(delBtn);
      }
      tbody.appendChild(el('tr', null,
        el('td', { class: 'td-strong td-mono', text: it.value || '—' }),
        el('td', null, el('span', { class: 'chip', text: it.kind === 'cidr' ? 'IP 网段' : 'IP 地址' })),
        el('td', { text: it.reason || '—' }),
        expCell,
        el('td', { class: 'td-num', text: fmtNumber(it.hits) }),
        el('td', { text: it.created_by || '—' }),
        el('td', null, acts.length ? el('div', { class: 'row-actions' }, acts) : el('span', { class: 'muted', text: '—' }))));
    });
    return el('div', { class: 'table-scroll' }, el('table', { class: 'table' }, thead, tbody));
  }

  function openBlacklistModal(entry, onDone) {
    entry = entry || {};
    var isEdit = !!entry.id;
    var kindSelect = el('select', { class: 'inline-select', 'aria-label': '类型' });
    kindSelect.appendChild(el('option', { value: 'ip', text: 'IP 地址' }));
    kindSelect.appendChild(el('option', { value: 'cidr', text: 'IP 网段(CIDR)' }));
    kindSelect.value = entry.kind === 'cidr' ? 'cidr' : 'ip';
    var valueInput = el('input', {
      class: 'input', type: 'text', maxlength: '128',
      placeholder: '例如 192.0.2.10 或 192.0.2.0/24',
      value: String(entry.value || '')
    });
    var reasonInput = el('input', {
      class: 'input', type: 'text', maxlength: '256',
      placeholder: '可选,记录加入原因', value: String(entry.reason || '')
    });
    var expiryInput = el('input', {
      class: 'input', type: 'datetime-local', value: toLocalInputValue(entry.expires_at)
    });
    if (isEdit) {
      kindSelect.disabled = true;
      valueInput.readOnly = true;
    }
    var err = el('div', { class: 'form-err hidden', role: 'alert' });
    var body = el('div', { class: 'modal-body' },
      el('div', { class: 'field' }, el('label', { class: 'field-label', text: '类型' }), kindSelect),
      el('div', { class: 'field' }, el('label', { class: 'field-label', text: '值' }), valueInput),
      el('div', { class: 'field' }, el('label', { class: 'field-label', text: '原因(可选)' }), reasonInput),
      el('div', { class: 'field' },
        el('label', { class: 'field-label', text: '过期时间(可选)' }), expiryInput,
        el('div', { class: 'form-hint', text: isEdit
          ? '类型与值创建后不可修改;清空过期时间表示永久有效。'
          : '留空表示永久屏蔽;到期后自动失效。' })),
      err);
    var submitBtn = el('button', { class: 'btn btn-primary', type: 'button', text: isEdit ? '保存' : '加入' });
    var cancelBtn = el('button', { class: 'btn btn-secondary', type: 'button', text: '取消', onclick: function () { handle.close(); } });
    var foot = el('div', { class: 'modal-foot' }, cancelBtn, submitBtn);
    var handle = openModal({
      title: isEdit ? '编辑黑名单「' + (entry.value || '—') + '」' : '加入黑名单',
      body: body, footer: foot, dismissable: false
    });

    function showErr(msg) { err.textContent = msg; err.classList.remove('hidden'); }
    [kindSelect, valueInput, reasonInput, expiryInput].forEach(function (inp) {
      inp.addEventListener('input', function () { err.classList.add('hidden'); });
      inp.addEventListener('change', function () { err.classList.add('hidden'); });
    });
    submitBtn.addEventListener('click', function () {
      var value = valueInput.value.trim();
      if (!isEdit && !value) { showErr('请输入 IP 或 CIDR 值'); return; }
      var iso = fromLocalInputValue(expiryInput.value);
      if (iso === undefined) { showErr('过期时间无效'); return; }
      var payload = { reason: reasonInput.value.trim() };
      if (isEdit) {
        payload.expires_at = iso === null ? '' : iso; // 空串表示取消过期(永久)
      } else {
        payload.kind = kindSelect.value;
        payload.value = value;
        if (iso) payload.expires_at = iso;
      }
      err.classList.add('hidden');
      setBtnLoading(submitBtn, true, isEdit ? '保存中…' : '提交中…');
      cancelBtn.disabled = true;
      api(isEdit ? 'PATCH' : 'POST',
        isEdit ? '/api/blacklist/' + encodeURIComponent(String(entry.id)) : '/api/blacklist', payload)
        .then(function () {
          handle.close();
          toast(isEdit ? '黑名单已更新' : '已加入黑名单', 'success');
          if (onDone) onDone();
        })
        .catch(function (e) { showErr(e.message); })
        .then(function () { setBtnLoading(submitBtn, false); cancelBtn.disabled = false; });
    });
    valueInput.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') { e.preventDefault(); submitBtn.click(); }
    });
    setTimeout(function () { (isEdit ? reasonInput : valueInput).focus(); }, 0);
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
    var pwdToggle = $('#login-password-toggle');
    function setPasswordVisible(visible) {
      pwdInput.type = visible ? 'text' : 'password';
      if (pwdToggle) pwdToggle.setAttribute('aria-pressed', String(visible));
    }
    if (pwdToggle) {
      pwdToggle.addEventListener('click', function () {
        setPasswordVisible(pwdInput.type === 'password');
      });
    }

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
      setPasswordVisible(false);
      setBtnLoading(submitBtn, true, '正在登录…');
      api('POST', '/api/auth/login', { username: u, password: pwd }, { skipAuthRedirect: true })
        .then(function (data) {
          data = data || {};
          var uname = (data.user && data.user.username) ? data.user.username : u;
          setAuth(data.token, data.expires_in, uname);
          if (data.user) applyMe(data.user); // 登录响应已带角色时立即可用
          pwdInput.value = '';
          toast('登录成功', 'success');
          /* 角色以 /api/me 为准:拉取完成后再渲染,避免只读/运维角色短暂看到写按钮 */
          return api('GET', '/api/me', null, { skipAuthRedirect: true })
            .then(function (me) { applyMe(me); })
            .catch(function () { /* 旧后端无 role 字段或接口异常:沿用默认 admin */ })
            .then(function () {
              var target = pendingHash || '#/overview';
              pendingHash = null;
              if (location.hash === target) renderRoute();
              else location.hash = target;
            });
        })
        .catch(function (e) {
          if (e.status === 401) {
            /* 凭据错误:用户名与密码成对校验,两个输入框都标记错误态(destructive 边框) */
            setFieldInvalid(userInput, true);
            setFieldInvalid(pwdInput, true);
            showErr('用户名或密码错误，请核对后重试。');
          } else if (e.status === 429 && e.data && e.data.error === 'too_many_attempts') {
            showErr(e.retryAfter ? '尝试次数过多，请在 ' + e.retryAfter + ' 秒后重试。' : '尝试次数过多，请稍后重试。');
          } else if (e.status === 404) {
            // 平台停用:/api/* 未挂载,静态面板仍可访问
            showErr('管理 API 未启用。请启用平台功能并重启服务。');
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
    function showSignedOut() {
      clearAuth();
      toast('已退出登录', 'info');
      if (location.hash === '#/login') showLogin();
      else location.hash = '#/login';
    }
    btn.addEventListener('click', function () {
      setBtnLoading(btn, true, '退出中…');
      api('POST', '/api/auth/logout', null, { skipAuthRedirect: true })
        .then(showSignedOut)
        .catch(function (e) {
          if (e.status === 401) {
            showSignedOut();
            return;
          }
          var retryHint = e.retryAfter ? '请在 ' + e.retryAfter + ' 秒后重试。' : '请稍后重试。';
          toast('退出未完成。' + retryHint, 'error');
        })
        .finally(function () { setBtnLoading(btn, false); });
    });
  }

  function boot() {
    initTheme();
    bindThemeButtons();
    bindSidebarToggle();
    bindLogin();
    bindLogout();
    bindSearchHotkey();
    window.addEventListener('hashchange', renderRoute);

    // B-12: 登录态以 /api/me 为权威(cookie 会话对前端不可读);内存 token
    // 仅用于恢复缓存的用户名与 Bearer 双轨。
    try {
      state.username = localStorage.getItem(LS_USERNAME) || '';
    } catch (e) { /* 忽略 */ }

    // T-07: 首屏不等 /api/me 往返,立即渲染登录页或已有会话的页面,消除
    // 冷启动 1s+ 的整页空白窗口(首访与再访行为一致);/api/me 返回后按原
    // 逻辑校正最终视图。
    if (isLoggedIn()) {
      bootRendered = true;
      renderRoute();
    } else {
      showLogin();
    }

    api('GET', '/api/me', null, { skipAuthRedirect: true })
      .then(function (me) {
        meVerified = true; // 服务端确认会话有效:F5 后由此恢复登录态
        applyMe(me); // 角色 / user_id 落全局;旧后端不返回 role 时按 admin 兼容
        if (bootRendered) {
          showApp(); // 顶栏用户名/头像用 /api/me 的权威值刷新,页面数据已在首轮加载
          /* 首屏先渲染的历史行为保留;若本次 /api/me 带来受限角色,
           * 重新渲染当前管理页以移除写操作(admin 不重复拉取数据)。 */
          var h = location.hash || '';
          if (state.role !== 'admin' &&
              (h === '#/users' || h === '#/logs' || h === '#/blacklist')) {
            renderRoute();
          }
          return;
        }
        renderRoute();
      })
      .catch(function (e) {
        /* 首屏 /api/me 可能在用户输入凭据期间才返回 401(请求早于登录发出);
         * 此时会话已建立,不能让过期响应把用户踢回登录页。 */
        if (isLoggedIn()) return;
        if (e.status === 401) {
          // 首次访问(无 cookie/无内存 token)静默回登录页,不误报「登录已过期」。
          clearAuth();
          showLogin();
          return;
        }
        showLogin();
        if (e.status === 404) {
          setLoginMsg('管理 API 未启用。请启用平台功能并重启服务。', true);
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
