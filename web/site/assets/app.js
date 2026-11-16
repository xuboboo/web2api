/* ============================================================
   Web2API 网站收录查询开放平台 — 官网交互
   零依赖、零构建,原生 ES2018。
   契约接线:① 旧版面板书签兼容(#/ hash → 控制台)
            ② 「进入控制台」统一 href=/web-tool/console/(在 HTML 中)
            ③ 主题键 w2a_site_theme(与面板 w2a_* 键互不干扰)
   ============================================================ */
(function () {
  'use strict';

  /* ---------- 0) 旧书签兼容:hash 以 #/ 开头 = 面板路由,跳转控制台同路径 ----------
     例:任意旧面板书签 /web-tool/#/keys → /web-tool/console/#/keys */
  if (location.hash.indexOf('#/') === 0) {
    location.replace('/web-tool/console/' + location.hash);
    return;
  }

  var doc = document.documentElement;

  /* ---------- 1) 主题切换 ---------- */
  var THEME_KEY = 'w2a_site_theme';

  function initTheme() {
    var btn = document.getElementById('themeToggle');
    if (!btn) return;
    var isDark = function () { return doc.classList.contains('dark'); };
    var sync = function () {
      btn.setAttribute('aria-pressed', isDark() ? 'true' : 'false');
      btn.setAttribute('aria-label', isDark() ? '切换到浅色主题' : '切换到深色主题');
    };
    btn.addEventListener('click', function () {
      var dark = !isDark();
      doc.classList.toggle('dark', dark);
      try { localStorage.setItem(THEME_KEY, dark ? 'dark' : 'light'); } catch (e) {}
      sync();
    });
    sync();
  }

  /* ---------- 2) 导航:滚动状态 + 锚点激活(rAF 节流) ---------- */
  function initNav() {
    var nav = document.getElementById('siteNav');
    var heroSection = document.querySelector('.hero');
    var links = Array.prototype.slice.call(document.querySelectorAll('.nav-links a[data-section]'));
    var items = [];
    links.forEach(function (a) {
      var sec = document.getElementById(a.getAttribute('data-section'));
      if (sec) items.push({ link: a, sec: sec });
    });

    var setActive = function (link) {
      items.forEach(function (it) { it.link.classList.toggle('active', it.link === link); });
    };

    var ticking = false;
    var onScroll = function () {
      /* 导航状态切换是廉价的单行 class 操作,同步执行——
       * 放进 rAF 会在后台/合成节流标签页里卡住状态(实测踩坑) */
      var heroEnd = 96;
      if (heroSection) {
        heroEnd = heroSection.getBoundingClientRect().bottom + window.scrollY;
      }
      if (nav) nav.classList.toggle('scrolled', window.scrollY > heroEnd - 64);

      if (ticking) return;
      ticking = true;
      requestAnimationFrame(function () {
        ticking = false;
        var probe = window.scrollY + 96; /* 导航 64px + 余量 */
        var current = null;
        for (var i = 0; i < items.length; i++) {
          if (items[i].sec.offsetTop <= probe) current = items[i].link;
        }
        var atBottom = window.innerHeight + window.scrollY >= doc.scrollHeight - 40;
        if (atBottom && items.length) current = items[items.length - 1].link;
        setActive(current);
      });
    };

    window.addEventListener('scroll', onScroll, { passive: true });
    window.addEventListener('resize', onScroll, { passive: true });
    onScroll();
    /* A-20:原此处返回的 closeMenuAndMark 死代码已删除(initNav() 返回值
       在启动段本就被丢弃,菜单收起由 initMenu() 自行处理) */
  }

  /* ---------- 3) 移动端汉堡菜单(200ms 过渡) ---------- */
  function initMenu() {
    var nav = document.getElementById('siteNav');
    var btn = document.getElementById('menuToggle');
    if (!nav || !btn) return;
    var setOpen = function (open) {
      nav.classList.toggle('menu-open', open);
      btn.setAttribute('aria-expanded', open ? 'true' : 'false');
      btn.setAttribute('aria-label', open ? '关闭导航菜单' : '打开导航菜单');
    };
    btn.addEventListener('click', function () {
      setOpen(!nav.classList.contains('menu-open'));
    });
    /* 点击锚点后收起菜单 */
    var links = nav.querySelectorAll('.nav-links a');
    Array.prototype.forEach.call(links, function (a) {
      a.addEventListener('click', function () { setOpen(false); });
    });
    /* 点击导航以外区域收起 */
    document.addEventListener('click', function (e) {
      if (!nav.classList.contains('menu-open')) return;
      if (nav.contains(e.target)) return;
      setOpen(false);
    });
    /* Esc 收起 */
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && nav.classList.contains('menu-open')) {
        setOpen(false);
        btn.focus();
      }
    });
  }

  /* ---------- 4) 滚动渐显 + rAF 计数(单例 IntersectionObserver) ---------- */
  var reduceMotion = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  function runCount(el) {
    var target = parseInt(el.getAttribute('data-count'), 10);
    if (isNaN(target)) return;
    var dur = 800;
    var start = null;
    var frame = function (ts) {
      if (start === null) start = ts;
      var p = Math.min((ts - start) / dur, 1);
      var eased = 1 - Math.pow(1 - p, 3); /* 缓出 */
      el.textContent = String(Math.round(target * eased));
      if (p < 1) requestAnimationFrame(frame);
      else el.textContent = String(target);
    };
    requestAnimationFrame(frame);
  }

  function initReveal() {
    var revealEls = Array.prototype.slice.call(document.querySelectorAll('.reveal'));
    var heroEls = Array.prototype.slice.call(document.querySelectorAll('.reveal-hero'));
    var countEls = Array.prototype.slice.call(document.querySelectorAll('[data-count]'));

    /* 首屏内容不交给 IO(避免白屏闪烁),也不用 rAF 双帧——
     * 后台标签页 rAF 被节流会永不执行导致内容永久不可见(实测踩坑);
     * setTimeout 在后台也会执行,最坏情况激活标签页时立即到终态 */
    setTimeout(function () {
      heroEls.forEach(function (el) { el.classList.add('is-visible'); });
    }, 50);

    if (reduceMotion || typeof IntersectionObserver === 'undefined') {
      /* 降级:全部直接可见 + 计数置终值(HTML 初始文本即终值) */
      revealEls.forEach(function (el) { el.classList.add('is-visible'); });
      return;
    }

    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (en) {
        if (!en.isIntersecting) return;
        var el = en.target;
        el.classList.add('is-visible');
        if (el.hasAttribute('data-count')) runCount(el);
        io.unobserve(el); /* 一次性,命中即解除 */
        el.addEventListener('transitionend', function () {
          el.style.willChange = 'auto'; /* 释放合成层 */
        }, { once: true });
      });
    }, { threshold: 0.15, rootMargin: '0px 0px -10% 0px' });

    revealEls.forEach(function (el) { io.observe(el); });
    countEls.forEach(function (el) {
      if (revealEls.indexOf(el) < 0) io.observe(el);
    });

    /* 兜底:1.2s 后仍在视口内却未被 IO 触发的元素强制可见
     * (IO 在部分环境会静默失效,内容可见性永远优先于入场动画) */
    setTimeout(function () {
      var vh = window.innerHeight || doc.documentElement.clientHeight;
      revealEls.forEach(function (el) {
        if (el.classList.contains('is-visible')) return;
        var r = el.getBoundingClientRect();
        if (r.top < vh && r.bottom > 0) {
          el.classList.add('is-visible');
          if (el.hasAttribute('data-count')) runCount(el);
          io.unobserve(el);
        }
      });
    }, 1200);
  }

  /* ---------- 5) 代码窗 tab(cURL / Python / Node / MCP) ---------- */
  function initTabs() {
    var tabs = Array.prototype.slice.call(document.querySelectorAll('.code-tab'));
    var panels = Array.prototype.slice.call(document.querySelectorAll('.code-panel'));
    if (!tabs.length) return;

    var activate = function (id, focus) {
      tabs.forEach(function (t) {
        var selected = t.getAttribute('data-tab') === id;
        t.classList.toggle('active', selected);
        t.setAttribute('aria-selected', selected ? 'true' : 'false');
        t.tabIndex = selected ? 0 : -1;
        if (selected && focus) t.focus();
      });
      panels.forEach(function (p) {
        var on = p.getAttribute('data-panel') === id;
        p.classList.toggle('active', on);
        if (on) p.removeAttribute('hidden');
        else p.setAttribute('hidden', 'hidden');
      });
    };

    tabs.forEach(function (t, idx) {
      t.addEventListener('click', function () { activate(t.getAttribute('data-tab')); });
      t.addEventListener('keydown', function (e) {
        var dir = e.key === 'ArrowRight' ? 1 : (e.key === 'ArrowLeft' ? -1 : 0);
        if (!dir) return;
        e.preventDefault();
        var next = (idx + dir + tabs.length) % tabs.length;
        activate(tabs[next].getAttribute('data-tab'), true);
      });
    });
  }

  /* ---------- 6) 复制按钮(clipboard API + execCommand 兜底,「已复制」1.5s) ---------- */
  function flashCopied(btn) {
    btn.classList.add('copied');
    var label = btn.querySelector('.copy-label');
    if (label) {
      if (!btn.getAttribute('data-orig-label')) btn.setAttribute('data-orig-label', label.textContent);
      label.textContent = '已复制';
    }
    if (btn._copyTimer) window.clearTimeout(btn._copyTimer);
    btn._copyTimer = window.setTimeout(function () {
      btn.classList.remove('copied');
      if (label) label.textContent = btn.getAttribute('data-orig-label') || '复制';
    }, 1500);
  }

  function legacyCopy(text) {
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', 'readonly');
    ta.style.cssText = 'position:fixed;top:-120px;left:0;opacity:0;';
    document.body.appendChild(ta);
    ta.select();
    var ok = false;
    try { ok = document.execCommand('copy'); } catch (e) {}
    document.body.removeChild(ta);
    return ok;
  }

  function copyText(text, btn) {
    var done = function () { flashCopied(btn); };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done, function () { legacyCopy(text); done(); });
    } else {
      legacyCopy(text);
      done();
    }
  }

  function initCopy() {
    /* 指定目标:pre / code 元素 */
    Array.prototype.forEach.call(document.querySelectorAll('[data-copy-target]'), function (btn) {
      btn.addEventListener('click', function () {
        var host = document.querySelector(btn.getAttribute('data-copy-target'));
        if (host) copyText(host.textContent, btn);
      });
    });
    /* 代码窗:复制当前激活 tab 的全部代码块(块标签不参与复制) */
    var winCopy = document.querySelector('[data-copy-panel]');
    if (winCopy) {
      winCopy.addEventListener('click', function () {
        var active = document.querySelector('.code-panel.active');
        if (!active) return;
        var pres = active.querySelectorAll('pre');
        var parts = [];
        Array.prototype.forEach.call(pres, function (pre) { parts.push(pre.textContent); });
        copyText(parts.join('\n\n'), winCopy);
      });
    }
  }

  /* ---------- 启动:no-js 先摘除(动效初始态生效),任何失败都兜底显示全部内容 ---------- */
  try {
    doc.classList.remove('no-js');
    initTheme();
    initNav();
    initMenu();
    initReveal();
    initTabs();
    initCopy();
    initOrigin();
    initEngineStatus();

    /* ---------- 9) 官网「服务器状态」看板:真实接口同步(10s 轮询) ---------- */
    function initEngineStatus() {
      var board = document.getElementById('engineStatusBoard');
      if (!board) return;
      var CN = { baidu: '百度', sogou: '搜狗', so360: '360 搜索', bing: '必应' };
      /* A-15:全站引擎展示统一排序 百度/搜狗/360 搜索/必应(看板数据为 API 序,前端按此固定序重排) */
      var ENGINE_ORDER = ['baidu', 'sogou', 'so360', 'bing'];
      var SEGS = 12;

      /* A-10:与 status 页同款 HTML 转义(纵深一致性;数据源为自有 API,无实际注入面) */
      function esc(s) {
        return String(s == null ? '' : s).replace(/[&<>"]/g, function (ch) {
          return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[ch];
        });
      }
      function pill(state) {
        if (state === 'cooldown') return '<span class="st-pill st-cool">冷却</span>';
        if (state === 'circuit_open') return '<span class="st-pill st-cb">熔断</span>';
        return '<span class="st-pill st-ok">正常</span>';
      }
      function remainingText(untilStr) {
        if (!untilStr) return '';
        var ms = new Date(untilStr).getTime() - Date.now();
        if (ms <= 0) return '等待恢复探测';
        var m = Math.floor(ms / 60000);
        var s = Math.floor((ms % 60000) / 1000);
        return '剩余 ' + (m < 10 ? '0' : '') + m + ':' + (s < 10 ? '0' : '') + s;
      }
      function render(data) {
        var engines = (data && data.engines) || [];
        if (!engines.length) {
          board.innerHTML = '<div class="engine-row"><div class="eng-top"><span class="eng-name">—</span><span class="st-pill st-ok">暂无数据</span></div></div>';
          return;
        }
        engines = engines.slice().sort(function (a, b) {
          var ia = ENGINE_ORDER.indexOf(a.engine); if (ia === -1) ia = 99;
          var ib = ENGINE_ORDER.indexOf(b.engine); if (ib === -1) ib = 99;
          return ia - ib;
        });
        var html = '';
        engines.forEach(function (eng) {
          var state = eng.state || 'ok';
          var now = Date.now();
          var remMin = Infinity;
          if (state === 'cooldown' && eng.until) {
            remMin = Math.max(0, (new Date(eng.until).getTime() - now) / 60000);
          }
          var filled = 0;
          if (state === 'ok') filled = SEGS;
          else if (state === 'cooldown' && isFinite(remMin)) filled = Math.max(1, Math.round(SEGS * Math.min(1, remMin / 120)));
          var segs = '';
          for (var i = 0; i < SEGS; i++) {
            var cls = 'seg';
            if (state === 'circuit_open') cls += ' on-err';
            else if (i < filled) cls += state === 'cooldown' ? ' on-warn' : ' on-ok';
            segs += '<span class="' + cls + '"></span>';
          }
          var extra = state === 'cooldown' && eng.until
            ? '<span class="st-extra cd-remaining" data-until="' + esc(eng.until) + '">' + esc(remainingText(eng.until)) + '</span>'
            : '';
          html += '<div class="engine-row">'
            + '<div class="eng-top"><span class="eng-name">' + esc(eng.engine) + '</span>'
            + '<span class="eng-cn">' + esc(CN[eng.engine] || eng.engine) + '</span>'
            + pill(state) + extra + '</div>'
            + '<div class="seg-bar" aria-hidden="true">' + segs + '</div>'
            + '</div>';
        });
        board.innerHTML = html;
      }
      function tick() {
        Array.prototype.forEach.call(board.querySelectorAll('.cd-remaining[data-until]'), function (el) {
          var u = el.getAttribute('data-until');
          if (!u) return;
          var ms = new Date(u).getTime() - Date.now();
          if (ms <= 0) { el.textContent = '等待恢复探测'; return; }
          var m = Math.floor(ms / 60000);
          var s = Math.floor((ms % 60000) / 1000);
          el.textContent = '剩余 ' + (m < 10 ? '0' : '') + m + ':' + (s < 10 ? '0' : '') + s;
        });
      }
      function load() {
        fetch('/web-tool/engines/status')
          .then(function (r) { return r.json(); })
          .then(function (data) { render(data); tick(); })
          .catch(function () {
            board.innerHTML = '<div class="engine-row"><div class="eng-top"><span class="eng-name">—</span><span class="st-pill st-cb">状态获取失败</span></div></div>';
          });
      }
      load();
      setInterval(load, 10000);
      setInterval(tick, 1000);
    }

    function initOrigin() {
      /* 自适应部署地址:源码中的 {{ORIGIN}} 占位符在运行时替换为当前访问源。
       * 源码不含任何写死的 IP/端口,避免复制源码/禁用 JS/SEO 抓取时暴露内网地址;
       * 同时兼容旧文案里遗留的 http://127.0.0.1:7000(平滑过渡,后续会被占位符取代)。 */
      var origin = location.origin;
      if (origin.indexOf('http') !== 0) return;
      var walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT, null);
      var hits = [];
      while (walker.nextNode()) {
        var v = walker.currentNode.nodeValue;
        if (v.indexOf('{{ORIGIN}}') !== -1 || v.indexOf('127.0.0.1:7000') !== -1) hits.push(walker.currentNode);
      }
      for (var i = 0; i < hits.length; i++) {
        hits[i].nodeValue = hits[i].nodeValue
          .split('{{ORIGIN}}').join(origin)
          .split('http://127.0.0.1:7000').join(origin);
      }
    }
  } catch (err) {
    /* 兜底:动效初始化失败时内容必须可见 */
    try {
      doc.classList.remove('no-js');
      var els = document.querySelectorAll('.reveal, .reveal-hero');
      Array.prototype.forEach.call(els, function (el) { el.classList.add('is-visible'); });
    } catch (e2) { /* noop */ }
  }
})();
