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
    t.setAttribute("aria-label", THEME_TITLE[s] || THEME_TITLE.system);
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
  function reflectAgentToggle(open) {
    document.querySelectorAll("[data-agent-toggle]").forEach(function (button) {
      button.setAttribute("aria-expanded", open ? "true" : "false");
    });
  }
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
  function openDock() { loadFrame(); root.classList.add("agent-open"); reflectAgentToggle(true); try { localStorage.setItem("afs-agent", "1"); } catch (e) {} requestAnimationFrame(initWorkspaceResizers); }
  function closeDock() { root.classList.remove("agent-open"); reflectAgentToggle(false); try { localStorage.setItem("afs-agent", "0"); } catch (e) {} requestAnimationFrame(initWorkspaceResizers); }
  if (dock) { try { if (localStorage.getItem("afs-agent") === "1" && !isPhone()) openDock(); } catch (e) {} }

  // ---- resizable file-workspace sidebars ----
  // The two separators are real grid tracks, so dragging never overlays the
  // document or steals space invisibly. Widths are remembered across notes;
  // narrower responsive layouts clamp or remove the relevant separator.
  var PANEL_WIDTHS = {
    tree: { css: "--file-tree-w", key: "afs-file-tree-width", min: 170, max: 480, selector: ".sidetree" },
    context: { css: "--file-context-w", key: "afs-file-context-width", min: 200, max: 420, selector: ".note-context" }
  };

  function panelStyleHost() { return document.querySelector(".file-shell"); }
  function panelBounds(kind, workspace) {
    var config = PANEL_WIDTHS[kind];
    var wide = window.matchMedia("(min-width: 1121px)").matches;
    var otherKind = kind === "tree" ? "context" : "tree";
    var other = workspace.querySelector(PANEL_WIDTHS[otherKind].selector);
    var otherWidth = 0;
    if (wide && other && getComputedStyle(other).display !== "none") otherWidth = other.getBoundingClientRect().width;
    var handles = wide ? 14 : 7;
    var readingMinimum = wide ? 640 : 420;
    var room = Math.floor(workspace.getBoundingClientRect().width - otherWidth - readingMinimum - handles);
    return { min: config.min, max: Math.max(config.min, Math.min(config.max, room)) };
  }

  function updatePanelHandle(kind, width, bounds) {
    var handle = document.querySelector('[data-workspace-resizer="' + kind + '"]');
    if (!handle) return;
    handle.setAttribute("aria-valuemin", String(bounds.min));
    handle.setAttribute("aria-valuemax", String(bounds.max));
    handle.setAttribute("aria-valuenow", String(Math.round(width)));
  }

  function setPanelWidth(kind, requested, persist) {
    var config = PANEL_WIDTHS[kind];
    var workspace = document.querySelector(".file-workspace");
    var host = panelStyleHost();
    if (!config || !workspace || !host) return 0;
    var bounds = panelBounds(kind, workspace);
    var width = Math.max(bounds.min, Math.min(bounds.max, Math.round(requested)));
    host.style.setProperty(config.css, width + "px");
    updatePanelHandle(kind, width, bounds);
    if (persist) { try { localStorage.setItem(config.key, String(width)); } catch (e) {} }
    return width;
  }

  function initWorkspaceResizers() {
    var workspace = document.querySelector(".file-workspace");
    if (!workspace) return;
    Object.keys(PANEL_WIDTHS).forEach(function (kind) {
      var config = PANEL_WIDTHS[kind];
      var panel = workspace.querySelector(config.selector);
      var handle = workspace.querySelector('[data-workspace-resizer="' + kind + '"]');
      if (!panel || !handle || getComputedStyle(handle).display === "none") return;
      var saved = NaN;
      try { saved = Number(localStorage.getItem(config.key)); } catch (e) {}
      if (Number.isFinite(saved) && saved > 0) setPanelWidth(kind, saved, false);
      else updatePanelHandle(kind, panel.getBoundingClientRect().width, panelBounds(kind, workspace));
    });
  }

  document.addEventListener("pointerdown", function (e) {
    var handle = e.target.closest && e.target.closest("[data-workspace-resizer]");
    if (!handle || e.button !== 0 || window.matchMedia("(max-width: 760px)").matches) return;
    var kind = handle.getAttribute("data-workspace-resizer");
    var workspace = handle.closest(".file-workspace");
    if (!PANEL_WIDTHS[kind] || !workspace) return;
    e.preventDefault();
    var pointerID = e.pointerId;
    root.classList.add("workspace-resizing");
    handle.classList.add("is-resizing");
    try { handle.setPointerCapture(pointerID); } catch (err) {}

    function widthAt(clientX) {
      var box = workspace.getBoundingClientRect();
      return kind === "tree" ? clientX - box.left : box.right - clientX;
    }
    function move(ev) {
      if (ev.pointerId !== pointerID) return;
      setPanelWidth(kind, widthAt(ev.clientX), false);
    }
    function finish(ev) {
      if (ev.pointerId !== pointerID) return;
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", finish);
      window.removeEventListener("pointercancel", finish);
      root.classList.remove("workspace-resizing");
      handle.classList.remove("is-resizing");
      try { handle.releasePointerCapture(pointerID); } catch (err) {}
      var panel = workspace.querySelector(PANEL_WIDTHS[kind].selector);
      if (panel) setPanelWidth(kind, panel.getBoundingClientRect().width, true);
    }
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", finish);
    window.addEventListener("pointercancel", finish);
  });

  document.addEventListener("dblclick", function (e) {
    var handle = e.target.closest && e.target.closest("[data-workspace-resizer]");
    if (!handle) return;
    var kind = handle.getAttribute("data-workspace-resizer");
    var config = PANEL_WIDTHS[kind], host = panelStyleHost();
    if (!config || !host) return;
    host.style.removeProperty(config.css);
    try { localStorage.removeItem(config.key); } catch (err) {}
    requestAnimationFrame(initWorkspaceResizers);
  });

  document.addEventListener("keydown", function (e) {
    var handle = e.target.closest && e.target.closest("[data-workspace-resizer]");
    if (!handle || !["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key)) return;
    var kind = handle.getAttribute("data-workspace-resizer");
    var config = PANEL_WIDTHS[kind];
    var workspace = handle.closest(".file-workspace");
    var panel = workspace && config ? workspace.querySelector(config.selector) : null;
    if (!workspace || !panel) return;
    var bounds = panelBounds(kind, workspace);
    var width = panel.getBoundingClientRect().width;
    if (e.key === "Home") width = bounds.min;
    else if (e.key === "End") width = bounds.max;
    else {
      var direction = e.key === "ArrowRight" ? 1 : -1;
      if (kind === "context") direction *= -1;
      width += direction * (e.shiftKey ? 32 : 16);
    }
    e.preventDefault();
    setPanelWidth(kind, width, true);
  });

  var panelResizeFrame = 0;
  window.addEventListener("resize", function () {
    cancelAnimationFrame(panelResizeFrame);
    panelResizeFrame = requestAnimationFrame(initWorkspaceResizers);
  });

  // ---- review mode: inline comments for the agent (owner-only, markdown notes) ----
  // The owner highlights passages in the rendered article and attaches comments;
  // "Handoff" posts them into the agent iframe (same-origin) which resolves them
  // by editing files and returns a diff to approve. Comments anchor by QUOTE
  // (selected text + context + occurrence index) against the article's normalized
  // text — never source offsets — so they survive re-rendering and reformatting.
  var CH_SUPPORTED = !!(window.CSS && CSS.highlights && window.Highlight);
  var reviewCtx = null;   // { user, repo, path, head } from the toolbar button
  var reviewDrafts = [];  // [{ id, quote, prefix, suffix, occurrence, note, matched }]
  var reviewHighlight = null;
  var reviewAck = { done: false };

  function reviewKey(ctx) { return "afs-review:" + ctx.user + "/" + ctx.repo + "/" + ctx.path; }
  function loadReviewDrafts(ctx) {
    try { return JSON.parse(localStorage.getItem(reviewKey(ctx)) || "[]") || []; } catch (e) { return []; }
  }
  function saveReviewDrafts() {
    if (!reviewCtx) return;
    try {
      if (reviewDrafts.length) {
        localStorage.setItem(reviewKey(reviewCtx), JSON.stringify(reviewDrafts.map(function (d) {
          return { id: d.id, quote: d.quote, prefix: d.prefix, suffix: d.suffix, occurrence: d.occurrence, note: d.note };
        })));
      } else {
        localStorage.removeItem(reviewKey(reviewCtx));
      }
    } catch (e) {}
  }
  function normText(s) { return String(s).replace(/\s+/g, " ").trim(); }
  function reviewArticle() { return document.querySelector(".reading .prose"); }

  // Build a text index of the (clean, un-marked) article: the concatenated raw
  // text of its text nodes, a whitespace-normalized view, and a map from each
  // normalized-char position back to the raw offset (and thence to a text node).
  function buildTextIndex(article) {
    var walker = document.createTreeWalker(article, NodeFilter.SHOW_TEXT, null);
    var nodes = [], raw = "", node;
    while ((node = walker.nextNode())) { nodes.push({ node: node, start: raw.length }); raw += node.nodeValue; }
    var norm = "", map = [], prevSpace = true;
    for (var i = 0; i < raw.length; i++) {
      var ch = raw[i];
      if (/\s/.test(ch)) { if (prevSpace) continue; norm += " "; map.push(i); prevSpace = true; }
      else { norm += ch; map.push(i); prevSpace = false; }
    }
    return { nodes: nodes, raw: raw, norm: norm, map: map };
  }
  function rawToNode(nodes, rawPos) {
    for (var i = nodes.length - 1; i >= 0; i--) {
      if (rawPos >= nodes[i].start) return { node: nodes[i].node, offset: rawPos - nodes[i].start };
    }
    return nodes.length ? { node: nodes[0].node, offset: 0 } : null;
  }
  // Occurrence positions of `quote` in the normalized text.
  function normOccurrences(norm, quote) {
    var out = [], from = 0, at;
    while ((at = norm.indexOf(quote, from)) !== -1) { out.push(at); from = at + Math.max(1, quote.length); }
    return out;
  }
  // A DOM Range covering the draft's quoted passage, or null if it no longer matches.
  function rangeForDraft(idx, c) {
    var positions = normOccurrences(idx.norm, c.quote);
    if (!positions.length) return null;
    var oi = (typeof c.occurrence === "number" && c.occurrence >= 0 && c.occurrence < positions.length) ? c.occurrence : 0;
    var normStart = positions[oi];
    var rawStart = idx.map[normStart];
    var rawEnd = idx.map[normStart + c.quote.length - 1] + 1;
    var s = rawToNode(idx.nodes, rawStart), e = rawToNode(idx.nodes, rawEnd);
    if (!s || !e) return null;
    var r = document.createRange();
    try { r.setStart(s.node, s.offset); r.setEnd(e.node, e.offset); } catch (err) { return null; }
    return r;
  }

  // Capture a quote anchor from the current selection inside the article.
  function anchorFromSelection(article, range) {
    var quote = normText(range.toString());
    if (quote.length < 2) return null;
    var idx = buildTextIndex(article);
    var pre = document.createRange();
    pre.selectNodeContents(article);
    try { pre.setEnd(range.startContainer, range.startOffset); } catch (e) { return null; }
    var startApprox = normText(pre.toString()).length;
    var positions = normOccurrences(idx.norm, quote);
    if (!positions.length) return null;
    var best = 0, bestDist = Infinity;
    positions.forEach(function (pos, i) { var d = Math.abs(pos - startApprox); if (d < bestDist) { bestDist = d; best = i; } });
    var at = positions[best];
    // Snap the anchor outward to whitespace boundaries: a drag that starts or
    // ends mid-word ("ins|talled.") makes an ugly quote in the rail and the
    // highlight. Expanding never loses what the user selected.
    var start = at, end = at + quote.length;
    while (start > 0 && /\S/.test(idx.norm[start]) && /\S/.test(idx.norm[start - 1])) start--;
    while (end < idx.norm.length && /\S/.test(idx.norm[end - 1]) && /\S/.test(idx.norm[end])) end++;
    if (start !== at || end !== at + quote.length) {
      quote = idx.norm.slice(start, end);
      var snapped = normOccurrences(idx.norm, quote);
      best = 0;
      for (var s = 0; s < snapped.length; s++) { if (snapped[s] === start) { best = s; break; } }
      at = start;
    }
    return {
      quote: quote,
      prefix: idx.norm.slice(Math.max(0, at - 30), at),
      suffix: idx.norm.slice(at + quote.length, at + quote.length + 30),
      occurrence: best
    };
  }

  function unwrapReviewMarks() {
    var article = reviewArticle();
    if (!article) return;
    article.querySelectorAll("mark.afs-cmark").forEach(function (m) {
      var parent = m.parentNode;
      while (m.firstChild) parent.insertBefore(m.firstChild, m);
      parent.removeChild(m);
      parent.normalize();
    });
  }
  function wrapRangeMarks(range, id) {
    var container = range.commonAncestorContainer;
    var targets = [], node;
    if (container.nodeType === Node.TEXT_NODE) {
      // The common case: the whole quote sits inside ONE text node, and a
      // TreeWalker rooted at a text node yields nothing from nextNode().
      targets = [container];
    } else {
      var walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT, null);
      while ((node = walker.nextNode())) { if (range.intersectsNode(node)) targets.push(node); }
    }
    targets.reverse().forEach(function (tn) {
      var so = (tn === range.startContainer) ? range.startOffset : 0;
      var eo = (tn === range.endContainer) ? range.endOffset : tn.nodeValue.length;
      if (so >= eo) return;
      var sub = document.createRange();
      sub.setStart(tn, so); sub.setEnd(tn, eo);
      var mark = document.createElement("mark");
      mark.className = "afs-cmark"; mark.setAttribute("data-comment-id", id);
      try { sub.surroundContents(mark); } catch (e) {}
    });
  }

  var reviewRanges = {}; // id -> Range (for scroll-to), kept for the Highlight-API path
  function renderReviewHighlights() {
    var article = reviewArticle();
    if (!article) return;
    unwrapReviewMarks();
    reviewRanges = {};
    if (reviewHighlight) reviewHighlight.clear();
    if (CH_SUPPORTED && reviewDrafts.length && !reviewHighlight) {
      reviewHighlight = new Highlight();
      CSS.highlights.set("afs-review", reviewHighlight);
    }
    var idx = buildTextIndex(article);
    reviewDrafts.forEach(function (c) {
      var r = rangeForDraft(idx, c);
      c.matched = !!r;
      if (!r) return;
      reviewRanges[c.id] = r;
      if (CH_SUPPORTED) reviewHighlight.add(r);
      else wrapRangeMarks(r, c.id);
    });
  }

  function reviewToast(message) {
    var t = document.createElement("div");
    t.className = "review-toast"; t.textContent = message;
    document.body.appendChild(t);
    requestAnimationFrame(function () { t.classList.add("show"); });
    setTimeout(function () { t.classList.remove("show"); setTimeout(function () { t.remove(); }, 300); }, 3200);
  }

  var reviewRail = null, reviewPopover = null;
  function ensureReviewRail() {
    if (reviewRail && document.body.contains(reviewRail)) return reviewRail;
    reviewRail = document.createElement("aside");
    reviewRail.className = "review-rail";
    reviewRail.setAttribute("aria-label", "Comments for the agent");
    document.body.appendChild(reviewRail);
    return reviewRail;
  }
  function removeReviewRail() { if (reviewRail) { reviewRail.remove(); reviewRail = null; } }

  function renderReviewRail() {
    if (!reviewDrafts.length && !root.classList.contains("comment-mode")) { removeReviewRail(); return; }
    var rail = ensureReviewRail();
    rail.textContent = "";
    var head = document.createElement("div");
    head.className = "review-rail-head";
    var title = document.createElement("span");
    title.textContent = "Comments (" + reviewDrafts.length + ")";
    head.appendChild(title);
    if (reviewDrafts.length) {
      var clear = document.createElement("button");
      clear.type = "button"; clear.className = "review-clear"; clear.setAttribute("data-review-clear", "");
      clear.textContent = "Clear all";
      head.appendChild(clear);
    }
    rail.appendChild(head);

    var list = document.createElement("ul");
    list.className = "review-list";
    reviewDrafts.forEach(function (c) {
      var li = document.createElement("li");
      li.setAttribute("data-cid", c.id);
      if (!c.matched) li.classList.add("stale");
      var quote = document.createElement("blockquote");
      quote.textContent = c.quote.length > 90 ? c.quote.slice(0, 88) + "…" : c.quote;
      li.appendChild(quote);
      if (!c.matched) {
        var badge = document.createElement("span");
        badge.className = "review-stale-badge"; badge.textContent = "text changed";
        li.appendChild(badge);
      }
      var note = document.createElement("p");
      note.className = "rc-note"; note.textContent = c.note || "(no comment)";
      li.appendChild(note);
      var actions = document.createElement("div");
      actions.className = "rc-actions";
      var edit = document.createElement("button");
      edit.type = "button"; edit.setAttribute("data-review-edit", c.id); edit.textContent = "Edit";
      var del = document.createElement("button");
      del.type = "button"; del.setAttribute("data-review-del", c.id); del.textContent = "Delete";
      actions.appendChild(edit); actions.appendChild(del);
      li.appendChild(actions);
      list.appendChild(li);
    });
    rail.appendChild(list);

    if (reviewDrafts.length) {
      var handoff = document.createElement("button");
      handoff.type = "button"; handoff.className = "review-handoff"; handoff.setAttribute("data-review-handoff", "");
      handoff.textContent = "Handoff to your agent (" + reviewDrafts.length + ")";
      rail.appendChild(handoff);
    } else {
      var hint = document.createElement("p");
      hint.className = "review-hint"; hint.textContent = "Select text in the note to add a comment.";
      rail.appendChild(hint);
    }
  }

  function closeReviewPopover() { if (reviewPopover) { reviewPopover.remove(); reviewPopover = null; } }
  function openReviewPopover(anchor, rect) {
    closeReviewPopover();
    var pop = document.createElement("div");
    pop.className = "review-popover";
    var ta = document.createElement("textarea");
    ta.rows = 2; ta.placeholder = "Comment for the agent…";
    pop.appendChild(ta);
    var row = document.createElement("div");
    row.className = "review-popover-actions";
    var cancel = document.createElement("button");
    cancel.type = "button"; cancel.setAttribute("data-review-cancel", ""); cancel.textContent = "Cancel";
    var add = document.createElement("button");
    add.type = "button"; add.className = "primary"; add.setAttribute("data-review-add", ""); add.textContent = "Add comment";
    row.appendChild(cancel); row.appendChild(add);
    pop.appendChild(row);
    document.body.appendChild(pop);
    var top, left;
    if (isPhone()) {
      // Phone: span the width; place below the selection, flipping ABOVE it
      // when it would fall below the viewport fold — and never off-screen.
      pop.style.width = "auto";
      pop.style.left = "12px";
      pop.style.right = "12px";
      var popH = pop.offsetHeight;
      if (rect.bottom + 8 + popH > window.innerHeight) {
        top = window.scrollY + rect.top - popH - 8;
        var minTop = window.scrollY + 8;
        if (top < minTop) top = minTop;
      } else {
        top = window.scrollY + rect.bottom + 8;
      }
      pop.style.top = top + "px";
    } else {
      top = window.scrollY + rect.bottom + 8;
      left = Math.max(12, Math.min(window.scrollX + rect.left, window.scrollX + window.innerWidth - pop.offsetWidth - 12));
      pop.style.top = top + "px"; pop.style.left = left + "px";
    }
    pop._anchor = anchor;
    ta.focus();
    ta.addEventListener("keydown", function (e) {
      if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); commitPopover(); }
      if (e.key === "Escape") { e.preventDefault(); closeReviewPopover(); }
    });
    reviewPopover = pop;
  }
  function commitPopover() {
    if (!reviewPopover || !reviewCtx) return;
    var anchor = reviewPopover._anchor;
    var note = reviewPopover.querySelector("textarea").value.trim();
    closeReviewPopover();
    if (!anchor) return;
    reviewDrafts.push({
      id: "c" + Date.now().toString(36) + Math.random().toString(36).slice(2, 6),
      quote: anchor.quote, prefix: anchor.prefix, suffix: anchor.suffix, occurrence: anchor.occurrence, note: note
    });
    saveReviewDrafts();
    renderReviewHighlights();
    renderReviewRail();
    try { window.getSelection().removeAllRanges(); } catch (e) {}
  }

  function deleteReviewDraft(id) {
    reviewDrafts = reviewDrafts.filter(function (d) { return d.id !== id; });
    saveReviewDrafts();
    renderReviewHighlights();
    renderReviewRail();
  }
  function editReviewDraft(id) {
    var c = reviewDrafts.filter(function (d) { return d.id === id; })[0];
    if (!c) return;
    var li = reviewRail && reviewRail.querySelector('li[data-cid="' + id + '"]');
    if (!li) return;
    var noteEl = li.querySelector(".rc-note");
    var ta = document.createElement("textarea");
    ta.className = "rc-edit"; ta.value = c.note || ""; ta.rows = 2;
    noteEl.replaceWith(ta);
    ta.focus();
    function save() {
      c.note = ta.value.trim();
      saveReviewDrafts();
      renderReviewRail();
    }
    ta.addEventListener("blur", save);
    ta.addEventListener("keydown", function (e) {
      if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); ta.blur(); }
      if (e.key === "Escape") { e.preventDefault(); renderReviewRail(); }
    });
  }

  function clearAllReviewDrafts() {
    reviewDrafts = [];
    saveReviewDrafts();
    renderReviewHighlights();
    renderReviewRail();
  }

  // Hand the comments to the agent. Two transports, same payload:
  // - Desktop: post into the agent iframe, retrying until it acks (or 10s).
  // - Phone (dock = full-page navigation): stage the payload in the SAME-ORIGIN
  //   localStorage key `afs-review-pending` and navigate to the agent, which
  //   consumes the key on load (delete-on-read, nonce/ts validated there).
  function reviewHandoffToAgent() {
    if (!reviewCtx || !reviewDrafts.length) return;
    var payload = {
      // Per-click nonce: lets the agent dedupe the 500ms retry copies of ONE
      // handoff (and replays of a consumed mobile handoff) while still accepting
      // a deliberate re-handoff of the same unchanged comments (e.g. after
      // discarding a proposal).
      nonce: Date.now().toString(36) + "-" + Math.random().toString(36).slice(2, 10),
      user: reviewCtx.user, repo: reviewCtx.repo, path: reviewCtx.path, head: reviewCtx.head,
      ts: Date.now(),
      comments: reviewDrafts.map(function (d) {
        return { id: d.id, quote: d.quote, prefix: d.prefix, suffix: d.suffix, occurrence: d.occurrence, note: d.note };
      })
    };
    if (isPhone()) {
      if (!agentUrl) { reviewToast("Your agent isn't available."); return; }
      try {
        localStorage.setItem("afs-review-pending", JSON.stringify(payload));
      } catch (e) {
        reviewToast("Couldn't stage the handoff — storage unavailable.");
        return;
      }
      window.location.href = agentUrl;
      return;
    }
    openDock();
    var message = { type: "afs-review-handoff", payload: payload };
    reviewAck = { done: false };
    var start = Date.now();
    (function tryPost() {
      if (reviewAck.done) return;
      if (Date.now() - start > 10000) { reviewToast("Couldn't reach your agent — try reopening the dock."); return; }
      var frame = dock && dock.querySelector("iframe");
      if (frame && frame.contentWindow) { try { frame.contentWindow.postMessage(message, location.origin); } catch (e) {} }
      setTimeout(tryPost, 500);
    })();
  }

  function onReviewCommitted(data) {
    if (!reviewCtx) return;
    clearAllReviewDrafts();
    if (root.classList.contains("comment-mode")) toggleCommentMode(false);
    reviewToast("Committed " + (data.commit || "changes") + " — refreshing");
    // Refresh the note to the new commit while keeping the dock (+ its chat) alive.
    if (typeof loadPage === "function") loadPage(location.href, false);
    else location.reload();
  }

  function toggleCommentMode(force) {
    var on = force != null ? force : !root.classList.contains("comment-mode");
    root.classList.toggle("comment-mode", on);
    document.querySelectorAll("[data-comment-toggle]").forEach(function (b) {
      b.setAttribute("aria-pressed", on ? "true" : "false");
    });
    if (!on) closeReviewPopover();
    renderReviewRail();
  }

  // Read the toolbar button's context + restore drafts/highlights for this note.
  // Called on load and after every pjax swap.
  function initReview() {
    closeReviewPopover();
    removeReviewRail();
    root.classList.remove("comment-mode");
    if (reviewHighlight) reviewHighlight.clear();
    reviewRanges = {};
    var btn = document.querySelector("[data-comment-toggle]");
    if (!btn) { reviewCtx = null; reviewDrafts = []; return; }
    reviewCtx = {
      user: btn.getAttribute("data-user"), repo: btn.getAttribute("data-repo"),
      path: btn.getAttribute("data-path"), head: btn.getAttribute("data-head")
    };
    reviewDrafts = loadReviewDrafts(reviewCtx);
    if (reviewDrafts.length) { renderReviewHighlights(); renderReviewRail(); }
  }

  // Clicks on review controls (buttons, not links — pjax ignores them).
  document.addEventListener("click", function (e) {
    if (e.target.closest("[data-comment-toggle]")) { toggleCommentMode(); return; }
    if (e.target.closest("[data-review-handoff]")) { reviewHandoffToAgent(); return; }
    if (e.target.closest("[data-review-clear]")) { clearAllReviewDrafts(); return; }
    var del = e.target.closest("[data-review-del]");
    if (del) { deleteReviewDraft(del.getAttribute("data-review-del")); return; }
    var edit = e.target.closest("[data-review-edit]");
    if (edit) { editReviewDraft(edit.getAttribute("data-review-edit")); return; }
    if (e.target.closest("[data-review-add]")) { commitPopover(); return; }
    if (e.target.closest("[data-review-cancel]")) { closeReviewPopover(); return; }
    var item = e.target.closest(".review-list li[data-cid]");
    if (item && !e.target.closest("button")) {
      var r = reviewRanges[item.getAttribute("data-cid")];
      var target = r ? (r.startContainer.parentElement || null) : null;
      if (target) target.scrollIntoView({ block: "center", behavior: "smooth" });
      return;
    }
  });

  // Select text in the article (in comment mode) → offer to add a comment.
  // Shared by both capture paths (mouseup on desktop, selectionchange on touch):
  // only acts on a non-collapsed selection fully inside the article.
  function maybeOfferComment() {
    if (!reviewCtx || !root.classList.contains("comment-mode")) return;
    if (reviewPopover) return;
    var sel = window.getSelection();
    if (!sel || sel.isCollapsed || !sel.rangeCount) return;
    var article = reviewArticle();
    if (!article) return;
    var range = sel.getRangeAt(0);
    if (!article.contains(range.startContainer) || !article.contains(range.endContainer)) return;
    var anchor = anchorFromSelection(article, range);
    if (!anchor) return;
    openReviewPopover(anchor, range.getBoundingClientRect());
  }

  var mouseIsDown = false;
  document.addEventListener("mousedown", function () { mouseIsDown = true; });
  document.addEventListener("mouseup", function (e) {
    mouseIsDown = false;
    // Don't re-open over an existing popover, or when interacting with the
    // popover/rail chrome (its own mouseup would otherwise re-trigger this).
    if (e.target.closest && e.target.closest(".review-popover, .review-rail")) return;
    setTimeout(maybeOfferComment, 0);
  });

  // Touch-first capture: dragging the selection handles on a phone fires no
  // mouseup, so watch the selection itself (debounced) while comment mode is
  // on. Suppressed while a mouse button is down, so desktop keeps its exact
  // select-then-release behavior.
  var selectionDebounce = 0;
  document.addEventListener("selectionchange", function () {
    if (!reviewCtx || !root.classList.contains("comment-mode")) return;
    if (reviewPopover || mouseIsDown) return;
    clearTimeout(selectionDebounce);
    selectionDebounce = setTimeout(maybeOfferComment, 300);
  });

  // Ack + committed messages from the same-origin agent iframe.
  window.addEventListener("message", function (e) {
    if (e.origin !== location.origin || !e.data) return;
    if (e.data.type === "afs-review-ack") { reviewAck.done = true; return; }
    if (e.data.type === "afs-review-committed") { onReviewCommitted(e.data); return; }
  });

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

  function toggleDashboardConnect() {
    var panel = document.getElementById("dashboard-connect");
    if (!panel) return;
    var open = panel.hidden;
    panel.hidden = !open;
    document.querySelectorAll("[data-dashboard-connect-toggle]").forEach(function (button) {
      button.setAttribute("aria-expanded", open ? "true" : "false");
    });
    if (open) panel.scrollIntoView({ block: "nearest" });
  }

  // ---- repository index view + sorting ----
  var REPO_SORT_DIRECTIONS = { updated: "desc", name: "asc", notes: "desc", access: "asc" };
  var REPO_SORT_LABELS = { updated: "last updated", name: "name", notes: "note count", access: "access" };

  function repoSortValue(item, key) {
    var value = item.getAttribute("data-sort-" + key) || "";
    return key === "updated" || key === "notes" ? Number(value) || 0 : value.toLocaleLowerCase();
  }

  function sortRepoContainer(container, key, direction) {
    if (!container) return;
    var items = Array.prototype.slice.call(container.querySelectorAll(":scope > [data-repo-item]"));
    items.sort(function (a, b) {
      var av = repoSortValue(a, key), bv = repoSortValue(b, key);
      // Empty repositories have no meaningful activity date. Keep them at the
      // bottom in both directions instead of letting zero look oldest/newest.
      if (key === "updated" && (!av || !bv) && av !== bv) return av ? -1 : 1;
      var result = typeof av === "number" ? av - bv : av.localeCompare(bv, undefined, { numeric: true, sensitivity: "base" });
      if (result === 0 && key !== "name") {
        return repoSortValue(a, "name").localeCompare(repoSortValue(b, "name"), undefined, { numeric: true, sensitivity: "base" });
      }
      return direction === "desc" ? -result : result;
    });
    items.forEach(function (item) { container.appendChild(item); });
    var add = container.querySelector(":scope > .repo-add-cell");
    if (add) container.appendChild(add);
  }

  function setDashboardSort(key, direction, remember, announce) {
    if (!REPO_SORT_DIRECTIONS[key]) key = "updated";
    if (direction !== "asc" && direction !== "desc") direction = REPO_SORT_DIRECTIONS[key];
    document.querySelectorAll("[data-repo-collection]").forEach(function (collection) {
      sortRepoContainer(collection.querySelector("[data-repo-grid]"), key, direction);
      var tbody = collection.querySelector("[data-repo-table] tbody");
      sortRepoContainer(tbody, key, direction);
    });
    var select = document.querySelector("[data-repo-sort]");
    if (select) select.value = key;
    var directionButton = document.querySelector("[data-repo-sort-direction]");
    if (directionButton) {
      var descending = direction === "desc";
      directionButton.querySelector("span").textContent = descending ? "↓" : "↑";
      directionButton.setAttribute("aria-label", descending ? "Sort descending" : "Sort ascending");
      directionButton.title = descending ? "Sort descending" : "Sort ascending";
      directionButton.setAttribute("data-direction", direction);
    }
    document.querySelectorAll(".repo-table thead th").forEach(function (th) {
      var button = th.querySelector("[data-repo-sort-key]");
      if (!button) return;
      var active = button.getAttribute("data-repo-sort-key") === key;
      if (active) th.setAttribute("aria-sort", direction === "desc" ? "descending" : "ascending");
      else th.removeAttribute("aria-sort");
      var indicator = button.querySelector("span");
      if (indicator) indicator.textContent = active ? (direction === "desc" ? "↓" : "↑") : "";
    });
    if (remember) {
      try {
        localStorage.setItem("afs-dashboard-sort", key);
        localStorage.setItem("afs-dashboard-sort-direction", direction);
      } catch (e) {}
    }
    if (announce) {
      var status = document.querySelector("[data-repo-sort-status]");
      if (status) status.textContent = "Repositories sorted by " + REPO_SORT_LABELS[key] + ", " + (direction === "desc" ? "descending" : "ascending") + ".";
    }
  }

  function setDashboardView(mode, remember) {
    if (mode !== "table") mode = "grid";
    document.querySelectorAll("[data-repo-grid]").forEach(function (grid) { grid.hidden = mode !== "grid"; });
    document.querySelectorAll("[data-repo-table]").forEach(function (table) { table.hidden = mode !== "table"; });
    document.querySelectorAll("[data-repo-view-mode]").forEach(function (button) {
      button.setAttribute("aria-pressed", button.getAttribute("data-repo-view-mode") === mode ? "true" : "false");
    });
    var main = document.getElementById("dashboard-main");
    if (main) main.setAttribute("data-repo-mode", mode);
    if (remember) { try { localStorage.setItem("afs-dashboard-view", mode); } catch (e) {} }
  }

  function initDashboardIndex() {
    if (!document.querySelector("[data-repo-sort]")) return;
    var key = "updated", direction = "desc", mode = "grid";
    try {
      key = localStorage.getItem("afs-dashboard-sort") || key;
      direction = localStorage.getItem("afs-dashboard-sort-direction") || REPO_SORT_DIRECTIONS[key] || direction;
      mode = localStorage.getItem("afs-dashboard-view") || mode;
    } catch (e) {}
    setDashboardSort(key, direction, false, false);
    setDashboardView(mode, false);
  }

  function reflectCopyResult(control, ok) {
    var old = control.innerHTML;
    control.textContent = ok ? "Copied" : "Copy failed";
    setTimeout(function () { control.innerHTML = old; }, 1200);
  }

  function fallbackCopy(text) {
    var input = document.createElement("textarea");
    input.value = text;
    input.setAttribute("readonly", "");
    input.style.position = "fixed";
    input.style.opacity = "0";
    document.body.appendChild(input);
    input.select();
    var ok = false;
    try { ok = document.execCommand("copy"); } catch (e) {}
    input.remove();
    return ok;
  }

  function copyText(text, control) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(function () {
        reflectCopyResult(control, true);
      }).catch(function () {
        reflectCopyResult(control, fallbackCopy(text));
      });
      return;
    }
    reflectCopyResult(control, fallbackCopy(text));
  }

  function setRepoPanel(name, remember) {
    document.querySelectorAll("[data-repo-tab]").forEach(function (b) {
      var active = b.getAttribute("data-repo-tab") === name;
      b.classList.toggle("active", active);
      b.setAttribute("aria-selected", active ? "true" : "false");
      b.setAttribute("tabindex", active ? "0" : "-1");
    });
    document.querySelectorAll("[data-repo-panel]").forEach(function (p) {
      p.hidden = p.getAttribute("data-repo-panel") !== name;
    });
    if (remember && document.querySelector("[data-repo-view]")) {
      try { localStorage.setItem("afs-repo-view:" + repoScope(location.pathname), name); } catch (e) {}
      try {
        var stateURL = new URL(location.href);
        if (name === "graph" || name === "table") stateURL.searchParams.set("view", name);
        else stateURL.searchParams.delete("view");
        if (name !== "graph") { stateURL.searchParams.delete("node"); stateURL.searchParams.delete("q"); }
        history.replaceState(history.state, "", stateURL.href);
      } catch (e2) {}
    }
    if (name === "graph") initRepoGraph();
  }

  // ---- sortable repository file table ----
  var FILE_SORT_DIRECTIONS = { updated: "desc", name: "asc", folder: "asc", type: "asc" };
  var FILE_SORT_LABELS = { updated: "last updated", name: "name", folder: "folder", type: "type" };

  function fileSortValue(row, key) {
    var value = row.getAttribute("data-sort-" + key) || "";
    return key === "updated" ? Number(value) || 0 : value.toLocaleLowerCase();
  }

  function setRepoFileSort(key, direction, remember, announce) {
    var table = document.querySelector(".repo-file-table");
    if (!table) return;
    if (!FILE_SORT_DIRECTIONS[key]) key = "updated";
    if (direction !== "asc" && direction !== "desc") direction = FILE_SORT_DIRECTIONS[key];
    var tbody = table.querySelector("tbody");
    var rows = Array.prototype.slice.call(tbody.querySelectorAll(":scope > [data-file-table-row]"));
    rows.sort(function (a, b) {
      var av = fileSortValue(a, key), bv = fileSortValue(b, key);
      if (key === "updated" && (!av || !bv) && av !== bv) return av ? -1 : 1;
      var result = typeof av === "number" ? av - bv : av.localeCompare(bv, undefined, { numeric: true, sensitivity: "base" });
      if (result === 0 && key !== "name") return fileSortValue(a, "name").localeCompare(fileSortValue(b, "name"), undefined, { numeric: true, sensitivity: "base" });
      return direction === "desc" ? -result : result;
    });
    rows.forEach(function (row) { tbody.appendChild(row); });
    var select = document.querySelector("[data-file-table-sort]");
    if (select) select.value = key;
    var directionButton = document.querySelector("[data-file-table-direction]");
    if (directionButton) {
      directionButton.setAttribute("data-direction", direction);
      directionButton.setAttribute("aria-label", direction === "desc" ? "Sort descending" : "Sort ascending");
      directionButton.title = direction === "desc" ? "Sort descending" : "Sort ascending";
      directionButton.querySelector("span").textContent = direction === "desc" ? "↓" : "↑";
    }
    table.querySelectorAll("thead th").forEach(function (th) {
      var button = th.querySelector("[data-file-sort-key]");
      if (!button) return;
      var active = button.getAttribute("data-file-sort-key") === key;
      if (active) th.setAttribute("aria-sort", direction === "desc" ? "descending" : "ascending");
      else th.removeAttribute("aria-sort");
      var indicator = button.querySelector("span");
      if (indicator) indicator.textContent = active ? (direction === "desc" ? "↓" : "↑") : "";
    });
    if (remember) {
      try {
        localStorage.setItem("afs-file-sort:" + repoScope(location.pathname), key);
        localStorage.setItem("afs-file-sort-direction:" + repoScope(location.pathname), direction);
      } catch (e) {}
    }
    if (announce) {
      var status = document.querySelector("[data-file-table-status]");
      if (status) status.textContent = "Files sorted by " + FILE_SORT_LABELS[key] + ", " + (direction === "desc" ? "descending" : "ascending") + ".";
    }
  }

  function filterRepoFileTable(input) {
    var table = document.querySelector(".repo-file-table");
    if (!table) return;
    var query = (input.value || "").trim().toLocaleLowerCase();
    var shown = 0;
    table.querySelectorAll("[data-file-table-row]").forEach(function (row) {
      var visible = !query || (row.getAttribute("data-filter-value") || "").toLocaleLowerCase().indexOf(query) !== -1;
      row.hidden = !visible;
      if (visible) shown++;
    });
    var count = document.querySelector("[data-file-table-count]");
    if (count) count.textContent = shown + (shown === 1 ? " file" : " files");
    var empty = document.querySelector("[data-file-table-empty]");
    if (empty) empty.hidden = shown !== 0;
  }

  function initRepoFileTable() {
    if (!document.querySelector(".repo-file-table")) return;
    var key = "updated", direction = "desc";
    try {
      key = localStorage.getItem("afs-file-sort:" + repoScope(location.pathname)) || key;
      direction = localStorage.getItem("afs-file-sort-direction:" + repoScope(location.pathname)) || FILE_SORT_DIRECTIONS[key] || direction;
    } catch (e) {}
    setRepoFileSort(key, direction, false, false);
  }

  function initRepoGraph() {
    var panel = document.querySelector('[data-repo-panel="graph"]');
    var host = panel && panel.querySelector("[data-graph-host]");
    var dataEl = document.getElementById("repo-graph-data");
    if (!panel || !host || !dataEl || host.dataset.ready) return;
    var graph;
    try { graph = JSON.parse(dataEl.textContent || "{}"); } catch (e) { return; }
    var nodes = graph.nodes || [], links = graph.links || [];
    var svg = host.querySelector("svg");
    if (!svg) return;
    if (!nodes.length) {
      host.dataset.ready = "1";
      host.setAttribute("aria-busy", "false");
      panel.classList.add("is-empty");
      svg.setAttribute("tabindex", "-1");
      svg.setAttribute("role", "img");
      panel.querySelectorAll(".graph-toolbar button, .graph-toolbar input").forEach(function (control) { control.disabled = true; });
      return;
    }
    host.dataset.ready = "1";
    host.classList.toggle("is-large-graph", nodes.length > 300);
    host.setAttribute("aria-busy", "true");
    var loading = document.createElement("div");
    loading.className = "graph-loading"; loading.textContent = "Laying out " + nodes.length + (nodes.length === 1 ? " note…" : " notes…");
    host.appendChild(loading);
    requestAnimationFrame(function () { requestAnimationFrame(function () {
    if (!document.body.contains(host)) return;

    var SVG_NS = "http://www.w3.org/2000/svg";
    var nodeByID = Object.create(null), nodeEls = Object.create(null), labelEls = Object.create(null), edgeRecords = [];
    var groups = [], groupIndex = Object.create(null), byGroup = Object.create(null);
    var palette = [
      "hsl(157 55% 40%)", "hsl(203 58% 49%)", "hsl(36 68% 49%)",
      "hsl(273 45% 55%)", "hsl(337 52% 53%)", "hsl(184 52% 42%)",
      "hsl(88 42% 42%)", "hsl(18 62% 52%)"
    ];
    var reducedMotion = false, coarsePointer = false;
    try {
      reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
      coarsePointer = window.matchMedia("(pointer: coarse)").matches;
    } catch (e) {}
    var stateKey = "afs-graph-state:" + repoScope(location.pathname);
    var positionKey = stateKey + ":positions";
    var savedState = {};
    try { savedState = JSON.parse(localStorage.getItem(stateKey) || "{}"); } catch (e) {}
    var savedPositions = {};
    try { savedPositions = JSON.parse(localStorage.getItem(positionKey) || "null") || {}; } catch (e) {}
    var migrateLegacyPositions = false;
    if (!Object.keys(savedPositions).length && savedState.positions && typeof savedState.positions === "object") {
      savedPositions = savedState.positions; migrateLegacyPositions = true;
    }
    var manualPositions = Object.create(null);

    nodes.forEach(function (n, i) {
      n.group = n.group || "root";
      n.incoming = [];
      n.outgoing = [];
      n.neighbors = Object.create(null);
      n.incident = [];
      n.layoutIndex = i;
      n.radius = Math.max(4.2, Math.min(12, 4.2 + Math.sqrt(n.degree || 0) * 0.9));
      nodeByID[String(n.id)] = n;
      if (!byGroup[n.group]) { byGroup[n.group] = []; groups.push(n.group); }
      byGroup[n.group].push(n);
    });
    groups.sort(function (a, b) {
      if (a === "root") return -1;
      if (b === "root") return 1;
      return a.localeCompare(b);
    });
    groups.forEach(function (g, i) { groupIndex[g] = i; });
    nodes.forEach(function (n) { n.color = palette[groupIndex[n.group] % palette.length]; });

    links.forEach(function (l) {
      var source = nodeByID[String(l.source)], target = nodeByID[String(l.target)];
      if (!source || !target) return;
      l.sourceNode = source;
      l.targetNode = target;
      l.count = l.count || 1;
      source.outgoing.push({ node: target, count: l.count });
      target.incoming.push({ node: source, count: l.count });
      source.neighbors[String(target.id)] = true;
      target.neighbors[String(source.id)] = true;
    });

    var box = host.getBoundingClientRect();
    var viewportWidth = Math.max(280, Math.round(box.width || 760));
    var viewportHeight = Math.max(360, Math.round(box.height || 560));
    var density = Math.max(1, Math.min(6, Math.sqrt(nodes.length / 125)));
    var layoutWidth = viewportWidth * density;
    var layoutHeight = viewportHeight * density;
    var cx = layoutWidth / 2, cy = layoutHeight / 2;
    var tau = Math.PI * 2, golden = Math.PI * (3 - Math.sqrt(5));
    var groupCenters = Object.create(null);

    groups.forEach(function (g, gi) {
      var angle = groups.length === 1 ? -Math.PI / 2 : (gi / groups.length) * tau - Math.PI / 2;
      var center = groups.length === 1 ? { x: cx, y: cy } : {
        x: cx + Math.cos(angle) * layoutWidth * 0.29,
        y: cy + Math.sin(angle) * layoutHeight * 0.28
      };
      groupCenters[g] = center;
      var bucket = byGroup[g].sort(function (a, b) { return b.degree - a.degree || a.path.localeCompare(b.path); });
      var spacing = Math.max(24, Math.min(43, Math.sqrt((layoutWidth * layoutHeight) / Math.max(1, nodes.length)) * 0.58));
      bucket.forEach(function (n, i) {
        var radius = i === 0 ? 0 : spacing * Math.sqrt(i);
        var a = i * golden;
        var hubPull = Math.min(0.34, (n.degree || 0) / 35);
        n.x = center.x + Math.cos(a) * radius;
        n.y = center.y + Math.sin(a) * radius;
        n.x = n.x * (1 - hubPull) + cx * hubPull;
        n.y = n.y * (1 - hubPull) + cy * hubPull;
      });
    });

    // Deterministic link springs + grid-based collision. The old layout only
    // pulled linked nodes together, so dense graphs collapsed into a knot.
    var layoutIterations = Math.min(82, 45 + Math.round(Math.sqrt(nodes.length) * 1.7));
    var cellSize = 42;
    for (var iter = 0; iter < layoutIterations; iter++) {
      links.forEach(function (l) {
        var source = l.sourceNode, target = l.targetNode;
        if (!source || !target) return;
        var dx = target.x - source.x, dy = target.y - source.y;
        var dist = Math.sqrt(dx * dx + dy * dy) || 1;
        var wanted = 58 + Math.min(72, Math.sqrt((source.degree || 1) + (target.degree || 1)) * 6);
        var pull = (dist - wanted) * 0.014;
        var mx = dx / dist * pull, my = dy / dist * pull;
        source.x += mx; source.y += my;
        target.x -= mx; target.y -= my;
      });

      var grid = Object.create(null);
      nodes.forEach(function (n) {
        var gx = Math.floor(n.x / cellSize), gy = Math.floor(n.y / cellSize);
        var key = gx + ":" + gy;
        (grid[key] || (grid[key] = [])).push(n);
      });
      nodes.forEach(function (n) {
        var ngx = Math.floor(n.x / cellSize), ngy = Math.floor(n.y / cellSize);
        for (var ox = -1; ox <= 1; ox++) {
          for (var oy = -1; oy <= 1; oy++) {
            var near = grid[(ngx + ox) + ":" + (ngy + oy)] || [];
            near.forEach(function (other) {
              if (other.layoutIndex <= n.layoutIndex) return;
              var dx = other.x - n.x, dy = other.y - n.y;
              var dist = Math.sqrt(dx * dx + dy * dy);
              var minimum = n.radius + other.radius + 11;
              if (dist >= minimum) return;
              if (!dist) { dx = ((n.layoutIndex % 3) - 1) || 0.5; dy = ((other.layoutIndex % 3) - 1) || -0.5; dist = Math.sqrt(dx * dx + dy * dy); }
              var push = (minimum - dist) * 0.28;
              var px = dx / dist * push, py = dy / dist * push;
              n.x -= px; n.y -= py;
              other.x += px; other.y += py;
            });
          }
        }
        var center = groupCenters[n.group];
        n.x += (center.x - n.x) * 0.0035 + (cx - n.x) * 0.0015;
        n.y += (center.y - n.y) * 0.0035 + (cy - n.y) * 0.0015;
        n.x = Math.max(28, Math.min(layoutWidth - 28, n.x));
        n.y = Math.max(28, Math.min(layoutHeight - 28, n.y));
      });
    }

    nodes.forEach(function (n) {
      n.autoX = n.x; n.autoY = n.y;
      if (!Object.prototype.hasOwnProperty.call(savedPositions, n.path)) return;
      var position = savedPositions[n.path];
      if (!Array.isArray(position) || position.length !== 2) return;
      var normalizedX = Number(position[0]), normalizedY = Number(position[1]);
      if (!isFinite(normalizedX) || !isFinite(normalizedY) || normalizedX < -2 || normalizedX > 3 || normalizedY < -2 || normalizedY > 3) return;
      n.x = normalizedX * layoutWidth; n.y = normalizedY * layoutHeight;
      n.manuallyPositioned = true;
      manualPositions[n.path] = [normalizedX, normalizedY];
    });
    if (migrateLegacyPositions && Object.keys(manualPositions).length) {
      try { localStorage.setItem(positionKey, JSON.stringify(manualPositions)); } catch (e) {}
    }

    var degreeCutoff = nodes.length > 120 ? Math.max(2, topDegree(nodes, 0.12)) : 1;
    var maxLabels = nodes.length > 500 ? 24 : (nodes.length > 220 ? 32 : (nodes.length > 90 ? 28 : Math.min(18, Math.max(8, Math.ceil(nodes.length * 0.5)))));
    var labelSet = {};
    nodes.slice().sort(function (a, b) { return b.degree - a.degree || a.path.localeCompare(b.path); }).slice(0, maxLabels).forEach(function (n) {
      labelSet[String(n.id)] = true;
    });

    function svgEl(name) { return document.createElementNS(SVG_NS, name); }
    svg.textContent = "";
    svg.setAttribute("viewBox", "0 0 " + viewportWidth + " " + viewportHeight);
    var cameraLayer = svgEl("g"), edgeLayer = svgEl("g"), nodeLayer = svgEl("g"), labelLayer = svgEl("g");
    cameraLayer.setAttribute("class", "graph-camera");
    edgeLayer.setAttribute("class", "graph-edge-layer");
    nodeLayer.setAttribute("class", "graph-node-layer");
    labelLayer.setAttribute("class", "graph-label-layer");
    edgeLayer.setAttribute("aria-hidden", "true");
    labelLayer.setAttribute("aria-hidden", "true");
    cameraLayer.appendChild(edgeLayer); cameraLayer.appendChild(nodeLayer); cameraLayer.appendChild(labelLayer);
    svg.appendChild(cameraLayer);

    links.forEach(function (l) {
      if (!l.sourceNode || !l.targetNode) return;
      var line = svgEl("line");
      line.setAttribute("x1", l.sourceNode.x.toFixed(1)); line.setAttribute("y1", l.sourceNode.y.toFixed(1));
      line.setAttribute("x2", l.targetNode.x.toFixed(1)); line.setAttribute("y2", l.targetNode.y.toFixed(1));
      line.setAttribute("class", "graph-edge");
      line.style.setProperty("--edge-width", (1 + Math.log(l.count + 1) * 0.42).toFixed(2));
      edgeLayer.appendChild(line);
      var record = { el: line, link: l };
      edgeRecords.push(record);
      l.sourceNode.incident.push(record);
      l.targetNode.incident.push(record);
    });

    nodes.forEach(function (n) {
      var wrap = svgEl("g"), title = svgEl("title"), halo = svgEl("circle"), hit = svgEl("circle"), dot = svgEl("circle");
      wrap.setAttribute("id", "graph-node-" + n.id);
      wrap.setAttribute("class", "graph-node-wrap" + (n.degree >= degreeCutoff ? " hub" : ""));
      wrap.setAttribute("transform", "translate(" + n.x.toFixed(1) + " " + n.y.toFixed(1) + ")");
      wrap.setAttribute("role", "button");
      wrap.setAttribute("aria-label", "Focus and drag " + n.path);
      wrap.setAttribute("aria-pressed", "false");
      wrap.dataset.nodeId = String(n.id);
      wrap.style.setProperty("--node-color", n.color);
      title.textContent = n.path + (n.desc ? " — " + n.desc : "");
      halo.setAttribute("r", (n.radius + 6).toFixed(1)); halo.setAttribute("class", "graph-node-halo");
      hit.setAttribute("r", Math.max(17, n.radius + 10).toFixed(1)); hit.setAttribute("class", "graph-node-hit");
      dot.setAttribute("r", n.radius.toFixed(1)); dot.setAttribute("class", "graph-node");
      wrap.appendChild(title); wrap.appendChild(halo); wrap.appendChild(hit); wrap.appendChild(dot);
      nodeLayer.appendChild(wrap);
      nodeEls[String(n.id)] = wrap;

      var label = svgEl("text");
      var labelLeft = n.x > cx || (Math.abs(n.x - cx) < 70 && n.layoutIndex % 2 === 0);
      n.labelLeft = labelLeft;
      label.setAttribute("x", (n.x + (labelLeft ? -n.radius - 5 : n.radius + 5)).toFixed(1)); label.setAttribute("y", n.y.toFixed(1));
      if (labelLeft) label.setAttribute("text-anchor", "end");
      label.setAttribute("class", "graph-label" + (labelSet[String(n.id)] ? "" : " secondary"));
      label.dataset.labelId = String(n.id);
      label.textContent = n.name.length > 28 ? n.name.slice(0, 26) + "…" : n.name;
      labelLayer.appendChild(label);
      labelEls[String(n.id)] = label;
    });

    var legend = panel.querySelector("[data-graph-legend]");
    if (legend) {
      legend.textContent = "";
      groups.forEach(function (g) {
        var button = document.createElement("button"), dot = document.createElement("span"), text = document.createElement("span");
        button.type = "button";
        button.className = "graph-legend-button";
        button.dataset.graphGroup = g;
        button.setAttribute("aria-pressed", "false");
        button.title = "Filter to " + g;
        button.style.setProperty("--group-color", palette[groupIndex[g] % palette.length]);
        dot.className = "graph-legend-dot"; text.textContent = g;
        button.appendChild(dot); button.appendChild(text); legend.appendChild(button);
      });
    }

    var inspector = panel.querySelector("[data-graph-inspector]");
    var inspectorEmpty = panel.querySelector("[data-graph-inspector-empty]");
    var inspectorSelection = panel.querySelector("[data-graph-inspector-selection]");
    var selectedGroup = panel.querySelector("[data-graph-selected-group]");
    var selectedDegree = panel.querySelector("[data-graph-selected-degree]");
    var selectedName = panel.querySelector("[data-graph-selected-name]");
    var selectedPath = panel.querySelector("[data-graph-selected-path]");
    var selectedDesc = panel.querySelector("[data-graph-selected-desc]");
    var selectedOut = panel.querySelector("[data-graph-selected-out]");
    var selectedIn = panel.querySelector("[data-graph-selected-in]");
    var neighborsEl = panel.querySelector("[data-graph-neighbors]");
    var openNote = panel.querySelector("[data-graph-open-note]");
    var tip = host.querySelector("[data-graph-tip]");
    var search = panel.querySelector("[data-graph-search]");
    var resultCount = panel.querySelector("[data-graph-result-count]");
    var searchResults = panel.querySelector("[data-graph-search-results]");
    var labelsButton = panel.querySelector("[data-graph-labels]");
    var fullscreenButton = panel.querySelector("[data-graph-fullscreen]");
    var resetLayoutButton = panel.querySelector("[data-graph-reset-layout]");
    var focusResetButton = host.querySelector("[data-graph-focus-reset]");
    var focusCount = host.querySelector("[data-graph-focus-count]");
    var graphLive = host.querySelector("[data-graph-live]");
    var graphHelp = host.querySelector(".graph-help");
    var filterMode = /^(all|connected|orphans)$/.test(savedState.filter || "") ? savedState.filter : "all";
    var showLabels = !!savedState.labels;
    var activeGroup = groups.indexOf(savedState.group) >= 0 ? savedState.group : "";
    var initialQuery = "";
    try { initialQuery = new URL(location.href).searchParams.get("q") || savedState.query || ""; } catch (e) { initialQuery = savedState.query || ""; }
    var query = initialQuery.trim().toLowerCase(), currentMatches = [], currentMatchIDs = Object.create(null), resultIndex = 0;
    if (search) search.value = initialQuery;
    var selectedID = null, hoverID = null, keyboardID = null, focusAutoFramed = false;
    var camera = { x: 0, y: 0, k: 1 }, cameraAnimation = 0, cameraFrame = 0, cameraDirty = false;
    var searchFrame = 0, geometryFrame = 0, layoutAnimation = 0;
    var dirtyGeometry = Object.create(null);
    var minZoom = 0.16, maxZoom = 5;

    function clamp(value, min, max) { return Math.max(min, Math.min(max, value)); }
    function applyCamera() {
      cameraLayer.setAttribute("transform", "translate(" + camera.x.toFixed(2) + " " + camera.y.toFixed(2) + ") scale(" + camera.k.toFixed(4) + ")");
      host.classList.toggle("is-zoomed", camera.k >= 1.55);
      if (nodes.length <= 300) {
        host.style.setProperty("--graph-pan-x", camera.x.toFixed(2) + "px");
        host.style.setProperty("--graph-pan-y", camera.y.toFixed(2) + "px");
      }
    }
    function queueCamera() {
      if (cameraFrame) return;
      cameraFrame = requestAnimationFrame(function () { cameraFrame = 0; applyCamera(); });
    }
    function stopCameraAnimation() {
      if (cameraAnimation) cancelAnimationFrame(cameraAnimation);
      cameraAnimation = 0;
      if (cameraFrame) { cancelAnimationFrame(cameraFrame); cameraFrame = 0; applyCamera(); }
    }
    function setCamera(target, animate) {
      target.k = clamp(target.k, minZoom, maxZoom);
      stopCameraAnimation();
      if (!animate || reducedMotion) { camera = target; applyCamera(); return; }
      var start = { x: camera.x, y: camera.y, k: camera.k };
      var started = performance.now();
      function tick(now) {
        var t = clamp((now - started) / 190, 0, 1);
        var eased = 1 - Math.pow(1 - t, 3);
        camera = {
          x: start.x + (target.x - start.x) * eased,
          y: start.y + (target.y - start.y) * eased,
          k: start.k + (target.k - start.k) * eased
        };
        applyCamera();
        if (t < 1) cameraAnimation = requestAnimationFrame(tick);
        else cameraAnimation = 0;
      }
      cameraAnimation = requestAnimationFrame(tick);
    }
    function baseVisible(n) {
      if (filterMode === "connected") return (n.degree || 0) > 0;
      if (filterMode === "orphans") return (n.degree || 0) === 0;
      return true;
    }
    function fitGraph(animate, explicitNodes) {
      var fitNodes = explicitNodes || nodes.filter(function (n) {
        if (!baseVisible(n)) return false;
        if (query && !currentMatchIDs[String(n.id)]) return false;
        if (activeGroup && n.group !== activeGroup) return false;
        return true;
      });
      if (!fitNodes.length) fitNodes = nodes.filter(baseVisible);
      if (!fitNodes.length) return;
      var minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
      fitNodes.forEach(function (n) {
        minX = Math.min(minX, n.x - n.radius); minY = Math.min(minY, n.y - n.radius);
        maxX = Math.max(maxX, n.x + n.radius); maxY = Math.max(maxY, n.y + n.radius);
      });
      var padding = viewportWidth < 520 ? 42 : 58;
      var paddingTop = padding, paddingBottom = padding, paddingLeft = padding, paddingRight = padding;
      if (legend && !legend.hidden) paddingTop = Math.max(paddingTop, legend.getBoundingClientRect().height + 28);
      if (focusResetButton && !focusResetButton.hidden && viewportWidth < 640) {
        paddingBottom = Math.max(paddingBottom, focusResetButton.getBoundingClientRect().height + 62);
      }
      var spanX = Math.max(48, maxX - minX), spanY = Math.max(48, maxY - minY);
      var availableWidth = Math.max(80, viewportWidth - paddingLeft - paddingRight);
      var availableHeight = Math.max(80, viewportHeight - paddingTop - paddingBottom);
      var scale = Math.min(availableWidth / spanX, availableHeight / spanY);
      scale = clamp(scale, minZoom, explicitNodes && explicitNodes.length === 1 ? 1.55 : 1.42);
      setCamera({
        k: scale,
        x: paddingLeft + availableWidth / 2 - (minX + maxX) / 2 * scale,
        y: paddingTop + availableHeight / 2 - (minY + maxY) / 2 * scale
      }, animate);
    }
    function centerNode(n, animate) {
      if (!n) return;
      var scale = Math.max(camera.k, viewportWidth < 520 ? 1.08 : 1.22);
      scale = Math.min(scale, 1.65);
      cameraDirty = true;
      setCamera({ x: viewportWidth / 2 - n.x * scale, y: viewportHeight / 2 - n.y * scale, k: scale }, animate);
    }
    function zoomAt(factor, px, py, animate) {
      var next = clamp(camera.k * factor, minZoom, maxZoom);
      var worldX = (px - camera.x) / camera.k, worldY = (py - camera.y) / camera.k;
      cameraDirty = true;
      setCamera({ x: px - worldX * next, y: py - worldY * next, k: next }, animate);
    }

    function renderNodePosition(n) {
      var id = String(n.id), wrap = nodeEls[id], label = labelEls[id];
      if (!wrap || !label) return;
      wrap.setAttribute("transform", "translate(" + n.x.toFixed(2) + " " + n.y.toFixed(2) + ")");
      n.labelLeft = n.x > cx || (Math.abs(n.x - cx) < 70 && n.layoutIndex % 2 === 0);
      label.setAttribute("x", (n.x + (n.labelLeft ? -n.radius - 5 : n.radius + 5)).toFixed(2));
      label.setAttribute("y", n.y.toFixed(2));
      if (n.labelLeft) label.setAttribute("text-anchor", "end");
      else label.removeAttribute("text-anchor");
      n.incident.forEach(function (record) {
        var l = record.link;
        if (l.sourceNode.id === n.id) {
          record.el.setAttribute("x1", n.x.toFixed(2));
          record.el.setAttribute("y1", n.y.toFixed(2));
        }
        if (l.targetNode.id === n.id) {
          record.el.setAttribute("x2", n.x.toFixed(2));
          record.el.setAttribute("y2", n.y.toFixed(2));
        }
      });
    }
    function flushNodeGeometry() {
      geometryFrame = 0;
      var pending = dirtyGeometry;
      dirtyGeometry = Object.create(null);
      Object.keys(pending).forEach(function (id) { renderNodePosition(pending[id]); });
    }
    function queueNodeGeometry(n) {
      dirtyGeometry[String(n.id)] = n;
      if (!geometryFrame) geometryFrame = requestAnimationFrame(flushNodeGeometry);
    }
    function updateResetLayoutControl() {
      if (!resetLayoutButton) return;
      var hasManualPositions = !!Object.keys(manualPositions).length;
      resetLayoutButton.disabled = !hasManualPositions;
      resetLayoutButton.hidden = !hasManualPositions;
    }
    function rememberNodePosition(n) {
      n.manuallyPositioned = true;
      manualPositions[n.path] = [
        Number((n.x / layoutWidth).toFixed(5)),
        Number((n.y / layoutHeight).toFixed(5))
      ];
      updateResetLayoutControl();
    }
    function saveManualPositions() {
      try {
        if (Object.keys(manualPositions).length) localStorage.setItem(positionKey, JSON.stringify(manualPositions));
        else localStorage.removeItem(positionKey);
      } catch (e) {}
    }
    function announceGraph(message) {
      if (!graphLive) return;
      graphLive.textContent = "";
      requestAnimationFrame(function () { if (document.body.contains(graphLive)) graphLive.textContent = message; });
    }
    function resetManualLayout() {
      var moved = nodes.filter(function (n) { return n.manuallyPositioned; });
      if (!moved.length) return;
      if (layoutAnimation) cancelAnimationFrame(layoutAnimation);
      host.classList.add("is-resetting-layout");
      var starts = moved.map(function (n) { return { node: n, x: n.x, y: n.y }; });
      manualPositions = Object.create(null);
      moved.forEach(function (n) { n.manuallyPositioned = false; });
      updateResetLayoutControl(); saveManualPositions(); saveGraphState();
      function finishReset() {
        starts.forEach(function (entry) {
          entry.node.x = entry.node.autoX; entry.node.y = entry.node.autoY; renderNodePosition(entry.node);
        });
        layoutAnimation = 0; cameraDirty = false; host.classList.remove("is-resetting-layout");
        fitGraph(true, selectedID === null ? null : focusedNodesFor(nodeByID[String(selectedID)]));
        announceGraph("Automatic graph layout restored.");
      }
      if (reducedMotion) { finishReset(); return; }
      var started = performance.now();
      function tickReset(now) {
        var t = clamp((now - started) / 240, 0, 1), eased = 1 - Math.pow(1 - t, 3);
        starts.forEach(function (entry) {
          entry.node.x = entry.x + (entry.node.autoX - entry.x) * eased;
          entry.node.y = entry.y + (entry.node.autoY - entry.y) * eased;
          renderNodePosition(entry.node);
        });
        if (t < 1) layoutAnimation = requestAnimationFrame(tickReset);
        else finishReset();
      }
      layoutAnimation = requestAnimationFrame(tickReset);
    }

    function saveGraphState() {
      var selected = selectedID === null ? null : nodeByID[String(selectedID)];
      try { localStorage.setItem(stateKey, JSON.stringify({
        node: selected ? selected.path : "", filter: filterMode, group: activeGroup, labels: showLabels, query: query
      })); } catch (e) {}
    }
    function syncGraphURL() {
      if (!document.body.contains(host)) return;
      try {
        var stateURL = new URL(location.href);
        stateURL.searchParams.set("view", "graph");
        if (selectedID !== null && nodeByID[String(selectedID)]) stateURL.searchParams.set("node", nodeByID[String(selectedID)].path);
        else stateURL.searchParams.delete("node");
        if (query) stateURL.searchParams.set("q", query);
        else stateURL.searchParams.delete("q");
        history.replaceState(history.state, "", stateURL.href);
      } catch (e) {}
    }
    function addNeighborSection(titleText, entries) {
      if (!neighborsEl || !entries.length) return;
      var section = document.createElement("section"), heading = document.createElement("h3"), list = document.createElement("div");
      section.className = "graph-neighbor-section"; list.className = "graph-neighbor-list";
      heading.textContent = titleText + " · " + entries.length;
      entries.slice().sort(function (a, b) {
        return (b.count - a.count) || (b.node.degree - a.node.degree) || a.node.path.localeCompare(b.node.path);
      }).slice(0, 7).forEach(function (entry) {
        var button = document.createElement("button"), text = document.createElement("span");
        button.type = "button"; button.className = "graph-neighbor";
        button.dataset.inspectNode = String(entry.node.id);
        button.style.setProperty("--neighbor-color", entry.node.color);
        text.textContent = entry.node.name; button.appendChild(text); list.appendChild(button);
      });
      section.appendChild(heading); section.appendChild(list); neighborsEl.appendChild(section);
    }
    function renderInspector(n) {
      if (!inspectorEmpty || !inspectorSelection) return;
      inspectorEmpty.hidden = !!n; inspectorSelection.hidden = !n;
      if (!n) return;
      selectedGroup.textContent = n.group;
      selectedGroup.style.setProperty("--group-color", n.color);
      selectedDegree.textContent = (n.degree || 0) + ((n.degree || 0) === 1 ? " reference" : " references");
      selectedName.textContent = n.name; selectedPath.textContent = n.path;
      selectedDesc.textContent = n.desc || "No description yet.";
      selectedOut.textContent = String(n.outgoing.length);
      selectedIn.textContent = String(n.incoming.length);
      neighborsEl.textContent = "";
      addNeighborSection("Links to", n.outgoing);
      addNeighborSection("Linked from", n.incoming);
      if (!n.outgoing.length && !n.incoming.length) {
        var none = document.createElement("p");
        none.className = "graph-no-neighbors"; none.textContent = "This note is not connected yet."; neighborsEl.appendChild(none);
      }
      openNote.href = n.href;
    }
    function focusedNodesFor(n) {
      if (!n) return [];
      var focused = [n];
      Object.keys(n.neighbors).forEach(function (id) {
        var neighbor = nodeByID[id];
        if (neighbor && baseVisible(neighbor)) focused.push(neighbor);
      });
      return focused;
    }
    function neighborhoodNeedsFrame(focusedNodes) {
      if (!focusedNodes.length) return false;
      var minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
      focusedNodes.forEach(function (n) {
        var x = n.x * camera.k + camera.x, y = n.y * camera.k + camera.y;
        minX = Math.min(minX, x); minY = Math.min(minY, y); maxX = Math.max(maxX, x); maxY = Math.max(maxY, y);
      });
      var padding = viewportWidth < 520 ? 38 : 54;
      var clipped = minX < padding || minY < padding || maxX > viewportWidth - padding || maxY > viewportHeight - padding;
      var span = Math.max(maxX - minX, maxY - minY);
      var tooSmall = camera.k < 0.58 || (focusedNodes.length > 1 && span < Math.min(viewportWidth, viewportHeight) * 0.3);
      return clipped || tooSmall;
    }
    var reflectedFocusIDs = Object.create(null), reflectedFocusEdges = [];
    var reflectedSelectedID = null, reflectedKeyboardID = null;
    function updateFocus() {
      var focusID = selectedID !== null ? selectedID : hoverID;
      var focused = focusID !== null && !!nodeByID[String(focusID)];
      var focusNode = focused ? nodeByID[String(focusID)] : null;
      Object.keys(reflectedFocusIDs).forEach(function (id) {
        nodeEls[id].classList.remove("active"); labelEls[id].classList.remove("active");
      });
      reflectedFocusEdges.forEach(function (record) { record.el.classList.remove("active", "outbound", "inbound"); });
      reflectedFocusIDs = Object.create(null); reflectedFocusEdges = [];
      host.classList.toggle("is-focused", focused);
      host.classList.toggle("is-node-focused", selectedID !== null);
      if (focusResetButton) focusResetButton.hidden = selectedID === null;
      if (graphHelp) graphHelp.textContent = selectedID === null
        ? (coarsePointer ? "Tap to focus · Drag a note to move · Pinch to zoom" : "Click to focus · Drag a note to move · Drag empty space to pan")
        : (coarsePointer ? "Drag notes to rearrange · Open from the inspector · Tap Show all to reset" : "Drag notes to rearrange · Double-click to open · Esc to show all");
      if (!focused) return;
      reflectedFocusIDs[String(focusNode.id)] = true;
      Object.keys(focusNode.neighbors).forEach(function (id) { reflectedFocusIDs[id] = true; });
      Object.keys(reflectedFocusIDs).forEach(function (id) {
        nodeEls[id].classList.add("active"); labelEls[id].classList.add("active");
      });
      focusNode.incident.forEach(function (record) {
        var l = record.link;
        record.el.classList.add("active");
        if (l.sourceNode.id === focusNode.id) record.el.classList.add("outbound");
        if (l.targetNode.id === focusNode.id) record.el.classList.add("inbound");
        reflectedFocusEdges.push(record);
      });
      if (selectedID !== null && focusResetButton) {
        var focusedCount = Object.keys(reflectedFocusIDs).length;
        if (focusCount) focusCount.textContent = focusedCount + (focusedCount === 1 ? " note in focus" : " notes in focus");
        focusResetButton.setAttribute("aria-label", "Clear focused note and show all notes");
      }
    }
    function updateSelectedNode() {
      if (reflectedSelectedID !== null && nodeEls[String(reflectedSelectedID)]) {
        nodeEls[String(reflectedSelectedID)].classList.remove("selected");
        nodeEls[String(reflectedSelectedID)].setAttribute("aria-pressed", "false");
        labelEls[String(reflectedSelectedID)].classList.remove("selected");
      }
      reflectedSelectedID = selectedID;
      if (reflectedSelectedID !== null && nodeEls[String(reflectedSelectedID)]) {
        nodeEls[String(reflectedSelectedID)].classList.add("selected");
        nodeEls[String(reflectedSelectedID)].setAttribute("aria-pressed", "true");
        labelEls[String(reflectedSelectedID)].classList.add("selected");
      }
    }
    function selectNode(id, center, sync) {
      var n = nodeByID[String(id)];
      if (!n) return;
      if (query && !currentMatchIDs[String(n.id)]) {
        query = "";
        if (search) search.value = "";
        updateFilters(false); hideSearchResults();
      }
      var sameSelection = selectedID === n.id, wasAutoFramed = focusAutoFramed;
      selectedID = n.id; keyboardID = n.id;
      updateKeyboardNode(); updateSelectedNode(); updateFocus(); renderInspector(n);
      var focusedNodes = focusedNodesFor(n);
      if (center === true) focusAutoFramed = true;
      else if (center === "auto") focusAutoFramed = wasAutoFramed || neighborhoodNeedsFrame(focusedNodes);
      else focusAutoFramed = sameSelection ? wasAutoFramed : false;
      if (focusAutoFramed) fitGraph(true, focusedNodes);
      saveGraphState();
      if (sync !== false) syncGraphURL();
      announceGraph("Focused " + n.name + ", " + Object.keys(n.neighbors).length + " connected " + (Object.keys(n.neighbors).length === 1 ? "note." : "notes."));
    }
    function clearSelection(sync) {
      selectedID = null; focusAutoFramed = false; updateSelectedNode(); renderInspector(null); updateFocus(); saveGraphState();
      if (sync !== false) syncGraphURL();
      announceGraph("Showing all notes.");
    }
    function updateKeyboardNode() {
      if (reflectedKeyboardID !== null && nodeEls[String(reflectedKeyboardID)]) nodeEls[String(reflectedKeyboardID)].classList.remove("keyboard-active");
      reflectedKeyboardID = document.activeElement === svg ? keyboardID : null;
      if (reflectedKeyboardID !== null && nodeEls[String(reflectedKeyboardID)]) nodeEls[String(reflectedKeyboardID)].classList.add("keyboard-active");
      if (keyboardID !== null) svg.setAttribute("aria-activedescendant", "graph-node-" + keyboardID);
      else svg.removeAttribute("aria-activedescendant");
    }
    function isNavigable(n) {
      if (!n || !baseVisible(n)) return false;
      if (selectedID !== null) {
        var selected = nodeByID[String(selectedID)];
        var inFocusedNeighborhood = selected && (n.id === selected.id || !!selected.neighbors[String(n.id)]);
        if (inFocusedNeighborhood) return true;
      }
      if (activeGroup && n.group !== activeGroup) return false;
      if (query && !currentMatchIDs[String(n.id)]) return false;
      return true;
    }
    function nearestInDirection(from, dx, dy) {
      var best = null, bestScore = Infinity;
      nodes.forEach(function (n) {
        if (n.id === from.id || !isNavigable(n)) return;
        var vx = n.x - from.x, vy = n.y - from.y;
        var forward = vx * dx + vy * dy;
        if (forward <= 0) return;
        var perpendicular = Math.abs(vx * dy - vy * dx);
        var distance = Math.sqrt(vx * vx + vy * vy);
        var score = perpendicular * 2.4 + distance * 0.35;
        if (score < bestScore) { bestScore = score; best = n; }
      });
      return best;
    }
    function moveKeyboard(dx, dy) {
      var current = keyboardID === null ? null : nodeByID[String(keyboardID)];
      if (!isNavigable(current)) current = nodes.filter(isNavigable).sort(function (a, b) { return b.degree - a.degree; })[0];
      if (!current) return;
      var next = nearestInDirection(current, dx, dy) || current;
      keyboardID = next.id; hoverID = next.id; updateKeyboardNode(); updateFocus(); centerNode(next, true);
    }
    function nudgeKeyboardNode(dx, dy) {
      var n = selectedID === null ? nodeByID[String(keyboardID)] : nodeByID[String(selectedID)];
      if (!n || !isNavigable(n)) return;
      if (selectedID !== n.id) selectNode(n.id, false, true);
      var amount = 12 / Math.max(camera.k, 0.2);
      n.x += dx * amount; n.y += dy * amount;
      cameraDirty = true; renderNodePosition(n); rememberNodePosition(n); saveManualPositions(); saveGraphState();
      announceGraph("Moved " + n.name + ". Its position is saved in this browser.");
    }

    function textMatches(n) {
      return !query || (n.path + " " + (n.desc || "")).toLowerCase().indexOf(query) !== -1;
    }
    function hideSearchResults() {
      if (!searchResults || !search) return;
      searchResults.hidden = true; search.setAttribute("aria-expanded", "false"); search.removeAttribute("aria-activedescendant");
    }
    function markActiveResult() {
      if (!searchResults || !search) return;
      var buttons = searchResults.querySelectorAll("[data-search-node]");
      if (!buttons.length) return;
      resultIndex = (resultIndex + buttons.length) % buttons.length;
      buttons.forEach(function (button, i) {
        var active = i === resultIndex;
        button.classList.toggle("active", active); button.setAttribute("aria-selected", active ? "true" : "false");
        if (active) search.setAttribute("aria-activedescendant", button.id);
      });
    }
    function renderSearchResults(open) {
      if (!searchResults || !search) return;
      searchResults.textContent = "";
      if (!open || !query) { hideSearchResults(); return; }
      if (!currentMatches.length) {
        var empty = document.createElement("div");
        empty.className = "graph-result-empty"; empty.textContent = "No notes match “" + search.value.trim() + "”.";
        searchResults.appendChild(empty); searchResults.hidden = false; search.setAttribute("aria-expanded", "true"); return;
      }
      currentMatches.slice(0, 7).forEach(function (n, i) {
        var button = document.createElement("button"), main = document.createElement("span"), name = document.createElement("span"), path = document.createElement("span"), degree = document.createElement("span");
        button.type = "button"; button.className = "graph-result"; button.id = "graph-result-" + n.id;
        button.tabIndex = -1;
        button.dataset.searchNode = String(n.id); button.setAttribute("role", "option"); button.setAttribute("aria-selected", "false");
        main.className = "graph-result-main"; name.className = "graph-result-name"; path.className = "graph-result-path"; degree.className = "graph-result-degree";
        name.textContent = n.name; path.textContent = n.path; degree.textContent = (n.degree || 0) + " refs";
        main.appendChild(name); main.appendChild(path); button.appendChild(main); button.appendChild(degree); searchResults.appendChild(button);
      });
      searchResults.hidden = false; search.setAttribute("aria-expanded", "true"); resultIndex = 0; markActiveResult();
    }
    function updateFilters(openResults) {
      currentMatches = []; currentMatchIDs = Object.create(null);
      var visibleCount = 0, scopedCount = 0;
      nodes.forEach(function (n) {
        var visible = baseVisible(n), inGroup = !activeGroup || n.group === activeGroup;
        var match = visible && inGroup && textMatches(n);
        if (visible) visibleCount++;
        if (visible && inGroup) scopedCount++;
        if (query && match) { currentMatches.push(n); currentMatchIDs[String(n.id)] = true; }
        nodeEls[String(n.id)].classList.toggle("is-hidden", !visible);
        labelEls[String(n.id)].classList.toggle("is-hidden", !visible);
        nodeEls[String(n.id)].classList.toggle("match", !!query && match);
        labelEls[String(n.id)].classList.toggle("match", !!query && match);
        nodeEls[String(n.id)].classList.toggle("group-match", !!activeGroup && inGroup);
        labelEls[String(n.id)].classList.toggle("group-match", !!activeGroup && inGroup);
      });
      edgeRecords.forEach(function (record) {
        var l = record.link;
        var visible = baseVisible(l.sourceNode) && baseVisible(l.targetNode);
        var match = !!query && (currentMatchIDs[String(l.sourceNode.id)] || currentMatchIDs[String(l.targetNode.id)]);
        var groupMatch = !!activeGroup && (l.sourceNode.group === activeGroup || l.targetNode.group === activeGroup);
        record.el.classList.toggle("is-hidden", !visible); record.el.classList.toggle("match", match); record.el.classList.toggle("group-match", groupMatch);
      });
      host.classList.toggle("is-searching", !!query);
      host.classList.toggle("is-group-filtering", !!activeGroup);
      host.classList.toggle("show-labels", showLabels);
      if (resultCount) {
        if (query) resultCount.textContent = currentMatches.length + " of " + scopedCount;
        else if (activeGroup) resultCount.textContent = scopedCount + " of " + visibleCount;
        else resultCount.textContent = visibleCount + (visibleCount === 1 ? " note" : " notes");
      }
      panel.querySelectorAll("[data-graph-filter]").forEach(function (button) {
        var active = button.dataset.graphFilter === filterMode;
        button.classList.toggle("active", active); button.setAttribute("aria-pressed", active ? "true" : "false");
      });
      if (labelsButton) {
        labelsButton.classList.toggle("active", showLabels);
        labelsButton.setAttribute("aria-pressed", showLabels ? "true" : "false");
        labelsButton.setAttribute("aria-label", showLabels ? "Show priority labels" : "Show all labels");
      }
      if (legend) legend.querySelectorAll("[data-graph-group]").forEach(function (button) {
        button.setAttribute("aria-pressed", button.dataset.graphGroup === activeGroup ? "true" : "false");
      });
      currentMatches.sort(function (a, b) {
        function nameRank(n) {
          var name = n.name.toLowerCase();
          if (name === query) return 3;
          if (name.indexOf(query) === 0) return 2;
          if (name.indexOf(query) !== -1) return 1;
          return 0;
        }
        var an = nameRank(a), bn = nameRank(b);
        return bn - an || b.degree - a.degree || a.path.localeCompare(b.path);
      });
      if (keyboardID !== null && !isNavigable(nodeByID[String(keyboardID)])) {
        var nextKeyboard = (query ? currentMatches : nodes.filter(isNavigable).sort(function (a, b) { return b.degree - a.degree; }))[0];
        keyboardID = nextKeyboard ? nextKeyboard.id : null;
        updateKeyboardNode();
      }
      if (hoverID !== null && !baseVisible(nodeByID[String(hoverID)])) { hoverID = null; hideTooltip(); updateFocus(); }
      renderSearchResults(openResults);
      if (selectedID !== null) {
        var selectedForFilter = nodeByID[String(selectedID)];
        if (!baseVisible(selectedForFilter) || (activeGroup && selectedForFilter.group !== activeGroup)) clearSelection(true);
      }
    }

    function showTooltip(n, evt) {
      if (!tip || !n || (evt && evt.pointerType === "touch")) return;
      if (!tip.firstChild) tip.innerHTML = "<b></b><span></span>";
      tip.querySelector("b").textContent = n.path;
      tip.querySelector("span").textContent = n.desc || ((n.degree || 0) + " references");
      positionTooltip(n, evt); tip.classList.add("visible");
    }
    function positionTooltip(n, evt) {
      if (!tip) return;
      var hostBox = host.getBoundingClientRect(), px, py;
      if (evt) { px = evt.clientX - hostBox.left + 14; py = evt.clientY - hostBox.top + 14; }
      else {
        var nodeBox = nodeEls[String(n.id)].getBoundingClientRect();
        px = nodeBox.right - hostBox.left + 10; py = nodeBox.top - hostBox.top + 6;
      }
      var tipWidth = Math.min(320, Math.max(180, hostBox.width - 28));
      tip.style.transform = "translate(" + Math.max(8, Math.min(px, hostBox.width - tipWidth - 8)) + "px," + Math.max(8, Math.min(py, hostBox.height - 90)) + "px)";
    }
    function hideTooltip() {
      if (!tip) return;
      tip.classList.remove("visible"); tip.style.transform = "translate(-999px,-999px)";
    }

    function localPoint(evt, knownBox) {
      var svgBox = knownBox || svg.getBoundingClientRect();
      return {
        x: (evt.clientX - svgBox.left) * viewportWidth / Math.max(1, svgBox.width),
        y: (evt.clientY - svgBox.top) * viewportHeight / Math.max(1, svgBox.height)
      };
    }
    function worldPoint(point) {
      return { x: (point.x - camera.x) / camera.k, y: (point.y - camera.y) / camera.k };
    }
    function nodeAtPoint(point) {
      var closest = null, closestDistance = Infinity;
      nodes.forEach(function (n) {
        if (!isNavigable(n)) return;
        var dx = n.x * camera.k + camera.x - point.x;
        var dy = n.y * camera.k + camera.y - point.y;
        var distance = Math.sqrt(dx * dx + dy * dy);
        var hitRadius = Math.max(18, Math.min(26, n.radius * camera.k + 8));
        if (distance <= hitRadius && distance < closestDistance) { closest = n; closestDistance = distance; }
      });
      return closest;
    }
    var pointers = Object.create(null), dragState = null, pinchState = null, suppressOpenUntil = 0, lastNodeClick = null;
    function pointerValues() { return Object.keys(pointers).map(function (key) { return pointers[key]; }); }
    function finishNodeDrag(state, cancelled) {
      if (!state || state.kind !== "node" || !state.dragging) return;
      var n = nodeByID[String(state.nodeID)];
      host.classList.remove("is-dragging-node");
      if (n && nodeEls[String(n.id)]) nodeEls[String(n.id)].classList.remove("dragging");
      state.dragging = false;
      if (!n) return;
      if (cancelled) {
        n.x = state.startNodeX; n.y = state.startNodeY;
        renderNodePosition(n);
        return;
      }
      renderNodePosition(n);
      rememberNodePosition(n); saveManualPositions(); saveGraphState();
      suppressOpenUntil = performance.now() + 700;
      announceGraph("Moved " + n.name + ". Its position is saved in this browser.");
    }
    function startPinch() {
      var values = pointerValues();
      if (values.length < 2) { pinchState = null; return; }
      if (dragState && dragState.kind === "node" && dragState.dragging) finishNodeDrag(dragState, false);
      if (dragState) { dragState.kind = "pinch"; dragState.nodeID = null; dragState.moved = true; }
      var a = values[0], b = values[1], mid = { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
      var distance = Math.sqrt(Math.pow(a.x - b.x, 2) + Math.pow(a.y - b.y, 2)) || 1;
      pinchState = { distance: distance, k: camera.k, worldX: (mid.x - camera.x) / camera.k, worldY: (mid.y - camera.y) / camera.k };
    }
    function finishPointer(evt, cancelled) {
      var wasDrag = dragState && dragState.pointerID === evt.pointerId ? dragState : null;
      delete pointers[String(evt.pointerId)];
      try { svg.releasePointerCapture(evt.pointerId); } catch (e) {}
      if (wasDrag && wasDrag.kind === "node" && wasDrag.dragging) finishNodeDrag(wasDrag, cancelled);
      var remaining = pointerValues();
      if (remaining.length === 1) {
        pinchState = null;
        dragState = {
          pointerID: remaining[0].id, pointerType: remaining[0].pointerType, kind: "pan",
          startX: remaining[0].x, startY: remaining[0].y, startClientX: remaining[0].clientX, startClientY: remaining[0].clientY,
          cameraX: camera.x, cameraY: camera.y, nodeID: null, moved: true, svgBox: remaining[0].svgBox
        };
        return;
      }
      if (!remaining.length) {
        host.classList.remove("is-panning", "is-dragging-node"); pinchState = null; dragState = null;
        if (wasDrag && wasDrag.moved) lastNodeClick = null;
        if (!cancelled && wasDrag && wasDrag.moved && wasDrag.kind !== "node") suppressOpenUntil = performance.now() + 700;
        if (!cancelled && wasDrag && wasDrag.clickEligible !== false && !wasDrag.moved) {
          var hitNode = wasDrag.nodeID !== null ? nodeByID[String(wasDrag.nodeID)] : nodeAtPoint({ x: wasDrag.startX, y: wasDrag.startY });
          var clickNow = performance.now();
          var repeatedNodeClick = lastNodeClick && clickNow - lastNodeClick.time < 700 &&
            Math.abs(wasDrag.startClientX - lastNodeClick.x) < 14 && Math.abs(wasDrag.startClientY - lastNodeClick.y) < 14 &&
            (!hitNode || hitNode.id === lastNodeClick.id);
          if (repeatedNodeClick) {
            var doubleClickedNode = nodeByID[String(lastNodeClick.id)];
            lastNodeClick = null; suppressOpenUntil = clickNow + 700;
            openGraphNode(doubleClickedNode); return;
          }
          if (hitNode && isNavigable(hitNode)) {
            lastNodeClick = { id: hitNode.id, time: clickNow, x: wasDrag.startClientX, y: wasDrag.startClientY };
            selectNode(hitNode.id, "auto", true);
          }
          else {
            lastNodeClick = null;
            var restoreFullGraph = focusAutoFramed;
            hoverID = null; clearSelection(true);
            if (restoreFullGraph) { cameraDirty = false; fitGraph(true); }
          }
        }
      }
    }
    svg.addEventListener("pointerdown", function (evt) {
      if (layoutAnimation) return;
      if (evt.button !== 0 && evt.button !== 1) return;
      var svgBox = svg.getBoundingClientRect(), point = localPoint(evt, svgBox), node = evt.target.closest("[data-node-id]");
      stopCameraAnimation();
      var directNode = node ? nodeByID[node.dataset.nodeId] : nodeAtPoint(point);
      var forcePan = evt.button === 1;
      if (!directNode || !isNavigable(directNode) || forcePan) directNode = null;
      var world = worldPoint(point);
      pointers[String(evt.pointerId)] = {
        id: evt.pointerId, pointerType: evt.pointerType, x: point.x, y: point.y,
        clientX: evt.clientX, clientY: evt.clientY, svgBox: svgBox
      };
      try { svg.setPointerCapture(evt.pointerId); } catch (e) {}
      if (pointerValues().length === 1) {
        dragState = {
          pointerID: evt.pointerId, pointerType: evt.pointerType, kind: directNode ? "node" : "pan",
          startX: point.x, startY: point.y, startClientX: evt.clientX, startClientY: evt.clientY,
          cameraX: camera.x, cameraY: camera.y, nodeID: directNode ? directNode.id : null,
          startNodeX: directNode ? directNode.x : 0, startNodeY: directNode ? directNode.y : 0,
          grabOffsetX: directNode ? world.x - directNode.x : 0, grabOffsetY: directNode ? world.y - directNode.y : 0,
          moved: false, dragging: false, clickEligible: evt.button === 0, svgBox: svgBox
        };
      } else startPinch();
      if (evt.pointerType !== "touch" || directNode) evt.preventDefault();
    });
    svg.addEventListener("pointermove", function (evt) {
      var stored = pointers[String(evt.pointerId)];
      if (stored) {
        var point = localPoint(evt, stored.svgBox); stored.x = point.x; stored.y = point.y; stored.clientX = evt.clientX; stored.clientY = evt.clientY;
        var values = pointerValues();
        if (values.length >= 2 && pinchState) {
          var a = values[0], b = values[1], mid = { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
          var distance = Math.sqrt(Math.pow(a.x - b.x, 2) + Math.pow(a.y - b.y, 2)) || 1;
          var scale = clamp(pinchState.k * distance / pinchState.distance, minZoom, maxZoom);
          camera = { x: mid.x - pinchState.worldX * scale, y: mid.y - pinchState.worldY * scale, k: scale };
          cameraDirty = true; host.classList.add("is-panning"); queueCamera();
        } else if (dragState && dragState.pointerID === evt.pointerId) {
          var dx = point.x - dragState.startX, dy = point.y - dragState.startY;
          var clientDistance = Math.sqrt(Math.pow(evt.clientX - dragState.startClientX, 2) + Math.pow(evt.clientY - dragState.startClientY, 2));
          var threshold = dragState.pointerType === "touch" ? 8 : (dragState.pointerType === "pen" ? 6 : 4);
          if (dragState.kind === "pan" && evt.pointerType === "touch" && Math.abs(dy) > Math.abs(dx) && !dragState.moved) return;
          if (clientDistance > threshold) dragState.moved = true;
          if (dragState.moved) {
            if (dragState.kind === "node") {
              var movedNode = nodeByID[String(dragState.nodeID)];
              if (!movedNode) return;
              if (!dragState.dragging) {
                dragState.dragging = true;
                host.classList.add("is-dragging-node");
                nodeEls[String(movedNode.id)].classList.add("dragging");
                nodeLayer.appendChild(nodeEls[String(movedNode.id)]);
                labelLayer.appendChild(labelEls[String(movedNode.id)]);
                if (selectedID !== movedNode.id) selectNode(movedNode.id, false, true);
              }
              var movedWorld = worldPoint(point);
              movedNode.x = movedWorld.x - dragState.grabOffsetX;
              movedNode.y = movedWorld.y - dragState.grabOffsetY;
              cameraDirty = true; queueNodeGeometry(movedNode); hideTooltip();
            } else {
              camera = { x: dragState.cameraX + dx, y: dragState.cameraY + dy, k: camera.k };
              cameraDirty = true; host.classList.add("is-panning"); queueCamera(); hideTooltip();
            }
          }
        }
      } else if (evt.pointerType !== "touch" && !host.classList.contains("is-panning") && !host.classList.contains("is-dragging-node")) {
        var hoveredElement = evt.target.closest("[data-node-id]");
        var hoveredNode = hoveredElement ? nodeByID[hoveredElement.dataset.nodeId] : (camera.k < 0.8 ? nodeAtPoint(localPoint(evt)) : null);
        if (hoveredNode && isNavigable(hoveredNode)) {
          if (hoverID !== hoveredNode.id) { hoverID = hoveredNode.id; updateFocus(); }
          showTooltip(hoveredNode, evt);
        } else if (hoverID !== null) {
          hoverID = null; updateFocus(); hideTooltip();
        }
      }
    });
    svg.addEventListener("pointerup", function (evt) { finishPointer(evt, false); });
    svg.addEventListener("pointercancel", function (evt) { finishPointer(evt, true); });
    svg.addEventListener("lostpointercapture", function (evt) {
      if (pointers[String(evt.pointerId)]) finishPointer(evt, true);
    });
    svg.addEventListener("wheel", function (evt) {
      if (!evt.ctrlKey && !evt.metaKey) return;
      evt.preventDefault();
      var delta = evt.deltaY;
      if (evt.deltaMode === 1) delta *= 16;
      else if (evt.deltaMode === 2) delta *= viewportHeight;
      var point = localPoint(evt), factor = Math.exp(-delta * 0.0014);
      zoomAt(factor, point.x, point.y, false);
    }, { passive: false });
    svg.addEventListener("pointerover", function (evt) {
      var node = evt.target.closest("[data-node-id]");
      if (!node || host.classList.contains("is-panning") || host.classList.contains("is-dragging-node")) return;
      hoverID = Number(node.dataset.nodeId); updateFocus(); showTooltip(nodeByID[String(hoverID)], evt);
    });
    svg.addEventListener("pointerout", function (evt) {
      var from = evt.target.closest("[data-node-id]");
      var to = evt.relatedTarget && evt.relatedTarget.closest ? evt.relatedTarget.closest("[data-node-id]") : null;
      if (!from || (to && to.dataset.nodeId === from.dataset.nodeId)) return;
      hoverID = null; updateFocus(); hideTooltip();
    });
    function openGraphNode(n) {
      if (!n || !n.href) return;
      var href = new URL(n.href, location.href).href;
      if (page && repoScope(new URL(href).pathname) === repoScope(location.pathname)) loadPage(href, true);
      else window.location.href = href;
    }
    svg.addEventListener("dblclick", function (evt) {
      if (performance.now() < suppressOpenUntil) { evt.preventDefault(); return; }
      var node = evt.target.closest("[data-node-id]");
      var n = node ? nodeByID[node.dataset.nodeId] : nodeAtPoint(localPoint(evt));
      if (!n) {
        fitGraph(true, selectedID === null ? null : focusedNodesFor(nodeByID[String(selectedID)]));
        return;
      }
      evt.preventDefault();
      openGraphNode(n);
    });
    svg.addEventListener("focus", function () {
      if (keyboardID === null) {
        var firstNavigable = nodes.filter(isNavigable).sort(function (a, b) { return b.degree - a.degree; })[0];
        keyboardID = selectedID !== null && isNavigable(nodeByID[String(selectedID)]) ? selectedID : (firstNavigable ? firstNavigable.id : null);
      }
      updateKeyboardNode();
    });
    svg.addEventListener("blur", function () {
      hoverID = null; hideTooltip(); updateKeyboardNode(); updateFocus();
    });
    svg.addEventListener("keydown", function (evt) {
      var handled = true;
      if (evt.altKey && evt.key === "ArrowRight") nudgeKeyboardNode(1, 0);
      else if (evt.altKey && evt.key === "ArrowLeft") nudgeKeyboardNode(-1, 0);
      else if (evt.altKey && evt.key === "ArrowDown") nudgeKeyboardNode(0, 1);
      else if (evt.altKey && evt.key === "ArrowUp") nudgeKeyboardNode(0, -1);
      else if (evt.key === "ArrowRight") moveKeyboard(1, 0);
      else if (evt.key === "ArrowLeft") moveKeyboard(-1, 0);
      else if (evt.key === "ArrowDown") moveKeyboard(0, 1);
      else if (evt.key === "ArrowUp") moveKeyboard(0, -1);
      else if (evt.key === "Enter" && evt.shiftKey && keyboardID !== null) openGraphNode(nodeByID[String(keyboardID)]);
      else if ((evt.key === "Enter" || evt.key === " ") && keyboardID !== null) selectNode(keyboardID, "auto", true);
      else if (evt.key === "Escape") {
        var restoreGraph = focusAutoFramed;
        hoverID = null; clearSelection(true); updateFocus();
        if (restoreGraph) { cameraDirty = false; fitGraph(true); }
      }
      else if (evt.key === "+" || evt.key === "=") zoomAt(1.25, viewportWidth / 2, viewportHeight / 2, true);
      else if (evt.key === "-") zoomAt(0.8, viewportWidth / 2, viewportHeight / 2, true);
      else if (evt.key === "0") fitGraph(true, selectedID === null ? null : focusedNodesFor(nodeByID[String(selectedID)]));
      else handled = false;
      if (handled) evt.preventDefault();
    });

    if (search) {
      search.addEventListener("input", function () {
        var nextQuery = search.value.trim().toLowerCase();
        if (nextQuery !== query && selectedID !== null) { query = nextQuery; hoverID = null; clearSelection(false); }
        else query = nextQuery;
        resultIndex = 0;
        if (searchFrame) cancelAnimationFrame(searchFrame);
        searchFrame = requestAnimationFrame(function () {
          searchFrame = 0; updateFilters(true); saveGraphState(); syncGraphURL();
        });
      });
      search.addEventListener("focus", function () { if (query) renderSearchResults(true); });
      search.addEventListener("blur", function () { setTimeout(hideSearchResults, 140); });
      search.addEventListener("keydown", function (evt) {
        if (searchFrame) { cancelAnimationFrame(searchFrame); searchFrame = 0; updateFilters(true); }
        if ((evt.key === "ArrowDown" || evt.key === "ArrowUp") && currentMatches.length) {
          resultIndex += evt.key === "ArrowDown" ? 1 : -1; markActiveResult(); evt.preventDefault();
        } else if (evt.key === "Enter" && currentMatches.length) {
          var chosen = currentMatches[Math.min(resultIndex, currentMatches.length - 1)];
          selectNode(chosen.id, true, true); hideSearchResults(); evt.preventDefault();
        } else if (evt.key === "Escape") {
          if (search.value) { search.value = ""; query = ""; updateFilters(false); saveGraphState(); syncGraphURL(); }
          else hideSearchResults();
          evt.preventDefault();
        }
      });
    }
    if (searchResults) {
      searchResults.addEventListener("mousedown", function (evt) {
        if (evt.target.closest("[data-search-node]")) evt.preventDefault();
      });
      searchResults.addEventListener("click", function (evt) {
        var button = evt.target.closest("[data-search-node]");
        if (!button) return;
        selectNode(Number(button.dataset.searchNode), true, true); hideSearchResults();
      });
    }
    panel.querySelectorAll("[data-graph-filter]").forEach(function (button) {
      button.addEventListener("click", function () {
        filterMode = button.dataset.graphFilter; updateFilters(false); saveGraphState(); cameraDirty = false;
        fitGraph(true, selectedID === null ? null : focusedNodesFor(nodeByID[String(selectedID)]));
      });
    });
    if (labelsButton) labelsButton.addEventListener("click", function () {
      showLabels = !showLabels; updateFilters(false); saveGraphState();
    });
    if (legend) legend.addEventListener("click", function (evt) {
      var button = evt.target.closest("[data-graph-group]");
      if (!button) return;
      activeGroup = activeGroup === button.dataset.graphGroup ? "" : button.dataset.graphGroup;
      updateFilters(false); saveGraphState(); cameraDirty = false;
      fitGraph(true, selectedID === null ? null : focusedNodesFor(nodeByID[String(selectedID)]));
    });
    if (neighborsEl) neighborsEl.addEventListener("click", function (evt) {
      var button = evt.target.closest("[data-inspect-node]");
      if (button) selectNode(Number(button.dataset.inspectNode), true, true);
    });
    var zoomIn = panel.querySelector("[data-graph-zoom-in]");
    var zoomOut = panel.querySelector("[data-graph-zoom-out]");
    var fitButton = panel.querySelector("[data-graph-fit]");
    if (zoomIn) zoomIn.addEventListener("click", function () { zoomAt(1.25, viewportWidth / 2, viewportHeight / 2, true); });
    if (zoomOut) zoomOut.addEventListener("click", function () { zoomAt(0.8, viewportWidth / 2, viewportHeight / 2, true); });
    if (fitButton) fitButton.addEventListener("click", function () {
      cameraDirty = false;
      fitGraph(true, selectedID === null ? null : focusedNodesFor(nodeByID[String(selectedID)]));
    });
    if (resetLayoutButton) resetLayoutButton.addEventListener("click", resetManualLayout);
    if (focusResetButton) focusResetButton.addEventListener("click", function () {
      hoverID = null; clearSelection(true); cameraDirty = false; fitGraph(true);
      try { svg.focus({ preventScroll: true }); } catch (e) { svg.focus(); }
    });

    function onFullscreenChange() {
      if (!fullscreenButton) return;
      var active = document.fullscreenElement === panel;
      fullscreenButton.textContent = active ? "×" : "⛶";
      fullscreenButton.setAttribute("aria-label", active ? "Exit fullscreen" : "Enter fullscreen");
      fullscreenButton.title = active ? "Exit fullscreen" : "Fullscreen";
    }
    if (fullscreenButton) {
      if (!panel.requestFullscreen) fullscreenButton.hidden = true;
      else fullscreenButton.addEventListener("click", function () {
        if (document.fullscreenElement === panel) document.exitFullscreen();
        else panel.requestFullscreen().catch(function () {});
      });
    }
    document.addEventListener("fullscreenchange", onFullscreenChange);

    function updateViewport() {
      var nextBox = host.getBoundingClientRect();
      var nextWidth = Math.max(280, Math.round(nextBox.width || viewportWidth));
      var nextHeight = Math.max(360, Math.round(nextBox.height || viewportHeight));
      if (nextWidth === viewportWidth && nextHeight === viewportHeight) return;
      stopCameraAnimation();
      var dx = (nextWidth - viewportWidth) / 2, dy = (nextHeight - viewportHeight) / 2;
      viewportWidth = nextWidth; viewportHeight = nextHeight;
      svg.setAttribute("viewBox", "0 0 " + viewportWidth + " " + viewportHeight);
      if (cameraDirty) { camera.x += dx; camera.y += dy; applyCamera(); }
      else fitGraph(false, selectedID === null ? null : focusedNodesFor(nodeByID[String(selectedID)]));
    }
    var resizeObserver = null;
    if (window.ResizeObserver) { resizeObserver = new ResizeObserver(updateViewport); resizeObserver.observe(host); }
    else window.addEventListener("resize", updateViewport);

    host._graphCleanup = function () {
      if (resizeObserver) resizeObserver.disconnect();
      else window.removeEventListener("resize", updateViewport);
      document.removeEventListener("fullscreenchange", onFullscreenChange);
      if (cameraAnimation) cancelAnimationFrame(cameraAnimation);
      if (cameraFrame) cancelAnimationFrame(cameraFrame);
      if (searchFrame) cancelAnimationFrame(searchFrame);
      if (geometryFrame) cancelAnimationFrame(geometryFrame);
      if (layoutAnimation) cancelAnimationFrame(layoutAnimation);
    };

    updateFilters(false);
    updateFocus();
    fitGraph(false);
    updateResetLayoutControl();
    host.classList.toggle("show-labels", showLabels);
    var requestedPath = "";
    try { requestedPath = new URL(location.href).searchParams.get("node") || savedState.node || ""; } catch (e) { requestedPath = savedState.node || ""; }
    if (requestedPath) {
      var requested = nodes.find(function (n) { return n.path === requestedPath; });
      if (requested && (!query || currentMatchIDs[String(requested.id)])) selectNode(requested.id, true, false);
    }
    if (query || selectedID !== null) syncGraphURL();
    loading.remove();
    host.setAttribute("aria-busy", "false");
    }); });
  }

  function topDegree(nodes, percentile) {
    var d = nodes.map(function (n) { return n.degree || 0; }).sort(function (a, b) { return b - a; });
    return d[Math.max(0, Math.min(d.length - 1, Math.floor(d.length * percentile)))] || 1;
  }

  // ---- delegated interactions (survive #page swaps) ----
  document.addEventListener("click", function (e) {
    if (e.target.closest("[data-dashboard-connect-toggle]")) {
      e.preventDefault();
      toggleDashboardConnect();
      return;
    }
    var viewMode = e.target.closest("[data-repo-view-mode]");
    if (viewMode) {
      setDashboardView(viewMode.getAttribute("data-repo-view-mode"), true);
      return;
    }
    var directionControl = e.target.closest("[data-repo-sort-direction]");
    if (directionControl) {
      var sortSelect = document.querySelector("[data-repo-sort]");
      setDashboardSort(sortSelect ? sortSelect.value : "updated", directionControl.getAttribute("data-direction") === "asc" ? "desc" : "asc", true, true);
      return;
    }
    var sortHeader = e.target.closest("[data-repo-sort-key]");
    if (sortHeader) {
      var headerKey = sortHeader.getAttribute("data-repo-sort-key");
      var currentSelect = document.querySelector("[data-repo-sort]");
      var currentDirection = document.querySelector("[data-repo-sort-direction]");
      var nextDirection = currentSelect && currentSelect.value === headerKey && currentDirection && currentDirection.getAttribute("data-direction") === "asc"
        ? "desc" : (currentSelect && currentSelect.value === headerKey ? "asc" : REPO_SORT_DIRECTIONS[headerKey]);
      setDashboardSort(headerKey, nextDirection, true, true);
      return;
    }
    var fileDirection = e.target.closest("[data-file-table-direction]");
    if (fileDirection) {
      var fileSortSelect = document.querySelector("[data-file-table-sort]");
      setRepoFileSort(fileSortSelect ? fileSortSelect.value : "updated", fileDirection.getAttribute("data-direction") === "asc" ? "desc" : "asc", true, true);
      return;
    }
    var fileSortHeader = e.target.closest("[data-file-sort-key]");
    if (fileSortHeader) {
      var fileKey = fileSortHeader.getAttribute("data-file-sort-key");
      var selectedFileSort = document.querySelector("[data-file-table-sort]");
      var currentFileDirection = document.querySelector("[data-file-table-direction]");
      var nextFileDirection = selectedFileSort && selectedFileSort.value === fileKey && currentFileDirection && currentFileDirection.getAttribute("data-direction") === "asc"
        ? "desc" : (selectedFileSort && selectedFileSort.value === fileKey ? "asc" : FILE_SORT_DIRECTIONS[fileKey]);
      setRepoFileSort(fileKey, nextFileDirection, true, true);
      return;
    }
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
    if (tab) { setRepoPanel(tab.getAttribute("data-repo-tab"), true); return; }
    var caret = e.target.closest(".tree .caret");
    if (caret) { var li = caret.closest("li.dir"); if (li) li.classList.toggle("collapsed"); return; }
    var cp = e.target.closest("[data-copy]");
    if (cp) {
      copyText(cp.getAttribute("data-copy"), cp);
      return;
    }
    pjaxClick(e);
  });

  document.addEventListener("input", function (e) {
    if (e.target.id === "tree-filter") filterTree(e.target);
    if (e.target.matches("[data-file-table-search]")) filterRepoFileTable(e.target);
  });

  document.addEventListener("change", function (e) {
    if (!e.target.matches("[data-repo-sort]")) return;
    var key = e.target.value;
    setDashboardSort(key, REPO_SORT_DIRECTIONS[key], true, true);
  });

  document.addEventListener("change", function (e) {
    if (!e.target.matches("[data-file-table-sort]")) return;
    var key = e.target.value;
    setRepoFileSort(key, FILE_SORT_DIRECTIONS[key], true, true);
  });

  document.addEventListener("keydown", function (e) {
    var tab = e.target.closest && e.target.closest("[data-repo-tab]");
    if (!tab || (e.key !== "ArrowLeft" && e.key !== "ArrowRight" && e.key !== "Home" && e.key !== "End")) return;
    var tablist = tab.closest('[role="tablist"]');
    if (!tablist) return;
    var tabs = Array.prototype.slice.call(tablist.querySelectorAll("[data-repo-tab]"));
    var index = tabs.indexOf(tab);
    if (index < 0) return;
    if (e.key === "Home") index = 0;
    else if (e.key === "End") index = tabs.length - 1;
    else index = (index + (e.key === "ArrowRight" ? 1 : -1) + tabs.length) % tabs.length;
    e.preventDefault();
    setRepoPanel(tabs[index].getAttribute("data-repo-tab"), true);
    tabs[index].focus();
  });

  // ---- content init (on load + after each pjax swap) ----
  function initContent() {
    initDashboardIndex();
    initRepoFileTable();
    initWorkspaceResizers();
    initReview();
    if (document.querySelector("[data-repo-view]")) {
      var requestedView = "", savedView = "";
      try { requestedView = new URL(location.href).searchParams.get("view") || ""; } catch (e) {}
      try { savedView = localStorage.getItem("afs-repo-view:" + repoScope(location.pathname)) || ""; } catch (e2) {}
      var initialView = requestedView || savedView;
      setRepoPanel(initialView === "graph" || initialView === "table" ? initialView : "files", false);
    } else if (document.querySelector('[data-repo-panel="graph"]:not([hidden])')) initRepoGraph();
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
    if (url.pathname === location.pathname && url.search === location.search && url.hash) return;
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
  try { history.scrollRestoration = "manual"; } catch (e) {}
  function destroyContent() {
    document.querySelectorAll("[data-graph-host]").forEach(function (host) {
      if (host._graphCleanup) host._graphCleanup();
    });
  }
  function loadPage(href, push, restoreScroll) {
    var token = ++navToken;
    if (push) {
      try {
        var currentState = Object.assign({}, history.state || {}, { pjax: true, scrollY: window.scrollY });
        history.replaceState(currentState, "", location.href);
      } catch (e) {}
    }
    if (page) page.setAttribute("aria-busy", "true");
    fetch(href, { headers: { "X-Requested-With": "pjax" }, credentials: "same-origin" })
      .then(function (r) { if (!r.ok) throw new Error("status " + r.status); return r.text(); })
      .then(function (html) {
        if (token !== navToken) return; // a newer navigation superseded this one
        var doc = new DOMParser().parseFromString(html, "text/html");
        var newPage = doc.getElementById("page");
        if (!newPage) throw new Error("no #page"); // e.g. a non-standard response
        destroyContent();
        // Page-level shell classes own the workspace grid and its CSS custom
        // properties. Keep them in sync during PJAX navigation just as a full
        // refresh would; html-level theme/tree/agent state intentionally stays.
        document.body.className = doc.body ? doc.body.className : "";
        page.innerHTML = newPage.innerHTML;
        var crumbs = document.querySelector(".crumbs"), nc = doc.querySelector(".crumbs");
        if (crumbs && nc) crumbs.innerHTML = nc.innerHTML;
        if (doc.title) document.title = doc.title;
        page.removeAttribute("aria-busy");
        if (push) history.pushState({ pjax: true, scrollY: 0 }, "", href);
        initContent();
        var destination = new URL(href, location.href), hashTarget = null;
        if (destination.hash) {
          var hashID = destination.hash.slice(1);
          try { hashID = decodeURIComponent(hashID); } catch (e) {}
          hashTarget = document.getElementById(hashID);
        }
        if (hashTarget) hashTarget.scrollIntoView();
        else window.scrollTo(0, push ? 0 : (typeof restoreScroll === "number" ? restoreScroll : 0));
      })
      .catch(function () {
        if (token !== navToken) return;
        if (page) page.removeAttribute("aria-busy");
        window.location.href = href; // robust full-nav fallback
      });
  }
  window.addEventListener("popstate", function (event) {
    if (page) loadPage(location.href, false, event.state && event.state.scrollY);
  });

  initContent();
})();
