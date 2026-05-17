// Package discord wires per-tenant Discord bots into the Argentum chat
// pipeline. Each tenant supplies their own bot token (stored encrypted in
// company_discord_credentials); cmd/discord opens one gateway WebSocket per
// enabled row and routes inbound messages into ChatEnqueuer. The interactions
// HTTP webhook (POST /webhook/discord/interactions) is wired but registers no
// slash commands yet — it only verifies Ed25519 signatures and responds to
// the PING handshake so the Discord dev portal "Save" action succeeds.
package discord
