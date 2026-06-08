package payment

import "encoding/json"

// RenderInfo contains UI-specific metadata for displaying a payment method.
type RenderInfo struct {
	DisplayName  string `json:"display_name"`
	Description  string `json:"description"`
	LogoURL      string `json:"logo_url"`
	DisplayOrder int    `json:"display_order"`
	PrimaryColor string `json:"primary_color,omitempty"`
}

// GatewayInfo is the aggregate DTO used for gateway discovery.
type GatewayInfo struct {
	ID         string          `json:"id"`
	IsActive   bool            `json:"is_active"`
	RenderInfo RenderInfo      `json:"render_info"`
	Config     json.RawMessage `json:"config,omitempty"`
}
