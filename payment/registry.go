// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package payment

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
)

// GatewayRegistry manages the discovery and lookup of payment gateways.
type GatewayRegistry interface {
	// Get retrieves a gateway implementation by its ID.
	Get(id string) (PaymentGateway, error)

	// ListInfo returns the aggregated metadata for the active gateways that
	// can collect payments for payee.
	ListInfo(payee Payee) []GatewayInfo

	// ResolvePayee returns the gateway's mapping for payee: the service's
	// entry merged over its org's defaults. A gateway with no entry for payee
	// returns an error wrapping ErrPayeeNotMapped.
	ResolvePayee(gatewayID string, payee Payee) (json.RawMessage, error)
}

type paymentRegistry struct {
	mu       sync.RWMutex
	gateways map[string]PaymentGateway
	infos    map[string]GatewayInfo
	payees   map[string]map[Payee]json.RawMessage
}

// methodConfig is one entry of the payment methods file: the method's
// discovery info plus the payees it can collect for.
type methodConfig struct {
	GatewayInfo
	Payees map[string]orgPayees `json:"payees"`
}

// NewRegistry initializes a new registry by loading configuration from a file.
// For each configured gateway it invokes the matching factory to construct a
// fully configured implementation, so gateways are immutable after init. Each
// of a gateway's payee mappings is checked with its ValidatePayeeMapping here,
// so a bad mapping fails startup rather than a payer's checkout.
func NewRegistry(configPath string, factories map[string]Factory) (GatewayRegistry, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read payment methods config: %w", err)
	}

	var config struct {
		Version string         `json:"version"`
		Methods []methodConfig `json:"methods"`
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal payment methods config: %w", err)
	}

	registry := &paymentRegistry{
		gateways: make(map[string]PaymentGateway),
		infos:    make(map[string]GatewayInfo),
		payees:   make(map[string]map[Payee]json.RawMessage),
	}

	for _, method := range config.Methods {
		info := method.GatewayInfo
		registry.infos[info.ID] = info

		payees, err := resolvePayees(method.Payees)
		if err != nil {
			return nil, fmt.Errorf("gateway %s: %w", info.ID, err)
		}
		registry.payees[info.ID] = payees

		// If a factory is registered for this info, construct the gateway from
		// its config once, here. No post-init mutation.
		if factory, ok := factories[info.ID]; ok {
			if factory == nil {
				return nil, fmt.Errorf("factory for gateway %s is nil", info.ID)
			}
			gateway, err := factory(info.Config)
			if err != nil {
				return nil, fmt.Errorf("failed to construct gateway %s: %w", info.ID, err)
			}
			for payee, mapping := range payees {
				if err := gateway.ValidatePayeeMapping(mapping); err != nil {
					return nil, fmt.Errorf("gateway %s: payee %s: %w", info.ID, payee, err)
				}
			}
			registry.gateways[info.ID] = gateway
		}
	}

	ids := make([]string, 0, len(registry.gateways))
	for id := range registry.gateways {
		ids = append(ids, id)
	}
	slog.Info("payment registry initialized", "gateway_count", len(registry.gateways), "active_ids", ids)
	return registry, nil
}

func (r *paymentRegistry) Get(id string) (PaymentGateway, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	gateway, ok := r.gateways[id]
	if !ok {
		return nil, fmt.Errorf("gateway %s not found in registry", id)
	}

	return gateway, nil
}

func (r *paymentRegistry) ResolvePayee(gatewayID string, payee Payee) (json.RawMessage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	mapping, ok := r.payees[gatewayID][payee]
	if !ok {
		return nil, fmt.Errorf("gateway %s, payee %s: %w", gatewayID, payee, ErrPayeeNotMapped)
	}
	return mapping, nil
}

func (r *paymentRegistry) ListInfo(payee Payee) []GatewayInfo {
	r.mu.RLock()

	// Pre-allocate capacity to avoid repeated slice re-allocations.
	activeMethods := make([]GatewayInfo, 0, len(r.infos))
	for _, info := range r.infos {
		if _, mapped := r.payees[info.ID][payee]; info.IsActive && mapped {
			// Sanitize: Return only UI-safe fields
			activeMethods = append(activeMethods, GatewayInfo{
				ID:         info.ID,
				IsActive:   info.IsActive,
				RenderInfo: info.RenderInfo,
				// Config is omitted intentionally
			})
		}
	}
	// Release the lock before the CPU-bound sort so other readers aren't blocked.
	r.mu.RUnlock()

	// Sort by DisplayOrder for consistent UI presentation, falling back to ID as a
	// stable tie-breaker so gateways sharing a DisplayOrder don't reorder between
	// calls (map traversal order is randomized).
	sort.Slice(activeMethods, func(i, j int) bool {
		if activeMethods[i].RenderInfo.DisplayOrder == activeMethods[j].RenderInfo.DisplayOrder {
			return activeMethods[i].ID < activeMethods[j].ID
		}
		return activeMethods[i].RenderInfo.DisplayOrder < activeMethods[j].RenderInfo.DisplayOrder
	})

	return activeMethods
}
