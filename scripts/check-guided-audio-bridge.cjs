// Exercise Hub's actual adapter against the pinned engine, without paid synthesis.
const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const context = vm.createContext({TextEncoder,TextDecoder,URL,AbortSignal,Date,document:{createElement(){return {set innerHTML(v){this.textContent=v;}};}}});
vm.runInContext(fs.readFileSync('internal/hub/assets/mdto/mdto.js','utf8'),context);
const source=fs.readFileSync('internal/hub/assets/mdto/view.js','utf8');
const start=source.indexOf('    var signedIn =');
const end=source.indexOf('    var recording = guidedRecording();',start);
assert.ok(start>=0&&end>start,'Hub voice adapter missing');
const attrs={'data-viewer':'alice','data-listen-base':'/alice/brain/listen','data-listen-path':'tour.md','data-listen-hash':'version'};
context.guided={getAttribute:k=>attrs[k]};
const requests=[];
context.fetch=async(url,init)=>{
 requests.push({url,init});
 return new Response(JSON.stringify(url.endsWith('/voices')?{voices:[{name:'Kore'}]}:{audio:{data:'AAAAAA==',mimeType:'audio/L16;codec=pcm;rate=24000',durationMs:10}}),{status:200});
};
vm.runInContext(source.slice(start,end),context);
const guide={chapters:[{beats:[{text:'An authored passage.'}]}]};
(async()=>{
 const health=await context.guidedAudio({type:'npe:health'},guide);
 assert.equal(health.data.authenticated,true);
 assert.equal(health.data.tts.available,true);
 assert.equal(requests.length,0,'opening a guide should not buy audio');
 const rejected=await context.guidedAudio({type:'npe:speech',payload:{text:'Injected speech'}},guide);
 assert.equal(rejected.ok,false);assert.equal(requests.length,0);
 const audio=await context.guidedAudio({type:'npe:speech',payload:{text:'An authored passage.'}},guide);
 assert.equal(audio.ok,true);assert.equal(audio.data.audioBase64,'AAAAAA==');
 assert.deepEqual(requests.map(r=>r.url),['/alice/brain/listen/voices','/alice/brain/listen/speech']);
 assert.equal(requests[1].init.credentials,'same-origin');
 assert.deepEqual(JSON.parse(requests[1].init.body),{path:'tour.md',hash:'version',index:0,voice:'Kore',text:'An authored passage.'});
 context.signedIn=false;
 assert.equal((await context.guidedAudio({type:'npe:health'},guide)).data.authenticated,false);
 assert.equal((await context.guidedAudio({type:'npe:speech',payload:{text:'An authored passage.'}},guide)).ok,false);
 console.log('Hub guided audio uses its session, rejects un-authored speech, and carries no credential into the frame.');
})().catch(e=>{console.error(e);process.exitCode=1});
