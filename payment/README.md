# Payment Package

The `payment` package provides a modular and extensible payment orchestration system. It follows a gateway-based architecture, separating protocol-level concerns from domain logic, and is designed to be imported and integrated into other repositories.

## Architecture Overview

The system consists of several key components:

1.  **PaymentGateway**: An interface for gateway-specific integrations (e.g., LankaPay, GovPay). It handles session creation, webhook parsing, and real-time validation formatting.
2.  **GatewayRegistry**: A pure discovery and lookup service. It manages gateway registration, configuration injection, and provides sanitized metadata for the UI.
3.  **PaymentRepository**: Handles persistence for `PaymentTransaction` records using GORM.
4.  **PaymentService**: The high-level orchestrator. It uses the Registry to find the correct Gateway and coordinates between the gateway logic, database, and internal events.
5.  **HTTPHandler**: Exposes the payment service via RESTful endpoints for both public and internal use.

## Integration

This package is designed to be imported into other repositories. To use it:

```go
import "github.com/OpenNSW/core/payment"
```

## Getting Started

### 1. Implement a PaymentGateway

Each payment gateway requires a dedicated implementation of the `PaymentGateway` interface.

```go
type MyGateway struct {}

// NewMyGateway is this gateway's Factory, called once by the registry at
// init time with its raw config from payment_methods.json.
func NewMyGateway(config json.RawMessage) (payment.PaymentGateway, error) {
    // Unmarshal gateway-specific settings from JSON.
    return &MyGateway{}, nil
}

func (g *MyGateway) GetFlowType() payment.InteractionType {
    return payment.FlowTypeRedirect
}

func (g *MyGateway) CreateSession(ctx context.Context, req payment.SessionRequest) (*payment.SessionResponse, error) {
    // Logic to initialize session with gateway
    return &payment.SessionResponse{...}, nil
}

// VerifyWebhook cryptographically authenticates the caller — using
// whatever scheme this gateway requires (HMAC signature, bearer token,
// etc.) — before ExtractReferenceNumber or ParseWebhook ever runs. There is
// no default: every gateway must implement a real check here, or no
// transaction can ever be settled through it.
func (g *MyGateway) VerifyWebhook(ctx context.Context, body []byte, headers map[string][]string) error {
    token := http.Header(headers).Get("Authorization")
    if !isValidBearerToken(token) {
        // Wrap ErrWebhookVerificationFailed only when verification has
        // positively determined the caller is invalid — this is what maps
        // to 401. Any other error (e.g. a timeout reaching an upstream
        // token-introspection endpoint) must be returned unwrapped, so it's
        // treated as a transient failure, not a rejection — see
        // "Verification error classification" below.
        return payment.NewWebhookVerificationError("invalid or missing bearer token")
    }
    return nil
}

func (g *MyGateway) ExtractReferenceNumber(ctx context.Context, reqData json.RawMessage) (string, error) {
    // Parse gateway-specific validation request to find the reference
    return "REF-123", nil
}

func (g *MyGateway) HandleValidateReference(ctx context.Context, tx *payment.ValidationTransaction, isPayable bool, reqData json.RawMessage) (*payment.ValidationResponse, error) {
    // Format the final response for the gateway
    return &payment.ValidationResponse{...}, nil
}

func (g *MyGateway) ParseWebhook(ctx context.Context, body []byte, headers map[string][]string) (*payment.WebhookPayload, *payment.WebhookResponse, error) {
    // Logic to parse the gateway webhook into a domain-neutral payload,
    // plus the gateway-specific acknowledgement to relay back.
    return &payment.WebhookPayload{...}, &payment.WebhookResponse{...}, nil
}
```

### 2. Configure Payment Methods

The `payment_methods.json` file is the source of truth for available methods.

```json
{
  "version": "1.0",
  "methods": [
    {
      "id": "lankapay",
      "is_active": true,
      "render_info": {
        "display_name": "Credit/Debit Card (LankaPay)",
        "description": "Pay securely using your card.",
        "display_order": 1
      },
      "config": {
        "base_url": "https://sandbox.govpay.lk"
      }
    }
  ]
}
```

