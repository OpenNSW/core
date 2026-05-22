package notifications

import (
	"errors"
)

// ChannelType identifies the delivery channel for a notification.
type ChannelType string

const (
	ChannelSMS   ChannelType = "sms"
	ChannelEmail ChannelType = "email"
)

var (
	ErrInvalidChannel = errors.New("invalid or missing channel")
	ErrToRequired     = errors.New("to address is required")
	ErrBodyRequired   = errors.New("body or html_body is required")
)

type Request struct {
	Channel  ChannelType `json:"channel"`
	To       string      `json:"to"`
	Subject  string      `json:"subject,omitempty"`
	Body     string      `json:"body,omitempty"`
	HTMLBody string      `json:"html_body,omitempty"`
}

func (r *Request) Validate() error {
	if r.Channel != ChannelSMS && r.Channel != ChannelEmail {
		return ErrInvalidChannel
	}

	if r.To == "" {
		return ErrToRequired
	}

	switch r.Channel {
	case ChannelEmail:
		if r.Body == "" && r.HTMLBody == "" {
			return ErrBodyRequired
		}
	case ChannelSMS:
		if r.Body == "" {
			return ErrBodyRequired
		}
	}

	return nil
}
