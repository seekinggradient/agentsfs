/* The Markdown To view: everything the Hub's thin page does in the browser.
 *
 * The Hub is renderer-ignorant. It detected one frontmatter key (`markdownto:`),
 * served the file's exact bytes base64'd into the page, and loaded the pinned
 * engine beside this file. From here on, the real Markdown To renderers decide
 * what the document looks like — this script only feeds them, mounts what comes
 * back, and (when the Hub said this viewer may write) carries their edits home.
 *
 * Four rules it must not break:
 *
 * 1. **The bytes are the file.** They arrive base64-encoded in a data attribute
 *    precisely so no markup escaping stands between the commit and the parser:
 *    what renders here is byte-identical to what a `git clone` gets. The same
 *    holds in the other direction — the board hands over the exact bytes its
 *    session is holding, and those bytes are what is PUT back.
 * 2. **The output is never trusted with this origin.** Every rendered document
 *    goes into an iframe through `srcdoc`, and both sandbox literals are
 *    authored in the HTML — never assembled here. Neither carries
 *    `allow-same-origin`, so the frame is always an opaque origin: it cannot
 *    read this page, its DOM, this Hub's cookies, or its storage. The read-only
 *    frame has no `allow-scripts` either and runs nothing at all.
 * 3. **A broken file still gets an honest page.** A parse error is not an error
 *    state to hide behind a spinner; the validation report IS the read-only view
 *    of a non-conforming file, and it renders down the same sandboxed path.
 * 4. **A save never overwrites blind.** Every PUT carries `If-Match` with the
 *    hash of the bytes the board was drawn from — the patch engine's own
 *    `sourceHash`. A 412 stops the loop and shows the conflict; it never
 *    retries with `*` and never resends.
 *
 * The live half exists only when `#mdto-live` does, and the Hub emits that
 * element only for a viewer with write access on an authenticated instance
 * page. A share link, a reader, and an anonymous visitor reach the bottom of
 * this file having run the same code they always did.
 */