### 3. Instantiate the Registry

The `GatewayRegistry` loads the configuration and maps each method ID to its implementation.

```go
factories := map[string]payment.Factory{
    "lankapay": lankapay.NewGateway,
    "govpay":   govpay.NewGateway,
}

registry, err := payment.NewRegistry("configs/payment_methods.json", factories)
```

### 4. Setup the Orchestrator

The `PaymentService` acts as the orchestrator using the Registry as a lookup.

```go
repo := payment.NewPaymentRepository(db)
service := payment.NewPaymentService(repo, registry)

handler := payment.NewHTTPHandler(service)
```

## Key Flows

### Checkout Initialization
The frontend calls `CreateCheckoutSession`. The Service generates an NSW reference, looks up the gateway implementation via the Registry, and delegates the session creation to that gateway.

### Real-Time Validation
When a user enters a reference in a bank app, the gateway calls NSW.
1. The Service looks up the Gateway via the Registry, then calls **VerifyWebhook** to authenticate the caller. A failure here stops the flow immediately — no reference lookup happens, and no presentment info is disclosed.
2. The Service uses the Gateway to **Extract** the reference number.
3. The Service fetches the transaction from the **Database**.
4. The Service passes the record back to the Gateway to **Validate** and format the protocol-specific response.

### Webhook Processing
Gateways notify the payment service of results. The Service looks up the gateway via the Registry, calls **VerifyWebhook** to authenticate the caller (again, a failure stops the flow before any parsing or settlement), delegates the parsing, and then performs domain actions: updating status, persisting metadata, and firing internal events.

## Verification error classification

`VerifyWebhook` implementations must distinguish two different kinds of failure:

