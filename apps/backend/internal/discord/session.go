package discord

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/sirupsen/logrus"
)

// Session wraps a discordgo.Session for one tenant. It captures the company
// id and bot user id so handlers can route inbound events without an extra
// repo lookup per message.
type Session struct {
	CompanyID     string
	ApplicationID string
	BotUserID     string

	dg *discordgo.Session
}

// openSession starts a gateway WebSocket for a single tenant. token is the
// decrypted bot token. The MessageContent intent is required for non-mention
// DM/channel reads.
func openSession(companyID, applicationID, token string, dispatcher Dispatcher) (*Session, error) {
	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("discordgo.New: %w", err)
	}
	dg.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentsDirectMessages |
		discordgo.IntentsMessageContent

	s := &Session{
		CompanyID:     companyID,
		ApplicationID: applicationID,
		dg:            dg,
	}

	dg.AddHandler(func(sd *discordgo.Session, m *discordgo.MessageCreate) {
		s.onMessageCreate(sd, m, dispatcher)
	})

	if err := dg.Open(); err != nil {
		return nil, fmt.Errorf("discord open: %w", err)
	}
	if dg.State != nil && dg.State.User != nil {
		s.BotUserID = dg.State.User.ID
	}
	logrus.WithFields(logrus.Fields{
		"company_id":     companyID,
		"application_id": applicationID,
		"bot_user_id":    s.BotUserID,
	}).Info("discord session opened")
	return s, nil
}

// Close shuts the gateway WS down.
func (s *Session) Close() error {
	if s.dg == nil {
		return nil
	}
	return s.dg.Close()
}

// Send writes a text message to a Discord channel. Discord caps content at
// 2000 chars per message; longer payloads are sliced into chunks.
func (s *Session) Send(_ context.Context, channelID, content string) error {
	if s.dg == nil {
		return fmt.Errorf("session closed")
	}
	for _, chunk := range chunkContent(content, 2000) {
		if _, err := s.dg.ChannelMessageSend(channelID, chunk); err != nil {
			return err
		}
	}
	return nil
}

func chunkContent(s string, max int) []string {
	if len(s) <= max {
		return []string{s}
	}
	out := make([]string, 0, (len(s)/max)+1)
	for len(s) > max {
		// Try to break on a newline near the boundary.
		cut := max
		if nl := lastIndexByteUpTo(s, '\n', max); nl > max/2 {
			cut = nl
		}
		out = append(out, s[:cut])
		s = s[cut:]
	}
	if len(s) > 0 {
		out = append(out, s)
	}
	return out
}

func lastIndexByteUpTo(s string, b byte, upTo int) int {
	if upTo > len(s) {
		upTo = len(s)
	}
	for i := upTo - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}