(function () {
  "use strict";

  var source = document.getElementById("mdto-source");
  var stage = document.getElementById("mdto-stage");
  var status = document.getElementById("mdto-status");
  if (source === null || stage === null) {
    return;
  }

  /* The Hub's one statement about this viewer: may they write? Absent = no, and
     every live branch below is guarded on it. */
  var live = document.getElementById("mdto-live");
  var filename = source.getAttribute("data-name") || "document.md";

  function fail(message) {
    if (status !== null) {
      status.textContent = message;
      status.hidden = false;
    }
    stage.hidden = true;
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

  /* Whether this file has a live, writable view.
   *
   * No spec name is tested here and none is written down in the Hub either: the
   * engine puts a task spec's IR in `document` (kanban, todo) or in `backlog`,
   * and a manuscript in `narrate`, so this is the same one-line question the
   * playground asks. A spec the bundle grows tomorrow gets a board here without
   * an edit, and one it never draws keeps the static view. */
  function isLive(result) {
    return errorsIn(result).length === 0 && !!(result.document || result.backlog);
  }

  /* Which document to build. The board is offered only when the page can save
     it: a viewer who cannot write gets the static render of the same file, which
     is the honest read-only view of a board and always has been. */
  function documentFor(text, result) {
    if (errorsIn(result).length > 0) {
      return { html: MDTO.renderDiagnosticsHtml(result, { filename: filename }), mode: "validation report", live: false };
    }
    if (live !== null && typeof MDTO.renderBoard === "function" && isLive(result)) {
      var doc = result.document;
      return {
        /* `embedded` because this page already says it: the bar above carries
           the file's name, the spec it declares, and the ways out of the view.
           The option only ever removes a second printing — the counts, the
           change note and the diagnostics are the board's own and stay. */
        html: MDTO.renderBoard(text, filename, "embedded"),
        mode: result.backlog ? "live backlog" : doc && doc.spec === "todo" ? "live checklist" : "live board",
        live: true
      };
    }
    var d = result.document;
    return {
      html: MDTO.renderHtml(result, { filename: filename }),
      mode: d && d.spec === "kanban" ? "board" : d && d.spec === "todo" ? "checklist"
        : result.narrate ? "manuscript" : result.backlog ? "backlog" : "document",
      live: false
    };
  }

  /* The frame that is currently a running board, or null. Everything the
     writeback loop accepts is checked against this exact window. */
  var board = null;

  function mount(page) {
    if (!page.live) {
      stage.srcdoc = page.html;
      board = null;
      return;
    }
    /* A sandbox is read when the document loads, so the widened attribute has to
       go on a FRESH element — the same reason the playground replaces its frame
       rather than editing one. The literal is read out of the HTML; this script
       never composes one, so it cannot widen a page the Hub did not widen. */
    var next = document.createElement("iframe");
    next.className = stage.className;
    next.id = stage.id;
    next.title = stage.title;
    next.setAttribute("sandbox", live.getAttribute("data-sandbox") || "allow-downloads");
    next.srcdoc = page.html;
    stage.replaceWith(next);
    stage = next;
    board = next;
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
    page = documentFor(text, MDTO.parse(text));
  } catch (err) {
    fail("The Markdown To engine could not render this file. It is plain Markdown — read it as Markdown, or download it.");
    return;
  }

  /* What the file holds, as far as this page knows. It starts as the bytes the
     Hub served and moves only when the board says it moved. */
  var mountedSource = text;

  mount(page);
  var mode = document.getElementById("mdto-mode");
  if (mode !== null) {
    mode.textContent = page.mode;
  }

  if (!page.live) {
    return;                       /* read-only: nothing below this line runs */
  }

  /* ----------------------------------------------------------------------
     The writeback loop
     ---------------------------------------------------------------------- */

  var saveURL = live.getAttribute("data-save") || "";
  /* The If-Match the next save will carry. It starts as the hash of the bytes
     on this page and is replaced by the hash the Hub reports back, so a run of
     mutations never needs a round trip through GET. */
  var held = live.getAttribute("data-hash") || "";

  var saveTag = document.getElementById("mdto-save");
  var conflictPanel = document.getElementById("mdto-conflict");
  var reloadButton = document.getElementById("mdto-reload");
  var takeLink = document.getElementById("mdto-take");

  var pending = null;             /* the newest text not yet sent */
  var inFlight = false;
  var halted = false;             /* a conflict stops the loop for good */

  function setSave(state, label) {
    if (saveTag === null) {
      return;
    }
    saveTag.setAttribute("data-state", state);
    saveTag.textContent = label;
  }

  /* The board's newest bytes, offered as a file. A conflict must not cost
     somebody the move they just made, and this page is the only place those
     bytes exist. */
  function offerUnsaved(latest) {
    if (takeLink !== null) {
      takeLink.href = "data:text/markdown;charset=utf-8," + encodeURIComponent(latest);
    }
  }

  function conflict(body, latest) {
    halted = true;
    pending = null;
    setSave("conflict", "not saved");
    offerUnsaved(latest);
    if (conflictPanel !== null) {
      conflictPanel.hidden = false;
      conflictPanel.scrollIntoView({ block: "nearest" });
    } else {
      fail("This file changed somewhere else, so your last move was not saved. Reload to pick the board up where the file is now.");
    }
    if (body && typeof body.why === "string" && status !== null && conflictPanel !== null) {
      status.textContent = body.why + ".";
      status.hidden = false;
    }
  }

  if (reloadButton !== null) {
    reloadButton.addEventListener("click", function () {
      location.reload();
    });
  }

  /* One save at a time, newest text wins. A drag can land several mutations
     before the first commit answers, and sending them in parallel would race the
     If-Match against itself: each would name a hash the one before it had
     already replaced. Coalescing is not a compromise here — the file the person
     is looking at is the last one, and it is the one that gets committed. */
  function pump() {
    if (halted || inFlight || pending === null) {
      return;
    }
    var text = pending;
    pending = null;
    inFlight = true;
    setSave("saving", "saving…");

    fetch(saveURL, {
      method: "PUT",
      /* Same-origin, first-party: the Hub's own session cookie is the
         credential, exactly as it is for the note editor's form post. The Hub
         additionally refuses a write whose Origin is not its own. */
      credentials: "same-origin",
      cache: "no-store",
      headers: {
        "Content-Type": "text/markdown; charset=utf-8",
        "If-Match": '"' + held + '"'
      },
      body: text
    }).then(function (res) {
      return res.json().then(function (body) {
        return { res: res, body: body };
      }, function () {
        return { res: res, body: {} };
      });
    }).then(function (out) {
      inFlight = false;
      if (out.res.ok && typeof out.body.hash === "string") {
        held = out.body.hash;
        setSave("saved", "saved");
        pump();
        return;
      }
      /* 412 is the file having moved underneath this board; 428 is this page
         having lost the hash it must name. Neither is retryable by resending,
         and neither may be escalated to an unconditional overwrite. */
      if (out.res.status === 412 || out.res.status === 428) {
        conflict(out.body, text);
        return;
      }
      setSave("error", "not saved");
      offerUnsaved(text);
      if (status !== null) {
        status.textContent = (out.body && out.body.error ? out.body.error : "the hub answered " + out.res.status) +
          ". Your next move will try again; take a copy of the file if you would rather not lose this one.";
        status.hidden = false;
      }
    }, function () {
      inFlight = false;
      setSave("error", "not saved");
      offerUnsaved(text);
      if (status !== null) {
        status.textContent = "The hub could not be reached, so that move is not committed yet. Your next move will try again.";
        status.hidden = false;
      }
    });
  }

  /* The board posts its whole file out after every mutation — the exact bytes
     its session is holding, handed over by the bridge built into the pinned
     bundle (site/tools/build-app.mjs in the markdownto repo wraps
     `renderSourceLines`, which is the wire and not only the source panel's
     renderer).

     Three checks stand between that and a commit, and none is ceremonial:

     1. `event.source` identity, and only that. The frame has an opaque origin,
        so `event.origin` is the string "null" for it and is worth nothing as a
        test. This is the check that means something, and it is exact.
     2. The shape, which keeps every other message a page might receive — an
        extension, another frame, an opener — out of the file.
     3. The echo drop. A fresh frame's bridge has posted nothing yet, so its
        FIRST render posts the bytes it was mounted from: a quotation, not an
        edit. Committing it would put an identical-bytes commit in the log for
        every board anybody ever opened.

     The playground has a fourth check — the typist wins — for the race between
     its textarea and the frame. There is no editor on this page, so there is no
     such race and no such check: the board is the only writer here. */
  window.addEventListener("message", function (event) {
    if (board === null || event.source !== board.contentWindow) {
      return;
    }
    var data = event.data;
    if (!data || typeof data !== "object") {
      return;
    }
    /* The board forwards Escape out so a host can leave a presentation. This
       page has no present mode and no drawer to close, so the message is read,
       recognised, and deliberately dropped — silently doing something with a
       key nobody pressed here would be the surprise. */
    if (data.mdto === "key") {
      return;
    }
    if (data.mdto !== "source" || typeof data.source !== "string") {
      return;
    }
    if (data.source === mountedSource) {
      return;                     /* the frame quoting what we sent it */
    }
    mountedSource = data.source;
    offerUnsaved(data.source);
    if (halted) {
      return;                     /* the board keeps working; this page stopped saving */
    }
    pending = data.source;
    pump();
  });
})();
