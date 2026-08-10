// A signing server in about thirty lines (T-21's gate, T-22's example).
//
// This is the piece every integrator writes and the piece that decides whether
// the integration is safe. Two rules, and both are why it runs here rather than
// in the page:
//
//   1. The signing secret never leaves this process.
//   2. `userRef` comes from *your* session, never from the request body. A
//      signing endpoint that signs whatever it is asked to sign is an endpoint
//      that lets any visitor become any employee.

import { createHmac } from "node:crypto";
import { createServer } from "node:http";
import { readFileSync } from "node:fs";

const PORT = process.env.PORT ?? 4321;
const CLIENT_KEY = process.env.ARGENTUM_CLIENT_KEY ?? "";
const SECRET = process.env.ARGENTUM_EMBED_SECRET ?? "";
const API_BASE = process.env.ARGENTUM_BASE_URL ?? "http://localhost:8080";

if (!CLIENT_KEY || !SECRET) {
  console.error("Set ARGENTUM_CLIENT_KEY and ARGENTUM_EMBED_SECRET (Settings → Embed).");
  process.exit(1);
}

createServer((req, res) => {
  if (req.url === "/identity") {
    // Pretend this came from a session cookie. In your app it must.
    const userRef = "emp_812";
    const exp = Math.floor(Date.now() / 1000) + 900;
    const sig = createHmac("sha256", SECRET).update(`${userRef}:${exp}`).digest("hex");

    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ clientKey: CLIENT_KEY, apiBase: API_BASE, user: { ref: userRef, name: "Rina", exp, sig } }));
    return;
  }

  res.writeHead(200, { "Content-Type": "text/html" });
  res.end(readFileSync(new URL("./index.html", import.meta.url), "utf8"));
}).listen(PORT, () => console.log(`http://localhost:${PORT}`));
