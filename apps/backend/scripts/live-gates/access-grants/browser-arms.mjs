// Roadmap 12's live gate (RIZ-25): the browser arms of live-gate-backlog.md §7c,
// driven over the DevTools protocol against the dashboard dev server — Settings →
// Team end to end and at 390 px, the member's picker and room menu, the
// restricted dashboard embed, Knowledge, the paused link and the approvals badge.
//
//   node --experimental-websocket browser-arms.mjs <screenshot-dir>
//
// Needs headless Chrome on CDP_PORT (default 9333), the dashboard on WEB_URL
// (default http://localhost:5173) proxying to the API on API_URL (default
// http://localhost:8080), and a **fresh seed**: it restricts HR, Payroll and
// Payroll 2026.pdf as it goes and leaves them so. README.md has the whole recipe.
// One PASS/FAIL line per arm and a screenshot per step; exits non-zero on a FAIL.
//
// It logs in by writing the dashboard's own session into localStorage — what the
// login form stores — and clicks with real mouse events, because a Radix tab or
// select ignores a synthetic click().
import fs from "node:fs";
import { connect, newPage, sleep } from "./cdp.mjs";

const OUT = process.argv[2];
if (!OUT) {
  console.error("usage: browser-arms.mjs <screenshot-dir>");
  process.exit(2);
}
fs.mkdirSync(OUT, { recursive: true });
const WEB = process.env.WEB_URL ?? "http://localhost:5173";
const API = process.env.API_URL ?? "http://localhost:8080";
for (const u of [WEB, API]) {
  if (!/^http:\/\/(localhost|127\.0\.0\.1)(:\d+)?$/.test(u) && process.env.FORCE !== "1") {
    console.error(`refusing ${u}: the browser arms write, and only run against this machine (FORCE=1 overrides)`);
    process.exit(2);
  }
}
const ids = {
  M: "a0000000-0001-4000-8000-000000000002",
  A: "a0000000-0001-4000-8000-000000000001",
  HR: "a0000000-0002-4000-8000-000000000001",
  PAYROLL: "a0000000-0003-4000-8000-000000000001",
  PAYROLL_PDF: "a0000000-0006-4000-8000-000000000001",
  T_HR: "a0000000-0008-4000-8000-000000000001",
  T_FIN: "a0000000-0008-4000-8000-000000000006",
  DOC_HR: "a0000000-000a-4000-8000-000000000001",
};
const MEMBER = "riz25-member@example.test";
const results = [];
const record = (arm, pass, detail = "") => {
  results.push({ arm, pass, detail });
  console.log(`${pass ? "PASS" : "FAIL"} ${arm}${detail ? " — " + detail : ""}`);
};

async function login(email) {
  const r = await fetch(`${API}/api/auth/login`, {
    method: "POST", headers: { "content-type": "application/json" },
    body: JSON.stringify({ email, password: "riz25-scratch-pass" }),
  });
  const j = await r.json();
  return { token: j.access_token, user: { ...j.user, company_name: "RIZ-25 Gate A" } };
}
async function api(token, method, path, body) {
  const r = await fetch(`${API}${path}`, {
    method, headers: { authorization: `Bearer ${token}`, "content-type": "application/json" },
    body: body ? JSON.stringify(body) : undefined,
  });
  let j = null; try { j = await r.json(); } catch {}
  return { status: r.status, body: j };
}

// The API allows sixty requests a minute per company, and every page here makes
// a dozen; a page that met a 429 renders its error state, which is not what an
// arm is asking about. So each navigation waits for what the page should show,
// and on a miss waits for the bucket to refill and loads again — the same retry
// arms.sh makes on a 429.
async function open(page, url, ready, after) {
  for (let attempt = 0; attempt < 4; attempt++) {
    await page.goto(url, 2500);
    if (after) {
      try { await after(); } catch {}
    }
    if (await page.waitFor(ready, 10000)) return true;
    await sleep(20000);
  }
  return false;
}
const teamTab = (page) => () => page.clickText("Team", "[role=tab]");

const b = await connect(Number(process.env.CDP_PORT ?? 9333));
const admin = await login("riz25-admin@example.test");
const member = await login(MEMBER);

const A = await newPage(b);
const M = await newPage(b);
await A.login(WEB, admin.token, admin.user);
await M.login(WEB, member.token, member.user);

