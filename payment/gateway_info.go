// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package payment

// RenderInfo contains UI-specific metadata for displaying a payment method.
type RenderInfo struct {
	DisplayName  string `json:"display_name" yaml:"display_name"`
	Description  string `json:"description" yaml:"description"`
	LogoURL      string `json:"logo_url" yaml:"logo_url"`
	DisplayOrder int    `json:"display_order" yaml:"display_order"`
	PrimaryColor string `json:"primary_color,omitempty" yaml:"primary_color,omitempty"`
}

// GatewayInfo is the aggregate DTO used for gateway discovery. It carries only
// UI-safe fields: a method's gateway config never leaves the registry.
type GatewayInfo struct {
	ID         string     `json:"id" yaml:"id"`
	IsActive   bool       `json:"is_active" yaml:"is_active"`
	RenderInfo RenderInfo `json:"render_info" yaml:"render_info"`
}
