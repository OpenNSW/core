// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package providers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"net/url"

	"github.com/OpenNSW/core/notification"
	"github.com/OpenNSW/core/remote"
	"github.com/OpenNSW/core/remote/auth"
)

// EmailConfig is the email provider's configuration. It carries yaml tags so
// an application can embed it in its own config and load it with
// configyaml.LoadAndExpand.
type EmailConfig struct {
	// BaseURL is PingMailer's API, for example
	// https://api-server.mail.svc.cluster.local:8000.
	BaseURL string `yaml:"baseURL"`
	// Path is the endpoint path on BaseURL to POST the email to. Optional:
	// defaults to "/notify" when empty.
	Path string `yaml:"path"`
	// AllowInsecureHTTP permits a plain-HTTP BaseURL on a host other than
	// loopback, such as an in-cluster service. The access token travels on
	// that call, so it is off by default.
	AllowInsecureHTTP bool `yaml:"allowInsecureHTTP"`
	// Auth gets the token PingMailer hands to SMTP: an oauth2 client allowed to
	// send as Sender.
	Auth auth.Config `yaml:"auth"`
	// SMTPHost and SMTPPort are the SMTP server PingMailer relays to.
	SMTPHost string `yaml:"smtpHost"`
	SMTPPort int    `yaml:"smtpPort"`
	// Sender is the address every email is sent from, and the SMTP identity
	// the token is presented for.
	Sender string `yaml:"sender"`
	// SenderName is the From header's display name. Optional: the From header
	// is the bare Sender without it.
	SenderName string `yaml:"senderName"`
}

// Validate reports a missing or invalid setting.
func (c EmailConfig) Validate() error {
	if c.BaseURL == "" {
		return errors.New("email: baseURL is required")
	}
	if err := validateEmailBaseURL(c.BaseURL, c.AllowInsecureHTTP); err != nil {
		return fmt.Errorf("email: %w", err)
	}
	if c.Auth.Type == "" {
		return errors.New("email: auth is required")
	}
	if c.SMTPHost == "" {
		return errors.New("email: smtpHost is required")
	}
	if c.SMTPPort <= 0 || c.SMTPPort > 65535 {
		return errors.New("email: smtpPort must be between 1 and 65535")
	}
	if _, err := mail.ParseAddress(c.Sender); err != nil {
		return fmt.Errorf("email: sender: %w", err)
	}
	return nil
}

type emailRecipient struct {
	Email string `json:"email"`
}

// emailRequest is PingMailer's POST /notify body with the content as written:
// PingMailer applies no template to subject, text_body and html_body.
type emailRequest struct {
	SMTPHost     string           `json:"smtp_host"`
	SMTPPort     int              `json:"smtp_port"`
	SMTPUsername string           `json:"smtp_username"`
	SMTPSender   string           `json:"smtp_sender"`
	Recipients   []emailRecipient `json:"recipients"`
	Subject      string           `json:"subject"`
	TextBody     string           `json:"text_body,omitempty"`
	HTMLBody     string           `json:"html_body,omitempty"`
}

// EmailProvider sends email notifications through PingMailer's API
// (POST /notify). PingMailer relays each message to SMTP, authenticating with
// the access token the call carries (XOAUTH2), so the provider holds an
// authenticator (such as an OAuth2 client) and sends a live token, refreshed as
// it expires.
type EmailProvider struct {
	client *remote.Client
	cfg    EmailConfig
}

const defaultEmailPath = "/notify"

// NewEmailProvider validates cfg and returns an EmailProvider ready to send.
func NewEmailProvider(cfg EmailConfig) (*EmailProvider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Path == "" {
		cfg.Path = defaultEmailPath
	}
	authenticator, err := auth.Build(cfg.Auth)
	if err != nil {
		return nil, fmt.Errorf("email: auth: %w", err)
	}
	return &EmailProvider{
		client: remote.NewClient(cfg.BaseURL, remote.WithAuthenticator(authenticator)),
		cfg:    cfg,
	}, nil
}

func (e *EmailProvider) Type() notification.ChannelType { return notification.ChannelEmail }

// Send hands the email to PingMailer. PingMailer answers 202 once it has
// queued the email and talks to SMTP afterwards, so a nil error means the
// email was accepted, not delivered: a delivery failure shows only in
// PingMailer's logs.
func (e *EmailProvider) Send(ctx context.Context, req notification.Request) error {
	if e.client == nil {
		return errors.New("email provider not configured")
	}
	// PingMailer's smtp_username is the From header's display name; the
	// sender address is the From address and the SMTP identity.
	name := e.cfg.SenderName
	if name == "" {
		name = e.cfg.Sender
	}
	path := e.cfg.Path
	if path == "" {
		path = defaultEmailPath
	}
	if err := e.client.Request(ctx, remote.Request{
		Method: http.MethodPost,
		Path:   path,
		Body: remote.JSONBody{V: emailRequest{
			SMTPHost:     e.cfg.SMTPHost,
			SMTPPort:     e.cfg.SMTPPort,
			SMTPUsername: name,
			SMTPSender:   e.cfg.Sender,
			Recipients:   []emailRecipient{{Email: req.To}},
			Subject:      req.Subject,
			TextBody:     req.Body,
			HTMLBody:     req.HTMLBody,
		}},
	}, nil); err != nil {
		return fmt.Errorf("email send: %w", err)
	}
	return nil
}

// validateEmailBaseURL requires an absolute HTTPS URL. Plain HTTP is allowed to
// a loopback host, for local development, and to any host when allowHTTP is set.
func validateEmailBaseURL(raw string, allowHTTP bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid baseURL: %w", err)
	}
	if u.Scheme == "" || u.Hostname() == "" {
		return errors.New("baseURL must be an absolute URL")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		ip := net.ParseIP(host)
		if allowHTTP || host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
		return errors.New("baseURL must use HTTPS; set allowInsecureHTTP to send to a plain-HTTP host")
	default:
		return fmt.Errorf("baseURL scheme %q is not http or https", u.Scheme)
	}
}
