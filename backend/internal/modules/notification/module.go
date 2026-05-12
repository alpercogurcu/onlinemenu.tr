// Package notification delivers outbound messages via email, SMS, and push channels.
package notification

import "go.uber.org/fx"

// Module is the fx module definition for the notification domain.
var Module = fx.Module("notification")
