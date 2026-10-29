// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package notification

import (
	"context"
	"errors"
	"testing"
)

// stubProvider is a test double for Provider.
type stubProvider struct {
	channelType ChannelType
	sendErr     error
	sendCalled  bool
}

func (s *stubProvider) Type() ChannelType { return s.channelType }

func (s *stubProvider) Send(_ context.Context, _ Request) error {
	s.sendCalled = true
	return s.sendErr
}

func TestNewManager(t *testing.T) {
	t.Parallel()

	t.Run("routes to the provider for the channel", func(t *testing.T) {
		t.Parallel()
		p := &stubProvider{channelType: ChannelEmail}
		m, err := NewManager(p)
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		if err := m.Send(context.Background(), Request{Channel: ChannelEmail, To: "a@b.com", Body: "hi"}); err != nil {
			t.Errorf("Send: %v", err)
		}
		if !p.sendCalled {
			t.Error("Send was not called on provider")
		}
	})

	t.Run("no providers", func(t *testing.T) {
		t.Parallel()
		if _, err := NewManager(); !errors.Is(err, ErrNoProviders) {
			t.Errorf("got %v, want ErrNoProviders", err)
		}
	})

	t.Run("nil provider", func(t *testing.T) {
		t.Parallel()
		if _, err := NewManager(nil); err == nil {
			t.Fatal("expected an error for a nil provider, got nil")
		}
	})

	t.Run("two providers for one channel", func(t *testing.T) {
		t.Parallel()
		if _, err := NewManager(&stubProvider{channelType: ChannelEmail}, &stubProvider{channelType: ChannelEmail}); err == nil {
			t.Fatal("expected an error for a duplicate channel, got nil")
		}
	})
}

func TestManager_Send(t *testing.T) {
	t.Parallel()

	makeManager := func(t *testing.T, p *stubProvider) *Manager {
		t.Helper()
		m, err := NewManager(p)
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		return m
	}

	t.Run("invalid request — validate error, provider not called", func(t *testing.T) {
		t.Parallel()
		p := &stubProvider{channelType: ChannelEmail}
		m := makeManager(t, p)
		err := m.Send(context.Background(), Request{}) // empty channel
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if p.sendCalled {
			t.Error("provider Send should not be called on invalid request")
		}
	})

	t.Run("unknown channel — error", func(t *testing.T) {
		t.Parallel()
		p := &stubProvider{channelType: ChannelEmail}
		m := makeManager(t, p)
		err := m.Send(context.Background(), Request{Channel: ChannelSMS, To: "+1", Body: "hi"})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("provider Send failure propagates", func(t *testing.T) {
		t.Parallel()
		sendErr := errors.New("provider down")
		p := &stubProvider{channelType: ChannelEmail, sendErr: sendErr}
		m := makeManager(t, p)
		err := m.Send(context.Background(), Request{Channel: ChannelEmail, To: "a@b.com", Body: "hi"})
		if !errors.Is(err, sendErr) {
			t.Errorf("got %v, want wrapping %v", err, sendErr)
		}
	})
}