// ---------------------------------------------------------------- T-Z7 as A
await open(A, `${WEB}/settings`, `document.body.innerText.includes("Restrict Finance…") && !!document.querySelector('[aria-label="Access for ${MEMBER}"]')`, teamTab(A));
await A.shot(`${OUT}/01-team-open.png`);
{
  const agentsOpen = await A.eval(`(() => { const t = document.body.innerText; return ["HR","Finance","Ops"].every(n => t.includes("Restrict " + n + "…")); })()`);
  const nobody = await A.eval(`(document.body.innerText.match(/Nobody is granted it\\./g) || []).length`);
  record("T-Z7 Team lists HR, Finance and Ops open, each with a Restrict control, nobody granted", agentsOpen && nobody >= 3, `"Nobody is granted it." ×${nobody}`);
}
await A.clickLabel(`Access for ${MEMBER}`);
await A.waitFor(`!!document.querySelector('[aria-label="HR granted to ${MEMBER}"]')`);
const reqBeforeTick = A.requests.length;
await A.clickLabel(`HR granted to ${MEMBER}`);
const chip = await A.waitFor(`!!document.querySelector('[aria-label="Revoke HR from ${MEMBER}"]')`, 8000);
const navigated = A.requests.slice(reqBeforeTick).some((r) => r.url.startsWith(WEB) && !r.url.includes("/api/") && !r.url.includes("/@") && !r.url.includes("/src/") && !r.url.includes("node_modules"));
record("T-Z7 ticking HR for M puts a chip on HR's row with no reload", chip && !navigated, `chip=${chip} documentReload=${navigated}`);
await A.shot(`${OUT}/02-chip-after-tick.png`);

// Restrict HR…: the warning, then Cancel sends nothing.
await A.clickText("Restrict HR…");
await A.waitFor(`!!document.querySelector('[role=alertdialog][aria-label="Restrict HR"]')`);
const warning = await A.eval(`document.querySelector('[role=alertdialog][aria-label="Restrict HR"]').innerText`);
await A.shot(`${OUT}/03-restrict-hr-warning.png`);
record("T-Z7 Restrict HR… warns, naming who loses it, the admin as (you)", /\(you\)|you included/i.test(warning), warning.replace(/\s+/g, " ").slice(0, 300));
const reqBeforeCancel = A.requests.length;
await A.clickWhere(`[...document.querySelectorAll('[role=alertdialog] button')].find(e => e.innerText.trim() === "Cancel")`, "Cancel");
await sleep(1200);
const afterCancel = A.requests.slice(reqBeforeCancel).filter((r) => r.method !== "GET");
record("T-Z7 Cancel sends nothing (network)", afterCancel.length === 0, JSON.stringify(afterCancel));
await A.clickText("Restrict HR…");
await A.clickWhere(`[...document.querySelectorAll('[role=alertdialog] button')].find(e => e.innerText.trim() === "Restrict HR")`, "Restrict HR");
const flipped = await A.waitFor(`document.body.innerText.includes("You are not granted HR")`, 8000);
const putSent = A.requests.slice(reqBeforeCancel).some((r) => r.method === "PUT" && r.url.includes(`/api/access/agent/${ids.HR}/mode`));
record("T-Z7 Restrict flips the badge and says \"You are not granted HR\"", flipped && putSent, `PUT sent=${putSent}`);
await A.shot(`${OUT}/04-hr-restricted.png`);

// Voice for M.
await A.clickLabel(`Voice for ${MEMBER}`);
await sleep(1200);
const caps = await api(admin.token, "GET", `/api/users/${ids.M}/capabilities`);
record("T-Z7 ticking Voice for M → GET /api/users/<m>/capabilities lists it", JSON.stringify(caps.body).includes("voice"), JSON.stringify(caps.body).slice(0, 200));

