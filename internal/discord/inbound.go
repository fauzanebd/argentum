package discord

import (
	"context"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/sirupsen/logrus"
)

// InboundMessage is the normalized shape passed to the Dispatcher.
type InboundMessage struct {
	CompanyID     string
	ApplicationID string
	UserID        string // Discord user id (snowflake string)
	ChannelID     string
	GuildID       string // empty for DMs
	Content       string
	MessageID     string
}

// Dispatcher is called for each inbound message that passes the addressed-to-
// bot filter. Implementations enqueue the message into the chat pipeline.
type Dispatcher interface {
	Dispatch(ctx context.Context, in InboundMessage) error
}

// DispatcherFunc adapts a function to the Dispatcher interface.
type DispatcherFunc func(ctx context.Context, in InboundMessage) error

func (f DispatcherFunc) Dispatch(ctx context.Context, in InboundMessage) error {
	return f(ctx, in)
}

// onMessageCreate is the gateway hook. The bot replies only when the message
// is a DM, an @mention of the bot, or a reply to one of the bot's messages.
// Everything else is silently ignored so the bot doesn't spam channels it
// happens to read.
func (s *Session) onMessageCreate(_ *discordgo.Session, m *discordgo.MessageCreate, dispatcher Dispatcher) {
	if m == nil || m.Author == nil {
		return
	}
	if m.Author.Bot || m.Author.ID == s.BotUserID {
		return
	}

	isDM := m.GuildID == ""
	addressed := isDM || mentionsBot(m.Mentions, s.BotUserID) || isReplyToBot(m, s.BotUserID)
	if !addressed {
		return
	}

	content := stripMention(m.Content, s.BotUserID)
	if strings.TrimSpace(content) == "" {
		return
	}

	if dispatcher == nil {
		return
	}
	in := InboundMessage{
		CompanyID:     s.CompanyID,
		ApplicationID: s.ApplicationID,
		UserID:        m.Author.ID,
		ChannelID:     m.ChannelID,
		GuildID:       m.GuildID,
		Content:       content,
		MessageID:     m.ID,
	}
	if err := dispatcher.Dispatch(context.Background(), in); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": s.CompanyID,
			"user_id":    m.Author.ID,
		}).Warn("discord dispatch failed")
	}
}

func mentionsBot(mentions []*discordgo.User, botID string) bool {
	for _, u := range mentions {
		if u != nil && u.ID == botID {
			return true
		}
	}
	return false
}

func isReplyToBot(m *discordgo.MessageCreate, botID string) bool {
	if m.MessageReference == nil || m.ReferencedMessage == nil {
		return false
	}
	if m.ReferencedMessage.Author == nil {
		return false
	}
	return m.ReferencedMessage.Author.ID == botID
}

// stripMention removes a leading "<@botID>" or "<@!botID>" so the agent sees
// a clean prompt. Mid-message mentions are left intact.
func stripMention(content, botID string) string {
	for _, prefix := range []string{"<@" + botID + ">", "<@!" + botID + ">"} {
		if strings.HasPrefix(strings.TrimSpace(content), prefix) {
			content = strings.TrimSpace(content)
			content = strings.TrimPrefix(content, prefix)
			return strings.TrimSpace(content)
		}
	}
	return content
}
