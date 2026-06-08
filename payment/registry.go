package payment

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
)

// GatewayRegistry manages the discovery and lookup of payment gateways.
type GatewayRegistry interface {
	// Get retrieves a gateway implementation by its ID.
	Get(id string) (PaymentGateway, error)

	// ListInfo returns the aggregated metadata for all supported gateways.
	ListInfo() []GatewayInfo
}

type paymentRegistry struct {
	mu       sync.RWMutex
	gateways map[string]PaymentGateway
	infos    map[string]GatewayInfo
}

// NewRegistry initializes a new registry by loading configuration from a file.
// For each configured gateway it invokes the matching factory to construct a
// fully configured implementation, so gateways are immutable after init.
func NewRegistry(configPath string, factories map[string]Factory) (GatewayRegistry, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read payment methods config: %w", err)
	}

	var config struct {
		Version string        `json:"version"`
		Methods []GatewayInfo `json:"methods"`
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal payment methods config: %w", err)
	}

	registry := &paymentRegistry{
		gateways: make(map[string]PaymentGateway),
		infos:    make(map[string]GatewayInfo),
	}

	for _, info := range config.Methods {
		registry.infos[info.ID] = info

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
			registry.gateways[info.ID] = gateway
		}
	}

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

func (r *paymentRegistry) ListInfo() []GatewayInfo {
	r.mu.RLock()

	// Pre-allocate capacity to avoid repeated slice re-allocations.
	activeMethods := make([]GatewayInfo, 0, len(r.infos))
	for _, info := range r.infos {
		if info.IsActive {
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
