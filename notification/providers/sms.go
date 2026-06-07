package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/OpenNSW/nsw/backend/pkg/notifications"
	"github.com/OpenNSW/nsw/backend/pkg/remote"
)

type smsConfig struct {
	BaseURL  string `json:"baseURL"`
	SIDCode  string `json:"sidCode"`
	UserName string `json:"userName"`
	Password string `json:"password"`
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
	cfg    smsConfig
	client *remote.Client
}

// NewSMSProvider returns an SMSProvider ready for Configure.
func NewSMSProvider() *SMSProvider {
	return &SMSProvider{}
}

func (s *SMSProvider) Type() notifications.ChannelType { return notifications.ChannelSMS }

func (s *SMSProvider) Configure(raw json.RawMessage) error {
	if err := json.Unmarshal(raw, &s.cfg); err != nil {
		return fmt.Errorf("unmarshal sms config: %w", err)
	}
	if s.cfg.BaseURL == "" {
		return errors.New("baseURL is required")
	}
	if err := validateBaseURL(s.cfg.BaseURL); err != nil {
		return err
	}
	if s.cfg.SIDCode == "" {
		return errors.New("sidCode is required")
	}
	if s.cfg.UserName == "" {
		return errors.New("userName is required")
	}
	if s.cfg.Password == "" {
		return errors.New("password is required")
	}
	s.client = remote.NewClient(s.cfg.BaseURL)
	return nil
}

func (s *SMSProvider) Send(ctx context.Context, req notifications.Request) error {
	if s.client == nil {
		return errors.New("sms provider not configured")
	}
	if err := s.client.JSONRequest(ctx, remote.Request{
		Method: http.MethodPost,
		Path:   "/send",
		Body: SMSRequest{
			Data:        req.Body,
			PhoneNumber: req.To,
			SIDCode:     s.cfg.SIDCode,
			UserName:    s.cfg.UserName,
			Password:    s.cfg.Password,
		},
	}, nil); err != nil {
		return fmt.Errorf("govsms send: %w", err)
	}
	return nil
}
