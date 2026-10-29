// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package providers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/OpenNSW/core/notification"
	"github.com/OpenNSW/core/notification/providers"
	"github.com/OpenNSW/core/remote/auth"
)

func validEmailConfig(baseURL string) providers.EmailConfig {
	return providers.EmailConfig{
		BaseURL:  baseURL,
		Auth:     auth.Config{Type: auth.TypeBearer, Bearer: &auth.BearerConfig{Token: "access-token"}},
		SMTPHost: "mail.example.com",
		SMTPPort: 587,
		Sender:   "no-reply@example.com",
	}
}

// An application embeds the provider configs in its own config, as below.
// The unquoted values decode the way configyaml's resolved placeholders do:
// YAML would read them as a number, an octal number and a bool if the fields
// weren't strings.
func TestConfigs_KeepNumberLookingValuesAsWritten(t *testing.T) {
	t.Parallel()
	var cfg struct {
		Notification struct {
			SMS   providers.SMSConfig   `yaml:"sms"`
			Email providers.EmailConfig `yaml:"email"`
		} `yaml:"notification"`
	}
	err := yaml.Unmarshal([]byte(`
notification:
  sms:
    baseURL: https://sms.example.com
    userName: user
    password: 12345678
    sidCode: 0123
  email:
    baseURL: https://email.example.com
    auth:
      type: bearer
      bearer:
        token: true
    smtpHost: mail.example.com
    smtpPort: 587
    sender: no-reply@example.com
`), &cfg)
	if err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}

	sms, email := cfg.Notification.SMS, cfg.Notification.Email
	if sms.Password != "12345678" || sms.SIDCode != "0123" || email.Auth.Bearer == nil || email.Auth.Bearer.Token != "true" {
		t.Errorf("password, sidCode, token = %q, %q, %+v; want \"12345678\", \"0123\", \"true\"", sms.Password, sms.SIDCode, email.Auth.Bearer)
	}
	if _, err := providers.NewSMSProvider(sms); err != nil {
		t.Errorf("NewSMSProvider: %v", err)
	}
	if _, err := providers.NewEmailProvider(email); err != nil {
		t.Errorf("NewEmailProvider: %v", err)
	}
}

