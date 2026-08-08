// Package slack implements the Slack channel transport. Inbound messages
// arrive via the Events API webhook (handled by the API). Outbound replies
// are sent from the worker through Client.Reply, which calls
// chat.postMessage with the tenant's decrypted xoxb- bot token.
//
// One Slack thread maps 1:1 to one conversation_thread row: the bot is
// triggered by an app_mention in a channel or any message in a DM, and it
// always replies inside the thread (starting one on the first @mention).
// Thread lookup is keyed by (slack_channel_id, slack_thread_ts) because
// Slack's `ts` is only unique within a channel; thread_ts falls back to the
// message's own ts when the trigger message isn't already in a thread.
package slack
