// Package public exposes the notification module's API surface.
// Other modules may only import this package, never internal sub-packages.
package public

// Channel represents the delivery channel for a notification.
type Channel string

const (
	ChannelEmail Channel = "email"
	ChannelSMS   Channel = "sms"
	ChannelPush  Channel = "push"
)

// Message is a notification request sent to one or more recipients.
type Message struct {
	TenantID   string
	Channel    Channel
	Recipients []string
	Subject    string
	Body       string
}

// Sender is the interface other modules use to send notifications.
type Sender interface {
	Send(ctx interface{ Value(any) any }, msg Message) error
}
