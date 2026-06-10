# notification

A multi-channel notification router with a pluggable provider model. Your application registers one provider per channel type (email, SMS, etc.); the manager dispatches each `Request` to the correct provider at runtime.

## Usage

```go
import "github.com/OpenNSW/core/notification"

manager, err := notification.NewManager(
    notification.Config{Path: "configs/notification.json"},
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

| Constant | Value |
|---|---|
| `notification.ChannelEmail` | `"email"` |
| `notification.ChannelSMS` | `"sms"` |

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
- `Configure` is called at startup with the provider-specific JSON block from `notification.json`.
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

## Provider config file

`notification.json` holds provider-specific configuration keyed by channel type:

```json
{
  "providers": {
    "email": {
      "api_key": "sg-xxxxx",
      "from_address": "noreply@example.com"
    },
    "sms": {
      "account_sid": "ACxxxxx",
      "auth_token": "xxxxx",
      "from_number": "+61400000000"
    }
  }
}
```
