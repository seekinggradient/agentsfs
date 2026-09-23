// Deterministic scheduler tests: run the real reader functions with delayed TTS.
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const code = fs.readFileSync(require('node:path').join(__dirname,'../internal/hub/assets/listen.js'),'utf8');
function fixture() {
  const nodes = new Map(), calls = [], sounds = [];
  let active = 0, peak = 0;
  function node(id) {
    if (!nodes.has(id)) nodes.set(id,{dataset:{base:'/listen',viewer:'alice'},value:id==='listen-voice'?'Kore':'1',checked:false,
      addEventListener(){},querySelectorAll(){return []},querySelector(){return null}});
    return nodes.get(id);
  }
  const ctx = {document:{getElementById:node},localStorage:{getItem(){},setItem(){}},window:{addEventListener(){}},
    URL:{createObjectURL(){return 'blob:test'},revokeObjectURL(){}},Blob,ArrayBuffer,DataView,Uint8Array,atob,
    AbortSignal,Option:function(){},console,
    Audio:class {constructor(){sounds.push(this)} play(){this.paused=false;return Promise.resolve()} pause(){this.paused=true} removeAttribute(){} load(){}},
    fetch(url,opts){assert.equal(url,'/listen/speech');active++;peak=Math.max(peak,active);
      return new Promise(resolve=>calls.push({body:JSON.parse(opts.body),finish(ok=true){active--;resolve({ok,status:ok?200:429,headers:{get(){return null}},json:async()=>ok?{audio:{data:'AAAAAA==',durationMs:1,mimeType:'audio/L16;codec=pcm;rate=24000'}}:{error:'Slow down'}})}}));}
  };
  const cut = code.lastIndexOf('  (async function(){');
  vm.runInNewContext(code.slice(0,cut)+`globalThis.test={
    start(){source={path:'page.md',hash:'one',passages:Array.from({length:12},(_,i)=>({target:String(i)}))};playing=true;voiceReady=true;},
    speak,stop,toggle,seek,
    changeVoice(){stop();el('voice').value='Puck';playing=true;return speak(true);},
    get index(){return index;}
  };})();`,ctx);
  ctx.test.start();
  return {api:ctx.test,calls,sounds,node,peak:()=>peak};
}
const tick=()=>new Promise(r=>setImmediate(r));
(async()=>{
  const f=fixture();const start=f.api.speak(true);await tick();
  assert.deepEqual(f.calls.map(x=>x.body.index),[0,1]);
  f.calls[0].finish();await tick();assert.equal(f.sounds.length,0,'must build startup cushion');
  f.calls[1].finish();await tick();assert.equal(f.sounds.length,0);
  f.calls[2].finish();await tick();await start;assert.equal(f.sounds.length,1);
  assert.deepEqual(f.calls.map(x=>x.body.index),[0,1,2,3,4],'four ahead scheduled');
  f.calls[3].finish();f.calls[4].finish();await tick();
  f.sounds[0].onended();await tick();
  assert.equal(f.api.index,1);assert.equal(f.sounds.length,2,'cached transition starts immediately');
  assert.equal(f.calls.at(-1).body.index,5,'buffer refills');
  assert.equal(f.peak(),2);
  f.api.toggle();const before=f.calls.length;f.calls.at(-1).finish();await tick();assert.equal(f.calls.length,before,'pause stops refill');

  const g=fixture();const old=g.api.speak(true);await tick();const changed=g.api.changeVoice();await tick();
  assert.equal(g.calls.length,2,'new voice waits for old in-flight work');
  g.calls[0].finish();g.calls[1].finish();await tick();
  assert.deepEqual(g.calls.slice(2).map(x=>x.body.voice),['Puck','Puck']);
  assert.equal(g.calls.filter(x=>x.body.voice==='Kore').length,2,'old queued passages cancelled');
  g.api.stop();g.calls[2].finish();g.calls[3].finish();await tick();await Promise.all([old,changed]);
  assert.equal(g.calls.length,4);assert.equal(g.sounds.length,0);assert.equal(g.peak(),2);

  const j=fixture();const js=j.api.speak(true);await tick();j.api.seek(8,true);await tick();
  j.calls[0].finish();j.calls[1].finish();await tick();
  assert.deepEqual(j.calls.slice(2).map(x=>x.body.index),[8,9],'seek prioritizes new position');
  j.api.stop();j.calls[2].finish();j.calls[3].finish();await tick();await js;
  assert.equal(j.calls.length,4,'seek drops queued old position');

  const h=fixture();const hs=h.api.speak(true);await tick();h.calls[0].finish();h.calls[1].finish(false);await tick();
  h.calls[2].finish();await tick();await hs;
  h.api.seek(1,true);await tick();
  assert.equal(h.calls.filter(x=>x.body.index===1).length,1,'failed prefetch is not retried blindly');
  assert.equal(h.node('listen-play').textContent,'Play','failed foreground pauses');
  for(const call of h.calls.slice(3)) call.finish();await tick();
  console.log('Passed: startup cushion, rolling refill, cached transitions, two-request limit, pause, voice reset, failure cooldown.');
})().catch(e=>{console.error(e);process.exitCode=1;});
