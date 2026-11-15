/* ============================================================
 * Web2API 管理控制台 · 主题预置脚本(自 index.html 内联外置)
 *
 * 职责:CSS 绘制前同步 w2a_theme 到 <html class>,避免暗色闪白;
 * 失败时保持浅色默认。支持三态:'light' | 'dark' | 'system'
 * ('system' 跟随操作系统 prefers-color-scheme)。
 * 放置于 <head> 中、样式表之前;同步加载、无依赖。
 * (外置动机:控制台 CSP 转正后仅允许外部脚本,内联将不再放行。)
 * ============================================================ */
(function () {
  try {
    var mode = localStorage.getItem('w2a_theme');
    if (mode !== 'dark' && mode !== 'light' && mode !== 'system') mode = 'light';
    var dark = mode === 'dark' ||
      (mode === 'system' && window.matchMedia &&
        window.matchMedia('(prefers-color-scheme: dark)').matches);
    if (dark) {
      document.documentElement.classList.add('dark');
      var m = document.querySelector('meta[name="color-scheme"]');
      if (m) m.setAttribute('content', 'dark');
    }
  } catch (e) { /* 忽略:localStorage 不可用或 matchMedia 缺失时保持浅色默认 */ }
})();
