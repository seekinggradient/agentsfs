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

  // On the file view, scroll the side tree so the current note is in view —
  // without moving the page (contained scroll of the sidebar only).
  var current = document.querySelector(".sidetree .node-name.current");
  if (current) {
    var box = current.closest(".sidetree");
    if (box && box.scrollHeight > box.clientHeight) {
      var cr = current.getBoundingClientRect(), br = box.getBoundingClientRect();
      box.scrollTop += (cr.top - br.top) - box.clientHeight / 2 + cr.height / 2;
    }
  }

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

  // Agent side dock: on desktop, open the agent alongside the wiki content in a
  // right-hand panel; on phones, fall through to the full-page agent.
  var dock = document.getElementById("agent-dock");
  if (dock) {
    var agentUrl = dock.getAttribute("data-agent-url");
    var dockBody = dock.querySelector(".agent-dock-body");
    var isPhone = function () { return window.matchMedia("(max-width: 860px)").matches; };
    var loadFrame = function () {
      if (dock.dataset.loaded) return;
      var f = document.createElement("iframe");
      f.src = agentUrl;
      f.title = "Agent";
      f.setAttribute("allow", "microphone; clipboard-write");
      dockBody.appendChild(f);
      dock.dataset.loaded = "1";
    };
    var openDock = function () {
      loadFrame();
      root.classList.add("agent-open");
      try { localStorage.setItem("afs-agent", "1"); } catch (e) {}
    };
    var closeDock = function () {
      root.classList.remove("agent-open");
      try { localStorage.setItem("afs-agent", "0"); } catch (e) {}
    };
    document.querySelectorAll("[data-agent-toggle]").forEach(function (b) {
      b.addEventListener("click", function (e) {
        if (isPhone()) { window.location.href = agentUrl; return; } // full-page on phones
        e.preventDefault();
        if (root.classList.contains("agent-open")) closeDock(); else openDock();
      });
    });
    var dockClose = dock.querySelector("[data-agent-close]");
    if (dockClose) dockClose.addEventListener("click", closeDock);
    // Keep the panel open across wiki navigation on desktop (the chat itself
    // reloads per page — a known limit of the iframe approach).
    try {
      if (localStorage.getItem("afs-agent") === "1" && !isPhone()) openDock();
    } catch (e) {}
  }

  // File-tree show/hide on the reading view (initial state applied in <head>).
  document.querySelectorAll("[data-tree-toggle]").forEach(function (b) {
    b.addEventListener("click", function () {
      var hidden = root.classList.toggle("tree-hidden");
      try { localStorage.setItem("afs-tree-hidden", hidden ? "1" : "0"); } catch (e) {}
    });
  });

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
