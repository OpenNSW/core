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
)

// SMSConfig is the SMS provider's configuration. It carries yaml tags so an
// application can embed it in its own config and load it with
// configyaml.LoadAndExpand. Every field is a string, so a secret that only
// looks like a number (a password of 12345678, a SID code of 0123) decodes as
// written.
type SMSConfig struct {
	BaseURL  string `yaml:"baseURL"`
	SIDCode  string `yaml:"sidCode"`
	UserName string `yaml:"userName"`
	Password string `yaml:"password"`
}

// SMSRequest matches the GovSMS V1 API envelope.
// Credentials are sent per-request in the body as required by the spec.
type SMSRequest struct {
	Data        string `json:"data"`
	PhoneNumber string `json:"phoneNumber"`
	SIDCode     string `json:"sIDCode"`
	UserName    string `json:"userName"`
	Password    string `json:"password"`
}

// SMSProvider sends SMS via the GovSMS service.
type SMSProvider struct {
	cfg    SMSConfig
	client *remote.Client
}

// NewSMSProvider validates cfg and returns an SMSProvider ready to send.
func NewSMSProvider(cfg SMSConfig) (*SMSProvider, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("sms: baseURL is required")
	}
	if err := validateBaseURL(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("sms: %w", err)
	}
	if cfg.SIDCode == "" {
		return nil, errors.New("sms: sidCode is required")
	}
	if cfg.UserName == "" {
		return nil, errors.New("sms: userName is required")
	}
	if cfg.Password == "" {
		return nil, errors.New("sms: password is required")
	}
	return &SMSProvider{cfg: cfg, client: remote.NewClient(cfg.BaseURL)}, nil
}

func (s *SMSProvider) Type() notification.ChannelType { return notification.ChannelSMS }

func (s *SMSProvider) Send(ctx context.Context, req notification.Request) error {
	if s.client == nil {
		return errors.New("sms provider not configured")
	}
	if err := s.client.Request(ctx, remote.Request{
		Method: http.MethodPost,
		Path:   "/send",
		Body: remote.JSONBody{V: SMSRequest{
			Data:        req.Body,
			PhoneNumber: req.To,
			SIDCode:     s.cfg.SIDCode,
			UserName:    s.cfg.UserName,
			Password:    s.cfg.Password,
		}},
	}, nil); err != nil {
		return fmt.Errorf("govsms send: %w", err)
	}
	return nil
}
