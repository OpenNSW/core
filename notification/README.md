# notification

A multi-channel notification router with a pluggable provider model. Your application registers one provider per channel type (email, SMS, etc.); the manager dispatches each `Request` to the correct provider at runtime.

## Usage

```go
import "github.com/OpenNSW/core/notification"

manager, err := notification.NewManager(
    notification.Config{
        Providers: map[notification.ChannelType]map[string]any{
            notification.ChannelEmail: {
                "api_key":      "sg-xxxxx",
                "from_address": "noreply@example.com",
            },
            notification.ChannelSMS: {
                "account_sid": "ACxxxxx",
                "auth_token":  "xxxxx",
                "from_number": "+61400000000",
            },
        },
    },
    myEmailProvider,
    mySMSProvider,
)

err = manager.Send(ctx, notification.Request{
    Channel: notification.ChannelEmail,
    To:      "applicant@example.com",
    Subject: "Application received",
    Body:    "Your application #12345 has been received and is under review.",
    HTMLBody: "<p>Your application <strong>#12345</strong> has been received.</p>",
})
```

## Channels

| Constant                    | Value     |
|-----------------------------|-----------|
| `notification.ChannelEmail` | `"email"` |
| `notification.ChannelSMS`   | `"sms"`   |

## Writing a provider

Implement `notification.Provider`:

```go
type Provider interface {
    Type()                          ChannelType
    Configure(cfg json.RawMessage)  error
    Send(ctx context.Context, req Request) error
}
```

- `Type()` declares which channel this provider handles.
- `Configure` is called at startup with the provider's block from `Config.Providers`, re-marshaled to JSON (so existing `Provider` implementations are unaffected by how the block was sourced).
- `Send` delivers the message.

```go
type MyEmailProvider struct {
    apiKey string
}

func (p *MyEmailProvider) Type() notification.ChannelType { return notification.ChannelEmail }

func (p *MyEmailProvider) Configure(cfg json.RawMessage) error {
    var c struct{ APIKey string `json:"api_key"` }
    if err := json.Unmarshal(cfg, &c); err != nil { return err }
    p.apiKey = c.APIKey
    return nil
}

func (p *MyEmailProvider) Send(ctx context.Context, req notification.Request) error {
    // send via your email API
    return nil
}
```

## Provider configuration

`Config.Providers` holds provider-specific configuration keyed by channel type — no standalone config file is needed. It carries a `yaml` struct tag (`providers`), so it can be embedded in a larger application config struct and populated generically, e.g. via [`configyaml.LoadAndExpand`](../configyaml/README.md) so a provider's API key can be sourced from an env var or a mounted file instead of living in the checked-in config:

```yaml
notification:
  providers:
    email:
      api_key: "{{env:SENDGRID_API_KEY}}"
      from_address: noreply@example.com
    sms:
      account_sid: ACxxxxx
      auth_token: "{{env:SMS_AUTH_TOKEN}}"
      from_number: "+61400000000"
```
