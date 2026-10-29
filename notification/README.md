# notification

`github.com/OpenNSW/core/notification` is a multi-channel notification router with a pluggable provider model. Your application registers one provider per channel type (email, SMS, etc.); the manager dispatches each `Request` to the correct provider at runtime.

## Usage

Each provider takes its own typed config in its constructor and is handed, ready to send, to `NewManager`:

```go
import (
    "github.com/OpenNSW/core/notification"
    "github.com/OpenNSW/core/notification/providers"
)

sms, err := providers.NewSMSProvider(cfg.Notification.SMS)
email, err := providers.NewEmailProvider(cfg.Notification.Email)

manager, err := notification.NewManager(sms, email)

err = manager.Send(ctx, notification.Request{
    Channel:  notification.ChannelEmail,
    To:       "applicant@example.com",
    Subject:  "Application received",
    Body:     "Your application #12345 has been received and is under review.",
    HTMLBody: "<p>Your application <strong>#12345</strong> has been received.</p>",
})
```

`NewManager` returns an error when it gets no providers, a nil one, or two for the same channel.

## Providers

| Constructor                     | Channel | Sends through                                   |
|---------------------------------|---------|-------------------------------------------------|
| `providers.NewSMSProvider`      | `sms`   | GovSMS V1 API                                   |
| `providers.NewEmailProvider`    | `email` | PingMailer's API, which relays to SMTP (XOAUTH2) |
| `providers.NewSMTPProvider`     | `email` | Any SMTP server, directly                       |

`NewEmailProvider` and `NewSMTPProvider` both handle the `email` channel, so register one or the other.

The SMTP provider opens a connection per email. It secures the connection with STARTTLS (the default), implicit TLS (`tlsMode: tls`), or not at all (`tlsMode: none`, for a local relay or a development mail catcher, and never with credentials). It authenticates with AUTH PLAIN when `username` is set. A send is bounded by its context, or by 30 seconds when the context has no deadline, and a nil error means the server accepted the message.

## Channels

| Constant                    | Value     |
|-----------------------------|-----------|
| `notification.ChannelEmail` | `"email"` |
| `notification.ChannelSMS`   | `"sms"`   |

## Writing a provider

Implement `notification.Provider`:

```go
type Provider interface {
    Type() ChannelType
    Send(ctx context.Context, req Request) error
}
```

- `Type()` declares which channel this provider handles.
- `Send` delivers the message.

Give the provider its own exported config type, with `yaml` tags, and a constructor that validates it and returns a ready provider, as `providers.NewSMSProvider` and `providers.NewEmailProvider` do:

```go
type MyEmailConfig struct {
    APIKey string `yaml:"apiKey"`
}

func NewMyEmailProvider(cfg MyEmailConfig) (*MyEmailProvider, error) {
    if cfg.APIKey == "" {
        return nil, errors.New("apiKey is required")
    }
    return &MyEmailProvider{apiKey: cfg.APIKey}, nil
}
```

## Provider configuration

An application embeds the provider configs in its own config struct and loads it with [`configyaml.LoadAndExpand`](../configyaml/README.md), so a provider's credentials can come from an env var or a mounted file instead of the checked-in config:

```go
type AppConfig struct {
    Notification struct {
        SMS   providers.SMSConfig   `yaml:"sms"`
        Email providers.EmailConfig `yaml:"email"`
    } `yaml:"notification"`
}
```

```yaml
notification:
  sms:
    baseURL: https://sms.example.com
    userName: nsw
    password: "{{env:SMS_PASSWORD}}"
    sidCode: NSW
  email:
    baseURL: https://pingmailer.example.com
    # path: /notify # optional, defaults to "/notify"
    # allowInsecureHTTP: true # only for a plain-HTTP in-cluster URL
    auth:
      type: oauth2
      oauth2:
        tokenURL: https://idp.example.com/oauth2/token
        clientID: replace-with-pingmailer-client-id
        clientSecret: "{{env:NOTIFICATION_EMAIL_CLIENT_SECRET}}"
        endpointParams:
          resource: https://mail.example.com
    smtpHost: mail.example.com
    smtpPort: 587
    sender: no-reply@example.com
    # senderName: Trade National Single Window
```

Or, to send through an SMTP server directly, configure `providers.SMTPConfig` in place of the email provider:

```yaml
notification:
  smtp:
    host: smtp.example.com
    # port: 587 # optional: 587 for starttls, 465 for tls, 25 for none
    # tlsMode: starttls # starttls (default), tls or none
    username: no-reply@example.com
    password: "{{env:NOTIFICATION_SMTP_PASSWORD}}"
    sender: no-reply@example.com
    # senderName: Trade National Single Window
```

The config fields are typed, so a value decodes into its field's type: a secret that only looks like a number (a password of `12345678`, a SID code of `0123`) stays the string it was. Secret placeholders are resolved by `configyaml`, and the provider uses the resolved values as written.
