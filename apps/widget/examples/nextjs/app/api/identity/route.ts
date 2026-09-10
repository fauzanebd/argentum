// The signing endpoint as a route handler, which is the reason a Next example
// exists next to the React one: it is server-only by construction. An env var
// without the `NEXT_PUBLIC_` prefix is never inlined into the browser bundle,
// so the secret cannot leak by a careless import.
//
// The other rule is the same one every integrator has to hold: `userRef` comes
// from *your* session, never from the request. An endpoint that signs whatever
// it is asked to sign lets any visitor become any employee.

import { createHmac } from "node:crypto";

const CLIENT_KEY = process.env.ARGENTUM_CLIENT_KEY ?? "";
const SECRET = process.env.ARGENTUM_EMBED_SECRET ?? "";
const API_BASE = process.env.ARGENTUM_BASE_URL ?? "http://localhost:8080";

// Every response carries a fresh `exp`, so this must never be one of the routes
// Next renders once at build time and serves from the cache afterwards.
export const dynamic = "force-dynamic";

export function GET() {
  if (!CLIENT_KEY || !SECRET) {
    const error = "Set ARGENTUM_CLIENT_KEY and ARGENTUM_EMBED_SECRET (Settings → Embed).";
    return Response.json({ error }, { status: 500 });
  }

  // Pretend this came from a session cookie. In your app it must.
  const userRef = "emp_812";
  const exp = Math.floor(Date.now() / 1000) + 900;
  const sig = createHmac("sha256", SECRET).update(`${userRef}:${exp}`).digest("hex");

  return Response.json({ clientKey: CLIENT_KEY, apiBase: API_BASE, user: { ref: userRef, name: "Rina", exp, sig } });
}
