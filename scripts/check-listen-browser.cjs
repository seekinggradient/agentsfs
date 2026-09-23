const {chromium}=require('playwright');
const fs=require('fs'), os=require('os'), path=require('path'), {spawn}=require('child_process');
const directory=fs.mkdtempSync(path.join(os.tmpdir(),'afs-listen-browser-'));
const fixturePath=path.join(directory,'fixture.json');
const server=spawn('go',['test','./internal/hub','-run','^TestListenBrowserFixture$','-count=1','-timeout=12m'],{cwd:path.resolve(__dirname,'..'),env:{...process.env,AFS_LISTEN_BROWSER_FIXTURE:fixturePath},stdio:['ignore','pipe','pipe']});
let serverOutput='';server.stdout.on('data',b=>serverOutput+=b);server.stderr.on('data',b=>serverOutput+=b);
let browser;
async function cleanup(){fs.writeFileSync(fixturePath+'.stop','');if(browser)await browser.close();setTimeout(()=>server.kill(),2000).unref();}
(async()=>{
 const deadline=Date.now()+30000;
 while(!fs.existsSync(fixturePath)){if(server.exitCode!==null||Date.now()>deadline)throw Error('Fixture failed: '+serverOutput);await new Promise(r=>setTimeout(r,100));}
 const fixture=JSON.parse(fs.readFileSync(fixturePath,'utf8'));
 browser=await chromium.launch({channel:process.env.CHROME_CHANNEL || 'chrome',headless:true});
 const context=await browser.newContext({viewport:{width:1280,height:900}});
 await context.addCookies([{name:fixture.cookie.Name,value:fixture.cookie.Value,url:fixture.url}]);
 const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
 let posts=[];page.on('request',r=>{if(r.url().endsWith('/speech'))posts.push(JSON.parse(r.postData()));});
 await page.goto(fixture.url+'/alice/brain/listen?path=01-reading.md');
 await page.waitForFunction(()=>document.querySelector('#listen-play').disabled===false);
 await page.waitForFunction(()=>document.querySelector('#listen-voice').options.length===3);
 if(posts.length)throw Error('Audio generated on page load');
 await page.screenshot({path:path.join(directory,'desktop.png'),fullPage:true});
 await page.locator('#listen-play').click();
 await page.waitForFunction(()=>document.querySelector('#listen-status').textContent.startsWith('Reading with'));
 await page.locator('#listen-play').click();
 if(await page.locator('#listen-play').innerText()!=='Play')throw Error('pause failed');
 const count=posts.length;await page.waitForTimeout(2200);if(posts.length!==count)throw Error('pause advanced playback');
 await page.locator('#listen-next').click();
 if(!/Passage 2 /.test(await page.locator('#listen-position').innerText()))throw Error('seek failed');
 await page.locator('#listen-voice').selectOption('Puck');
 await page.locator('#listen-play').click();
 await page.waitForFunction(()=>document.querySelector('#listen-status').textContent==='Reading with Puck.');
 if(!posts.some(p=>p.voice==='Puck' && p.index===1))throw Error('wrong voice/passage');
 await page.locator('#listen-play').click();
 await page.reload();await page.waitForFunction(()=>document.querySelector('#listen-play').disabled===false);
 if(!/Passage 2 /.test(await page.locator('#listen-position').innerText()))throw Error('resume lost position');
 if(await page.locator('#listen-voice').inputValue()!=='Puck')throw Error('resume lost voice');
 await page.locator('#listen-all').click();await page.locator('#listen-queue').click();
 await page.waitForFunction(()=>document.querySelector('#listen-position').textContent.includes('Page 1 of 2'));
 // Jump to the last passage and let actual HTMLAudioElement ended advance the queue.
 await page.locator('#listen-seek').evaluate(e=>{e.value=e.max;e.dispatchEvent(new Event('change',{bubbles:true}));});
 await page.locator('#listen-play').click();
 await page.waitForFunction(()=>document.querySelector('#listen-position').textContent.includes('Page 2 of 2'),null,{timeout:12000});
 await page.locator('#listen-play').click();
 await page.reload();await page.waitForFunction(()=>document.querySelector('#listen-position').textContent.includes('Page 2 of 2'));
 await page.setViewportSize({width:375,height:812});await page.reload();
 await page.waitForFunction(()=>document.querySelector('#listen-play').disabled===false);
 const width=await page.evaluate(()=>({w:innerWidth,scroll:document.documentElement.scrollWidth}));
 if(width.scroll>width.w)throw Error('mobile overflow '+JSON.stringify(width));
 await page.screenshot({path:path.join(directory,'mobile.png'),fullPage:true});
 // The current page/queue survive a reload after automatically advancing.
 if(!/Page 2 of 2/.test(await page.locator('#listen-position').innerText()))throw Error('playlist resume lost');
 for(const width of [320,768,1024]){await page.setViewportSize({width,height:900});if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('overflow at '+width);}
 // A delayed recording cannot restart playback after Pause.
 await page.locator('#listen-voice').selectOption('Aoede');
 await page.locator('#listen-play').click();await page.locator('#listen-play').click();
 await page.waitForTimeout(1000);
 if(await page.locator('#listen-play').innerText()!=='Play')throw Error('late audio resumed after pause');
 if(errors.length)throw Error(errors.join('\n'));
 console.log(JSON.stringify({passed:true,screenshots:directory,speechRequests:posts.length,mobileWidth:width,checks:['no load-time synthesis','pause','seek','voice selection','resume','automatic page transition','mobile containment','no JS errors']}));
 await cleanup();
})().catch(async e=>{console.error(e);await cleanup();process.exitCode=1;});
