// agentsfs hub — small progressive-enhancement layer. No dependencies.
(function () {
  var root = document.documentElement;

  // Theme toggle (persisted). Initial theme is set inline in <head> to avoid flash.
  var toggle = document.getElementById("theme-toggle");
  if (toggle) {
    toggle.addEventListener("click", function () {
      var cur = root.getAttribute("data-theme");
      if (!cur) {
        cur = window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
      }
      var next = cur === "dark" ? "light" : "dark";
      root.setAttribute("data-theme", next);
      try { localStorage.setItem("afs-theme", next); } catch (e) {}
      toggle.textContent = next === "dark" ? "☼" : "☾"; // sun / moon
    });
  }

  // Collapsible folders.
  document.querySelectorAll(".tree .caret").forEach(function (c) {
    c.addEventListener("click", function () {
      var li = c.closest("li.dir");
      if (li) li.classList.toggle("collapsed");
    });
  });

  // Client-side tree filter: show matching notes and their ancestor folders.
  var filter = document.getElementById("tree-filter");
  var tree = document.querySelector(".tree");
  if (filter && tree) {
    filter.addEventListener("input", function () {
      var q = filter.value.trim().toLowerCase();
      var all = tree.querySelectorAll("li");
      if (!q) {
        all.forEach(function (li) { li.style.display = ""; });
        return;
      }
      all.forEach(function (li) { li.style.display = "none"; });
      tree.querySelectorAll("li:not(.dir)").forEach(function (leaf) {
        if (leaf.textContent.toLowerCase().indexOf(q) !== -1) {
          leaf.style.display = "";
          var p = leaf.parentElement.closest("li.dir");
          while (p) {
            p.style.display = "";
            p.classList.remove("collapsed");
            p = p.parentElement.closest("li.dir");
          }
        }
      });
    });
  }

  // Copy buttons.
  document.querySelectorAll("[data-copy]").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var text = btn.getAttribute("data-copy");
      navigator.clipboard.writeText(text).then(function () {
        var old = btn.textContent;
        btn.textContent = "copied";
        setTimeout(function () { btn.textContent = old; }, 1200);
      });
    });
  });
})();
