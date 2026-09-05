import { test } from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { Editor } from "@tiptap/core";
import {
  extensions,
  splitFrontmatter,
  sourceReason,
  preservation,
  serializePreserving,
  safeURL,
} from "./model.js";
const dom = new JSDOM("<!doctype html><html><body></body></html>");
for (const key of [
  "window",
  "document",
  "navigator",
  "Node",
  "HTMLElement",
  "Element",
  "MutationObserver",
  "DOMParser",
  "getComputedStyle",
])
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom.window[key],
  });
globalThis.requestAnimationFrame = (cb) => setTimeout(cb, 0);
globalThis.cancelAnimationFrame = clearTimeout;
function editorFor(body) {
  return new Editor({
    element: document.createElement("div"),
    extensions: extensions(),
    content: body,
    contentType: "markdown",
  });
}
test("unchanged rich documents round trip byte-for-byte", () => {
  for (const source of [
    "# Hello\n\nText with **bold**, *italics*, [a link](https://example.com).\n",
    "## Heading\r\n\r\nWindows line endings\r\n",
    "* one\n* two\n\n> Quote\n",
    "- [x] Done\n- [ ] Next\n",
    "| A | B |\n| --- | --- |\n| C | D |\n",
    "```mermaid\ngraph TD; A-->B\n```\n",
    "Before [[areas/topic|Topic]] and [[Note]].\n",
    '![Image](./image.png "Caption")\n',
    "\n\n# Spacing\n\n\nParagraph\n",
  ]) {
    const editor = editorFor(source),
      state = preservation(editor, source);
    assert.equal(serializePreserving(editor, state), source);
    editor.destroy();
  }
});
test("editing a paragraph preserves other blocks and wiki links", () => {
  const source =
    "# Original title\n\n* one\n* two\n\nRead [[areas/topic|Topic]].\n\nFinal paragraph.\n";
  const editor = editorFor(source),
    state = preservation(editor, source);
  editor.commands.insertContentAt(editor.state.doc.content.size - 1, " Added.");
  const result = serializePreserving(editor, state);
  assert.match(result, /\* one\n\* two/);
  assert.match(result, /\[\[areas\/topic\|Topic\]\]/);
  assert.match(result, /Final paragraph\. Added\./);
  editor.destroy();
});
test("YAML is kept verbatim and advanced syntax uses source mode", () => {
  const prefix = '---\r\ndescription: "Quoted"\r\ntags: [one, two]\r\n---\r\n';
  assert.deepEqual(splitFrontmatter(prefix + "# Note"), {
    prefix,
    body: "# Note",
  });
  for (const body of [
    "<details>\nHello\n</details>",
    "$$x^2$$",
    "A $x$ value",
    "[^1]: Footnote",
    "[ref]: https://example.com",
    ":::custom\nbody",
    "| A | B |\n| :--- | ---: |",
    "---\nbroken: metadata",
  ])
    assert.ok(sourceReason(body), body);
  assert.equal(sourceReason("```html\n<details>\n</details>\n```\n"), "");
});
test("active URL schemes are refused", () => {
  for (const url of [
    "javascript:alert(1)",
    "data:text/html,test",
    "//evil.test",
    "java\nscript:alert(1)",
    "\\evil.test",
  ])
    assert.equal(safeURL(url), false, url);
  for (const url of [
    "https://example.com",
    "../note.md",
    "#heading",
    "mailto:a@example.com",
  ])
    assert.equal(safeURL(url), true, url);
  assert.equal(safeURL("mailto:a@example.com", true), false);
});
test("GFM tables, tasks and wiki labels survive editing a neighbor", () => {
  const source =
    "| A | B |\n| --- | --- |\n| C | D |\n\n- [x] Done\n- [ ] Next\n\n[[Note|A label]]\n\nEnd\n";
  const editor = editorFor(source),
    state = preservation(editor, source);
  editor.commands.insertContentAt(editor.state.doc.content.size - 1, "!");
  const result = serializePreserving(editor, state);
  assert.ok(result.startsWith(source.slice(0, source.lastIndexOf("End"))));
  assert.match(result, /End!/);
  editor.destroy();
});

test("complete knowledge note preserves lists when editing the final quote", async () => {
  const { readFile } = await import("node:fs/promises");
  const go = await readFile("../editor_http_test.go", "utf8");
  const source = JSON.parse(go.match(/const editorSeed = (".*")/)[1]);
  const { body } = splitFrontmatter(source);
  const editor = editorFor("");
  editor.commands.setContent(body, {
    contentType: "markdown",
    emitUpdate: false,
  });
  const state = preservation(editor, body);
  editor.commands.insertContentAt(editor.state.doc.content.size - 2, " Added.");
  const result = serializePreserving(editor, state);
  assert.match(result, /\* Gather observations/);
  editor.destroy();
});
