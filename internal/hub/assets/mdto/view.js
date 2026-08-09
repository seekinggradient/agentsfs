/* The Markdown To view: everything the Hub's thin page does in the browser.
 *
 * The Hub is renderer-ignorant. It detected one frontmatter key (`markdownto:`),
 * served the file's exact bytes base64'd into the page, and loaded the pinned
 * engine beside this file. From here on, the real Markdown To renderers decide
 * what the document looks like — this script only feeds them and mounts what
 * comes back.
 *
 * Three rules it must not break:
 *
 * 1. **The bytes are the file.** They arrive base64-encoded in a data attribute
 *    precisely so no markup escaping stands between the commit and the parser:
 *    what renders here is byte-identical to what a `git clone` gets.
 * 2. **The output is never trusted with this origin.** Every rendered document
 *    goes into the iframe through `srcdoc`, whose `sandbox` attribute is
 *    authored in the HTML and never touched here — no `allow-scripts`, no
 *    `allow-same-origin`. The frame runs at an opaque origin with no script.
 * 3. **A broken file still gets an honest page.** A parse error is not an error
 *    state to hide behind a spinner; the validation report IS the read-only view
 *    of a non-conforming file, and it renders down the same sandboxed path.
 */
(function () {
  "use strict";

  var source = document.getElementById("mdto-source");
  var frame = document.getElementById("mdto-stage");
  var status = document.getElementById("mdto-status");
  if (source === null || frame === null) {
    return;
  }

  var filename = source.getAttribute("data-name") || "document.md";

  function fail(message) {
    if (status !== null) {
      status.textContent = message;
      status.hidden = false;
    }
    frame.hidden = true;
  }

  /* base64 -> bytes -> UTF-8 text. The round trip is deliberate: the attribute
     carries the blob's bytes, not a string the server re-encoded, so a file with
     a BOM, an emoji, or CRLF line endings reaches the parser unchanged. */
  function decode(b64) {
    var binary = atob(b64);
    var bytes = new Uint8Array(binary.length);
    for (var i = 0; i < binary.length; i++) {
      bytes[i] = binary.charCodeAt(i);
    }
    return new TextDecoder("utf-8").decode(bytes);
  }

  function errorsIn(result) {
    return result.diagnostics.filter(function (d) {
      return d.severity === "error";
    });
  }

  /* Which document to build. The live board (the only view that runs script) is
     deliberately absent: this slice is read-only, so a kanban file renders
     through renderHtml, which draws the board as a static document. */
  function documentFor(result) {
    if (errorsIn(result).length > 0) {
      return { html: MDTO.renderDiagnosticsHtml(result, { filename: filename }), mode: "validation report" };
    }
    var doc = result.document;
    return {
      html: MDTO.renderHtml(result, { filename: filename }),
      mode: doc && doc.spec === "kanban" ? "board" : doc && doc.spec === "todo" ? "checklist"
        : result.audio ? "manuscript" : result.backlog ? "backlog" : "document"
    };
  }

  if (typeof MDTO === "undefined" || typeof MDTO.parse !== "function") {
    fail("The Markdown To engine could not be loaded, so this file is not rendered here. It is plain Markdown — read it as Markdown, or download it.");
    return;
  }

  var text;
  try {
    text = decode(source.getAttribute("data-b64") || "");
  } catch (err) {
    fail("This file could not be read as text here. Download it to open it elsewhere.");
    return;
  }

  var page;
  try {
    page = documentFor(MDTO.parse(text));
  } catch (err) {
    fail("The Markdown To engine could not render this file. It is plain Markdown — read it as Markdown, or download it.");
    return;
  }

  frame.srcdoc = page.html;
  var mode = document.getElementById("mdto-mode");
  if (mode !== null) {
    mode.textContent = page.mode;
  }
})();
