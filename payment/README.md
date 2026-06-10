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

func (g *MyGateway) ApplyConfig(config json.RawMessage) error {
    // Inject gateway-specific settings from JSON
    return nil
}

func (g *MyGateway) GetFlowType() payment.InteractionType {
    return payment.FlowTypeRedirect
}

func (g *MyGateway) CreateSession(ctx context.Context, req payment.SessionRequest) (*payment.SessionResponse, error) {
    // Logic to initialize session with gateway
    return &payment.SessionResponse{...}, nil
}

func (g *MyGateway) ExtractReferenceNumber(ctx context.Context, reqData json.RawMessage) (string, error) {
    // Parse gateway-specific validation request to find the reference
    return "REF-123", nil
}

func (g *MyGateway) HandleValidateReference(ctx context.Context, tx *payment.ValidationTransaction, isPayable bool, reqData json.RawMessage) (*payment.ValidationResponse, error) {
    // Format the final response for the gateway
    return &payment.ValidationResponse{...}, nil
}

func (g *MyGateway) ParseWebhook(ctx context.Context, body []byte, headers map[string][]string) (*payment.WebhookPayload, error) {
    // Logic to parse and validate gateway webhook
    return &payment.WebhookPayload{...}, nil
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
gateways := map[string]payment.PaymentGateway{
    "lankapay": &lankapay.Gateway{},
    "govpay":   &govpay.Gateway{},
}

registry, err := payment.NewRegistry("configs/payment_methods.json", gateways)
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
1. The Service uses the Gateway to **Extract** the reference number.
2. The Service fetches the transaction from the **Database**.
3. The Service passes the record back to the Gateway to **Validate** and format the protocol-specific response.

### Webhook Processing
Gateways notify the payment service of results. The Service looks up the gateway via the Registry, delegates the parsing, and then performs domain actions: updating status, persisting metadata, and firing internal events.

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

- `NewRegistry(configPath string, gateways map[string]PaymentGateway)`: Create a gateway registry
- `NewPaymentService(repo PaymentRepository, registry *GatewayRegistry)`: Create payment service
- `NewPaymentRepository(db *gorm.DB)`: Create payment repository
- `NewHTTPHandler(service PaymentService)`: Create HTTP handler

### Error Types

- `ErrUnsupportedWebhookStatus`: Gateway status cannot be normalized
- `ErrTransactionNotFound`: Payment transaction not found
- `ErrAmountMismatch`: Payment amount or currency mismatch

## Integration Example

In your consuming repository:

```go
package main

import (
    "github.com/OpenNSW/core/payment"
)

func setupPayments(db *gorm.DB) *payment.HTTPHandler {
    // Create your gateway implementations
    gateways := map[string]payment.PaymentGateway{
        "your-gateway": &yourgateway.Gateway{},
    }
    
    // Initialize registry
    registry, err := payment.NewRegistry("path/to/config.json", gateways)
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