func TestNewSMSProvider_Validates(t *testing.T) {
	t.Parallel()
	valid := providers.SMSConfig{BaseURL: "https://sms.example.com", SIDCode: "sid", UserName: "user", Password: "pass"}
	for _, tc := range []struct {
		name   string
		mutate func(*providers.SMSConfig)
		want   string
	}{
		{"no baseURL", func(c *providers.SMSConfig) { c.BaseURL = "" }, "baseURL is required"},
		{"plain-HTTP baseURL", func(c *providers.SMSConfig) { c.BaseURL = "http://sms.example.com" }, "HTTPS"},
		{"no sidCode", func(c *providers.SMSConfig) { c.SIDCode = "" }, "sidCode is required"},
		{"no userName", func(c *providers.SMSConfig) { c.UserName = "" }, "userName is required"},
		{"no password", func(c *providers.SMSConfig) { c.Password = "" }, "password is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := valid
			tc.mutate(&cfg)
			if _, err := providers.NewSMSProvider(cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("NewSMSProvider = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestNewEmailProvider_Validates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*providers.EmailConfig)
		want string
	}{
		{"valid", func(*providers.EmailConfig) {}, ""},
		{"https", func(c *providers.EmailConfig) { c.BaseURL = "https://mail-api.example.com" }, ""},
		{"plain http", func(c *providers.EmailConfig) { c.BaseURL = "http://api-server.mail.svc.cluster.local:8000" }, "must use HTTPS"},
		{"plain http, allowed", func(c *providers.EmailConfig) {
			c.BaseURL, c.AllowInsecureHTTP = "http://api-server.mail.svc.cluster.local:8000", true
		}, ""},
		{"no baseURL", func(c *providers.EmailConfig) { c.BaseURL = "" }, "baseURL is required"},
		{"relative baseURL", func(c *providers.EmailConfig) { c.BaseURL = "/notify" }, "absolute"},
		{"no auth", func(c *providers.EmailConfig) { c.Auth = auth.Config{} }, "auth is required"},
		{"invalid auth", func(c *providers.EmailConfig) { c.Auth = auth.Config{Type: auth.TypeBearer} }, "auth:"},
		{"no smtpHost", func(c *providers.EmailConfig) { c.SMTPHost = "" }, "smtpHost is required"},
		{"no smtpPort", func(c *providers.EmailConfig) { c.SMTPPort = 0 }, "smtpPort"},
		{"sender not an address", func(c *providers.EmailConfig) { c.Sender = "no-reply" }, "sender"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := validEmailConfig("http://localhost:8000")
			tc.edit(&cfg)
			_, err := providers.NewEmailProvider(cfg)
			if tc.want == "" {
				if err != nil {
					t.Errorf("NewEmailProvider = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("NewEmailProvider = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// An email is posted to /notify with the SMTP settings, one recipient and the
// content as written, carrying the client's access token.
func TestEmailProvider_Send_PostsTheEmailToNotify(t *testing.T) {
	t.Parallel()
	var path, authorization string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, authorization = r.URL.Path, r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	p, err := providers.NewEmailProvider(validEmailConfig(srv.URL))
	if err != nil {
		t.Fatalf("NewEmailProvider: %v", err)
	}

	err = p.Send(context.Background(), notification.Request{
		Channel:  notification.ChannelEmail,
		To:       "trader@example.com",
		Subject:  "Tom & Jerry's application",
		Body:     "Dear <Name>, {{.not_a_template}}",
		HTMLBody: "<p>Dear Tom &amp; Jerry</p>",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if path != "/notify" {
		t.Errorf("path = %q, want \"/notify\"", path)
	}
	if authorization != "Bearer access-token" {
		t.Errorf("Authorization = %q, want \"Bearer access-token\"", authorization)
	}
	want := map[string]any{
		"smtp_host":     "mail.example.com",
		"smtp_port":     float64(587),
		"smtp_username": "no-reply@example.com",
		"smtp_sender":   "no-reply@example.com",
		"recipients":    []any{map[string]any{"email": "trader@example.com"}},
		"subject":       "Tom & Jerry's application",
		"text_body":     "Dear <Name>, {{.not_a_template}}",
		"html_body":     "<p>Dear Tom &amp; Jerry</p>",
	}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("body = %#v, want %#v", body, want)
	}
}

// The sender's display name, when set, is PingMailer's smtp_username, and a
// configured path overrides the default /notify endpoint.
func TestEmailProvider_Send_SenderNameAndCustomPath(t *testing.T) {
	t.Parallel()
	var path string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	cfg := validEmailConfig(srv.URL)
	cfg.Path = "/v1/notify"
	cfg.SenderName = "Trade National Single Window"
	p, err := providers.NewEmailProvider(cfg)
	if err != nil {
		t.Fatalf("NewEmailProvider: %v", err)
	}

	if err := p.Send(context.Background(), notification.Request{Channel: notification.ChannelEmail, To: "a@example.com", Body: "hi"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if path != "/v1/notify" {
		t.Errorf("path = %q, want \"/v1/notify\"", path)
	}
	if body["smtp_username"] != "Trade National Single Window" {
		t.Errorf("smtp_username = %v, want \"Trade National Single Window\"", body["smtp_username"])
	}
	if body["smtp_sender"] != "no-reply@example.com" {
		t.Errorf("smtp_sender = %v, want \"no-reply@example.com\"", body["smtp_sender"])
	}
	if _, ok := body["html_body"]; ok {
		t.Errorf("html_body present in body %v; an email without an HTML body sends none", body)
	}
}

// A refusal from PingMailer is the send's error.
func TestEmailProvider_Send_Refused(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Missing required field: text_body or html_body", http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	p, err := providers.NewEmailProvider(validEmailConfig(srv.URL))
	if err != nil {
		t.Fatalf("NewEmailProvider: %v", err)
	}

	if err := p.Send(context.Background(), notification.Request{Channel: notification.ChannelEmail, To: "a@example.com", Body: "hi"}); err == nil {
		t.Fatal("Send = nil, want error")
	}
}

// The token is used exactly as configured. configyaml has already resolved
// any placeholder, so a token that happens to look like a secret reference
// ("file:...", "env:...") must not be resolved again.
func TestEmailProvider_UsesTokenAsWritten(t *testing.T) {
	t.Parallel()
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	const token = "file:/not/a/path/on/this/machine"
	cfg := validEmailConfig(srv.URL)
	cfg.Auth = auth.Config{Type: auth.TypeBearer, Bearer: &auth.BearerConfig{Token: token}}
	p, err := providers.NewEmailProvider(cfg)
	if err != nil {
		t.Fatalf("NewEmailProvider: %v", err)
	}
	if err := p.Send(context.Background(), notification.Request{Channel: notification.ChannelEmail, To: "a@b.com", Body: "hi"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if authHeader := <-got; authHeader != "Bearer "+token {
		t.Errorf("Authorization = %q, want %q", authHeader, "Bearer "+token)
	}
}
