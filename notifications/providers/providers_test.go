// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package providers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	notification "github.com/OpenNSW/core/notifications"
	"github.com/OpenNSW/core/notifications/providers"
)

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
    token: true
`), &cfg)
	if err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}

	sms, email := cfg.Notification.SMS, cfg.Notification.Email
	if sms.Password != "12345678" || sms.SIDCode != "0123" || email.Token != "true" {
		t.Errorf("password, sidCode, token = %q, %q, %q; want \"12345678\", \"0123\", \"true\"", sms.Password, sms.SIDCode, email.Token)
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
		cfg  providers.EmailConfig
		want string
	}{
		{"no baseURL", providers.EmailConfig{Token: "t"}, "baseURL is required"},
		{"no token", providers.EmailConfig{BaseURL: "https://email.example.com"}, "token is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := providers.NewEmailProvider(tc.cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("NewEmailProvider = %v, want an error containing %q", err, tc.want)
			}
		})
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
	p, err := providers.NewEmailProvider(providers.EmailConfig{BaseURL: srv.URL, Token: token})
	if err != nil {
		t.Fatalf("NewEmailProvider: %v", err)
	}
	if err := p.Send(context.Background(), notification.Request{Channel: notification.ChannelEmail, To: "a@b.com", Body: "hi"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if auth := <-got; auth != "Bearer "+token {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer "+token)
	}
}
