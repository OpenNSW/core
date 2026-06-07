package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/OpenNSW/nsw/backend/pkg/notifications"
	"github.com/OpenNSW/nsw/backend/pkg/remote"
	remoteauth "github.com/OpenNSW/nsw/backend/pkg/remote/auth"
)

type emailConfig struct {
	BaseURL string `json:"baseURL"`
	Token   string `json:"token"`
}

type emailRequest struct {
	To       string `json:"to"`
	Subject  string `json:"subject,omitempty"`
	Body     string `json:"body,omitempty"`
	HTMLBody string `json:"htmlBody,omitempty"`
}

// EmailProvider sends email via an HTTP API using bearer token auth.
type EmailProvider struct {
	client *remote.Client
}

// NewEmailProvider returns a new EmailProvider ready for Configure.
func NewEmailProvider() *EmailProvider {
	return &EmailProvider{}
}

func (e *EmailProvider) Type() notifications.ChannelType { return notifications.ChannelEmail }

func (e *EmailProvider) Configure(raw json.RawMessage) error {
	var cfg emailConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("unmarshal email config: %w", err)
	}
	if cfg.BaseURL == "" {
		return errors.New("baseURL is required")
	}
	if err := validateBaseURL(cfg.BaseURL); err != nil {
		return err
	}
	if cfg.Token == "" {
		return errors.New("token is required")
	}
	e.client = remote.NewClient(cfg.BaseURL, remote.WithAuthenticator(remoteauth.NewBearer(remoteauth.BearerConfig{Token: cfg.Token})))
	return nil
}

func (e *EmailProvider) Send(ctx context.Context, req notifications.Request) error {
	if e.client == nil {
		return errors.New("email provider not configured")
	}
	if err := e.client.JSONRequest(ctx, remote.Request{
		Method: http.MethodPost,
		Path:   "/send",
		Body: emailRequest{
			To:       req.To,
			Subject:  req.Subject,
			Body:     req.Body,
			HTMLBody: req.HTMLBody,
		},
	}, nil); err != nil {
		return fmt.Errorf("email send: %w", err)
	}
	return nil
}
