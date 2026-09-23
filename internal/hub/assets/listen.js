/* Source-following narration. The server chooses speech from the exact source
   revision; this client never submits arbitrary text or holds a provider key. */
(function () {
  "use strict";
  var root = document.getElementById("listen-app");
  if (!root) return;
  var base = root.dataset.base, viewer = root.dataset.viewer;
  var el = function (id) { return document.getElementById("listen-" + id); };
  var pages = [], queue = [], pageIndex = 0, source = null, index = 0;
  var audio = null, audioURL = null, playing = false, epoch = 0, loading = false;
  var cache = new Map(), cacheBytes = 0, pending = new Map();
  var storageKey = "hub-listen-v1:" + viewer + ":" + base;
  var saved = null, retryAt = 0, voiceReady = false, voiceRequest = null;
  try { saved = JSON.parse(localStorage.getItem(storageKey)); } catch (_) {}
  if (saved && typeof saved.voice === "string") {
    var option = new Option(saved.voice, saved.voice); el("voice").replaceChildren(option);
  }
  if (saved && ["0.75", "1", "1.25", "1.5", "2"].includes(saved.speed)) el("speed").value = saved.speed;
  function status(text, error) {
    el("status").textContent = text; el("status").dataset.error = String(!!error);
    el("recovery").hidden = !error;
  }
  async function request(route, body) {
    var response = await fetch(base + "/" + route, {
      method: body ? "POST" : "GET", credentials: "same-origin", cache: "no-store",
      headers: body ? {"Content-Type":"application/json"} : {}, body: body ? JSON.stringify(body) : undefined,
      signal: AbortSignal.timeout(body ? 110000 : 35000)
    });
    var data;
    try { data = await response.json(); } catch (_) { throw new Error("The Hub could not complete this request. Please retry."); }
    if (!response.ok) {
      var error = new Error(data.error || "Could not load narration.");
      error.status = response.status;
      var retry = response.headers.get("Retry-After");
      error.retryMs = retry ? (Number.isFinite(Number(retry)) ? Number(retry) * 1000 : Math.max(0, Date.parse(retry) - Date.now())) : 0;
      throw error;
    }
    return data;
  }
  function remember() {
    if (!source) return;
    try { localStorage.setItem(storageKey, JSON.stringify({queue:queue, path:source.path, hash:source.hash, index:index, voice:el("voice").value, speed:el("speed").value})); } catch (_) {}
  }
  function stopAudio() {
    if (audio) { audio.onended = null; audio.pause(); audio.removeAttribute("src"); audio.load(); audio = null; }
    if (audioURL) { URL.revokeObjectURL(audioURL); audioURL = null; }
  }
  function stop() { epoch++; playing = false; stopAudio(); update(); }
  function update() {
    var ready = source && source.passages.length && !loading;
    el("play").disabled = !ready || !viewer;
    el("play").textContent = playing ? "Pause" : "Play";
    el("prev").disabled = !ready || (index === 0 && pageIndex === 0);
    el("next").disabled = !ready || (index >= source.passages.length - 1 && pageIndex >= queue.length - 1);
    el("seek").disabled = !ready;
    el("seek").max = ready ? source.passages.length - 1 : 0;
    el("seek").value = index;
    el("position").textContent = ready ? "Passage " + (index + 1) + " of " + source.passages.length + (queue.length > 1 ? " · Page " + (pageIndex + 1) + " of " + queue.length : "") : "";
    el("next-page").hidden = pageIndex >= queue.length - 1;
    el("article").querySelectorAll(".is-speaking").forEach(function (node) { node.classList.remove("is-speaking"); node.removeAttribute("aria-current"); });
    if (ready) {
      var chapter = 0; source.passages.forEach(function(p,i){if(p.heading && i<=index)chapter=i;}); el("chapters").value=String(chapter);
      var target = el("article").querySelector('[data-listen-target="' + source.passages[index].target + '"]');
      if (target) { target.classList.add("is-speaking"); target.setAttribute("aria-current", "true"); }
    }
  }
  function follow() {
    if (!source || !el("follow").checked) return;
    var target = el("article").querySelector('[data-listen-target="' + source.passages[index].target + '"]');
    if (target) target.scrollIntoView({block:"center", behavior:window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "instant" : "smooth"});
  }
  async function loadPage(p, autoplay, restore, last) {
    stop(); var token = epoch; loading = true; update(); status("Loading page…");
    try {
      var next = await request("source?path=" + encodeURIComponent(p));
      if (token !== epoch) return;
      source = next; retryAt = 0; index = 0; pageIndex = Math.max(0, queue.indexOf(p));
      var here = new URL(location.href); here.searchParams.set("path",p); history.replaceState(null,"",here);
      if (last) index = Math.max(0, source.passages.length - 1);
      if (restore && saved && saved.path === p && saved.hash === source.hash && Number.isInteger(saved.index)) index = Math.max(0, Math.min(saved.index, source.passages.length - 1));
      // This HTML is produced by Hub's safe Markdown renderer, never by TTS or an agent.
      el("article").innerHTML = source.html;
      el("article").querySelectorAll(".listen-passage").forEach(function (node) { node.tabIndex = 0; node.title = "Read from this passage"; });
      el("page-path").textContent = p;
      el("original").href = base.replace(/\/listen$/, "/blob/") + p.split("/").map(encodeURIComponent).join("/"); el("original").hidden = false;
      el("chapters").replaceChildren(new Option("Start of page", "0"));
      source.passages.forEach(function (passage, i) { if (passage.heading) el("chapters").append(new Option(passage.text, String(i))); });
      loading = false; update(); markPage(); remember();
      status(source.passages.length ? (restore && index ? "Ready to resume. Press Play." : "Ready. Press Play to hear this page.") : "This page has no readable text. Choose another page.");
      if (autoplay && source.passages.length) { playing = true; update(); await speak(); }
      else if (autoplay && pageIndex + 1 < queue.length) await loadPage(queue[pageIndex + 1], true, false);
    } catch (error) { if (token === epoch) { loading = false; source = null; update(); status(error.message, true); } }
  }
  async function voices() {
    if (voiceReady) return;
    if (voiceRequest) return voiceRequest;
    voiceRequest = (async function () {
      var data = await request("voices"), selected = el("voice").value;
      var values = (data.voices || []).filter(function (v) { return typeof v.name === "string" && v.usable !== false; });
      if (!values.length) throw new Error("No Gemini voices are available right now.");
      el("voice").replaceChildren();
      values.forEach(function (v) { el("voice").append(new Option(v.name + (v.description ? " — " + v.description : ""), v.name)); });
      el("voice").value = values.some(function(v){return v.name === selected;}) ? selected : values[0].name;
      voiceReady = true;
    })().finally(function(){voiceRequest=null;});
    return voiceRequest;
  }
  function wavBlob(data) {
    if (!data || typeof data.data !== "string" || data.data.length > 16000000 || !Number.isFinite(data.durationMs) || data.durationMs <= 0) throw new Error("Gemini returned invalid audio.");
    var match = /^audio\/L16;codec=pcm;rate=(\d+)$/.exec(data.mimeType || "");
    if (!match) throw new Error("Gemini returned an unsupported audio format.");
    var rate = Number(match[1]); if (rate < 8000 || rate > 96000) throw new Error("Invalid audio sample rate.");
    var binary = atob(data.data), length = binary.length;
    if (!length || length % 2) throw new Error("Gemini returned incomplete audio.");
    var buffer = new ArrayBuffer(44 + length), view = new DataView(buffer), bytes = new Uint8Array(buffer);
    function ascii(at, str) { for (var j=0;j<str.length;j++) bytes[at+j]=str.charCodeAt(j); }
    ascii(0,"RIFF"); view.setUint32(4,36+length,true); ascii(8,"WAVEfmt "); view.setUint32(16,16,true); view.setUint16(20,1,true); view.setUint16(22,1,true); view.setUint32(24,rate,true); view.setUint32(28,rate*2,true); view.setUint16(32,2,true); view.setUint16(34,16,true); ascii(36,"data"); view.setUint32(40,length,true);
    for (var i=0;i<length;i++) bytes[44+i]=binary.charCodeAt(i);
    return new Blob([buffer],{type:"audio/wav"});
  }
  async function recording(current, at, voice) {
    var key = current.path + ":" + current.hash + ":" + at + ":" + voice;
    if (cache.has(key)) return cache.get(key);
    if (pending.has(key)) return pending.get(key);
    var task = request("speech", {path:current.path,hash:current.hash,index:at,voice:voice}).then(function (result) {
      var blob = wavBlob(result.audio); cache.set(key,blob); cacheBytes+=blob.size;
      while (cacheBytes>16000000 || cache.size>24) { var oldest=cache.keys().next().value;cacheBytes-=cache.get(oldest).size;cache.delete(oldest); }
      return blob;
    }).finally(function(){pending.delete(key);});
    pending.set(key,task); return task;
  }
  async function speak() {
    var token = epoch, current = source, at = index;
    if (!playing || !current) return;
    status("Preparing Gemini voice…"); follow();
    try {
      await voices(); if (token !== epoch || !playing) return;
      var voice = el("voice").value;
      var currentRecording = recording(current, at, voice);
      // One passage of lookahead, only after Play. Bounded by the server's two
      // request allowance; a completed result survives pause/seek in the cache.
      if (at + 1 < current.passages.length) recording(current, at + 1, voice).catch(function () {});
      var blob = await currentRecording;
      if (token !== epoch || !playing) return;
      stopAudio(); audioURL = URL.createObjectURL(blob); audio = new Audio(audioURL); audio.playbackRate = Number(el("speed").value);
      audio.onended = function () { if (token === epoch && playing) advance(1, true); };
      await audio.play(); if (token !== epoch) return;
      status("Reading with " + el("voice").value + "."); remember();
    } catch (error) {
      if (token !== epoch || !playing) return;
      playing = false; update();
      retryAt = Date.now() + (error.retryMs || (error.name === "TimeoutError" ? 60000 : 0));
      status(error.name === "NotAllowedError" ? "Audio is ready. Press Play to start listening." : error.message, true);
    }
  }
  function toggle() {
    if (!playing && Date.now() < retryAt) { status("Please wait " + Math.ceil((retryAt-Date.now())/1000) + " seconds before retrying.", true); return; }
    if (playing) {
      playing = false;
      if (audio) audio.pause(); else epoch++;
      update(); status("Paused."); remember(); return;
    }
    if (!source || !source.passages.length) return;
    playing = true; update();
    if (audio && audio.paused && !audio.ended) {
      audio.play().then(function(){status("Reading with " + el("voice").value + ".");}).catch(function(e){playing=false;update();status(e.message,true);});
    } else speak();
  }
  function seek(at, autoplay) {
    stop(); retryAt = 0; index = Math.max(0,Math.min(at,source.passages.length-1));update();follow();remember();
    if (autoplay && viewer) {playing=true;update();speak();} else status("Ready at passage " + (index+1) + ".");
  }
  function advance(delta, autoplay) {
    if (!source) return;
    var next=index+delta;
    if (next>=0 && next<source.passages.length) {seek(next,autoplay);return;}
    var p=pageIndex+delta;
    if(p>=0 && p<queue.length) {loadPage(queue[p],autoplay,false,delta<0);return;}
    stop(); status("Finished reading.");remember();
  }
  function markPage() {el("page-list").querySelectorAll("button[data-path]").forEach(function(b){if(source && b.dataset.path===source.path)b.setAttribute("aria-current","page");else b.removeAttribute("aria-current");});}
  function buildPages() {
    el("page-list").replaceChildren();
    pages.forEach(function(p){
      var row=document.createElement("div");row.className="listen-page-row";row.dataset.path=p;
      var check=document.createElement("input");check.type="checkbox";check.value=p;check.checked=queue.includes(p);check.setAttribute("aria-label","Include "+p);
      var button=document.createElement("button");button.type="button";button.textContent=p;button.dataset.path=p;
      button.addEventListener("click",function(){queue=[p];el("page-list").querySelectorAll("input").forEach(function(c){c.checked=c.value===p;});loadPage(p,false,false);});row.append(check,button);el("page-list").append(row);
    });
    el("count").textContent="("+pages.length+")";markPage();
  }
  el("play").addEventListener("click",toggle);
  el("prev").addEventListener("click",function(){advance(-1,playing);});el("next").addEventListener("click",function(){advance(1,playing);});
  el("next-page").addEventListener("click",function(){if(pageIndex+1<queue.length)loadPage(queue[pageIndex+1],playing,false);});
  el("seek").addEventListener("change",function(){seek(Number(this.value),playing);});
  el("chapters").addEventListener("change",function(){if(source)seek(Number(this.value),playing);});
  el("speed").addEventListener("change",function(){if(audio)audio.playbackRate=Number(this.value);remember();});
  el("voice").addEventListener("change",function(){var resume=playing;stop();remember();if(resume){playing=true;update();speak();}else status("Voice changed. Press Play.");});
  el("follow").addEventListener("change",follow);
  el("retry").addEventListener("click",function(){if(Date.now()<retryAt){status("Please wait "+Math.ceil((retryAt-Date.now())/1000)+" seconds before retrying.",true);return;}if(!source){if(queue[pageIndex])loadPage(queue[pageIndex],false,false);else location.reload();return;}stop();playing=true;update();speak();});
  el("reload").addEventListener("click",function(){if(queue[pageIndex])loadPage(queue[pageIndex],false,false);else location.reload();});
  el("article").addEventListener("click",function(event){if(event.target.closest("a,button,input"))return;var node=event.target.closest(".listen-passage");if(node && source)seek(source.passages.findIndex(function(p){return p.target===node.dataset.listenTarget;}),playing);});
  el("article").addEventListener("keydown",function(event){if(event.key!=="Enter" || !event.target.classList.contains("listen-passage"))return;event.preventDefault();event.target.click();});
  el("all").addEventListener("click",function(){el("page-list").querySelectorAll("input").forEach(function(c){c.checked=true;});});
  el("none").addEventListener("click",function(){el("page-list").querySelectorAll("input").forEach(function(c){c.checked=false;});});
  el("filter").addEventListener("input",function(){var q=this.value.toLowerCase();el("page-list").querySelectorAll(".listen-page-row").forEach(function(row){row.hidden=!row.dataset.path.toLowerCase().includes(q);});});
  el("queue").addEventListener("click",function(){var chosen=Array.from(el("page-list").querySelectorAll("input:checked")).map(function(c){return c.value;});if(!chosen.length){status("Select at least one page.");return;}queue=chosen;loadPage(queue[0],false,false);});
  window.addEventListener("pagehide",function(){remember();stop();});
  (async function(){
    try {
      var data=await request("pages");pages=data.pages;
      var initial=root.dataset.initial;
      if(initial && pages.includes(initial) && (!saved || saved.path !== initial)) queue=[initial];
      else if(saved && Array.isArray(saved.queue))queue=saved.queue.filter(function(p){return pages.includes(p);});
      if (!queue.length && initial && pages.includes(initial)) queue=[initial];
      buildPages();
      if(window.matchMedia("(max-width: 760px)").matches && queue.length)el("pages").open=false;
      if(queue.length)await loadPage(initial && pages.includes(initial)?initial:(saved && queue.includes(saved.path)?saved.path:queue[0]),false,true);
      else status(pages.length?"Choose a page to begin.":"This workspace has no Markdown pages.");
      if(viewer)voices().catch(function(){status("Voice list unavailable. Press Play to retry.",false);});
    }catch(error){status(error.message,true);}
  })();
})();
