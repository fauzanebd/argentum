package discord

// Provider is the outbound contract exercised by the worker indirectly via
// the outbound pub/sub channel. Implementations send a message to a Discord
// channel as a specific tenant. The concrete impl lives in SessionManager;
// the interface is here so non-discord packages can take it as a dependency
// without importing discordgo.
type Provider interface {
	Send(companyID, channelID, content string) error
}
