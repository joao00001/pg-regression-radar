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
