// The smallest DevTools-protocol client browser-arms.mjs needs: Node's global
// WebSocket (node --experimental-websocket), one browser context per person so
// two sessions never share localStorage, real mouse events, and screenshots.
// No dependency, so the gate runs from a fresh clone with only Chrome and Node.
import fs from "node:fs";

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export async function connect(port = 9333) {
  const v = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json();
  const ws = new WebSocket(v.webSocketDebuggerUrl);
  await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
  let id = 0;
  const pending = new Map();
  const listeners = [];
  ws.onmessage = (ev) => {
    const msg = JSON.parse(ev.data);
    if (msg.id && pending.has(msg.id)) {
      const { res, rej } = pending.get(msg.id);
      pending.delete(msg.id);
      msg.error ? rej(new Error(JSON.stringify(msg.error))) : res(msg.result);
    } else if (msg.method) {
      for (const l of listeners) l(msg);
    }
  };
  const send = (method, params = {}, sessionId) =>
    new Promise((res, rej) => {
      const mid = ++id;
      pending.set(mid, { res, rej });
      ws.send(JSON.stringify({ id: mid, method, params, ...(sessionId ? { sessionId } : {}) }));
    });
  return { send, listeners, close: () => ws.close() };
}

export async function newPage(b, { width = 1400, height = 1000 } = {}) {
  const { browserContextId } = await b.send("Target.createBrowserContext", { disposeOnDetach: true });
  const { targetId } = await b.send("Target.createTarget", { url: "about:blank", browserContextId });
  const { sessionId } = await b.send("Target.attachToTarget", { targetId, flatten: true });
  const s = (m, p) => b.send(m, p, sessionId);
  await s("Page.enable");
  await s("Runtime.enable");
  await s("Network.enable");
  const requests = [];
  b.listeners.push((msg) => {
    if (msg.sessionId === sessionId && msg.method === "Network.requestWillBeSent") {
      requests.push({ method: msg.params.request.method, url: msg.params.request.url, t: Date.now() });
    }
  });
  const page = {
    s, requests,
    async viewport(w, h) {
      await s("Emulation.setDeviceMetricsOverride", { width: w, height: h, deviceScaleFactor: 1, mobile: w < 600 });
    },
    async goto(url, wait = 1500) {
      await s("Page.navigate", { url });
      await sleep(wait);
    },
    async eval(expr) {
      const r = await s("Runtime.evaluate", { expression: expr, returnByValue: true, awaitPromise: true });
      if (r.exceptionDetails) throw new Error(JSON.stringify(r.exceptionDetails).slice(0, 400));
      return r.result.value;
    },
    async waitFor(expr, timeout = 10000) {
      const end = Date.now() + timeout;
      while (Date.now() < end) {
        try { if (await page.eval(expr)) return true; } catch {}
        await sleep(250);
      }
      return false;
    },
    text() { return page.eval("document.body.innerText"); },
    async has(str) { return (await page.text()).includes(str); },
    // Real mouse events at the centre of the first element matching the JS
    // finder expression (Radix ignores a synthetic click).
    // Waits up to eight seconds for the element: a list behind a query renders
    // its controls a beat after the tab that holds it.
    async clickWhere(finder, label = finder) {
      let rect = null;
      for (let i = 0; i < 32 && !rect; i++) {
        rect = await page.eval(`(() => { const el = (${finder}); if (!el) return null; el.scrollIntoView({block: "center"}); const r = el.getBoundingClientRect(); return {x: r.x + r.width / 2, y: r.y + r.height / 2}; })()`);
        if (!rect) await sleep(250);
      }
      if (!rect) throw new Error(`nothing to click: ${label}`);
      await sleep(150);
      const rect2 = await page.eval(`(() => { const el = (${finder}); const r = el.getBoundingClientRect(); return {x: r.x + r.width / 2, y: r.y + r.height / 2}; })()`);
      for (const type of ["mouseMoved", "mousePressed", "mouseReleased"]) {
        await s("Input.dispatchMouseEvent", { type, x: rect2.x, y: rect2.y, button: "left", clickCount: 1 });
      }
      await sleep(600);
    },
    clickText(text, sel = "button,[role=tab],a,[role=option],[role=menuitem],[role=menuitemcheckbox]") {
      return page.clickWhere(`[...document.querySelectorAll(${JSON.stringify(sel)})].find(e => e.innerText && e.innerText.trim() === ${JSON.stringify(text)})`, text);
    },
    clickLabel(label) {
      return page.clickWhere(`document.querySelector(${JSON.stringify(`[aria-label="${label}"]`)})`, label);
    },
    async shot(path, full = true) {
      let clip;
      if (full) {
        const m = await s("Page.getLayoutMetrics");
        const w = Math.ceil(m.cssContentSize?.width ?? m.contentSize.width);
        const h = Math.min(Math.ceil(m.cssContentSize?.height ?? m.contentSize.height), 6000);
        clip = { x: 0, y: 0, width: w, height: h, scale: 1 };
      }
      const r = await s("Page.captureScreenshot", { format: "png", ...(clip ? { clip, captureBeyondViewport: true } : {}) });
      fs.writeFileSync(path, Buffer.from(r.data, "base64"));
    },
    async login(origin, token, user) {
      await page.goto(origin + "/login", 1200);
      await page.eval(`localStorage.setItem("argentum-auth", ${JSON.stringify(JSON.stringify({ state: { accessToken: token, user }, version: 0 }))}); true`);
    },
  };
  await page.viewport(width, height);
  return page;
}

export { sleep };
