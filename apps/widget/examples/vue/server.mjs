// The signing endpoint, in about thirty lines. It is the piece every integrator
// writes and the piece that decides whether the integration is safe. Two rules,
// and both are why it runs here rather than in the page:
//
//   1. The signing secret never leaves this process.
//   2. `userRef` comes from *your* session, never from the request. An endpoint
//      that signs whatever it is asked to sign is an endpoint that lets any
//      visitor become any employee.

import { createHmac } from "node:crypto";
import { createServer } from "node:http";

const PORT = process.env.PORT ?? 4321;
const CLIENT_KEY = process.env.ARGENTUM_CLIENT_KEY ?? "";
const SECRET = process.env.ARGENTUM_EMBED_SECRET ?? "";
const API_BASE = process.env.ARGENTUM_BASE_URL ?? "http://localhost:8080";

if (!CLIENT_KEY || !SECRET) {
  console.error("Set ARGENTUM_CLIENT_KEY and ARGENTUM_EMBED_SECRET (Settings → Embed).");
  process.exit(1);
}

createServer((req, res) => {
  if (req.url !== "/identity") {
    res.writeHead(404).end();
    return;
  }

  // Pretend this came from a session cookie. In your app it must.
  const userRef = "emp_812";
  const exp = Math.floor(Date.now() / 1000) + 900;
  const sig = createHmac("sha256", SECRET).update(`${userRef}:${exp}`).digest("hex");

  res.writeHead(200, { "Content-Type": "application/json" });
  res.end(JSON.stringify({ clientKey: CLIENT_KEY, apiBase: API_BASE, user: { ref: userRef, name: "Rina", exp, sig } }));
}).listen(PORT, () => console.log(`identity on http://localhost:${PORT}`));
