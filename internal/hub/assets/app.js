// agentsfs hub — progressive enhancement + in-place (pjax) navigation. Swapping
// only #page on internal links keeps the agent side dock (and its live
// conversation) alive while you browse the wiki. No dependencies.
(function () {
  var root = document.documentElement;
  var page = document.getElementById("page");

  // Theme: system (follow the device) → light → dark, cycled by the header
  // toggle. "system" removes the override so the CSS prefers-color-scheme media
  // query drives it; light/dark pin it. The resolved theme is also pushed to the
  // embedded agent iframe so the chat matches the site instead of drifting to
  // its own OS default.
  var THEME_ICON = { system: "◐", light: "☼", dark: "☾" };
  var THEME_TITLE = { system: "Theme: auto (matches your device)", light: "Theme: light", dark: "Theme: dark" };
  function themeState() { try { return localStorage.getItem("afs-theme") || "system"; } catch (e) { return "system"; } }
  function osDark() { return window.matchMedia("(prefers-color-scheme: dark)").matches; }
  function effectiveTheme() { var s = themeState(); return s === "system" ? (osDark() ? "dark" : "light") : s; }
  function reflectToggle() {
    var t = document.getElementById("theme-toggle");
    if (!t) return;
    var s = themeState();
    t.textContent = THEME_ICON[s] || "◐";
    t.title = THEME_TITLE[s] || THEME_TITLE.system;
  }
  function pushThemeToAgent() {
    var d = document.getElementById("agent-dock");
    var f = d && d.querySelector("iframe");
    if (f && f.contentWindow) { try { f.contentWindow.postMessage({ type: "afs-theme", theme: effectiveTheme() }, "*"); } catch (e) {} }
  }
  function setThemeState(next) {
    if (next === "system") { root.removeAttribute("data-theme"); try { localStorage.removeItem("afs-theme"); } catch (e) {} }
    else { root.setAttribute("data-theme", next); try { localStorage.setItem("afs-theme", next); } catch (e) {} }
    reflectToggle(); pushThemeToAgent();
  }
  reflectToggle();
  // In "system" mode, re-sync the agent iframe when the device theme flips (the
  // page itself follows automatically via the media query).
  try {
    window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", function () {
      if (themeState() === "system") pushThemeToAgent();
    });
  } catch (e) {}

  // ---- agent side dock (persists across pjax navigation) ----
  var dock = document.getElementById("agent-dock");
  var agentUrl = dock ? dock.getAttribute("data-agent-url") : null;
  var isPhone = function () { return window.matchMedia("(max-width: 860px)").matches; };
  function loadFrame() {
    if (!dock || dock.dataset.loaded) return;
    var f = document.createElement("iframe");
    // Carry the resolved theme so the chat renders in the site's theme from its
    // first paint; live toggles are then pushed via postMessage.
    f.src = agentUrl + (agentUrl.indexOf("?") === -1 ? "?" : "&") + "afstheme=" + effectiveTheme();
    f.title = "Agent";
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

  function setRepoPanel(name) {
    document.querySelectorAll("[data-repo-tab]").forEach(function (b) {
      var active = b.getAttribute("data-repo-tab") === name;
      b.classList.toggle("active", active);
      b.setAttribute("aria-selected", active ? "true" : "false");
    });
    document.querySelectorAll("[data-repo-panel]").forEach(function (p) {
      p.hidden = p.getAttribute("data-repo-panel") !== name;
    });
    if (name === "graph") initRepoGraph();
  }

  function initRepoGraph() {
    var host = document.querySelector("[data-graph-host]");
    var dataEl = document.getElementById("repo-graph-data");
    if (!host || !dataEl || host.dataset.ready) return;
    var graph;
    try { graph = JSON.parse(dataEl.textContent || "{}"); } catch (e) { return; }
    var nodes = graph.nodes || [], links = graph.links || [];
    var svg = host.querySelector("svg");
    if (!svg || !nodes.length) return;
    host.dataset.ready = "1";

    var width = 1000, height = 620, cx = width / 2, cy = height / 2;
    var groups = [], groupIndex = {};
    nodes.forEach(function (n) {
      n.links = [];
      n.group = n.group || "root";
      if (groupIndex[n.group] === undefined) { groupIndex[n.group] = groups.length; groups.push(n.group); }
    });
    links.forEach(function (l) {
      var s = nodes[l.source], t = nodes[l.target];
      if (!s || !t) return;
      s.links.push(t.id); t.links.push(s.id);
    });

    var byGroup = {};
    nodes.forEach(function (n) { (byGroup[n.group] || (byGroup[n.group] = [])).push(n); });
    var tau = Math.PI * 2, golden = Math.PI * (3 - Math.sqrt(5));
    Object.keys(byGroup).forEach(function (g) {
      var bucket = byGroup[g].sort(function (a, b) { return b.degree - a.degree || a.path.localeCompare(b.path); });
      var gi = groupIndex[g], ga = groups.length === 1 ? -Math.PI / 2 : (gi / groups.length) * tau - Math.PI / 2;
      var gr = groups.length === 1 ? 0 : Math.min(245, 95 + groups.length * 12);
      var gx = cx + Math.cos(ga) * gr, gy = cy + Math.sin(ga) * gr;
      bucket.forEach(function (n, i) {
        var r = 18 + Math.sqrt(i) * 18;
        var a = i * golden;
        var hub = Math.min(0.5, (n.degree || 0) / 22);
        n.x = gx + Math.cos(a) * r;
        n.y = gy + Math.sin(a) * r;
        n.x = n.x * (1 - hub) + cx * hub;
        n.y = n.y * (1 - hub) + cy * hub;
      });
    });
    for (var iter = 0; iter < 56; iter++) {
      links.forEach(function (l) {
        var s = nodes[l.source], t = nodes[l.target];
        if (!s || !t) return;
        var dx = t.x - s.x, dy = t.y - s.y;
        var dist = Math.sqrt(dx * dx + dy * dy) || 1;
        var want = 58 + Math.min(70, Math.sqrt((s.degree || 1) + (t.degree || 1)) * 7);
        var pull = (dist - want) * 0.018;
        var mx = dx / dist * pull, my = dy / dist * pull;
        s.x += mx; s.y += my; t.x -= mx; t.y -= my;
      });
      nodes.forEach(function (n) {
        n.x += (cx - n.x) * 0.006;
        n.y += (cy - n.y) * 0.006;
        n.x = Math.max(24, Math.min(width - 24, n.x));
        n.y = Math.max(24, Math.min(height - 24, n.y));
      });
    }

    var degreeCutoff = nodes.length > 120 ? Math.max(2, topDegree(nodes, 0.12)) : 1;
    var maxLabels = nodes.length > 500 ? 16 : (nodes.length > 180 ? 32 : 80);
    var labelSet = {};
    nodes.slice().sort(function (a, b) { return b.degree - a.degree || a.path.localeCompare(b.path); }).slice(0, maxLabels).forEach(function (n) {
      if ((n.degree || 0) > 0 || nodes.length <= 80) labelSet[n.id] = true;
    });
    var edgeByKey = {};
    svg.textContent = "";
    var layer = document.createElementNS("http://www.w3.org/2000/svg", "g");
    svg.appendChild(layer);
    links.forEach(function (l) {
      var s = nodes[l.source], t = nodes[l.target];
      if (!s || !t) return;
      var line = document.createElementNS("http://www.w3.org/2000/svg", "line");
      line.setAttribute("x1", s.x.toFixed(1)); line.setAttribute("y1", s.y.toFixed(1));
      line.setAttribute("x2", t.x.toFixed(1)); line.setAttribute("y2", t.y.toFixed(1));
      line.setAttribute("class", "graph-edge");
      line.dataset.source = String(s.id); line.dataset.target = String(t.id);
      layer.appendChild(line);
      edgeByKey[s.id + ":" + t.id] = line;
      edgeByKey[t.id + ":" + s.id] = line;
    });
    nodes.forEach(function (n) {
      var r = Math.max(3.2, Math.min(10, 3.6 + Math.sqrt(n.degree || 0)));
      var circle = document.createElementNS("http://www.w3.org/2000/svg", "circle");
      circle.setAttribute("cx", n.x.toFixed(1)); circle.setAttribute("cy", n.y.toFixed(1));
      circle.setAttribute("r", r.toFixed(1));
      circle.setAttribute("class", "graph-node" + (n.degree >= degreeCutoff ? " hub" : ""));
      circle.dataset.nodeId = String(n.id);
      circle.setAttribute("tabindex", "0");
      circle.setAttribute("role", "link");
      circle.setAttribute("aria-label", n.path);
      layer.appendChild(circle);
      if (labelSet[n.id]) {
        var label = document.createElementNS("http://www.w3.org/2000/svg", "text");
        label.setAttribute("x", (n.x + r + 4).toFixed(1)); label.setAttribute("y", (n.y + 4).toFixed(1));
        label.setAttribute("class", "graph-label");
        label.dataset.labelId = String(n.id);
        label.textContent = n.name;
        layer.appendChild(label);
      }
    });

    var tip = host.querySelector("[data-graph-tip]");
    function focusNode(id, evt) {
      var n = nodes[id];
      if (!n) return;
      host.classList.add("is-focused");
      host.querySelectorAll(".active").forEach(function (el) { el.classList.remove("active"); });
      var active = {}; active[id] = true;
      n.links.forEach(function (other) { active[other] = true; var e = edgeByKey[id + ":" + other]; if (e) e.classList.add("active"); });
      Object.keys(active).forEach(function (k) {
        var node = host.querySelector('[data-node-id="' + k + '"]');
        var label = host.querySelector('[data-label-id="' + k + '"]');
        if (node) node.classList.add("active");
        if (label) label.classList.add("active");
      });
      if (tip) {
        tip.innerHTML = "<b></b><span></span>";
        tip.querySelector("b").textContent = n.path;
        tip.querySelector("span").textContent = n.desc || (n.degree + " links");
        var box = host.getBoundingClientRect();
        var px = evt ? evt.clientX - box.left + 14 : n.x + 14;
        var py = evt ? evt.clientY - box.top + 14 : n.y + 14;
        tip.style.transform = "translate(" + Math.max(8, Math.min(px, box.width - 334)) + "px," + Math.max(8, Math.min(py, box.height - 86)) + "px)";
      }
    }
    function clearFocus() {
      host.classList.remove("is-focused");
      host.querySelectorAll(".active").forEach(function (el) { el.classList.remove("active"); });
      if (tip) tip.style.transform = "translate(-999px,-999px)";
    }
    host.addEventListener("pointerover", function (e) {
      var c = e.target.closest("[data-node-id]");
      if (c) focusNode(Number(c.dataset.nodeId), e);
    });
    host.addEventListener("pointerout", function (e) {
      if (!host.contains(e.relatedTarget)) clearFocus();
    });
    host.addEventListener("click", function (e) {
      var c = e.target.closest("[data-node-id]");
      if (!c) return;
      var n = nodes[Number(c.dataset.nodeId)];
      if (n && n.href) {
        var href = new URL(n.href, location.href).href;
        if (page && repoScope(new URL(href).pathname) === repoScope(location.pathname)) loadPage(href, true);
        else window.location.href = href;
      }
    });
    host.addEventListener("keydown", function (e) {
      if (e.key !== "Enter" && e.key !== " ") return;
      var c = e.target.closest("[data-node-id]");
      if (!c) return;
      e.preventDefault();
      var n = nodes[Number(c.dataset.nodeId)];
      if (n && n.href) {
        var href = new URL(n.href, location.href).href;
        if (page && repoScope(new URL(href).pathname) === repoScope(location.pathname)) loadPage(href, true);
        else window.location.href = href;
      }
    });
    var search = document.querySelector("[data-graph-search]");
    if (search) {
      search.addEventListener("input", function () {
        var q = search.value.trim().toLowerCase();
        host.classList.toggle("is-searching", !!q);
        nodes.forEach(function (n) {
          var match = !!q && ((n.path + " " + (n.desc || "")).toLowerCase().indexOf(q) !== -1);
          var node = host.querySelector('[data-node-id="' + n.id + '"]');
          var label = host.querySelector('[data-label-id="' + n.id + '"]');
          if (node) node.classList.toggle("match", match);
          if (label) label.classList.toggle("match", match);
        });
      });
    }
  }

  function topDegree(nodes, percentile) {
    var d = nodes.map(function (n) { return n.degree || 0; }).sort(function (a, b) { return b - a; });
    return d[Math.max(0, Math.min(d.length - 1, Math.floor(d.length * percentile)))] || 1;
  }

  // ---- delegated interactions (survive #page swaps) ----
  document.addEventListener("click", function (e) {
    if (e.target.closest("#theme-toggle")) {
      var order = ["system", "light", "dark"];
      setThemeState(order[(order.indexOf(themeState()) + 1) % order.length]);
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
    var tab = e.target.closest("[data-repo-tab]");
    if (tab) { setRepoPanel(tab.getAttribute("data-repo-tab")); return; }
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
    if (document.querySelector('[data-repo-panel="graph"]:not([hidden])')) initRepoGraph();
    var current = document.querySelector(".sidetree .node-name.current");
    if (current) {
      var box = current.closest(".sidetree");
      if (box && box.scrollHeight > box.clientHeight) {
        var cr = current.getBoundingClientRect(), br = box.getBoundingClientRect();
        box.scrollTop += (cr.top - br.top) - box.clientHeight / 2 + cr.height / 2;
      }
    }
  }

  // The <user>/<repo> a path belongs to ("" for the dashboard/other pages).
  function repoScope(path) {
    var m = path.match(/^\/([^/]+)\/([^/]+)/);
    return m ? m[1] + "/" + m[2] : "";
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
    if (/^\/_assets\//.test(p) || /\/raw\//.test(p) || /^\/agent(\/|$)/.test(p) || p === "/login" || p === "/logout") return;
    // Only swap in place WITHIN the same repo — so the agent dock keeps its
    // conversation while browsing a repo's notes, but a full navigation (which
    // re-renders the dock for the new repo) happens when you cross into another
    // repo or the dashboard. Otherwise the dock would show the wrong repo's agent.
    if (repoScope(p) !== repoScope(location.pathname)) return;
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
