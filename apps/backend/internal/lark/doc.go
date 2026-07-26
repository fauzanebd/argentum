// Package lark implements the Lark (Feishu) channel transport. Inbound
// messages arrive via webhook callback (handled by the API). Outbound
// replies are sent from the worker through Client.Reply, which posts to
// the Lark Open Platform REST API using a per-tenant cached
// tenant_access_token.
//
// One Lark reply-thread maps 1:1 to one conversation_thread row: every
// turn must @mention the bot, and the bot always replies inside the thread
// (creating one on the first @mention from a main chat). Thread lookup is
// keyed by lark_thread_key = thread_id ?? root_id ?? message_id from the
// inbound event.
package lark
