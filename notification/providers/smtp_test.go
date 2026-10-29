// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package providers_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/OpenNSW/core/notification"
	"github.com/OpenNSW/core/notification/providers"
)

// smtpSession is what the fake server saw in one session.
type smtpSession struct {
	from, rcpt string
	data       string
	// tls reports whether the message arrived over TLS, and auth is the
	// decoded AUTH PLAIN response, if any.
	tls  bool
	auth string
}

// fakeSMTPOptions configures fakeSMTPServer.
type fakeSMTPOptions struct {
	// stall accepts connections and never greets.
	stall bool
	// tls, when set, is the server's TLS config: it offers STARTTLS, or
	// speaks TLS from the start when implicitTLS is set, and offers AUTH
	// PLAIN once the connection is secured.
	tls         *tls.Config
	implicitTLS bool
}

// fakeSMTPServer is a minimal SMTP server on loopback that records each
// session.
type fakeSMTPServer struct {
	ln       net.Listener
	opts     fakeSMTPOptions
	sessions chan smtpSession
}

func newFakeSMTPServer(t *testing.T, opts fakeSMTPOptions) *fakeSMTPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeSMTPServer{ln: ln, opts: opts, sessions: make(chan smtpSession, 1)}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

func (s *fakeSMTPServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *fakeSMTPServer) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	if s.opts.stall {
		_, _ = io.Copy(io.Discard, conn)
		return
	}
	var sess smtpSession
	if s.opts.implicitTLS {
		conn = tls.Server(conn, s.opts.tls)
		sess.tls = true
	}
	tp := textproto.NewConn(conn)
	_ = tp.PrintfLine("220 fake ESMTP")
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO":
			lines := []string{"fake"}
			if s.opts.tls != nil && !sess.tls {
				lines = append(lines, "STARTTLS")
			}
			if sess.tls {
				lines = append(lines, "AUTH PLAIN")
			}
			for i, l := range lines {
				sep := "-"
				if i == len(lines)-1 {
					sep = " "
				}
				_ = tp.PrintfLine("250%s%s", sep, l)
			}
		case "STARTTLS":
			_ = tp.PrintfLine("220 ready to start TLS")
			conn = tls.Server(conn, s.opts.tls)
			tp = textproto.NewConn(conn)
			sess.tls = true
		case "AUTH":
			mech, resp, _ := strings.Cut(arg, " ")
			decoded, err := base64.StdEncoding.DecodeString(resp)
			if !strings.EqualFold(mech, "PLAIN") || err != nil {
				_ = tp.PrintfLine("504 unsupported")
				continue
			}
			sess.auth = string(decoded)
			_ = tp.PrintfLine("235 authenticated")
		case "MAIL":
			sess.from = strings.TrimPrefix(line, "MAIL FROM:")
			_ = tp.PrintfLine("250 ok")
		case "RCPT":
			sess.rcpt = strings.TrimPrefix(line, "RCPT TO:")
			if strings.Contains(sess.rcpt, "reject") {
				_ = tp.PrintfLine("550 no such user")
				continue
			}
			_ = tp.PrintfLine("250 ok")
		case "DATA":
			_ = tp.PrintfLine("354 go ahead")
			data, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			sess.data = string(data)
			_ = tp.PrintfLine("250 queued")
			s.sessions <- sess
		case "QUIT":
			_ = tp.PrintfLine("221 bye")
			return
		default:
			_ = tp.PrintfLine("502 not implemented")
		}
	}
}

// testServerTLS returns a TLS config for a server on 127.0.0.1 and a pool
// that trusts its certificate. It borrows httptest's self-signed
// certificate, which is valid for 127.0.0.1.
func testServerTLS(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return &tls.Config{Certificates: srv.TLS.Certificates}, pool
}

func plainSMTPConfig(port int) providers.SMTPConfig {
	return providers.SMTPConfig{
		Host:    "127.0.0.1",
		Port:    port,
		TLSMode: providers.SMTPTLSNone,
		Sender:  "no-reply@example.com",
	}
}

