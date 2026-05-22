package notifications

import (
	"context"
	"encoding/json"
)

// Provider is implemented by each notification channel (email, SMS, etc.).
type Provider interface {
	Type() ChannelType
	Configure(cfg json.RawMessage) error
	Send(ctx context.Context, req Request) error
}
