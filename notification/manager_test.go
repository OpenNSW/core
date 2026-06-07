package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

// stubProvider is a test double for Provider.
type stubProvider struct {
	channelType     ChannelType
	configureErr    error
	sendErr         error
	configureCalled bool
	sendCalled      bool
}

func (s *stubProvider) Type() ChannelType { return s.channelType }

func (s *stubProvider) Configure(_ json.RawMessage) error {
	s.configureCalled = true
	return s.configureErr
}

func (s *stubProvider) Send(_ context.Context, _ Request) error {
	s.sendCalled = true
	return s.sendErr
}

func TestNewManager(t *testing.T) {
	t.Parallel()

	validReq := func(ch ChannelType) Request {
		return Request{Channel: ch, To: "a@b.com", Body: "hi"}
	}

	t.Run("happy path — single provider", func(t *testing.T) {
		t.Parallel()
		f := writeTempJSON(t, `{"email":{"host":"localhost"}}`)
		p := &stubProvider{channelType: ChannelEmail}
		m, err := NewManager(Config{Path: f}, p)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !p.configureCalled {
			t.Error("Configure was not called")
		}
		if err := m.Send(context.Background(), validReq(ChannelEmail)); err != nil {
			t.Errorf("Send: %v", err)
		}
		if !p.sendCalled {
			t.Error("Send was not called on provider")
		}
	})

	t.Run("missing config key for provider", func(t *testing.T) {
		t.Parallel()
		f := writeTempJSON(t, `{"sms":{"host":"localhost"}}`)
		p := &stubProvider{channelType: ChannelEmail}
		_, err := NewManager(Config{Path: f}, p)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("Configure failure propagates", func(t *testing.T) {
		t.Parallel()
		f := writeTempJSON(t, `{"email":{}}`)
		configErr := errors.New("bad config")
		p := &stubProvider{channelType: ChannelEmail, configureErr: configErr}
		_, err := NewManager(Config{Path: f}, p)
		if !errors.Is(err, configErr) {
			t.Errorf("got %v, want wrapping %v", err, configErr)
		}
	})

	t.Run("invalid Config.Path", func(t *testing.T) {
		t.Parallel()
		_, err := NewManager(Config{})
		if !errors.Is(err, ErrConfigPathRequired) {
			t.Errorf("got %v, want ErrConfigPathRequired", err)
		}
	})

	t.Run("bad JSON config file", func(t *testing.T) {
		t.Parallel()
		f := writeTempJSON(t, `not-json`)
		_, err := NewManager(Config{Path: f})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("duplicate channel type returns error", func(t *testing.T) {
		t.Parallel()
		f := writeTempJSON(t, `{"email":{"a":1}}`)
		p1 := &stubProvider{channelType: ChannelEmail}
		p2 := &stubProvider{channelType: ChannelEmail}
		_, err := NewManager(Config{Path: f}, p1, p2)
		if err == nil {
			t.Fatal("expected error for duplicate channel type, got nil")
		}
	})
}

func TestManager_Send(t *testing.T) {
	t.Parallel()

	makeManager := func(t *testing.T, p *stubProvider) *Manager {
		t.Helper()
		key := string(p.channelType)
		f := writeTempJSON(t, fmt.Sprintf(`{%q:{}}`, key))
		m, err := NewManager(Config{Path: f}, p)
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
