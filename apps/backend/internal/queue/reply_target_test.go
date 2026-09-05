package queue

import (
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// The target a hand-off carries is exactly the set of fields the turn's own
// delivery reads. A field missing here is a channel a render result can
// never reach, and it would look like a working feature on the dashboard.
func TestReplyTargetCarriesEveryDeliveryRef(t *testing.T) {
	p := ChatRunPayload{
		Channel: domain.ChannelSlack, PhoneNumber: "+62", DiscordChannelID: "dc", DiscordUserID: "du",
		LarkChatID: "lc", LarkMessageID: "lm", SlackChannelID: "sc", SlackThreadTS: "ts",
		// Not delivery refs; they must not leak into the target.
		LarkOpenID: "lo", LarkThreadKey: "lk", SlackTeamID: "st", SlackUserID: "su", UserID: "u",
	}
	want := tenantctx.ReplyTarget{
		Channel: "slack", PhoneNumber: "+62", DiscordChannelID: "dc", DiscordUserID: "du",
		LarkChatID: "lc", LarkMessageID: "lm", SlackChannelID: "sc", SlackThreadTS: "ts",
	}
	if got := p.ReplyTarget(); got != want {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}
