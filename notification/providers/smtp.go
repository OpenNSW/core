// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package providers

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/OpenNSW/core/notification"
)

// SMTPTLSMode is how the SMTP provider secures its connection to the server.
type SMTPTLSMode string

const (
	// SMTPTLSStartTLS connects in plain text and upgrades with STARTTLS
	// before authenticating or sending. The server must offer STARTTLS.
	SMTPTLSStartTLS SMTPTLSMode = "starttls"
	// SMTPTLSImplicit connects over TLS from the start (SMTPS, usually port
	// 465).
	SMTPTLSImplicit SMTPTLSMode = "tls"
	// SMTPTLSNone sends in plain text. It is meant for a local relay or a
	// development mail catcher, and cannot be combined with credentials.
	SMTPTLSNone SMTPTLSMode = "none"
)

// SMTPConfig is the SMTP provider's configuration. It carries yaml tags so an
// application can embed it in its own config and load it with
// configyaml.LoadAndExpand.
type SMTPConfig struct {
	// Host is the SMTP server's host name. With TLS, it is also the name
	// the server's certificate is verified against.
	Host string `yaml:"host"`
	// Port is the SMTP server's port. Optional: defaults to 587 for
	// starttls, 465 for tls and 25 for none.
	Port int `yaml:"port"`
	// TLSMode is starttls, tls or none. Optional: defaults to starttls.
	TLSMode SMTPTLSMode `yaml:"tlsMode"`
	// Username and Password are the SMTP credentials, sent with AUTH PLAIN.
	// Optional: the provider sends without authenticating when Username is
	// empty.
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	// Sender is the address every email is sent from: the envelope sender
	// and the From header's address.
	Sender string `yaml:"sender"`
	// SenderName is the From header's display name. Optional: the From
	// header is the bare Sender without it.
	SenderName string `yaml:"senderName"`
}

// Validate reports a missing or invalid setting.
func (c SMTPConfig) Validate() error {
	if c.Host == "" {
		return errors.New("smtp: host is required")
	}
	if c.Port < 0 || c.Port > 65535 {
		return errors.New("smtp: port must be between 1 and 65535")
	}
	switch c.TLSMode {
	case "", SMTPTLSStartTLS, SMTPTLSImplicit, SMTPTLSNone:
	default:
		return fmt.Errorf("smtp: tlsMode %q is not starttls, tls or none", c.TLSMode)
	}
	if c.TLSMode == SMTPTLSNone && c.Username != "" {
		return errors.New("smtp: username and password need tlsMode starttls or tls")
	}
	if c.Username == "" && c.Password != "" {
		return errors.New("smtp: password is set without a username")
	}
	if _, err := mail.ParseAddress(c.Sender); err != nil {
		return fmt.Errorf("smtp: sender: %w", err)
	}
	return nil
}

// SMTPProvider sends email notifications straight to an SMTP server. It opens
// a connection per email, so a nil error from Send means the server accepted
// the message for delivery.
type SMTPProvider struct {
	cfg  SMTPConfig
	from mail.Address
	// rootCAs verifies the server's certificate; nil uses the system roots.
	rootCAs *x509.CertPool
}

// smtpDefaultTimeout bounds a send whose context has no deadline.
const smtpDefaultTimeout = 30 * time.Second

// NewSMTPProvider validates cfg and returns an SMTPProvider ready to send.
func NewSMTPProvider(cfg SMTPConfig) (*SMTPProvider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.TLSMode == "" {
		cfg.TLSMode = SMTPTLSStartTLS
	}
	if cfg.Port == 0 {
		switch cfg.TLSMode {
		case SMTPTLSImplicit:
			cfg.Port = 465
		case SMTPTLSNone:
			cfg.Port = 25
		default:
			cfg.Port = 587
		}
	}
	sender, _ := mail.ParseAddress(cfg.Sender)
	return &SMTPProvider{
		cfg:  cfg,
		from: mail.Address{Name: cfg.SenderName, Address: sender.Address},
	}, nil
}

func (s *SMTPProvider) Type() notification.ChannelType { return notification.ChannelEmail }

// Send delivers the email to the SMTP server. The connection is closed when
// ctx is done, and is bounded by a default timeout when ctx has no deadline.
func (s *SMTPProvider) Send(ctx context.Context, req notification.Request) error {
	if s == nil || s.cfg.Host == "" {
		return errors.New("smtp provider not configured")
	}
	to, err := mail.ParseAddress(req.To)
	if err != nil {
		return fmt.Errorf("smtp send: to: %w", err)
	}
	msg, err := s.buildMessage(to, req)
	if err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	if err := s.deliver(ctx, to.Address, msg); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}