// ---------------------------------------------------------------- as M, granted
await open(M, `${WEB}/chat`, `!!document.querySelector('[aria-label="Agent"]')`);
await M.clickLabel("Agent");
await sleep(600);
const pickerGranted = await M.eval(`[...document.querySelectorAll('[role=option]')].map(e => e.innerText.split("\\n")[0].trim())`);
await M.shot(`${OUT}/05-member-picker-granted.png`, false);
record("T-Z7 M, granted: HR is in the picker", pickerGranted.includes("HR"), JSON.stringify(pickerGranted));
await M.s("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
await sleep(300);

// A revokes from the chip; M's next message on HR is refused and the HR
// conversation leaves the list on its next refetch.
await A.clickLabel(`Revoke HR from ${MEMBER}`);
await A.waitFor(`!document.querySelector('[aria-label="Revoke HR from ${MEMBER}"]')`, 8000);
const next = await api(member.token, "POST", "/api/chat", { message: "still there, HR?", agent_id: ids.HR });
record("T-Z7 after A revokes from the chip, M's next HR message is refused", next.status === 404 || next.status === 403, `${next.status} ${JSON.stringify(next.body)}`);
await open(M, `${WEB}/chat`, `document.body.innerText.includes("Quarterly revenue") && !!document.querySelector('[aria-label="Agent"]')`);
const sidebar = await M.text();
record("T-Z7/T-Z10 M's thread list drops the HR conversation on its next fetch", !sidebar.includes("Headcount by department") && sidebar.includes("Quarterly revenue"), "");
await M.shot(`${OUT}/06-member-threads-after-revoke.png`);

// T-Z4: the picker, the new-chat caption, the room's add menu.
await M.clickLabel("Agent");
await sleep(600);
const picker = await M.eval(`[...document.querySelectorAll('[role=option]')].map(e => e.innerText.split("\\n")[0].trim())`);
await M.shot(`${OUT}/07-member-picker-hr-restricted.png`, false);
record("T-Z4 M's picker never shows HR", !picker.includes("HR") && picker.length >= 2, JSON.stringify(picker));
await M.s("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
await sleep(300);
const pill = await M.eval(`document.querySelector('[aria-label="Agent"]')?.innerText.trim()`);
record("T-Z4 the new chat names the agent HR's default falls through to", pill === "Finance", `picker pill reads "${pill}"`);
await open(M, `${WEB}/chat/${ids.T_FIN}`, `[...document.querySelectorAll('button')].some(e => /^Add (another )?agent$/.test(e.innerText.trim()))`);
let addMenu = [];
try {
  await M.clickWhere(`[...document.querySelectorAll('button')].find(e => /^Add (another )?agent$/.test(e.innerText.trim()))`, "Add agent");
  await sleep(600);
  addMenu = await M.eval(`[...document.querySelectorAll('[role=menuitem]')].map(e => e.innerText.split("\\n")[0].trim())`);
} catch (e) { addMenu = ["ERR " + e.message]; }
await M.shot(`${OUT}/08-member-room-add-menu.png`, false);
record("T-Z4 the room's add menu never shows HR", !addMenu.includes("HR") && !addMenu.some((x) => x.startsWith("ERR")), JSON.stringify(addMenu));
await M.s("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });

// ---------------------------------------------------------------- T-Z5
await sleep(15000);
await open(A, `${WEB}/settings`, `document.body.innerText.includes("Restrict Payroll…")`, teamTab(A));
await A.clickText("Restrict Payroll…");
await A.waitFor(`!!document.querySelector('[role=alertdialog][aria-label="Restrict Payroll"]')`);
await sleep(1200);
const dashWarning = await A.eval(`document.querySelector('[role=alertdialog][aria-label="Restrict Payroll"]').innerText`);
await A.shot(`${OUT}/09-restrict-payroll-warning.png`);
record("T-Z5 the Payroll warning counts its 2 live links before the press", /\b2\b/.test(dashWarning) && /link/i.test(dashWarning), dashWarning.replace(/\s+/g, " ").slice(0, 300));
await A.clickWhere(`[...document.querySelectorAll('[role=alertdialog] button')].find(e => e.innerText.trim() === "Restrict Payroll")`, "Restrict Payroll");
await A.waitFor(`document.body.innerText.includes("You are not granted Payroll")`, 8000);
await open(M, `${WEB}/chat/${ids.T_FIN}`, `document.body.innerText.includes("This dashboard is restricted")`);
const embed = await M.text();
await M.shot(`${OUT}/10-member-embed-restricted.png`);
record("T-Z5 the chat embed of Payroll says restricted, not could-not-be-loaded", embed.includes("This dashboard is restricted") && !/could not be loaded/i.test(embed), "");

// ---------------------------------------------------------------- T-Z6 Knowledge as M
await api(admin.token, "PUT", `/api/access/document/${ids.PAYROLL_PDF}/mode`, { access_mode: "restricted" });
await sleep(15000);
await open(M, `${WEB}/knowledge`, `document.body.innerText.includes("Employee Handbook 2026.pdf")`);
const knowledge = await M.text();
await M.shot(`${OUT}/11-member-knowledge.png`);
record("T-Z6 Knowledge as M has no Payroll 2026.pdf", !knowledge.includes("Payroll 2026.pdf") && knowledge.includes("Employee Handbook 2026.pdf"), "");
await open(M, `${WEB}/knowledge/${ids.PAYROLL_PDF}`, `document.body.innerText.includes("This document is restricted")`);
await M.shot(`${OUT}/12-member-knowledge-review-restricted.png`);
record("T-Z6 its review URL says restricted", (await M.text()).includes("This document is restricted"), "");

// ---------------------------------------------------------------- T-Z13 documents page
await api(admin.token, "PUT", `/api/access/agent/${ids.HR}/grants/${ids.A}`);
await open(A, `${WEB}/documents`, `document.body.innerText.includes("HR headcount deck.pptx")`);
try {
  await A.clickWhere(`[...document.querySelectorAll('div.p-4')].find(d => d.innerText.includes("HR headcount deck.pptx")).querySelector('button')`, "Share on HR's deck");
} catch (e) { console.log("share click: " + e.message); }
await A.waitFor(`document.body.innerText.includes("Paused")`, 6000);
const docsPage = await A.text();
await A.shot(`${OUT}/13-admin-documents-paused.png`);
record("T-Z13 the documents page says Paused on HR's link", docsPage.includes("Paused"), "");
await api(admin.token, "DELETE", `/api/access/agent/${ids.HR}/grants/${ids.A}`);

// ---------------------------------------------------------------- T-Z14 badge
for (const [who, page] of [["M", M], ["A", A]]) {
  await sleep(10000);
  await open(page, `${WEB}/chat`, `[...document.querySelectorAll('a')].some(e => e.innerText.includes("Approvals"))`);
  const badge = await page.eval(`(() => { const a = [...document.querySelectorAll('a')].find(e => e.innerText.includes("Approvals")); return a ? a.innerText.replace(/\\s+/g, " ").trim() : null; })()`);
  await page.shot(`${OUT}/14-approvals-badge-${who}.png`, false);
  record(`T-Z14 ${who}: the approvals badge counts 1`, badge === "Approvals 1", `badge "${badge}"`);
}

// ---------------------------------------------------------------- Team at 390 px
await A.viewport(390, 844);
await sleep(15000);
await open(A, `${WEB}/settings`, `document.body.innerText.includes("Restrict Finance…") && !!document.querySelector('[aria-label="Access for ${MEMBER}"]')`, teamTab(A));
await A.clickLabel(`Access for ${MEMBER}`);
await sleep(1200);
const layout = await A.eval(`(() => {
  const vw = document.documentElement.clientWidth;
  const overflowX = document.documentElement.scrollWidth - vw;
  // Every visible control inside the Team tab's panels: none may poke past the
  // viewport, and no two may overlap.
  const panel = document.querySelector('[role=tabpanel][data-state=active]') || document.body;
  const ctrls = [...panel.querySelectorAll('button, [role=combobox], input')].filter(e => { const r = e.getBoundingClientRect(); return r.width > 0 && r.height > 0; });
  const rects = ctrls.map(e => ({ r: e.getBoundingClientRect(), label: e.getAttribute('aria-label') || e.innerText.trim().slice(0, 30) }));
  const outside = rects.filter(x => x.r.right > vw + 0.5 || x.r.left < -0.5).map(x => x.label);
  const overlaps = [];
  for (let i = 0; i < rects.length; i++) for (let j = i + 1; j < rects.length; j++) {
    const a = rects[i].r, b = rects[j].r;
    const w = Math.min(a.right, b.right) - Math.max(a.left, b.left), h = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
    if (w > 1 && h > 1) overlaps.push(rects[i].label + " × " + rects[j].label);
  }
  // A scrolling container (the tab strip) may be wider than the viewport by design.
  return { vw, overflowX, controls: rects.length, outside, overlaps: overlaps.slice(0, 10) };
})()`);
await A.shot(`${OUT}/15-team-390px.png`);
record("T-Z7 Team at 390 px: no horizontal overflow, no control outside or overlapping", layout.overflowX <= 0 && layout.outside.length === 0 && layout.overlaps.length === 0, JSON.stringify(layout));
// The arm RIZ-25 found failing: every member row's email squeezed to 0 px by
// the three controls beside it, so an admin could not see whose row it was.
const names = await A.eval(`[...document.querySelectorAll('[aria-label^="Access for "]')].map(btn => Math.round(btn.closest('.py-3').querySelector('.truncate').getBoundingClientRect().width))`);
record("T-Z7 Team at 390 px: every member's email is visible beside their controls", names.length >= 3 && names.every((w) => w > 120), `email widths ${JSON.stringify(names)} px`);

fs.writeFileSync(`${OUT}/results.json`, JSON.stringify(results, null, 2));
const failed = results.filter((r) => !r.pass).length;
console.log(`# ${results.length - failed} passed, ${failed} failed`);
b.close();
process.exit(failed ? 1 : 0);
