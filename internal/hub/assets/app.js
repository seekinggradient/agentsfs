// agentsfs hub — progressive enhancement + in-place (pjax) navigation. Swapping
// only #page on internal links keeps the agent side dock (and its live
// conversation) alive while you browse the wiki. No dependencies.
(function () {
  var root = document.documentElement;
  var page = document.getElementById("page");

  function setTheme(next) {
    root.setAttribute("data-theme", next);
    try { localStorage.setItem("afs-theme", next); } catch (e) {}
    var t = document.getElementById("theme-toggle");
    if (t) t.textContent = next === "dark" ? "☼" : "☾";
  }
  // Reflect the persisted theme on the toggle icon at load.
  (function () {
    var cur = root.getAttribute("data-theme");
    var t = document.getElementById("theme-toggle");
    if (cur && t) t.textContent = cur === "dark" ? "☼" : "☾";
  })();

  // ---- agent side dock (persists across pjax navigation) ----
  var dock = document.getElementById("agent-dock");
  var agentUrl = dock ? dock.getAttribute("data-agent-url") : null;
  var isPhone = function () { return window.matchMedia("(max-width: 860px)").matches; };
  function loadFrame() {
    if (!dock || dock.dataset.loaded) return;
    var f = document.createElement("iframe");
    f.src = agentUrl; f.title = "Agent";
    f.setAttribute("allow", "microphone; clipboard-write");
    dock.querySelector(".agent-dock-body").appendChild(f);
    dock.dataset.loaded = "1";
  }
  function openDock() { loadFrame(); root.classList.add("agent-open"); try { localStorage.setItem("afs-agent", "1"); } catch (e) {} }
  function closeDock() { root.classList.remove("agent-open"); try { localStorage.setItem("afs-agent", "0"); } catch (e) {} }
  if (dock) { try { if (localStorage.getItem("afs-agent") === "1" && !isPhone()) openDock(); } catch (e) {} }

  function filterTree(input) {
    var tree = document.querySelector(".tree");
    if (!tree) return;
    var q = input.value.trim().toLowerCase();
    var all = tree.querySelectorAll("li");
    if (!q) { all.forEach(function (li) { li.style.display = ""; }); return; }
    all.forEach(function (li) { li.style.display = "none"; });
    tree.querySelectorAll("li:not(.dir)").forEach(function (leaf) {
      if (leaf.textContent.toLowerCase().indexOf(q) !== -1) {
        leaf.style.display = "";
        var p = leaf.parentElement.closest("li.dir");
        while (p) { p.style.display = ""; p.classList.remove("collapsed"); p = p.parentElement.closest("li.dir"); }
      }
    });
  }

  // ---- delegated interactions (survive #page swaps) ----
  document.addEventListener("click", function (e) {
    if (e.target.closest("#theme-toggle")) {
      var cur = root.getAttribute("data-theme") || (window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
      setTheme(cur === "dark" ? "light" : "dark");
      return;
    }
    var at = e.target.closest("[data-agent-toggle]");
    if (at) {
      if (isPhone()) { window.location.href = agentUrl; return; }
      e.preventDefault();
      if (root.classList.contains("agent-open")) closeDock(); else openDock();
      return;
    }
    if (e.target.closest("[data-agent-close]")) { closeDock(); return; }
    if (e.target.closest("[data-tree-toggle]")) {
      var hidden = root.classList.toggle("tree-hidden");
      try { localStorage.setItem("afs-tree-hidden", hidden ? "1" : "0"); } catch (e2) {}
      return;
    }
    var caret = e.target.closest(".tree .caret");
    if (caret) { var li = caret.closest("li.dir"); if (li) li.classList.toggle("collapsed"); return; }
    var cp = e.target.closest("[data-copy]");
    if (cp) {
      navigator.clipboard.writeText(cp.getAttribute("data-copy")).then(function () {
        var old = cp.textContent; cp.textContent = "copied";
        setTimeout(function () { cp.textContent = old; }, 1200);
      });
      return;
    }
    pjaxClick(e);
  });

  document.addEventListener("input", function (e) {
    if (e.target.id === "tree-filter") filterTree(e.target);
  });

  // ---- content init (on load + after each pjax swap) ----
  function initContent() {
    var current = document.querySelector(".sidetree .node-name.current");
    if (current) {
      var box = current.closest(".sidetree");
      if (box && box.scrollHeight > box.clientHeight) {
        var cr = current.getBoundingClientRect(), br = box.getBoundingClientRect();
        box.scrollTop += (cr.top - br.top) - box.clientHeight / 2 + cr.height / 2;
      }
    }
  }

  // ---- pjax: swap #page in place for internal links ----
  function pjaxClick(e) {
    if (!page || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    var a = e.target.closest("a[href]");
    if (!a) return;
    if (a.target && a.target !== "_self") return;
    if (a.hasAttribute("download")) return;
    var url;
    try { url = new URL(a.getAttribute("href"), location.href); } catch (_) { return; }
    if (url.origin !== location.origin) return;
    var p = url.pathname;
    // Skip non-HTML / special routes (assets, raw files, the agent proxy, auth).
    if (/^\/_assets\//.test(p) || /\/raw\//.test(p) || /\/agent(\/|$)/.test(p) || p === "/login" || p === "/logout") return;
    e.preventDefault();
    loadPage(url.href, true);
  }

  var navToken = 0;
  function loadPage(href, push) {
    var token = ++navToken;
    if (page) page.classList.add("pjax-loading");
    fetch(href, { headers: { "X-Requested-With": "pjax" }, credentials: "same-origin" })
      .then(function (r) { if (!r.ok) throw new Error("status " + r.status); return r.text(); })
      .then(function (html) {
        if (token !== navToken) return; // a newer navigation superseded this one
        var doc = new DOMParser().parseFromString(html, "text/html");
        var newPage = doc.getElementById("page");
        if (!newPage) throw new Error("no #page"); // e.g. a non-standard response
        page.innerHTML = newPage.innerHTML;
        var crumbs = document.querySelector(".crumbs"), nc = doc.querySelector(".crumbs");
        if (crumbs && nc) crumbs.innerHTML = nc.innerHTML;
        if (doc.title) document.title = doc.title;
        page.classList.remove("pjax-loading");
        if (push) history.pushState({ pjax: true }, "", href);
        window.scrollTo(0, 0);
        initContent();
      })
      .catch(function () { window.location.href = href; }); // robust full-nav fallback
  }
  window.addEventListener("popstate", function () {
    if (page) loadPage(location.href, false);
  });

  initContent();
})();