func (s *SMTPProvider) deliver(ctx context.Context, to string, msg []byte) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, smtpDefaultTimeout)
		defer cancel()
	}
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	tlsConfig := &tls.Config{ServerName: s.cfg.Host, RootCAs: s.rootCAs, MinVersion: tls.VersionTLS12}

	var conn net.Conn
	var err error
	if s.cfg.TLSMode == SMTPTLSImplicit {
		conn, err = (&tls.Dialer{Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	// Closing the connection unblocks any read or write in progress.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return ctxErr(ctx, err)
	}
	defer func() { _ = c.Close() }()

	if s.cfg.TLSMode == SMTPTLSStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("server does not offer STARTTLS")
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			return ctxErr(ctx, fmt.Errorf("starttls: %w", err))
		}
	}
	if s.cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return ctxErr(ctx, fmt.Errorf("auth: %w", err))
		}
	}
	if err := c.Mail(s.from.Address); err != nil {
		return ctxErr(ctx, fmt.Errorf("mail from: %w", err))
	}
	if err := c.Rcpt(to); err != nil {
		return ctxErr(ctx, fmt.Errorf("rcpt to: %w", err))
	}
	w, err := c.Data()
	if err != nil {
		return ctxErr(ctx, fmt.Errorf("data: %w", err))
	}
	if _, err := w.Write(msg); err != nil {
		return ctxErr(ctx, fmt.Errorf("data: %w", err))
	}
	if err := w.Close(); err != nil {
		return ctxErr(ctx, fmt.Errorf("data: %w", err))
	}
	// The server has accepted the message; a failed QUIT doesn't undo that.
	_ = c.Quit()
	return nil
}

// ctxErr reports ctx's error alongside err when ctx is done, since a canceled
// or expired send surfaces as a closed-connection or timeout error. The
// connection's deadline is ctx's deadline, so a timeout can arrive just
// before ctx reports it has expired; that counts as ctx's deadline too.
func ctxErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%w: %w", ctx.Err(), err)
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return fmt.Errorf("%w: %w", context.DeadlineExceeded, err)
	}
	return err
}

// buildMessage renders req as a MIME message: text/plain or text/html when
// the request has one body, multipart/alternative when it has both. Bodies
// are quoted-printable, and the subject is encoded as needed, so neither can
// inject headers.
func (s *SMTPProvider) buildMessage(to *mail.Address, req notification.Request) ([]byte, error) {
	var buf bytes.Buffer
	header := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }

	header("From", s.from.String())
	header("To", to.String())
	header("Subject", mime.QEncoding.Encode("utf-8", req.Subject))
	header("Date", time.Now().Format(time.RFC1123Z))
	header("Message-ID", messageID(s.from.Address))
	header("MIME-Version", "1.0")

	if req.Body != "" && req.HTMLBody != "" {
		mw := multipart.NewWriter(&buf)
		header("Content-Type", "multipart/alternative; boundary="+mw.Boundary())
		buf.WriteString("\r\n")
		for _, part := range []struct{ contentType, body string }{
			{"text/plain", req.Body},
			{"text/html", req.HTMLBody},
		} {
			pw, err := mw.CreatePart(textproto.MIMEHeader{
				"Content-Type":              {part.contentType + "; charset=utf-8"},
				"Content-Transfer-Encoding": {"quoted-printable"},
			})
			if err != nil {
				return nil, err
			}
			if err := writeQuotedPrintable(pw, part.body); err != nil {
				return nil, err
			}
		}
		if err := mw.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	contentType, body := "text/plain", req.Body
	if req.Body == "" {
		contentType, body = "text/html", req.HTMLBody
	}
	header("Content-Type", contentType+"; charset=utf-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	buf.WriteString("\r\n")
	if err := writeQuotedPrintable(&buf, body); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeQuotedPrintable(w io.Writer, body string) error {
	qw := quotedprintable.NewWriter(w)
	if _, err := qw.Write([]byte(body)); err != nil {
		return err
	}
	return qw.Close()
}

// messageID returns a unique Message-ID on the sender's domain.
func messageID(sender string) string {
	domain := "localhost"
	if at := strings.LastIndexByte(sender, '@'); at >= 0 {
		domain = sender[at+1:]
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "<" + hex.EncodeToString(b) + "@" + domain + ">"
}
