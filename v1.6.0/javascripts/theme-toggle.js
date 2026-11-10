/* Marks the moment the theme toggle is pressed, so its icon animates only when
   the reader swaps the theme and not on every page load. */
document.addEventListener("click", function (event) {
  var button = event.target.closest(".md-header__option label");
  if (!button) {
    return;
  }
  var root = document.documentElement;
  root.classList.add("pgrr-theme-swap");
  window.setTimeout(function () {
    root.classList.remove("pgrr-theme-swap");
  }, 700);
});

/* One theme for the whole site (see overrides/main.html). The palette choice
   is mirrored to a shared localStorage key whenever it changes, and a page
   that did not get the shared choice before it painted (an old version with
   no head snippet) picks it up here. */
(function () {
  var KEY = "pgrr.palette";
  function own() {
    try {
      return localStorage.getItem(new URL(window.__md_scope ? window.__md_scope.href : ".", location).pathname + ".__palette");
    } catch (e) { return null; }
  }
  function share() {
    try {
      var v = own();
      if (v) localStorage.setItem(KEY, v);
    } catch (e) {}
  }
  try {
    var shared = localStorage.getItem(KEY);
    if (shared && shared !== own()) {
      var want = JSON.parse(shared);
      var radios = document.querySelectorAll('[data-md-component="palette"] input[type="radio"]');
      if (want && radios[want.index] && !radios[want.index].checked) {
        radios[want.index].checked = true;
        radios[want.index].dispatchEvent(new Event("change", { bubbles: true }));
      }
    }
  } catch (e) {}
  new MutationObserver(function () { window.setTimeout(share, 60); })
    .observe(document.body, { attributes: true, attributeFilter: ["data-md-color-scheme"] });
})();
