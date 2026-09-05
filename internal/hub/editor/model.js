import { Node } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import { Markdown } from "@tiptap/markdown";
import { TableKit } from "@tiptap/extension-table";
import TaskList from "@tiptap/extension-task-list";
import TaskItem from "@tiptap/extension-task-item";
import Image from "@tiptap/extension-image";
import { NativeHistory } from "./history.js";

export const WikiLink = Node.create({
  name: "wikiLink",
  group: "inline",
  inline: true,
  atom: true,
  addAttributes: () => ({ target: { default: "" }, label: { default: "" } }),
  parseHTML: () => [
    {
      tag: "span[data-wiki-link]",
      getAttrs: (el) => ({
        target: el.dataset.wikiLink,
        label: el.textContent,
      }),
    },
  ],
  renderHTML: ({ node }) => [
    "span",
    { "data-wiki-link": node.attrs.target, class: "wiki-link" },
    node.attrs.label || node.attrs.target,
  ],
  markdownTokenizer: {
    name: "wikiLink",
    level: "inline",
    start: (src) => src.indexOf("[["),
    tokenize(src) {
      const match = /^\[\[([^\]\n]+)\]\]/.exec(src);
      if (!match) return;
      const [target, ...label] = match[1].split("|");
      return {
        type: "wikiLink",
        raw: match[0],
        target,
        label: label.join("|"),
      };
    },
  },
  parseMarkdown: (token) => ({
    type: "wikiLink",
    attrs: { target: token.target, label: token.label },
  }),
  renderMarkdown: (node) =>
    "[[" +
    node.attrs.target +
    (node.attrs.label ? "|" + node.attrs.label : "") +
    "]]",
});

// Images and links never accept active content. Relative images resolve to the
// repository raw route without changing their Markdown destination on disk.
export function safeURL(value, image = false) {
  const v = value.trim();
  if (
    !v ||
    /[\u0000-\u0020\u007f]/.test(v) ||
    v.startsWith("//") ||
    v.includes("\\")
  )
    return false;
  if (/^[a-z][a-z\d+.-]*:/i.test(v))
    return (image ? /^https?:/i : /^(https?:|mailto:|tel:)/i).test(v);
  return true;
}
export function extensions(rawBase = "") {
  const SafeImage = Image.extend({
    renderHTML({ HTMLAttributes }) {
      let src = HTMLAttributes.src || "";
      if (!safeURL(src, true)) src = "";
      else if (rawBase && !/^(https?:|\/)/i.test(src))
        src = new URL(src, rawBase).href;
      return [
        "img",
        {
          ...HTMLAttributes,
          src,
          loading: "lazy",
          referrerpolicy: "no-referrer",
        },
      ];
    },
  });
  return [
    StarterKit.configure({
      underline: false,
      link: {
        openOnClick: false,
        autolink: false,
        enableClickSelection: true,
        isAllowedUri: (url) => safeURL(url),
      },
    }),
    Markdown,
    NativeHistory,
    TableKit.configure({ table: { resizable: false } }),
    TaskList,
    TaskItem.configure({
      nested: true,
      HTMLAttributes: { "data-type": "taskItem" },
    }),
    SafeImage,
    WikiLink,
  ];
}
export function splitFrontmatter(source) {
  const match =
    /^(?:\uFEFF)?---\r?\n[\s\S]*?\r?\n(?:---|\.\.\.)(?:\r?\n|$)/.exec(source);
  return match
    ? { prefix: match[0], body: source.slice(match[0].length) }
    : { prefix: "", body: source };
}
// Be conservative for Markdown dialects whose semantics the visual schema does
// not represent. Code fences are excluded so literal examples don't block editing.
export function sourceReason(source, markdown = true) {
  if (!markdown) return "This file opens in the text editor.";
  if (source.length > 250000)
    return "This long note opens in Markdown for responsive editing.";
  const { body } = splitFrontmatter(source);
  const text = body.replace(
    /^ {0,3}(`{3,}|~{3,})[^\n]*\n[\s\S]*?^ {0,3}\1[^\n]*(?:\n|$)/gm,
    "",
  );
  if (/^(?:\uFEFF)?---\r?\n/.test(source) && !splitFrontmatter(source).prefix)
    return "Unclosed document properties are preserved in Markdown.";
  if (/<\/?[A-Za-z!][^>]*>|<!--|^\s*:::/m.test(text))
    return "This note contains HTML or custom blocks. Markdown keeps them intact.";
  if (/\$[^\n$]+\$|\$\$|\\\(|\\\[|^\[\^[^\]]+\]:/m.test(text))
    return "This note contains math or footnotes. Markdown keeps them intact.";
  if (/^ {0,3}\[[^\]]+\]:/m.test(text))
    return "This note uses reference links. Markdown preserves their definitions.";
  if (/^\s*\|?\s*:?-{3,}:\s*\||\|\s*:-{3,}/m.test(text))
    return "This note uses aligned tables. Markdown preserves their layout.";
  return "";
}

// Preserve unchanged top-level blocks verbatim, including their separators.
// Merely opening a note or switching modes must never rewrite it. Changed
// blocks use the editor's serializer; all other blocks retain their bytes.
export function preservation(editor, body) {
  const original = editor.getJSON();
  const pieces = [];
  let pending = "";
  for (const token of editor.markdown.instance.lexer(body)) {
    if (token.type === "space") {
      if (pieces.length) pieces.at(-1).raw += token.raw;
      else pending += token.raw;
      continue;
    }
    const parsed = editor.markdown.parse(token.raw.trimEnd()).content || [];
    if (parsed.length !== 1) return { original, body, pieces: [] };
    pieces.push({
      key: JSON.stringify(editor.schema.nodeFromJSON(parsed[0]).toJSON()),
      raw: pending + token.raw,
    });
    pending = "";
  }
  const nodes = original.content || [];
  if (pieces.some((piece, i) => piece.key !== JSON.stringify(nodes[i])))
    return { original, body, pieces: [] };
  for (const node of nodes.slice(pieces.length)) {
    if (node.type !== "paragraph" || node.content?.length)
      return { original, body, pieces: [] };
    pieces.push({ key: JSON.stringify(node), raw: "" });
  }
  return { original, body, pieces };
}
export function serializePreserving(editor, state) {
  const json = editor.getJSON();
  if (JSON.stringify(json) === JSON.stringify(state.original))
    return state.body;
  const queues = new Map();
  for (const piece of state.pieces) {
    if (!queues.has(piece.key)) queues.set(piece.key, []);
    queues.get(piece.key).push(piece.raw);
  }
  let result = "";
  for (const node of json.content || []) {
    const queue = queues.get(JSON.stringify(node));
    const raw = queue?.length
      ? queue.shift()
      : editor.markdown.serialize({ type: "doc", content: [node] });
    if (!raw) continue;
    if (result && !result.endsWith("\n\n"))
      result += result.endsWith("\n") ? "\n" : "\n\n";
    result += raw;
  }
  // Preserve the author's end-of-file convention when the last block changes.
  if (state.body.endsWith("\n") && !result.endsWith("\n")) result += "\n";
  return result;
}