func TestNewSMTPProvider_Validates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*providers.SMTPConfig)
		want string
	}{
		{"valid", func(*providers.SMTPConfig) {}, ""},
		{"defaults", func(c *providers.SMTPConfig) { c.Port, c.TLSMode = 0, "" }, ""},
		{"starttls with credentials", func(c *providers.SMTPConfig) {
			c.TLSMode, c.Username, c.Password = providers.SMTPTLSStartTLS, "user", "pass"
		}, ""},
		{"no host", func(c *providers.SMTPConfig) { c.Host = "" }, "host is required"},
		{"bad port", func(c *providers.SMTPConfig) { c.Port = 70000 }, "port"},
		{"bad tlsMode", func(c *providers.SMTPConfig) { c.TLSMode = "ssl" }, "tlsMode"},
		{"credentials in plain text", func(c *providers.SMTPConfig) { c.Username, c.Password = "user", "pass" }, "need tlsMode"},
		{"password without username", func(c *providers.SMTPConfig) {
			c.TLSMode, c.Password = providers.SMTPTLSStartTLS, "pass"
		}, "without a username"},
		{"sender not an address", func(c *providers.SMTPConfig) { c.Sender = "no-reply" }, "sender"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := plainSMTPConfig(2525)
			tc.edit(&cfg)
			_, err := providers.NewSMTPProvider(cfg)
			if tc.want == "" {
				if err != nil {
					t.Errorf("NewSMTPProvider = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("NewSMTPProvider = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func sendAndReceive(t *testing.T, cfg providers.SMTPConfig, srv *fakeSMTPServer, req notification.Request) (smtpSession, *mail.Message) {
	t.Helper()
	p, err := providers.NewSMTPProvider(cfg)
	if err != nil {
		t.Fatalf("NewSMTPProvider: %v", err)
	}
	if err := p.Send(context.Background(), req); err != nil {
		t.Fatalf("Send: %v", err)
	}
	sess := <-srv.sessions
	msg, err := mail.ReadMessage(strings.NewReader(sess.data))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	return sess, msg
}

// A text-only email goes to the envelope recipient as a quoted-printable
// text/plain message from the sender, with its subject encoded.
func TestSMTPProvider_Send_TextOnly(t *testing.T) {
	t.Parallel()
	srv := newFakeSMTPServer(t, fakeSMTPOptions{})
	cfg := plainSMTPConfig(srv.port())
	cfg.SenderName = "Trade National Single Window"

	sess, msg := sendAndReceive(t, cfg, srv, notification.Request{
		Channel: notification.ChannelEmail,
		To:      "Trader <trader@example.com>",
		Subject: "Ünïcode subject\r\nBcc: victim@example.com",
		Body:    "Dear Tom & Jerry,\nyour application is ready.",
	})

	if sess.from != "<no-reply@example.com>" || sess.rcpt != "<trader@example.com>" {
		t.Errorf("envelope = %q -> %q, want <no-reply@example.com> -> <trader@example.com>", sess.from, sess.rcpt)
	}
	if got := msg.Header.Get("From"); got != `"Trade National Single Window" <no-reply@example.com>` {
		t.Errorf("From = %q", got)
	}
	if msg.Header.Get("Bcc") != "" {
		t.Errorf("subject injected a Bcc header: %q", msg.Header.Get("Bcc"))
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subject != "Ünïcode subject\r\nBcc: victim@example.com" {
		t.Errorf("Subject = %q (%v)", subject, err)
	}
	if ct := msg.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if msg.Header.Get("Message-ID") == "" || msg.Header.Get("Date") == "" {
		t.Errorf("Message-ID or Date missing: %v", msg.Header)
	}
	body, _ := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if got := strings.TrimSuffix(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n"); got != "Dear Tom & Jerry,\nyour application is ready." {
		t.Errorf("body = %q", got)
	}
}

// An email with both bodies is multipart/alternative, text first.
func TestSMTPProvider_Send_TextAndHTML(t *testing.T) {
	t.Parallel()
	srv := newFakeSMTPServer(t, fakeSMTPOptions{})

	_, msg := sendAndReceive(t, plainSMTPConfig(srv.port()), srv, notification.Request{
		Channel:  notification.ChannelEmail,
		To:       "trader@example.com",
		Subject:  "Application received",
		Body:     "Your application has been received.",
		HTMLBody: "<p>Your application has been <strong>received</strong>.</p>",
	})

	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" {
		t.Fatalf("Content-Type = %q (%v)", msg.Header.Get("Content-Type"), err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var parts []string
	for {
		part, err := mr.NextPart() // decodes quoted-printable
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		b, _ := io.ReadAll(part)
		parts = append(parts, part.Header.Get("Content-Type")+"|"+string(b))
	}
	want := []string{
		"text/plain; charset=utf-8|Your application has been received.",
		"text/html; charset=utf-8|<p>Your application has been <strong>received</strong>.</p>",
	}
	if strings.Join(parts, "\n") != strings.Join(want, "\n") {
		t.Errorf("parts = %q, want %q", parts, want)
	}
}

func TestSMTPProvider_Send_HTMLOnly(t *testing.T) {
	t.Parallel()
	srv := newFakeSMTPServer(t, fakeSMTPOptions{})
	_, msg := sendAndReceive(t, plainSMTPConfig(srv.port()), srv, notification.Request{
		Channel: notification.ChannelEmail, To: "a@example.com", HTMLBody: "<p>hi</p>",
	})
	if ct := msg.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestSMTPProvider_Send_Errors(t *testing.T) {
	t.Parallel()

	t.Run("recipient refused", func(t *testing.T) {
		t.Parallel()
		srv := newFakeSMTPServer(t, fakeSMTPOptions{})
		p, _ := providers.NewSMTPProvider(plainSMTPConfig(srv.port()))
		err := p.Send(context.Background(), notification.Request{Channel: notification.ChannelEmail, To: "reject@example.com", Body: "hi"})
		if err == nil || !strings.Contains(err.Error(), "550") {
			t.Errorf("Send = %v, want the server's 550", err)
		}
	})

	t.Run("to not an address", func(t *testing.T) {
		t.Parallel()
		p, _ := providers.NewSMTPProvider(plainSMTPConfig(2525))
		err := p.Send(context.Background(), notification.Request{Channel: notification.ChannelEmail, To: "nobody", Body: "hi"})
		if err == nil || !strings.Contains(err.Error(), "to:") {
			t.Errorf("Send = %v, want a to: error", err)
		}
	})

	t.Run("server without STARTTLS", func(t *testing.T) {
		t.Parallel()
		srv := newFakeSMTPServer(t, fakeSMTPOptions{})
		cfg := plainSMTPConfig(srv.port())
		cfg.TLSMode = providers.SMTPTLSStartTLS
		p, _ := providers.NewSMTPProvider(cfg)
		err := p.Send(context.Background(), notification.Request{Channel: notification.ChannelEmail, To: "a@example.com", Body: "hi"})
		if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
			t.Errorf("Send = %v, want a STARTTLS error", err)
		}
	})

	t.Run("context deadline on a stalled server", func(t *testing.T) {
		t.Parallel()
		srv := newFakeSMTPServer(t, fakeSMTPOptions{stall: true})
		p, _ := providers.NewSMTPProvider(plainSMTPConfig(srv.port()))
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		start := time.Now()
		err := p.Send(ctx, notification.Request{Channel: notification.ChannelEmail, To: "a@example.com", Body: "hi"})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Send = %v, want context.DeadlineExceeded", err)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("Send took %v; the deadline did not stop it", elapsed)
		}
	})
}

// Over STARTTLS and implicit TLS, the provider verifies the server's
// certificate, then authenticates and sends inside the encrypted connection.
func TestSMTPProvider_Send_TLSModes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		mode     providers.SMTPTLSMode
		implicit bool
	}{
		{"starttls", providers.SMTPTLSStartTLS, false},
		{"tls", providers.SMTPTLSImplicit, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			serverTLS, roots := testServerTLS(t)
			srv := newFakeSMTPServer(t, fakeSMTPOptions{tls: serverTLS, implicitTLS: tc.implicit})
			cfg := plainSMTPConfig(srv.port())
			cfg.TLSMode, cfg.Username, cfg.Password = tc.mode, "smtp-user", "s3cret"

			p, err := providers.NewSMTPProvider(cfg)
			if err != nil {
				t.Fatalf("NewSMTPProvider: %v", err)
			}
			providers.SetSMTPRootCAs(p, roots)
			if err := p.Send(context.Background(), notification.Request{Channel: notification.ChannelEmail, To: "a@example.com", Subject: "hi", Body: "hi"}); err != nil {
				t.Fatalf("Send: %v", err)
			}
			sess := <-srv.sessions
			if !sess.tls {
				t.Error("message arrived in plain text, want TLS")
			}
			if sess.auth != "\x00smtp-user\x00s3cret" {
				t.Errorf("AUTH PLAIN = %q, want \"\\x00smtp-user\\x00s3cret\"", sess.auth)
			}
			if sess.rcpt != "<a@example.com>" {
				t.Errorf("rcpt = %q", sess.rcpt)
			}
		})

		// Without the test roots, the self-signed certificate is refused and
		// nothing is sent.
		t.Run(tc.name+" untrusted certificate", func(t *testing.T) {
			t.Parallel()
			serverTLS, _ := testServerTLS(t)
			srv := newFakeSMTPServer(t, fakeSMTPOptions{tls: serverTLS, implicitTLS: tc.implicit})
			cfg := plainSMTPConfig(srv.port())
			cfg.TLSMode = tc.mode

			p, err := providers.NewSMTPProvider(cfg)
			if err != nil {
				t.Fatalf("NewSMTPProvider: %v", err)
			}
			err = p.Send(context.Background(), notification.Request{Channel: notification.ChannelEmail, To: "a@example.com", Body: "hi"})
			if _, ok := errors.AsType[*tls.CertificateVerificationError](err); !ok {
				t.Errorf("Send = %v, want a certificate verification error", err)
			}
			select {
			case sess := <-srv.sessions:
				t.Errorf("server received a message: %+v", sess)
			default:
			}
		})
	}
}

// The provider plugs into the manager as the email channel.
func TestSMTPProvider_WithManager(t *testing.T) {
	t.Parallel()
	srv := newFakeSMTPServer(t, fakeSMTPOptions{})
	p, err := providers.NewSMTPProvider(plainSMTPConfig(srv.port()))
	if err != nil {
		t.Fatalf("NewSMTPProvider: %v", err)
	}
	m, err := notification.NewManager(p)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := m.Send(context.Background(), notification.Request{Channel: notification.ChannelEmail, To: "a@example.com", Body: "hi"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sess := <-srv.sessions; sess.rcpt != "<a@example.com>" {
		t.Errorf("rcpt = %q", sess.rcpt)
	}
}