- **The caller is not genuinely this gateway** (an invalid or expired token, a signature that doesn't match): wrap `payment.ErrWebhookVerificationFailed` (via `%w`) when returning the error. `HTTPHandler` maps this to `401 Unauthorized`.
- **Verification could not be completed for an operational reason** (a timeout reaching an upstream introspection/JWKS endpoint, a missing local configuration, a cancelled context): return any other error, unwrapped. This is NOT proof the caller is invalid, and is treated like any other unclassified error — a transient `500`, so the gateway's own retry can re-drive it.

Conflating the two means an operational blip on your own side (not an attack) can permanently drop a legitimate webhook, since most providers treat a `401` as "credentials are bad, stop retrying" rather than something to retry.

## Extending verification beyond body and headers

`VerifyWebhook(ctx, body, headers)` was deliberately kept to plain params rather than `*http.Request`, to keep gateways transport-agnostic and unit-testable without `httptest`. This covers header-based schemes (an OAuth2 bearer token) and body-based schemes (an HMAC signature) directly — but some schemes need more: a signature computed over the HTTP method and request path, a check against query parameters, an IP allowlist against the remote address, or an mTLS client-certificate check against the TLS connection state.

Rather than changing `VerifyWebhook`'s signature every time a scheme needs a different dimension of the request — which would mean a coordinated breaking change across every implementer, every time — that data is available via context: `HTTPHandler` attaches the full inbound `*http.Request` to the context (via `payment.ContextWithRequest`) before ever calling into `PaymentService`, on every request, so a gateway's `VerifyWebhook` can retrieve it with `payment.RequestFromContext(ctx)` and read whatever dimension its own scheme requires, without `core/payment` ever having to anticipate which one that is:

```go
func (g *MyGateway) VerifyWebhook(ctx context.Context, body []byte, headers map[string][]string) error {
    req := payment.RequestFromContext(ctx)
    if req == nil {
        // Not available — e.g. PaymentService was invoked directly,
        // bypassing HTTPHandler (a unit test, say). Fall back to whatever
        // the explicit body/headers params allow, or fail closed if this
        // scheme can't verify without the request.
        return errors.New("request context unavailable: cannot verify without the inbound request")
    }

    // Pick whatever dimension(s) this scheme actually needs:
    query := req.URL.Query()                 // signature/timestamp in query params
    method, path := req.Method, req.URL.Path // method+path-bound signatures
    tlsState := req.TLS                      // mTLS client-certificate checks
    remoteAddr := req.RemoteAddr             // source-IP allowlisting

    if !isValid(query, method, path, tlsState, remoteAddr) {
        return payment.NewWebhookVerificationError("invalid signature")
    }
    return nil
}
```

`req.Body` has already been drained by `HTTPHandler` by the time `VerifyWebhook` runs (to produce the `body` parameter above) — use the explicit `body` parameter for the payload, not `req.Body`. Treat a `nil` return from `RequestFromContext` as "not available" rather than a zero-value request, and never mutate the returned request — `HTTPHandler` is still using it to serve the response.

If your scheme needs TLS state or the real client IP specifically: confirm with your infrastructure team where TLS actually terminates first. If a WAF/LB/ingress terminates TLS ahead of this process, `req.TLS` here is nil regardless of any code change — real mTLS requires the edge to verify the client certificate and forward the result via a trusted header, safe to trust only if network policy guarantees the edge is the sole path in. The same caveat applies to source-IP allowlisting via `X-Forwarded-For` vs. `req.RemoteAddr`. This is a deployment-topology decision, not something this package can resolve on its own.

## Exported Types and Functions

### Core Interfaces

- **PaymentGateway**: Interface for gateway implementations
- **PaymentService**: Main orchestrator service
- **PaymentRepository**: Database persistence layer

### Data Types

- `SessionRequest`: Checkout session initialization
- `SessionResponse`: Session response with checkout details
- `WebhookPayload`: Incoming webhook data
- `ValidationTransaction`: Transaction details for validation
- `ValidationResponse`: Validation response format
- `InteractionType`: Enum for flow types (REDIRECT, INSTRUCTION)
- `WebhookStatus`: Canonical webhook status (PENDING, SUCCESS, FAILED)

### Constructor Functions

- `NewRegistry(configPath string, factories map[string]Factory)`: Create a gateway registry
- `NewPaymentService(repo PaymentRepository, registry GatewayRegistry)`: Create payment service
- `NewPaymentRepository(db *gorm.DB)`: Create payment repository
- `NewHTTPHandler(service PaymentService)`: Create HTTP handler

### Context Helpers

- `ContextWithRequest(ctx context.Context, r *http.Request) context.Context`: Attach the inbound request to a context — called by `HTTPHandler`
- `RequestFromContext(ctx context.Context) *http.Request`: Retrieve it — see "Extending verification beyond body and headers" above

### Error Types

- `ErrUnsupportedWebhookStatus`: Gateway status cannot be normalized
- `ErrTransactionNotFound`: Payment transaction not found
- `ErrAmountMismatch`: Payment amount or currency mismatch
- `ErrWebhookVerificationFailed`: Caller could not be cryptographically verified — see "Verification error classification" above for when a gateway should (and should not) use this
- `NewWebhookVerificationError(reason string) error`: Optional helper that builds a correctly-wrapped `ErrWebhookVerificationFailed` rejection

## Integration Example

In your consuming repository:

```go
package main

import (
    "github.com/OpenNSW/core/payment"
)

func setupPayments(db *gorm.DB) *payment.HTTPHandler {
    // Wire your gateway Factories
    factories := map[string]payment.Factory{
        "your-gateway": yourgateway.NewGateway,
    }
    
    // Initialize registry
    registry, err := payment.NewRegistry("path/to/config.json", factories)
    if err != nil {
        panic(err)
    }
    
    // Setup service
    repo := payment.NewPaymentRepository(db)
    service := payment.NewPaymentService(repo, registry)
    
    // Return handler for HTTP endpoints
    return payment.NewHTTPHandler(service)
}
```
