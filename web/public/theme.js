// Runs before the page paints, so nobody sees the wrong theme flash. It's a file because
// the Content-Security-Policy forbids inline scripts. web/src/lib/theme.ts is the other
// half, and uses the same storage key.
(function () {
  var query = window.matchMedia("(prefers-color-scheme: dark)");

  function apply() {
    var choice = null;
    try {
      choice = localStorage.getItem("dextro.theme");
    } catch {
      // Storage can be refused. The system's preference still works.
    }
    var dark = choice === "dark" || (choice !== "light" && query.matches);
    document.documentElement.dataset.theme = dark ? "dark" : "light";
  }

  apply();
  query.addEventListener("change", apply);
})();
