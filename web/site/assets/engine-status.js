// Both public pages use the same live snapshot and status terminology.
(function () {
  'use strict';

  var board = document.querySelector('[data-engine-status]');
  if (!board) return;
  var refresh = document.getElementById('statusRefresh');
  var summary = document.getElementById('statusSummary');
  var summaryText = document.getElementById('statusSummaryText');
  var updated = document.getElementById('statusUpdated');
  var names = new Map([['baidu', '百度'], ['sogou', '搜狗'], ['so360', '360 搜索'], ['bing', '必应']]);
  var order = Array.from(names.keys());
  var states = {
    ok: ['正常', '可接收查询'],
    cooldown: ['冷却中', '暂缓向该引擎发送查询'],
    circuit_open: ['熔断中', '暂缓查询，等待恢复探测'],
    half_open: ['探测中', '正在确认引擎能否恢复'],
    disabled: ['不可用', '当前实例未初始化该引擎'],
    unknown: ['状态未知', '暂未获取有效状态']
  };
  var reasons = new Map([['FORCED', '计划维护'], ['COOLDOWN', '引擎风控']]);
  var busy = false;
  var renderedKey = null;
  var observedCooldowns = new Map();

  function node(tag, className, label) {
    var el = document.createElement(tag);
    el.className = className;
    if (label) el.textContent = label;
    return el;
  }

  function setSummary(state, text) {
    if (!summary) return;
    summary.dataset.state = state;
    if (summaryText.textContent !== text) summaryText.textContent = text;
  }

  function setTime(stamp) {
    if (!updated) return;
    updated.textContent = stamp ? '更新于 ' + stamp : '';
  }

  function empty(message) {
    renderedKey = null;
    board.replaceChildren(node('li', 'state-message', message));
  }

  function updateCountdown() {
    var now = Date.now();
    board.querySelectorAll('.status-cooldown').forEach(function (card) {
      var remaining = Math.max(0, Number(card.dataset.until) - now);
      var seconds = Math.ceil(remaining / 1000);
      var minutes = String(Math.floor(seconds / 60)).padStart(2, '0');
      var remainder = String(seconds % 60).padStart(2, '0');
      card.querySelector('progress').value = remaining;
      card.querySelector('.status-countdown').textContent =
        seconds ? '剩余 ' + minutes + ':' + remainder : '等待恢复探测';
    });
  }

  function statusBar(state) {
    var bar = node('div', 'status-segments');
    bar.setAttribute('aria-hidden', 'true');
    bar.dataset.state = state;
    for (var i = 0; i < 16; i++) bar.append(node('span', ''));
    return bar;
  }

  function cooldownBar(until, initialRemaining) {
    var block = node('div', 'status-cooldown');
    block.dataset.until = until;
    var track = node('progress', 'status-progress');
    track.max = initialRemaining;
    track.setAttribute('aria-hidden', 'true');
    block.append(track, node('span', 'status-countdown'));
    return block;
  }

  function engineState(engine) {
    if (engine.enabled === false) return 'disabled';
    var state = engine.state;
    if (state === 'ok' && engine.breaker && engine.breaker.state === 'half_open') return 'half_open';
    return state === 'ok' || state === 'cooldown' || state === 'circuit_open' ? state : 'unknown';
  }

  function render(data) {
    var engines = data.engines.filter(function (entry) {
      return entry && typeof entry.engine === 'string' && entry.engine;
    });
    engines.sort(function (a, b) {
      function position(id) { return names.has(id) ? order.indexOf(id) : order.length; }
      return position(a.engine) - position(b.engine);
    });
    setTime(typeof data.generated_at === 'string' ? data.generated_at : '');
    if (!engines.length) {
      setSummary('unknown', '暂无引擎数据');
      empty('当前实例没有返回引擎状态。');
      return;
    }
    // Polling refreshes the timestamp without restarting unchanged animations.
    var key = JSON.stringify(engines.map(function (engine) {
      return [engine.engine, engineState(engine), engine.until, engine.reason];
    }));
    if (renderedKey === key) return;
    var healthy = 0;
    var now = Date.now();
    var nextCooldowns = new Map();
    var cards = engines.map(function (engine) {
      var state = engineState(engine);
      if (state === 'ok') healthy++;
      var details = states[state];
      var card = node('li', 'status-engine');
      var top = node('div', 'status-engine-top');
      var title = node('span', 'status-engine-name', names.get(engine.engine) || engine.engine);
      top.append(title, node('span', 'st-pill state-' + state, details[0]));
      var meta = node('div', 'status-engine-meta');
      meta.append(node('code', '', engine.engine));
      var reason = state === 'cooldown' && reasons.get(engine.reason);
      meta.append(node('span', '', reason || details[1]));
      var until = state === 'cooldown' ? Date.parse(engine.until) : NaN;
      card.append(top, meta);
      if (Number.isFinite(until)) {
        // Measure from the first observation of this deadline on the current page.
        var previous = observedCooldowns.get(engine.engine);
        var remaining = previous && previous.until === until ?
          previous.initialRemaining : Math.max(1, until - now);
        nextCooldowns.set(engine.engine, { until: until, initialRemaining: remaining });
        card.append(cooldownBar(until, remaining));
      } else {
        card.append(statusBar(state));
      }
      return card;
    });
    var allHealthy = healthy === engines.length;
    var summaryState = allHealthy ? 'ok' : 'warning';
    setSummary(summaryState, allHealthy ? '已配置的引擎均正常' :
      healthy + ' / ' + engines.length + ' 个引擎正常');
    board.replaceChildren.apply(board, cards);
    renderedKey = key;
    observedCooldowns = nextCooldowns;
    updateCountdown();
  }

  function load() {
    if (busy) return;
    busy = true;
    if (refresh) refresh.disabled = true;
    var controller = new AbortController();
    var timeout = setTimeout(function () { controller.abort(); }, 6000);
    fetch('/web-tool/engines/status', { cache: 'no-store', signal: controller.signal })
      .then(function (response) {
        if (!response.ok) throw new Error('Status service unavailable');
        return response.json();
      })
      .then(function (data) {
        if (!data || !Array.isArray(data.engines)) throw new Error('Invalid status response');
        render(data);
      })
      .catch(function () {
        setSummary('error', '暂时无法获取引擎状态');
        setTime('');
        empty('状态加载失败，请稍后刷新。');
      })
      .finally(function () {
        clearTimeout(timeout);
        busy = false;
        if (refresh) refresh.disabled = false;
      });
  }
  if (refresh) refresh.addEventListener('click', load);
  load();
  setInterval(function () { if (!document.hidden) load(); }, 10000);
  setInterval(function () { if (!document.hidden) updateCountdown(); }, 1000);
})();
