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
 *    goes into an iframe through `srcdoc`, and all three sandbox literals are
 *    authored in the HTML — never assembled here. None carries
 *    `allow-same-origin`, so the frame is always an opaque origin: it cannot
 *    read this page, its DOM, this Hub's cookies, or its storage. The plain
 *    read-only frame has no `allow-scripts` either and runs nothing at all; the
 *    two that do run it — a board that saves, and a guided reader that does
 *    not — are told apart by which element the Hub served the literal on.
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
 *
 * `#mdto-guided` is the other half of that idea and is not a privilege at all:
 * the Hub emits it for every viewer of a guided-narration manuscript, because a
 * player that cannot run is not a read-only view of one. It buys `allow-scripts`
 * and nothing else — no save URL, no hash, and no path from here into the
 * writeback loop below.
 *
 * It may also carry a recording, and that is the one place this script fetches
 * something the frame is not allowed to. A guided reader at an opaque origin
 * whose `connect-src 'self'` matches nothing can neither fetch audio nor sign
 * in for it; this page can do both, so it reads the committed narration over
 * /raw/ and hands it across base64'd inside the handshake. Bytes cross the
 * boundary; the frame's reach does not.
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
  /* The Hub's one statement about this FILE: is its view a player? Present for a
     guided narration and for nothing else, and it carries the third sandbox
     literal — `allow-scripts` for a frame that will never save a byte. */
  var guided = document.getElementById("mdto-guided");
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
    if (result.guidedNarration) {
      /* A guided narration is a script, not a board. It carries no `document`
         and no `backlog` today, so this is belt to that suspenders — but it is
         the one spec whose view RUNS without being writable, and "it runs"
         must never be allowed to drift into "it saves". */
      return false;
    }
    return errorsIn(result).length === 0 && !!(result.document || result.backlog);
  }

  /* The source article the Hub resolved beside a guided manuscript, or null.
     `data-guided-source-ref` is the `source:` string the Hub resolved it FROM;
     the reader will echo the engine's own parse of that same string out of the
     same bytes, and the two must agree exactly before this page claims to be
     holding the article the reader is asking for. If they do not — or if the
     Hub resolved nothing — the answer is null, the bridge is never switched on,
     and the reader draws its own paste/open form the moment it loads. That is
     the one failure this handshake must not have: a host that asks the reader to
     wait and then never answers leaves it empty forever. */
  function guidedArticle(doc) {
    if (guided === null || !doc || typeof doc.source !== "string") {
      return null;
    }
    var ref = source.getAttribute("data-guided-source-ref");
    var b64 = source.getAttribute("data-guided-source-b64");
    if (ref === null || b64 === null || ref !== doc.source) {
      return null;
    }
    try {
      /* One object, built once: the reply the handshake sends verbatim. A
         later phase that hands the reader its per-beat recordings as well adds
         a field here and nowhere else. */
      return { source: doc.source, saved: { format: source.getAttribute("data-guided-source-format") === "json" ? "json" : "markdown", text: decode(b64) } };
    } catch (err) {
      return null;
    }
  }

  /* ----------------------------------------------------------------------
     The recording, fetched here because the reader may not fetch anything
     ---------------------------------------------------------------------- */

  /* markdownto 0.3.1 lets a HOST hand the guided reader a finished narration on
     `guided-restore`, and on this Hub that is the preferred way to reuse an already-recorded narration. Missing recordings use
     the session-authenticated /listen speech bridge below. The reader runs in a sandboxed srcdoc frame: opaque
     origin, no cookies, a `connect-src 'self'` that matches nothing. It cannot fetch the MP3s sitting
     three directories from the manuscript, and the sign-in it would otherwise
     offer leads nowhere from in there. THIS script is first-party on the Hub's
     own origin with the viewer's session, so it does the fetching and hands over
     bytes.

     The spec allows the other arrangement too — entries naming a `url` that the
     reader page fetches for itself — and it is deliberately not taken. Those
     fetches would come from the guided frame, at an opaque origin that no
     'self' matches, so taking it would mean NAMING THIS HOST in the guided
     page's connect-src: handing ~300 KB of vendored renderer a browser-blessed
     channel back to this Hub, carrying nothing but the atob this page is
     already doing for the article. Instead the page's directive is a bare
     'self' — enough for this script, nothing for the frame — and the bytes
     cross as base64. The bytes are the same bytes either way; only what the
     frame is allowed differs, so that wins.

     Everything below is best-effort by construction. A beat whose file will not
     fetch is dropped from the recording and read by the computer voice instead;
     an index that will not parse costs the whole recording and nothing else. The
     one outcome that must not happen is a restore that never arrives — a reader
     told to wait and then ignored sits on an empty page forever — so the article
     is sent whatever the audio does, and a deadline guarantees "whatever" has an
     answer. */

  /* Beats in flight at once. The reader wants them in manuscript order but does
     not need them in that sequence, and 54 parallel requests for one page is not
     politeness. */
  var BEATS_IN_FLIGHT = 6;
  /* How long the reader is made to wait for its voice. Past this the restore goes
     with whatever arrived — which may be nothing, and nothing is still a reading. */
  var RECORDING_DEADLINE_MS = 30000;

  /* The reader matches a recorded passage to a beat by its whitespace-collapsed
     narration, so this must be the same collapse the producer wrote into the
     index and the player performs on the manuscript: one regex, no trimming of
     anything else. */
  function collapse(value) {
    return String(value === null || value === undefined ? "" : value).replace(/\s+/g, " ").trim();
  }

  /* Bytes to base64, in chunks, because `String.fromCharCode.apply` on a 300 KB
     array is how you find the engine's argument limit. */
  function base64Of(buffer) {
    var bytes = new Uint8Array(buffer);
    var binary = "";
    for (var i = 0; i < bytes.length; i += 0x8000) {
      binary += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
    }
    return btoa(binary);
  }

  /* One /raw/ read. Same-origin and credentialed for the same reason the save
     loop is: this is the Hub's own page asking the Hub for a blob the viewer has
     already been let through the read gate for. A private instance answers 404
     to a stranger here exactly as it does everywhere else. */
  function fetchRaw(url, signal) {
    return fetch(url, { credentials: "same-origin", cache: "no-store", signal: signal }).then(function (res) {
      if (!res.ok) {
        throw new Error("raw " + res.status);
      }
      return res;
    });
  }

  /* One index entry -> one thing to fetch, or null for an entry that could not be
     played anyway. `durationMs` is checked here rather than left to the reader on
     purpose: the reader validates a recording whole and refuses ALL of it over one
     malformed passage, so a single bad row must be dropped on this side of the
     wire or it takes the other fifty-three with it. */
  function beatRequest(dir, beat) {
    if (!beat || typeof beat !== "object") {
      return null;
    }
    var text = collapse(beat.text);
    var file = typeof beat.file === "string" ? beat.file : "";
    var ms = typeof beat.durationMs === "number" ? beat.durationMs : 0;
    if (text === "" || file === "" || file.indexOf("/") !== -1 || !isFinite(ms) || ms <= 0) {
      return null;
    }
    return { text: text, url: dir + encodeURIComponent(file), durationMs: ms };
  }

  /* The beats, bounded. Order is preserved by writing into the slot a request came
     from rather than by the order the network answers in — the reader is told the
     recording is in manuscript order and the index is what says what that order is. */
  function fetchBeats(requests, mimeType, signal) {
    var audio = new Array(requests.length);
    var next = 0;
    function worker() {
      if (next >= requests.length) {
        return Promise.resolve();
      }
      var slot = next++;
      var request = requests[slot];
      return fetchRaw(request.url, signal).then(function (res) {
        return res.arrayBuffer();
      }).then(function (buffer) {
        if (buffer.byteLength > 0) {
          audio[slot] = {
            text: request.text,
            audioBase64: base64Of(buffer),
            mimeType: mimeType,
            durationMs: request.durationMs
          };
        }
      }, function () {
        /* Gone, forbidden, aborted, or an LFS pointer this Hub would not resolve.
           This beat keeps the computer voice; the reading keeps everything else. */
      }).then(worker);
    }
    var workers = [];
    for (var i = 0; i < BEATS_IN_FLIGHT && i < requests.length; i++) {
      workers.push(worker());
    }
    return Promise.all(workers).then(function () {
      return audio.filter(function (entry) {
        return !!entry;
      });
    });
  }

  /* The recording this page will offer, or null. Never rejects: every refusal is
     the same answer, "read it in the computer voice". */
  function guidedRecording() {
    if (guided === null) {
      return Promise.resolve(null);
    }
    var href = guided.getAttribute("data-guided-audio");
    if (href === null || href === "") {
      return Promise.resolve(null);
    }
    var voice = guided.getAttribute("data-guided-voice") || "";
    /* The index names its beats by bare filename, beside itself. */
    var dir = href.slice(0, href.lastIndexOf("/") + 1);
    var controller = typeof AbortController === "function" ? new AbortController() : null;
    var signal = controller === null ? undefined : controller.signal;
    var deadline = setTimeout(function () {
      if (controller !== null) {
        controller.abort();
      }
    }, RECORDING_DEADLINE_MS);

    return fetchRaw(href, signal).then(function (res) {
      return res.json();
    }).then(function (index) {
      /* The contract by name. Anything else at this path is somebody else's file
         and is not going to be turned into speech on a guess. */
      if (!index || index.markdownto !== "guided-narration-audio@0.1" ||
          !Array.isArray(index.beats) || index.beats.length === 0) {
        throw new Error("not a guided-narration-audio@0.1 index");
      }
      var mimeType = typeof index.mimeType === "string" && index.mimeType !== "" ? index.mimeType : "audio/mpeg";
      var requests = [];
      for (var i = 0; i < index.beats.length; i++) {
        var request = beatRequest(dir, index.beats[i]);
        if (request !== null) {
          requests.push(request);
        }
      }
      if (requests.length === 0) {
        throw new Error("the index names no playable beat");
      }
      return fetchBeats(requests, mimeType, signal).then(function (audio) {
        if (audio.length === 0) {
          return null;
        }
        return {
          version: 1,
          /* The Hub's label first: it comes off the manifest the Hub validated and
             is the string the audio strip on this same page is already showing. */
          voice: voice || (typeof index.voice === "string" ? index.voice : "") || "Recorded voice",
          audio: audio
        };
      });
    }).then(function (recording) {
      clearTimeout(deadline);
      return recording;
    }, function () {
      clearTimeout(deadline);
      return null;
    });
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
    if (guided !== null && result.guidedNarration) {
      /* The guided reader. `chrome` is left alone — the loader only ever asks
         for `embedded` around a live board, and this is the same read-only
         render every other viewer of this file gets, one that happens to run.
         `guidedSourceBridge` is set only when there is an article to hand over:
         with it the reader announces itself and waits, and without it the
         reader starts on its own intake form instead. */
      var article = guidedArticle(result.guidedNarration);
      if (article) article.document = result.guidedNarration;
      return {
        html: MDTO.renderHtml(result, { filename: filename, guidedSourceBridge: article !== null }),
        mode: article !== null ? "guided reader" : "guided reader · source not found",
        live: false,
        scripted: true,
        guide: article
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
  /* The frame that is currently a running guided reader, or null. The handshake
     below accepts messages from this exact window and no other. */
  var reader = null;

  function mount(page) {
    if (!page.live && !page.scripted) {
      stage.srcdoc = page.html;
      board = null;
      reader = null;
      return;
    }
    /* A sandbox is read when the document loads, so the widened attribute has to
       go on a FRESH element — the same reason the playground replaces its frame
       rather than editing one. The literal is read out of the HTML; this script
       never composes one, so it cannot widen a page the Hub did not widen.

       Two elements can carry one: #mdto-live for a board that saves, and
       #mdto-guided for a reader that runs and does not. Neither literal
       contains `allow-same-origin`, and this line is the only place either is
       ever read. */
    var widened = page.live ? live : guided;
    var next = document.createElement("iframe");
    next.className = stage.className;
    next.id = stage.id;
    next.title = stage.title;
    next.setAttribute("sandbox", widened.getAttribute("data-sandbox") || "allow-downloads");
    /* The feature delegation, also authored in the HTML. The guided reader puts
       the article in a frame of its own and starts speaking there after a
       click; without the permission arriving from here it has none to pass on. */
    var allow = widened.getAttribute("data-allow");
    if (allow !== null && allow !== "") {
      next.setAttribute("allow", allow);
    }
    next.srcdoc = page.html;
    stage.replaceWith(next);
    stage = next;
    board = page.live ? next : null;
    reader = page.scripted ? next : null;
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

  /* ----------------------------------------------------------------------
     The guided reader's handshake
     ---------------------------------------------------------------------- */

  /* The reader posts `guided-ready` AS IT LOADS and then does nothing until it
     is answered, so this listener is installed BEFORE the frame is mounted.
     Installing it afterwards would be a race the page loses silently: the
     reader would sit on an empty paste box forever and no error would say why.

     Two checks, and they are the writeback loop's first two for the same
     reasons. The frame is an opaque origin, so `event.origin` is the string
     "null" and is worth nothing as a test — the identity of the window is the
     check that means something. The source string must be the one this page
     resolved an article for, so a message about some other guide is not
     answered with this one's bytes.

     `"*"` is the target origin because there is no other value that can reach
     an opaque origin. What travels is the article this Hub already served to
     this reader in the same response, into a frame this page created and holds
     the only reference to.

     Voice requests use the existing Hub session on first-party /listen routes.
     The frame receives audio bytes and capability results, never a token. */
  if (page.guide) {
    /* The download starts HERE, before the frame is mounted, because the reader is
       going to sit still until it is answered either way and there is nothing to
       be gained by making those two waits consecutive. */
    var signedIn = !!guided.getAttribute("data-viewer");
    var guidedAudio = MDTO.createGuidedAudioHost({
      isAuthenticated: function () { return signedIn; },
      request: async function (route, init) {
        var base = guided.getAttribute("data-listen-base");
        var options = { method: init.method, signal: init.signal, credentials: "same-origin", cache: "no-store" };
        var endpoint;
        if (route === "/v1/narrate/voices") endpoint = base + "/voices";
        else if (route === "/v1/narrate/synthesize") {
          var payload = JSON.parse(init.body);
          endpoint = base + "/speech";
          options.headers = {"Content-Type":"application/json"};
          options.body = JSON.stringify({path:guided.getAttribute("data-listen-path"), hash:guided.getAttribute("data-listen-hash"), index:0, voice:payload.voice, text:payload.text});
        } else throw new Error("Unsupported narration operation.");
        var response = await fetch(endpoint, options);
        if (response.status === 401) signedIn = false;
        return response;
      }
    });
    var recording = guidedRecording();
    var answered = false;

    window.addEventListener("message", function (event) {
      if (reader === null || event.source !== reader.contentWindow) {
        return;
      }
      var data = event.data;
      if (!data || typeof data !== "object" || data.source !== page.guide.source) {
        return;
      }
      if (data.mdto === "guided-auth") {
        if (!signedIn) window.location.assign("/login");
        return;
      }
      if (data.mdto === "guided-audio" && typeof data.id === "string" && data.id.length <= 100 && data.message) {
        var recipient = event.source;
        guidedAudio(data.message, page.guide.document).then(function (result) {
          if (reader && recipient === reader.contentWindow) recipient.postMessage({mdto:"guided-audio-result", id:data.id, result:result}, "*");
        });
        return;
      }
      /* What the reader made of the recording, put where it can be seen: a person
         with the inspector open, and a test that cannot reach inside an opaque
         origin to ask. `refused` is the interesting one — it means this page built
         something the reader would not take, and the page is still perfectly
         readable in the computer voice, so nothing else would ever say so. */
      if (data.mdto === "guided-recording-ready" || data.mdto === "guided-recording-refused") {
        markRecording(data);
        return;
      }
      if (data.mdto !== "guided-ready" || answered) {
        return;
      }
      answered = true;
      var target = reader;
      recording.then(function (audio) {
        if (target !== reader || target.contentWindow === null) {
          return;
        }
        /* One reply, built from the object guidedArticle() made, with the audio
           added only when there is audio. The reader restarts its whole run on
           every `guided-restore` it accepts, so this is sent once. */
        var saved = page.guide.saved;
        if (audio !== null) {
          saved = { format: saved.format, text: saved.text, recording: audio };
        }
        target.contentWindow.postMessage({
          mdto: "guided-restore",
          source: page.guide.source,
          saved: saved
        }, "*");
      });
    });
  }

  /* The reader's verdict, on the element the Hub put the recording's address on —
     `data-guided-recording="ready"` with `data-guided-beats`, or `"refused"` with
     the reason it gave — and in the mode chip beside the file's name, which is the
     one line on this page a reader actually looks at. */
  function markRecording(data) {
    var ready = data.mdto === "guided-recording-ready";
    if (guided !== null) {
      guided.setAttribute("data-guided-recording", ready ? "ready" : "refused");
      if (ready && typeof data.beats === "number") {
        guided.setAttribute("data-guided-beats", String(data.beats));
      }
      if (!ready && typeof data.reason === "string") {
        guided.setAttribute("data-guided-refused", data.reason);
      }
    }
    var chip = document.getElementById("mdto-mode");
    if (chip === null) {
      return;
    }
    if (!ready) {
      chip.textContent = page.mode + " · computer voice";
      return;
    }
    var beats = typeof data.beats === "number" ? data.beats : 0;
    chip.textContent = page.mode + " · recorded narration" +
      (beats > 0 ? " · " + beats + " beat" + (beats === 1 ? "" : "s") : "");
  }

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
