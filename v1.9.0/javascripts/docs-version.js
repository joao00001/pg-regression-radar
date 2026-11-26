/*
 * Send the "Docs" tab of the version-less pages (home, roadmap, playground, our
 * math, contribute, Basic Kubernetes) to the docs version the reader last used.
 *
 * Those pages are built once for every release, and their Docs tab is a fixed
 * link into `latest`. version-flyout.js records the version on the versioned
 * pages; here the tab is pointed at that version's first page instead. Every
 * release has `installation/`; `lab/` (the tab's target on `latest`) is newer.
 * Without a stored version, or with `latest`, the tab is left alone.
 */
(function () {
  try {
    var v = localStorage.getItem("pgrr.docsVersion");
    if (!v || !/^v\d+\.\d+\.\d+/.test(v)) {
      return;
    }
    var tabs = document.querySelectorAll(".md-tabs__link");
    Array.prototype.forEach.call(tabs, function (a) {
      var href = a.getAttribute("href") || "";
      if (/\/latest\/lab\/?$/.test(href)) {
        a.setAttribute("href", href.replace(/\/latest\/lab\/?$/, "/" + v + "/installation/"));
      }
    });
  } catch (e) {
    /* no storage: leave the tab as built */
  }
})();
