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

The config fields are typed, so a value decodes into its field's type: a secret that only looks like a number (a password of `12345678`, a SID code of `0123`) stays the string it was. Secret placeholders are resolved by `configyaml`, and the provider uses the resolved values as written.
