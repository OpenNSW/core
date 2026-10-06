# notifications

`github.com/OpenNSW/core/notifications` is its own Go module. It moved from `github.com/OpenNSW/core/notification` in the root module. The package is still named `notification`, so only the import path changes; code keeps using `notification.X`. Import it with the name written out, as `goimports` writes it when a package's name differs from its path:

```go
import notification "github.com/OpenNSW/core/notifications"
```

A multi-channel notification router with a pluggable provider model. Your application registers one provider per channel type (email, SMS, etc.); the manager dispatches each `Request` to the correct provider at runtime.

## Usage

Each provider takes its own typed config in its constructor and is handed, ready to send, to `NewManager`:

```go
import (
    notification "github.com/OpenNSW/core/notifications"
    "github.com/OpenNSW/core/notifications/providers"
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
    baseURL: https://email.example.com
    token: "{{env:EMAIL_TOKEN}}"
```

The config fields are typed, so a value decodes into its field's type: a secret that only looks like a number (a password of `12345678`, a SID code of `0123`) stays the string it was. The email `token` is the bearer token itself; configyaml resolves the placeholder, and the provider uses it as written.
