// Apply the saved site theme before CSS loads. Light is the default.
(function () {
  try {
    if (localStorage.getItem('w2a_site_theme') === 'dark') {
      document.documentElement.classList.add('dark');
    }
  } catch (e) { /* Storage can be disabled. */ }
})();
