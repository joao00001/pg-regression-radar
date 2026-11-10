/*
 * The playground's title block background, after shadcn.io's "Dot Pattern": a
 * grid of small dots that fade toward the edges, a slow diagonal wave that
 * brightens them in turn, and a soft red light that follows the pointer and
 * makes the dots near it bigger. The dots, the wave and the light are all CSS
 * (see playground.css); this only feeds the pointer position into two custom
 * properties, eased frame by frame so the light trails the cursor.
 *
 * Decorative (aria-hidden, no pointer events). Touch does not light the dots,
 * so scrolling stays smooth; with prefers-reduced-motion the wave stops and the
 * light follows the pointer without easing.
 */
(function () {
  "use strict";

  var head = document.querySelector(".pgrr-playground .pg-head");
  if (!head || head.querySelector(".pg-dots")) return;

  var reduce = window.matchMedia && matchMedia("(prefers-reduced-motion: reduce)").matches;
  var box = document.createElement("div");
  box.className = "pg-dots";
  box.setAttribute("aria-hidden", "true");
  box.innerHTML = '<i class="pg-dots-wave"></i><i class="pg-dots-glow"></i>';
  head.insertBefore(box, head.firstChild);
  if (reduce) box.classList.add("is-still");

  var tx = 0, ty = 0, x = 0, y = 0, raf = 0, hot = false;

  function frame() {
    raf = 0;
    var k = reduce ? 1 : 0.14;
    x += (tx - x) * k;
    y += (ty - y) * k;
    box.style.setProperty("--mx", x.toFixed(1) + "px");
    box.style.setProperty("--my", y.toFixed(1) + "px");
    if (Math.abs(tx - x) > 0.4 || Math.abs(ty - y) > 0.4) raf = requestAnimationFrame(frame);
  }

  head.addEventListener("pointermove", function (e) {
    if (e.pointerType && e.pointerType !== "mouse") return;
    var r = head.getBoundingClientRect();
    tx = e.clientX - r.left;
    ty = e.clientY - r.top;
    if (!hot) {
      hot = true;
      x = tx;
      y = ty;
      box.classList.add("is-hot");
    }
    if (!raf) raf = requestAnimationFrame(frame);
  });

  head.addEventListener("pointerleave", function () {
    hot = false;
    box.classList.remove("is-hot");
  });
})();
