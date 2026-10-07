// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	notification "github.com/OpenNSW/core/notifications"
	"github.com/OpenNSW/core/remote"
	"github.com/OpenNSW/core/remote/auth"
)

// EmailConfig is the email provider's configuration. It carries yaml tags so
// an application can embed it in its own config and load it with
// configyaml.LoadAndExpand. Token is the bearer token itself: a {{env:...}}
// or {{file:...}} placeholder is resolved by configyaml before the provider
// sees it, so it is used as written and never resolved a second time.
type EmailConfig struct {
	BaseURL string `yaml:"baseURL"`
	Token   string `yaml:"token"`
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

// NewEmailProvider validates cfg and returns an EmailProvider ready to send.
func NewEmailProvider(cfg EmailConfig) (*EmailProvider, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("email: baseURL is required")
	}
	if err := validateBaseURL(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("email: %w", err)
	}
	if cfg.Token == "" {
		return nil, errors.New("email: token is required")
	}
	return &EmailProvider{client: remote.NewClient(cfg.BaseURL, remote.WithAuthenticator(auth.NewBearer(cfg.Token)))}, nil
}

func (e *EmailProvider) Type() notification.ChannelType { return notification.ChannelEmail }

func (e *EmailProvider) Send(ctx context.Context, req notification.Request) error {
	if e.client == nil {
		return errors.New("email provider not configured")
	}
	if err := e.client.Request(ctx, remote.Request{
		Method: http.MethodPost,
		Path:   "/send",
		Body: remote.JSONBody{V: emailRequest{
			To:       req.To,
			Subject:  req.Subject,
			Body:     req.Body,
			HTMLBody: req.HTMLBody,
		}},
	}, nil); err != nil {
		return fmt.Errorf("email send: %w", err)
	}
	return nil
}
