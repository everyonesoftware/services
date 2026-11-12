// Real production bundles and service worker, served from isolated build copies.
// Only authenticated API reads are browser fixtures; no Docker/provider writes.
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { cp, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { join, extname } from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'
import { buildPwa } from '../scripts/build-pwa.mjs'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const directory = await mkdtemp(join(process.env.TMPDIR || tmpdir(), 'services-webapp-update-'))
const screenshots = process.env.WEBAPP_SCREENSHOT_DIR
if (screenshots) await mkdir(screenshots, {recursive:true})
const roots = Object.fromEntries(['a','b','c'].map(name=>[name,join(directory,name)]))
const ids = {}
for(const name of ['a','b','c']) {
  await cp(fileURLToPath(new URL('../dist',import.meta.url)), roots[name], {recursive:true})
  if(name !== 'a') {
    let html = await readFile(join(roots[name],'index.html'),'utf8')
    html = html.replace('</head>',`<link rel="stylesheet" href="/assets/update-proof.css"><meta name="fixture-build" content="${name}"></head>`)
    await writeFile(join(roots[name],'index.html'),html)
    await writeFile(join(roots[name],'assets/update-proof.css'),`:root { --fixture-build: ${name}; }`)
  }
  await buildPwa(roots[name])
  ids[name] = JSON.parse(await readFile(join(roots[name],'web-build.json'),'utf8')).id
}
assert.notEqual(ids.a,ids.b); assert.notEqual(ids.b,ids.c)
let serving='a', metadataMode='valid', delay=0
const requests=[], writes=[]
const user={id:'browser-fixture',username:'Browser fixture',role:'admin'}
const api={
  '/api/auth/status':{setupRequired:false}, '/api/auth/me':{user},
  '/api/auth/users':{users:[user]}, '/api/auth/keys':{keys:[]},
  '/api/projects':{projects:[]}, '/api/operations':{operations:[]}, '/api/icons':{available:false,icons:[]},
  '/api/monitor':{connected:true,lastChecked:new Date().toISOString(),error:null,incidents:[],events:[],notificationConfigured:false},
}
const server=createServer(async(req,res)=>{
  const path=new URL(req.url,'http://localhost').pathname
  requests.push({path,method:req.method})
  if(!['GET','HEAD'].includes(req.method)) writes.push(`${req.method} ${path}`)
  res.setHeader('Cache-Control','no-store')
  if(path.startsWith('/api/')) {
    res.setHeader('Content-Type','application/json')
    res.statusCode=Object.hasOwn(api,path)?200:404
    res.end(JSON.stringify(api[path]??{error:'Unexpected fixture request'})); return
  }
  if(path==='/web-build.json') {
    if(delay) await new Promise(resolve=>setTimeout(resolve,delay))
    if(metadataMode==='error') {res.statusCode=503;res.end('{}');return}
    if(metadataMode==='html') {res.setHeader('Content-Type','text/html');res.end('<html>Sign in</html>');return}
  }
  try {
    assert.ok(!decodeURIComponent(path).split('/').includes('..'))
    const bytes=await readFile(join(roots[serving],path==='/'?'index.html':decodeURIComponent(path).slice(1)))
    res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css','.json':'application/json','.webmanifest':'application/manifest+json','.png':'image/png'})[extname(path)]||(path==='/'?'text/html':'application/octet-stream'))
    res.end(bytes)
  } catch {res.statusCode=404;res.end('Not found')}
})
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve))
const origin=`http://127.0.0.1:${server.address().port}`
const browser=await chromium.launch({channel:process.env.BROWSER_CHANNEL||'msedge',headless:true})
try {
  for(const viewport of [{width:1440,height:900},{width:390,height:844},{width:320,height:560}]) {
    serving='a';metadataMode='valid';delay=0;requests.length=0;writes.length=0
    const context=await browser.newContext({viewport,serviceWorkers:'allow'})
    const page=await context.newPage(),errors=[]
    page.on('pageerror',error=>errors.push(error.message))
    page.setDefaultTimeout(10000)
    await page.goto(`${origin}/#monitor`)
    await page.getByRole('link',{name:'Settings',exact:true}).click()
    await page.getByRole('heading',{name:'Web app updates',exact:true}).waitFor()
    await page.evaluate(async()=>{
      await navigator.serviceWorker.ready
      if(!navigator.serviceWorker.controller) await new Promise(resolve=>navigator.serviceWorker.addEventListener('controllerchange',resolve,{once:true}))
      window.__updateMarker='unchanged'
    })
    const panel=page.locator('.webapp-update-card')
    const status=name=>panel.getByRole('heading',{name,exact:true})
    const check=panel.getByRole('button',{name:'Check for updates',exact:true})
    const apply=panel.getByRole('button',{name:'Update and reload',exact:true})
    const modal=page.getByRole('dialog',{name:'Reload to update?',exact:true})
    const current=()=>panel.locator('dd').first().getAttribute('title')
    const unchanged=async()=>assert.equal(await page.evaluate(()=>window.__updateMarker),'unchanged','No unapproved reload')
    const sections=page.getByRole('navigation',{name:'Settings sections',exact:true})
    const layout=async()=>assert.deepEqual(await page.evaluate(()=>({
      document:document.documentElement.scrollWidth<=document.documentElement.clientWidth,
      content:document.querySelector('.app-content').scrollWidth<=document.querySelector('.app-content').clientWidth,
      dialog:!document.querySelector('dialog').open||document.querySelector('dialog').scrollWidth<=document.querySelector('dialog').clientWidth,
    })),{document:true,content:true,dialog:true})
    const capture=async suffix=>{await layout();if(screenshots)await page.screenshot({path:join(screenshots,`${viewport.width}-${suffix}.png`)})}
    assert.equal(await current(),ids.a)
    assert.equal(requests.filter(r=>r.path==='/web-build.json').length,0,'No automatic frontend checks')
    delay=250
    await check.click()
    await status('Checking for updates…').waitFor()
    assert.equal(await panel.getByRole('button',{name:'Checking…',exact:true}).isDisabled(),true)
    await status('You’re up to date').waitFor();delay=0
    await capture('current')
    metadataMode='error';await check.click();await status('Couldn’t check for updates').waitFor()
    assert.equal(await apply.isVisible(),false)
    metadataMode='html';await check.click();await status('Couldn’t check for updates').waitFor()
    assert.match(await panel.textContent(),/Invalid web build response/)
    metadataMode='valid';serving='b'
    await check.click();await status('Update available').waitFor()
    assert.equal(await current(),ids.a);await unchanged()
    // Real account draft survives section changes and postponed/escaped reloads.
    await sections.getByRole('button',{name:'Administrators',exact:true}).click()
    await page.getByRole('textbox',{name:'Username',exact:true}).fill('Unsubmitted fixture draft')
    await sections.getByRole('button',{name:'Updates',exact:true}).click()
    await status('Update available').waitFor()
    await capture('available')
    await apply.click();await modal.waitFor()
    assert.equal(await modal.getByRole('button',{name:'Later',exact:true}).evaluate(el=>document.activeElement===el),true)
    await modal.getByText('Finish or save any unsaved changes before reloading. Your current view will close.',{exact:true}).waitFor()
    await capture('confirmation')
    await modal.getByRole('button',{name:'Later',exact:true}).click()
    await unchanged();assert.equal(await current(),ids.a)
    assert.equal(await apply.evaluate(el=>document.activeElement===el),true)
    await apply.click();await page.keyboard.press('Escape');assert.equal(await modal.isVisible(),false)
    await sections.getByRole('button',{name:'Administrators',exact:true}).click()
    assert.equal(await page.getByRole('textbox',{name:'Username',exact:true}).inputValue(),'Unsubmitted fixture draft')
    await sections.getByRole('button',{name:'Updates',exact:true}).click()
    // Offline check and offline confirmation never navigate or replay an action.
    await context.setOffline(true);await check.click();await status('You’re offline').waitFor()
    assert.equal(await apply.isVisible(),false);await unchanged()
    await context.setOffline(false);await check.click();await status('Update available').waitFor()
    await apply.click();await context.setOffline(true)
    await modal.getByRole('button',{name:'Update and reload',exact:true}).click()
    await status('You’re offline').waitFor();await unchanged()
    await context.setOffline(false);await check.click();await status('Update available').waitFor()
    // A failed fresh read before reload retains the current app and permits retry.
    metadataMode='error';await apply.click();await modal.getByRole('button',{name:'Update and reload',exact:true}).click()
    await status('Couldn’t reload the web app').waitFor();await unchanged();assert.equal(await current(),ids.a)
    await capture('reload-failure')
    // If the server rolls back, don't reload the already-current app.
    metadataMode='valid';serving='a';await apply.click();await modal.getByRole('button',{name:'Update and reload',exact:true}).click()
    await status('You’re up to date').waitFor();await unchanged()
    serving='b';await check.click();await status('Update available').waitFor()
    // A new target while confirmation is open must be reviewed again.
    await apply.click();serving='c';await modal.getByRole('button',{name:'Update and reload',exact:true}).click()
    await status('Update available').waitFor();await panel.getByText('A different web app build is now available. Review it again before reloading.',{exact:true}).waitFor()
    await unchanged()
    await apply.click()
    await Promise.all([page.waitForEvent('load'),modal.getByRole('button',{name:'Update and reload',exact:true}).click()])
    await page.getByRole('heading',{name:'Web app updates',exact:true}).waitFor()
    assert.equal(await current(),ids.c,'Actual page reload must acquire new generated HTML identity')
    await status('Web app updated').waitFor()
    assert.equal(await page.evaluate(()=>window.__updateMarker),undefined,'Actual document was replaced')
    assert.equal(await page.evaluate(()=>getComputedStyle(document.documentElement).getPropertyValue('--fixture-build').trim()),'c','New stylesheet loaded')
    assert.equal(await page.evaluate(()=>location.hash),'#settings')
    await check.click();await status('You’re up to date').waitFor()
    await capture('updated')
    const cache=await page.evaluate(async()=>{
      const entries=[]
      for(const key of await caches.keys())entries.push({key,paths:(await(await caches.open(key)).keys()).map(req=>new URL(req.url).pathname)})
      return entries
    })
    assert.equal(cache.length,1);assert.match(cache[0].key,/^services-offline-/);assert.deepEqual(cache[0].paths,['/offline.html'])
    assert.deepEqual(writes,[],'Web app updates must never invoke a mutation endpoint')
    assert.deepEqual(errors,[])
    console.log(`PASS production web app ${viewport.width}x${viewport.height}: current ${ids.a} -> ${ids.c}; manual uncached checks, errors/offline/retry, Later/Escape/draft/focus, stale target and rollback preflight, real reload/new CSS, offline-only cache; zero backend mutations`)
    await context.close()
  }
} finally {
  await browser.close()
  await new Promise(resolve=>server.close(resolve))
  await rm(directory,{recursive:true,force:true})
}
