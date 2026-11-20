// Drives headless Chrome over CDP: opens a page, sends trusted mouse-wheel events, reads the probe's status line.
import { spawn } from 'node:child_process';
const url = process.argv[2];
const chrome = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const proc = spawn(chrome, ['--headless=new', '--remote-debugging-port=9333', '--user-data-dir=/tmp/cdp-profile', '--window-size=600,800', '--enable-smooth-scrolling', 'about:blank'], { stdio: 'ignore' });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let ws;
for (let i = 0; i < 50; i++) {
  try { const r = await fetch('http://127.0.0.1:9333/json/list'); const t = (await r.json()).find((x) => x.type === 'page'); if (t) { ws = new WebSocket(t.webSocketDebuggerUrl); break; } } catch {}
  await sleep(200);
}
await new Promise((r) => ws.addEventListener('open', r));
let id = 0; const pending = new Map();
ws.addEventListener('message', (m) => { const d = JSON.parse(m.data); if (pending.has(d.id)) { pending.get(d.id)(d.result); pending.delete(d.id); } });
const send = (method, params = {}) => new Promise((r) => { pending.set(++id, r); ws.send(JSON.stringify({ id, method, params })); });
const status = async () => (await send('Runtime.evaluate', { expression: `[...document.querySelectorAll('*')].map(e=>e.childNodes.length===1&&e.firstChild.nodeType===3?e.textContent:'').find(t=>t.startsWith('ends='))`, returnByValue: true })).result.value;
const trace = async () => (await send('Runtime.evaluate', { expression: `JSON.stringify(window.__ys||[])`, returnByValue: true })).result.value;
await send('Page.enable');
await send('Page.navigate', { url });
await sleep(4000);
// Record every frame's scrollTop of the outer view (the biggest scroller)
await send('Runtime.evaluate', { expression: `(()=>{const s=[...document.querySelectorAll('.nsh-scroll')].sort((a,b)=>b.clientHeight-a.clientHeight)[0];window.__ys=[];const t0=performance.now();const f=()=>{window.__ys.push([Math.round(performance.now()-t0),Math.round(s.scrollTop*10)/10]);requestAnimationFrame(f)};requestAnimationFrame(f);return s.clientHeight})()`, returnByValue: true });
const wheel = (x, y, dy) => send('Input.dispatchMouseEvent', { type: 'mouseWheel', x, y, deltaX: 0, deltaY: dy });
console.log('start', await status());
// 1. one notch below the inner view (outer scrolls natively)
await wheel(300, 500, 100); await sleep(800);
console.log('after 1 notch on outer', await status());
// 2. three quick notches
for (let i = 0; i < 3; i++) { await wheel(300, 500, 100); await sleep(60); }
await sleep(800);
console.log('after 3 quick notches', await status());
const ys = JSON.parse(await trace()); const moves = []; let last = null; for (const [t, y] of ys) { if (y !== last) { moves.push(`${t}:${y}`); last = y; } }
console.log('frames (t:y)', moves.slice(0, 40).join(' '));
// 3. wheel over the inner view: inner scrolls (innermost)
await send('Runtime.evaluate', { expression: `document.querySelectorAll('.nsh-scroll').forEach(s=>s.scrollTop=0)` }); await sleep(500);
const before = await status();
await wheel(300, 120, 100); await sleep(800);
console.log('wheel over inner', before, '->', await status());
// 4. inner at its end, new wheel over it: nsh picks the outer
await send('Runtime.evaluate', { expression: `(()=>{const s=[...document.querySelectorAll('.nsh-scroll')].sort((a,b)=>a.clientHeight-b.clientHeight)[0];s.scrollTop=s.scrollHeight})()` }); await sleep(800);
const b2 = await status();
await wheel(300, 120, 100); await sleep(800);
console.log('inner at end, wheel over it', b2, '->', await status());
ws.close(); proc.kill();
