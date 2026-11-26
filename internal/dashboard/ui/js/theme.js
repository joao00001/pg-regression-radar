// Runs before the page paints, so the theme is right from the first frame.
// A classic script on purpose: the page's script-src allows only files of its own.
(function () {
  var t = null;
  try { t = localStorage.getItem('pgrr-theme'); } catch (e) { /* storage may be blocked */ }
  if (t !== 'light' && t !== 'dark') {
    t = window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
  }
  document.documentElement.setAttribute('data-theme', t);
})();
