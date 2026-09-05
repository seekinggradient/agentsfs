import { Editor } from "@tiptap/core";
import { closeHistory } from "@tiptap/pm/history";
import Placeholder from "@tiptap/extension-placeholder";
import { EditorView, basicSetup } from "codemirror";
import { Compartment, EditorState } from "@codemirror/state";
import { markdown } from "@codemirror/lang-markdown";
import { oneDark } from "@codemirror/theme-one-dark";
import { diffLines } from "diff";
import {
  createIcons,
  ArrowLeft,
  ArrowUpRight,
  Maximize2,
  Download,
  PenLine,
  Code2,
  Bold,
  Italic,
  Strikethrough,
  Link,
  List,
  ListTodo,
  Plus,
  Undo2,
  Redo2,
  SlidersHorizontal,
  X,
} from "lucide";
import {
  extensions,
  splitFrontmatter,
  sourceReason,
  preservation,
  serializePreserving,
  safeURL,
} from "./model.js";

const root = document.querySelector("[data-note-editor]");
if (root) {
  try {
    mount(root);
  } catch (error) {
    console.error("Editor initialization failed", error);
    root.classList.remove("editor-ready");
    root.querySelector("[data-rich-editor]").hidden = true;
    root.querySelector("[data-source-editor]").hidden = true;
    root.querySelector("[data-notice]").hidden = false;
    root.querySelector("[data-notice]").textContent =
      "The visual editor could not start. You can still edit and save Markdown below.";
  }
}
function mount(root) {
  const $ = (s) => root.querySelector(s),
    $$ = (s) => [...root.querySelectorAll(s)];
  const form = $("#note-edit-form"),
    source = $('[name="content"]'),
    head = $('[name="head"]');
  const noteName = $(".write-location h1").textContent;
  const isMarkdown = root.dataset.markdown === "true";
  const endpoint = location.pathname;
  let base = JSON.parse($("[data-editor-source]").textContent),
    saved = base,
    mode = "write",
    editor,
    cm,
    preserved,
    prefix = "",
    loading = false,
    saving = false,
    recovery = null,
    conflictHead = "",
    conflictContent = "";
  let conflictEditors = [],
    combinedEditor,
    combinedPreserved,
    combinedPrefix = "",
    conflictVisual = false;
  let cmOriginalDoc,
    cmOriginalSource = "",
    restoredDraft = null;
  let draftTimer,
    outlineTimer,
    activeLinkType = "link",
    selectedLinkRange = null,
    slashIndex = 0,
    slashRange = null;
  const themeCompartment = new Compartment();
  const lineSeparatorCompartment = new Compartment();
  const masthead = document.querySelector(".masthead");
  const layoutObserver = new ResizeObserver(() => {
    root.style.setProperty(
      "--write-masthead",
      masthead.getBoundingClientRect().height + "px",
    );
    root.style.setProperty(
      "--write-header",
      $(".write-header").getBoundingClientRect().height + "px",
    );
  });
  layoutObserver.observe(masthead);
  layoutObserver.observe($(".write-header"));
  const namespace =
    "afs-note-draft:v1:" +
    encodeURIComponent(root.dataset.viewer) +
    ":" +
    encodeURIComponent(endpoint) +
    ":";
  // Each loaded page gets its own slot, so duplicate tabs cannot overwrite drafts.
  const draftKey = namespace + crypto.randomUUID();
  const status = $("[data-status]"),
    notice = $("[data-notice]"),
    review = $("[data-review]");
  function notify(text) {
    notice.textContent = text;
    notice.hidden = !text;
  }
  function content() {
    if (mode === "source")
      return cm
        ? cm.state.doc === cmOriginalDoc
          ? cmOriginalSource
          : cm.state.sliceDoc()
        : source.value;
    return prefix + serializePreserving(editor, preserved);
  }
  function dirty() {
    return content() !== saved;
  }
  function saveDraft() {
    clearTimeout(draftTimer);
    const current = content();
    source.value = current;
    try {
      if (current === saved) {
        localStorage.removeItem(draftKey);
        status.textContent = "All changes saved";
        return;
      }
      localStorage.setItem(
        draftKey,
        JSON.stringify({
          content: current,
          base: saved,
          head: head.value,
          message: $("#version-message").value,
          updated: Date.now(),
        }),
      );
      status.textContent = navigator.onLine
        ? "Draft saved on this browser"
        : "Offline · draft saved here";
    } catch {
      status.textContent = "Draft recovery unavailable";
      notify(
        "Browser storage is unavailable or full. Keep this tab open, save a version, or download your draft.",
      );
    }
  }
  function changed() {
    if (loading) return;
    status.textContent = dirty() ? "Saving draft…" : "All changes saved";
    $("[data-save]").disabled = !dirty() || saving;
    clearTimeout(draftTimer);
    draftTimer = setTimeout(saveDraft, 450);
    clearTimeout(outlineTimer);
    outlineTimer = setTimeout(updateDocumentContext, 200);
  }
  function updateDocumentContext() {
    const text = splitFrontmatter(content()).body;
    const words = text.trim().match(/\S+/g)?.length || 0;
    $("[data-count]").textContent =
      words.toLocaleString() +
      " words · " +
      Math.max(1, Math.ceil(words / 220)) +
      " min read";
    const nav = $("[data-outline]");
    nav.replaceChildren();
    if (mode !== "write") {
      const hint = document.createElement("span");
      hint.className = "write-tip";
      hint.textContent = "Switch to Write to navigate headings.";
      nav.append(hint);
      return;
    }
    const headings = $$(
      "[data-rich-editor] h1,[data-rich-editor] h2,[data-rich-editor] h3",
    );
    for (const el of headings) {
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = el.textContent || "Untitled section";
      button.style.paddingLeft = (Number(el.tagName[1]) - 1) * 10 + "px";
      button.onclick = () =>
        el.scrollIntoView({
          behavior: matchMedia("(prefers-reduced-motion: reduce)").matches
            ? "instant"
            : "smooth",
          block: "start",
        });
      nav.append(button);
    }
    $(".write-tip").hidden = headings.length > 0;
  }
  function updateToolbar() {
    if (!editor) return;
    $$("[data-command]").forEach((button) => {
      const command = button.dataset.command;
      if (
        [
          "bold",
          "italic",
          "strike",
          "bulletList",
          "taskList",
          "orderedList",
          "blockquote",
          "codeBlock",
        ].includes(command)
      )
        button.setAttribute("aria-pressed", String(editor.isActive(command)));
      if (command === "undo" || command === "redo")
        button.disabled = mode !== "write" || !editor.can()[command]();
    });
    $("[data-block]").value = editor.isActive("heading", { level: 1 })
      ? "h1"
      : editor.isActive("heading", { level: 2 })
        ? "h2"
        : editor.isActive("heading", { level: 3 })
          ? "h3"
          : "paragraph";
    $("[data-table-tools]").hidden =
      mode !== "write" || !editor.isActive("table");
  }
  function loadRich(text) {
    const parts = splitFrontmatter(text);
    // A mode switch with unchanged text must retain the cursor and undo stack.
    if (
      preserved &&
      prefix === parts.prefix &&
      serializePreserving(editor, preserved) === parts.body
    )
      return;
    prefix = parts.prefix;
    loading = true;
    try {
      editor.view.dispatch(closeHistory(editor.state.tr));
      editor
        .chain()
        .setContent(parts.body, {
          contentType: "markdown",
          emitUpdate: false,
        })
        .setMeta("addToHistory", Boolean(preserved))
        .run();
      editor.view.dispatch(closeHistory(editor.state.tr));
      preserved = preservation(editor, parts.body);
    } finally {
      loading = false;
    }
    $("[data-metadata]").hidden = !prefix;
    $("[data-properties]").textContent = prefix
      .replace(/^---\r?\n/, "")
      .replace(/(?:---|\.\.\.)\r?\n?$/, "");
  }
  function darkTheme() {
    const t = document.documentElement.dataset.theme;
    return (
      t === "dark" || (!t && matchMedia("(prefers-color-scheme: dark)").matches)
    );
  }
  function loadSource(text) {
    loading = true;
    try {
      if (!cm)
        cm = new EditorView({
          doc: text,
          parent: $("[data-source-editor]"),
          extensions: [
            basicSetup,
            markdown(),
            EditorView.lineWrapping,
            themeCompartment.of(darkTheme() ? oneDark : []),
            lineSeparatorCompartment.of(
              EditorState.lineSeparator.of(
                text.includes("\r\n") ? "\r\n" : "\n",
              ),
            ),
            EditorView.contentAttributes.of({
              "aria-label": "Markdown source",
              spellcheck: "true",
            }),
            EditorView.updateListener.of((update) => {
              if (update.docChanged) changed();
            }),
          ],
        });
      else
        cm.dispatch({
          changes: { from: 0, to: cm.state.doc.length, insert: text },
          effects: lineSeparatorCompartment.reconfigure(
            EditorState.lineSeparator.of(text.includes("\r\n") ? "\r\n" : "\n"),
          ),
        });
      cmOriginalDoc = cm.state.doc;
      cmOriginalSource = text;
    } finally {
      loading = false;
    }
  }
  function setMode(next, text = content(), focus = true) {
    const reason = sourceReason(text, isMarkdown);
    if (next === "write" && reason) {
      notify(reason + " You can continue editing here.");
      next = "source";
    }
    mode = next;
    if (next === "write") loadRich(text);
    else loadSource(text);
    $("[data-rich-editor]").hidden = next !== "write";
    $("[data-source-editor]").hidden = next !== "source";
    $(".write-tools").hidden = next !== "write";
    $("[data-metadata]").hidden = next !== "write" || !prefix;
    $$("[data-mode]").forEach((button) =>
      button.setAttribute("aria-pressed", String(button.dataset.mode === next)),
    );
    if (next === "write") notify("");
    updateToolbar();
    updateDocumentContext();
    if (focus) {
      if (next === "write") editor.commands.focus();
      else cm.focus();
    }
  }
  const rawBase = new URL(endpoint.replace("/edit/", "/raw/"), location.origin)
    .href;
  editor = new Editor({
    element: $("[data-rich-editor]"),
    extensions: [
      ...extensions(rawBase),
      Placeholder.configure({
        placeholder: "Start writing, or type / to add something…",
      }),
    ],
    content: "",
    editorProps: {
      attributes: {
        class: "write-prose",
        "aria-label": "Note content",
        role: "textbox",
        "aria-multiline": "true",
        spellcheck: "true",
      },
      handleKeyDown: (_view, event) => {
        if (!$("[data-slash]").hidden) {
          const items = $$("[data-slash] button");
          if (event.key === "ArrowDown" || event.key === "ArrowUp") {
            slashIndex =
              (slashIndex +
                (event.key === "ArrowDown" ? 1 : -1) +
                items.length) %
              items.length;
            renderSlashSelection();
            return true;
          }
          if (event.key === "Enter") {
            items[slashIndex]?.click();
            return true;
          }
          if (event.key === "Escape") {
            $("[data-slash]").hidden = true;
            slashRange = null;
            return true;
          }
        }
        return false;
      },
    },
    onUpdate: () => {
      changed();
      updateSlash();
    },
    onSelectionUpdate: () => {
      updateToolbar();
      if (!editor.state.selection.empty) $("[data-slash]").hidden = true;
    },
    onTransaction: () => updateToolbar(),
  });
  // No editor transaction is allowed to modify the initial source just by loading.
  const initialReason = sourceReason(base, isMarkdown);
  setMode(initialReason ? "source" : "write", base, false);
  if (initialReason) notify(initialReason);
  root.classList.add("editor-ready");
  $("[data-save]").disabled = true;
  createIcons({
    icons: {
      ArrowLeft,
      ArrowUpRight,
      Maximize2,
      Download,
      PenLine,
      Code2,
      Bold,
      Italic,
      Strikethrough,
      Link,
      List,
      ListTodo,
      Plus,
      Undo2,
      Redo2,
      SlidersHorizontal,
      X,
    },
  });
  function scanRecovery() {
    try {
      const drafts = [];
      for (let i = 0; i < localStorage.length; i++) {
        const key = localStorage.key(i);
        if (!key.startsWith(namespace) || key === draftKey) continue;
        try {
          const value = JSON.parse(localStorage.getItem(key));
          if (
            typeof value.content === "string" &&
            typeof value.base === "string" &&
            /^[a-f0-9]{40}$/.test(value.head) &&
            value.content !== base
          )
            drafts.push({ key, ...value });
        } catch {}
      }
      recovery = drafts.sort((a, b) => b.updated - a.updated)[0] || null;
      if (recovery) {
        $("[data-recovery]").hidden = false;
        $("[data-recovery-detail]").textContent =
          "Saved " +
          new Date(recovery.updated).toLocaleString() +
          ". Restore it to continue where you left off.";
      }
    } catch {
      notify(
        "Browser draft recovery is unavailable. You can still save versions and download your work.",
      );
    }
  }
  scanRecovery();
  function download(text = content()) {
    const blob = new Blob([text], { type: "text/markdown;charset=utf-8" }),
      url = URL.createObjectURL(blob),
      a = document.createElement("a");
    a.href = url;
    a.download = noteName;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
  function renderDiff() {
    const current = content(),
      host = $("[data-diff]");
    host.replaceChildren();
    if (saved.length + current.length > 500000) {
      host.textContent =
        "This note is too long for an inline comparison. Review the full text in Markdown or download your draft before saving.";
      $("[data-diff-count]").textContent = "Large document";
      return;
    }
    const parts = diffLines(saved, current, {
      timeout: 300,
      maxEditLength: 10000,
    });
    if (!parts) {
      host.textContent =
        "This is a large rewrite. Review the full draft in the editor before saving.";
      return;
    }
    let added = 0,
      removed = 0;
    for (const part of parts) {
      if (part.added) added += part.count;
      if (part.removed) removed += part.count;
      const row = document.createElement("div");
      row.className = part.added
        ? "added"
        : part.removed
          ? "removed"
          : "unchanged";
      // Keep the diff usable for long notes while clearly marking omitted context.
      const lines = part.value.split("\n");
      row.textContent =
        !part.added && !part.removed && lines.length > 9
          ? lines.slice(0, 3).join("\n") +
            "\n⋯ " +
            (lines.length - 6) +
            " unchanged lines ⋯\n" +
            lines.slice(-3).join("\n")
          : part.value;
      host.append(row);
    }
    $("[data-diff-count]").textContent =
      "+" + added + " / −" + removed + " lines";
  }
  function openReview() {
    if (saving || !dirty()) return;
    saveDraft();
    renderDiff();
    $("[data-save-error]").hidden = true;
    if (!$("#version-message").value.trim())
      $("#version-message").value =
        "Update " +
        noteName.replace(/\.(md|markdown)$/i, "").replace(/[-_]/g, " ");
    review.showModal();
    $("#version-message").focus();
    $("#version-message").select();
  }
  async function saveVersion() {
    if (saving) return;
    const snapshot = content(),
      versionMessage = $("#version-message").value.trim();
    if (!versionMessage) {
      $("#version-message").focus();
      $("[data-save-error]").textContent =
        "Add a short description so this version is easy to find later.";
      $("[data-save-error]").hidden = false;
      return;
    }
    saving = true;
    $$("[data-review] button").forEach((button) => (button.disabled = true));
    $("#version-message").disabled = true;
    $('[data-action="commit"]').textContent = "Saving…";
    $("[data-save-error]").hidden = true;
    saveDraft();
    try {
      const response = await fetch(endpoint, {
        method: "POST",
        credentials: "same-origin",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/x-www-form-urlencoded",
        },
        body: new URLSearchParams({
          content: snapshot,
          head: head.value,
          csrf: $('[name="csrf"]').value,
          message: versionMessage,
        }),
        signal: AbortSignal.timeout(30000),
      });
      if (response.redirected)
        throw new Error(
          "Please sign in again in another tab, then retry. Your draft is still here.",
        );
      const result = await response.json().catch(() => ({
        error:
          "The server could not save this version. Your draft is still here; please try again.",
      }));
      if (!response.ok) {
        if (result.conflict) {
          await openConflict();
          return;
        }
        throw new Error(result.error || "The version could not be saved.");
      }
      head.value = result.head;
      saved = base = snapshot;
      $("#version-message").value = "";
      try {
        localStorage.removeItem(draftKey);
        if (restoredDraft) {
          const previous = JSON.parse(
            localStorage.getItem(restoredDraft.key) || "null",
          );
          if (previous?.updated === restoredDraft.updated)
            localStorage.removeItem(restoredDraft.key);
        }
      } catch {}
      status.textContent = "Version saved";
      $("[data-save]").disabled = true;
      review.close();
      notify(
        result.unchanged
          ? "This version is already saved."
          : "Version saved. Your changes are now in the note’s history.",
      );
      const link = document.createElement("a");
      link.href = result.url;
      link.textContent = " View note ↗";
      notice.append(link);
    } catch (error) {
      $("[data-save-error]").textContent =
        error.name === "TimeoutError"
          ? "The save timed out. Your draft is safe here. Retry to check whether the version was saved."
          : error.message;
      $("[data-save-error]").hidden = false;
    } finally {
      saving = false;
      $$("[data-review] button").forEach((button) => (button.disabled = false));
      $("#version-message").disabled = false;
      $('[data-action="commit"]').textContent = "Save version";
      $("[data-save]").disabled = !dirty();
    }
  }
  async function openConflict() {
    const response = await fetch(endpoint, {
      headers: { Accept: "application/json" },
      cache: "no-store",
      signal: AbortSignal.timeout(15000),
    });
    if (!response.ok || response.redirected)
      throw new Error(
        "The current note could not be loaded. It may have been removed, or your access changed. Download your draft before reopening it.",
      );
    const latest = await response.json();
    conflictHead = latest.head;
    $("[data-latest]").textContent = latest.content;
    $("[data-conflict-draft]").textContent = content();
    $("#resolved-source").value = content();
    $("[data-conflict-confirm]").checked = false;
    $('[data-action="resolve"]').disabled = true;
    // Keep the current source independently until the user explicitly reconciles it.
    conflictContent = latest.content;
    conflictEditors.forEach((instance) => instance.destroy());
    conflictEditors = [];
    const draft = content();
    const canVisual =
      !sourceReason(latest.content, isMarkdown) &&
      !sourceReason(draft, isMarkdown) &&
      splitFrontmatter(latest.content).prefix ===
        splitFrontmatter(draft).prefix;
    conflictVisual = canVisual;
    for (const [selector, body, label] of [
      ["[data-latest-visual]", latest.content, "Latest saved note"],
      ["[data-draft-visual]", draft, "Your draft"],
    ]) {
      $(selector).replaceChildren();
      $(selector).hidden = !canVisual;
      if (canVisual)
        conflictEditors.push(
          new Editor({
            element: $(selector),
            extensions: extensions(rawBase),
            content: splitFrontmatter(body).body,
            contentType: "markdown",
            editable: false,
            editorProps: {
              attributes: { class: "write-prose", "aria-label": label },
            },
          }),
        );
    }
    $("[data-latest]").hidden = canVisual;
    $("[data-conflict-draft]").hidden = canVisual;
    $("[data-action='conflict-mode']").hidden = !canVisual;
    $("[data-action='conflict-mode']").textContent = "Edit Markdown";
    $("[data-combined-visual]").replaceChildren();
    if (canVisual) {
      const parts = splitFrontmatter(draft);
      combinedPrefix = parts.prefix;
      combinedEditor = new Editor({
        element: $("[data-combined-visual]"),
        extensions: extensions(rawBase),
        content: parts.body,
        contentType: "markdown",
        editorProps: {
          attributes: {
            class: "write-prose",
            role: "textbox",
            "aria-label": "Combined version",
            "aria-multiline": "true",
            spellcheck: "true",
          },
        },
      });
      combinedPreserved = preservation(combinedEditor, parts.body);
      conflictEditors.push(combinedEditor);
    }
    $("[data-combined-visual]").hidden = !canVisual;
    $("[data-combine-tools]").hidden = !canVisual;
    $("#resolved-source").hidden = canVisual;
    review.close();
    $("[data-conflict]").showModal();
  }
  function openLink(type) {
    activeLinkType = type;
    selectedLinkRange = {
      from: editor.state.selection.from,
      to: editor.state.selection.to,
    };
    $("#link-title").textContent =
      type === "image" ? "Add an image" : "Add a link";
    $("#link-url").value =
      type === "link" ? editor.getAttributes("link").href || "" : "";
    $("#link-text").value =
      type === "link"
        ? editor.state.doc.textBetween(
            selectedLinkRange.from,
            selectedLinkRange.to,
            " ",
          )
        : "";
    $("[data-link-text-label]").textContent =
      type === "image" ? "Image description" : "Text";
    $('[data-action="remove-link"]').hidden =
      type === "image" || !editor.isActive("link");
    $("[data-link-error]").hidden = true;
    $("[data-link-dialog]").showModal();
    $("#link-url").focus();
  }
  function command(name) {
    $(".write-insert").open = false;
    if (mode !== "write") return;
    if (name === "link" || name === "image") {
      openLink(name);
      return;
    }
    if (name !== "undo" && name !== "redo")
      editor.view.dispatch(closeHistory(editor.state.tr));
    const c = editor.chain().focus();
    const toggle = {
      bold: "toggleBold",
      italic: "toggleItalic",
      strike: "toggleStrike",
      bulletList: "toggleBulletList",
      orderedList: "toggleOrderedList",
      taskList: "toggleTaskList",
      blockquote: "toggleBlockquote",
      codeBlock: "toggleCodeBlock",
      horizontalRule: "setHorizontalRule",
    };
    if (name === "table")
      c.insertTable({ rows: 3, cols: 3, withHeaderRow: true }).run();
    else if (/^h[123]$/.test(name))
      c.toggleHeading({ level: Number(name[1]) }).run();
    else if (name === "paragraph") c.setParagraph().run();
    else if (typeof c[toggle[name] || name] === "function")
      c[toggle[name] || name]().run();
    if (name !== "undo" && name !== "redo")
      editor.view.dispatch(closeHistory(editor.state.tr));
    updateToolbar();
  }
  const blocks = [
    ["h1", "Heading 1"],
    ["h2", "Heading 2"],
    ["h3", "Heading 3"],
    ["bulletList", "Bullet list"],
    ["orderedList", "Numbered list"],
    ["taskList", "Checklist"],
    ["blockquote", "Quote"],
    ["codeBlock", "Code block"],
    ["table", "Table"],
    ["horizontalRule", "Divider"],
    ["image", "Image from URL"],
  ];
  function updateSlash() {
    const sel = editor.state.selection,
      parent = sel.$from.parent,
      text = parent.textContent;
    const host = $("[data-slash]");
    if (
      !sel.empty ||
      parent.type.name !== "paragraph" ||
      !/^\/[a-z 0-9]*$/i.test(text) ||
      text.length > 25
    ) {
      host.hidden = true;
      slashRange = null;
      return;
    }
    slashRange = { from: sel.$from.start(), to: sel.$from.end() };
    const matches = blocks.filter(([, label]) =>
      label.toLowerCase().includes(text.slice(1).toLowerCase()),
    );
    host.replaceChildren();
    slashIndex = 0;
    for (const [name, label] of matches) {
      const b = document.createElement("button");
      b.type = "button";
      b.role = "option";
      b.textContent = label;
      b.onmousedown = (e) => e.preventDefault();
      b.onclick = () => {
        editor.chain().focus().deleteRange(slashRange).run();
        host.hidden = true;
        command(name);
      };
      host.append(b);
    }
    host.hidden = !matches.length;
    if (host.hidden) return;
    const rect = editor.view.coordsAtPos(sel.from);
    host.style.left =
      Math.min(innerWidth - 252, Math.max(12, rect.left)) + "px";
    host.style.top = Math.min(innerHeight - 310, rect.bottom + 8) + "px";
    renderSlashSelection();
  }
  function renderSlashSelection() {
    $$("[data-slash] button").forEach((b, i) =>
      b.setAttribute("aria-selected", String(i === slashIndex)),
    );
  }
  review.addEventListener("cancel", (event) => {
    if (saving) event.preventDefault();
  });
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    openReview();
  });
  $(".write-insert").addEventListener("toggle", () => {
    const menu = $(".write-menu");
    if ($(".write-insert").open && innerWidth <= 850) {
      const rect = $(".write-insert summary").getBoundingClientRect();
      menu.style.position = "fixed";
      menu.style.top = rect.bottom + 8 + "px";
      menu.style.left =
        Math.min(innerWidth - 212, Math.max(12, rect.left)) + "px";
      menu.style.right = "auto";
    } else menu.removeAttribute("style");
  });
  $("[data-block]").addEventListener("change", (event) =>
    command(event.target.value),
  );
  $$("[data-command]").forEach((b) => {
    b.addEventListener("mousedown", (e) => e.preventDefault());
    b.addEventListener("click", () => command(b.dataset.command));
  });
  $$("[data-mode]").forEach((b) =>
    b.addEventListener("click", () => setMode(b.dataset.mode)),
  );
  $("[data-link-form]").addEventListener("submit", (event) => {
    event.preventDefault();
    const href = $("#link-url").value.trim(),
      text = $("#link-text").value;
    if (!safeURL(href, activeLinkType === "image")) {
      $("[data-link-error]").textContent =
        "Use a web address or a relative note path.";
      $("[data-link-error]").hidden = false;
      return;
    }
    $("[data-link-dialog]").close();
    editor.commands.setTextSelection(selectedLinkRange);
    if (activeLinkType === "image")
      editor.chain().focus().setImage({ src: href, alt: text }).run();
    else if (
      text &&
      (selectedLinkRange.from === selectedLinkRange.to ||
        text !==
          editor.state.doc.textBetween(
            selectedLinkRange.from,
            selectedLinkRange.to,
            " ",
          ))
    )
      editor
        .chain()
        .focus()
        .insertContent({
          type: "text",
          text,
          marks: [{ type: "link", attrs: { href } }],
        })
        .run();
    else if (
      selectedLinkRange.from === selectedLinkRange.to &&
      !editor.isActive("link")
    )
      editor
        .chain()
        .focus()
        .insertContent({
          type: "text",
          text: href,
          marks: [{ type: "link", attrs: { href } }],
        })
        .run();
    else editor.chain().focus().extendMarkRange("link").setLink({ href }).run();
  });
  $("#version-message").addEventListener("input", () => {
    if (dirty()) saveDraft();
  });
  $("[data-conflict-confirm]").addEventListener(
    "change",
    (e) => ($('[data-action="resolve"]').disabled = !e.target.checked),
  );
  const actions = {
    focus() {
      document.body.classList.toggle("write-focused");
      $('[data-action="focus"]').setAttribute(
        "aria-pressed",
        String(document.body.classList.contains("write-focused")),
      );
    },
    download() {
      download();
    },
    "edit-properties"() {
      setMode("source");
    },
    "cancel-review"() {
      review.close();
    },
    commit: saveVersion,
    "close-link"() {
      $("[data-link-dialog]").close();
    },
    "remove-link"() {
      $("[data-link-dialog]").close();
      editor.chain().focus().extendMarkRange("link").unsetLink().run();
    },
    recover() {
      if (!recovery) return;
      if (dirty()) saveDraft();
      restoredDraft = recovery;
      saved = recovery.base;
      head.value = recovery.head;
      $("#version-message").value = recovery.message || "";
      setMode(
        sourceReason(recovery.content, isMarkdown) ? "source" : "write",
        recovery.content,
      );
      $("[data-recovery]").hidden = true;
      changed();
      saveDraft();
      notify("Draft restored. Review and save a version when you’re ready.");
    },
    "discard-recovery"() {
      if (recovery)
        try {
          localStorage.removeItem(recovery.key);
        } catch {}
      recovery = null;
      $("[data-recovery]").hidden = true;
    },
    "conflict-mode"() {
      if (conflictVisual) {
        $("#resolved-source").value =
          combinedPrefix +
          serializePreserving(combinedEditor, combinedPreserved);
        conflictVisual = false;
      } else {
        const text = $("#resolved-source").value;
        if (sourceReason(text, isMarkdown)) return;
        const parts = splitFrontmatter(text);
        combinedPrefix = parts.prefix;
        combinedEditor.commands.setContent(parts.body, {
          contentType: "markdown",
          emitUpdate: false,
        });
        combinedPreserved = preservation(combinedEditor, parts.body);
        conflictVisual = true;
      }
      $("[data-combined-visual]").hidden = !conflictVisual;
      $("[data-combine-tools]").hidden = !conflictVisual;
      $("#resolved-source").hidden = conflictVisual;
      $("[data-action='conflict-mode']").textContent = conflictVisual
        ? "Edit Markdown"
        : "Edit visually";
    },
    resolve() {
      if (!$("[data-conflict-confirm]").checked) return;
      const combined = conflictVisual
        ? combinedPrefix +
          serializePreserving(combinedEditor, combinedPreserved)
        : $("#resolved-source").value;
      head.value = conflictHead;
      saved = conflictContent;
      setMode(
        sourceReason(combined, isMarkdown) ? "source" : "write",
        combined,
      );
      $("[data-conflict]").close();
      changed();
      saveDraft();
      notify(
        "Combined draft ready. Review the changes and save a new version.",
      );
    },
  };
  $$("[data-combine]").forEach((button) => {
    button.addEventListener("mousedown", (event) => event.preventDefault());
    button.addEventListener("click", () =>
      combinedEditor.chain().focus()[button.dataset.combine]().run(),
    );
  });
  $$("[data-action]").forEach((b) =>
    b.addEventListener("click", () => actions[b.dataset.action]?.()),
  );
  document.addEventListener("keydown", (event) => {
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "s") {
      event.preventDefault();
      if (!root.querySelector("dialog[open]")) openReview();
    }
    if (
      (event.metaKey || event.ctrlKey) &&
      event.key.toLowerCase() === "k" &&
      mode === "write" &&
      !root.querySelector("dialog[open]")
    ) {
      event.preventDefault();
      openLink("link");
    }
    if (event.key === "Escape") {
      document.body.classList.remove("write-focused");
      $('[data-action="focus"]').setAttribute("aria-pressed", "false");
    }
  });
  window.addEventListener("beforeunload", (event) => {
    if (dirty()) {
      saveDraft();
      event.preventDefault();
      event.returnValue = "";
    }
  });
  window.addEventListener("pagehide", () => {
    if (dirty()) saveDraft();
  });
  document.addEventListener("visibilitychange", () => {
    if (document.hidden && dirty()) saveDraft();
  });
  window.addEventListener("offline", () => {
    if (dirty()) saveDraft();
    else status.textContent = "Offline";
  });
  window.addEventListener("online", () => {
    status.textContent = dirty()
      ? "Draft saved on this browser"
      : "All changes saved";
  });
  const themeObserver = new MutationObserver(() => {
    if (cm)
      cm.dispatch({
        effects: themeCompartment.reconfigure(darkTheme() ? oneDark : []),
      });
  });
  themeObserver.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["data-theme"],
  });
  matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    if (cm)
      cm.dispatch({
        effects: themeCompartment.reconfigure(darkTheme() ? oneDark : []),
      });
  });
}
