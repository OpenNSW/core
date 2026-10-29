// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package payment

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// MockGateway is a mock implementation of Gateway
type MockGateway struct {
	mock.Mock

	// ValidatePayeeMappingFn overrides ValidatePayeeMapping for the tests that
	// care about it. Left nil, the gateway accepts any mapping — the common
	// case, and what keeps every other test free of an expectation for a call
	// it does not exercise.
	ValidatePayeeMappingFn func(mapping json.RawMessage) error

	// Payees is the PayeeLookup the last ParseWebhook call received.
	Payees PayeeLookup
}

func (m *MockGateway) ValidatePayeeMapping(mapping json.RawMessage) error {
	if m.ValidatePayeeMappingFn == nil {
		return nil
	}
	return m.ValidatePayeeMappingFn(mapping)
}

func (m *MockGateway) GetFlowType() InteractionType {
	args := m.Called()
	return args.Get(0).(InteractionType)
}

func (m *MockGateway) CreateSession(ctx context.Context, req SessionRequest) (*SessionResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*SessionResponse), args.Error(1)
}

func (m *MockGateway) VerifyWebhook(ctx context.Context, body []byte, headers map[string][]string) error {
	args := m.Called(ctx, body, headers)
	return args.Error(0)
}

func (m *MockGateway) ExtractReferenceNumber(ctx context.Context, reqData json.RawMessage) (string, error) {
	args := m.Called(ctx, reqData)
	return args.String(0), args.Error(1)
}

func (m *MockGateway) HandleValidateReference(ctx context.Context, tx *ValidationTransaction, isPayable bool, reqData json.RawMessage) (*ValidationResponse, error) {
	args := m.Called(ctx, tx, isPayable, reqData)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ValidationResponse), args.Error(1)
}

func (m *MockGateway) ParseWebhook(ctx context.Context, body []byte, headers map[string][]string, payees PayeeLookup) (*WebhookPayload, *WebhookResponse, error) {
	m.Payees = payees
	args := m.Called(ctx, body, headers)
	var payload *WebhookPayload
	if args.Get(0) != nil {
		payload = args.Get(0).(*WebhookPayload)
	}
	var resp *WebhookResponse
	if args.Get(1) != nil {
		resp = args.Get(1).(*WebhookResponse)
	}
	return payload, resp, args.Error(2)
}

// parseConfig decodes src the way a host's YAML loader would. JSON is valid
// YAML, so it accepts either.
func parseConfig(t *testing.T, src string) Config {
	t.Helper()
	var cfg Config
	require.NoError(t, yaml.Unmarshal([]byte(src), &cfg))
	return cfg
}

// lankapayConfig stands in for a gateway's own config type.
type lankapayConfig struct {
	APIKey     string `yaml:"api_key"`
	MerchantID string `yaml:"merchant_id"`
	Port       int    `yaml:"port"`
}

// The factory decodes the method's config into the gateway's own type.
func TestNewRegistry(t *testing.T) {
	for name, src := range map[string]string{
		"yaml": `
methods:
  - id: lankapay
    is_active: true
    render_info:
      display_name: LankaPay
      display_order: 1
    config:
      api_key: secret
      merchant_id: "00123"
      port: 8443
`,
		"json": `{
			"methods": [
				{
					"id": "lankapay",
					"is_active": true,
					"render_info": {"display_name": "LankaPay", "display_order": 1},
					"config": {"api_key": "secret", "merchant_id": "00123", "port": 8443}
				}
			]
		}`,
	} {
		t.Run(name, func(t *testing.T) {
			mockG := new(MockGateway)
			var got lankapayConfig
			factories := map[string]Factory{
				"lankapay": func(decode func(any) error) (Gateway, error) {
					if err := decode(&got); err != nil {
						return nil, err
					}
					return mockG, nil
				},
			}

			registry, err := NewRegistry(parseConfig(t, src), factories)
			require.NoError(t, err)
			assert.NotNil(t, registry)
			assert.Equal(t, lankapayConfig{APIKey: "secret", MerchantID: "00123", Port: 8443}, got)
		})
	}
}

// A method with no config leaves the gateway's config at its zero value.
func TestNewRegistry_NoGatewayConfig(t *testing.T) {
	got := lankapayConfig{APIKey: "unchanged"}
	_, err := NewRegistry(parseConfig(t, `methods: [{id: gw1, is_active: true}]`), map[string]Factory{
		"gw1": func(decode func(any) error) (Gateway, error) {
			return new(MockGateway), decode(&got)
		},
	})
	require.NoError(t, err)
	assert.Equal(t, lankapayConfig{APIKey: "unchanged"}, got)
}

// A config the gateway's type cannot hold fails the registry.
func TestNewRegistry_GatewayConfigDecodeError(t *testing.T) {
	cfg := parseConfig(t, `methods: [{id: gw1, is_active: true, config: {port: not-a-number}}]`)
	_, err := NewRegistry(cfg, map[string]Factory{
		"gw1": func(decode func(any) error) (Gateway, error) {
			var c lankapayConfig
			if err := decode(&c); err != nil {
				return nil, err
			}
			return new(MockGateway), nil
		},
	})
	require.ErrorContains(t, err, "failed to construct gateway gw1")
}

func TestListInfo(t *testing.T) {
	cfg := parseConfig(t, `{
		"methods": [
			{
				"id": "method2",
				"is_active": true,
				"render_info": {
					"display_name": "Method 2",
					"display_order": 2
				},
				"config": {"secret": "keep-away"},
				"payees": {"org_a": {"services": {"fee": {}}}}
			},
			{
				"id": "method1",
				"is_active": true,
				"render_info": {
					"display_name": "Method 1",
					"display_order": 1
				},
				"payees": {"org_a": {"services": {"fee": {}}}}
			},
			{
				"id": "other_payees",
				"is_active": true,
				"render_info": {
					"display_name": "Other payees",
					"display_order": 0
				},
				"payees": {"org_a": {"services": {"other_fee": {}}}, "org_b": {"services": {"fee": {}}}}
			},
			{
				"id": "inactive",
				"is_active": false,
				"render_info": {
					"display_name": "Inactive",
					"display_order": 3
				},
				"payees": {"org_a": {"services": {"fee": {}}}}
			}
		]
	}`)

	registry, err := NewRegistry(cfg, map[string]Factory{})
	require.NoError(t, err)
	infos := registry.ListInfo(Payee{OrgID: "org_a", ServiceID: "fee"})

	// 1. Should only contain active methods that map the payee
	assert.Equal(t, 2, len(infos))

	// 2. Should be sorted by DisplayOrder (Method 1 should come before Method 2)
	assert.Equal(t, "method1", infos[0].ID)
	assert.Equal(t, "method2", infos[1].ID)
	assert.Equal(t, RenderInfo{DisplayName: "Method 2", DisplayOrder: 2}, infos[1].RenderInfo)
}

func TestGet(t *testing.T) {
	mockG := new(MockGateway)
	factories := map[string]Factory{
		"gw1": func(func(any) error) (Gateway, error) { return mockG, nil },
	}
	registry, err := NewRegistry(parseConfig(t, `{"methods": [{"id": "gw1", "is_active": true}]}`), factories)
	require.NoError(t, err)

	gateway, err := registry.Get("gw1")
	assert.NoError(t, err)
	assert.Equal(t, mockG, gateway)

	_, err = registry.Get("non-existent")
	assert.Error(t, err)
}

func TestNewRegistry_FactoryError(t *testing.T) {
	cfg := parseConfig(t, `{"methods":[{"id":"gw1","is_active":true,"config":{"k":"v"}}]}`)
	factories := map[string]Factory{
		"gw1": func(func(any) error) (Gateway, error) {
			return nil, errors.New("bad config")
		},
	}

	_, err := NewRegistry(cfg, factories)
	require.Error(t, err)
}

func TestNewRegistry_NilFactory(t *testing.T) {
	_, err := NewRegistry(parseConfig(t, `methods: [{id: gw1}]`), map[string]Factory{"gw1": nil})
	require.ErrorContains(t, err, "factory for gateway gw1 is nil")
}

const payeesConfig = `{"methods":[{"id":"gw1","is_active":true,"payees":{
	"org_a": {
		"defaults": {"subinst_id": "002", "region": "west"},
		"services": {
			"certificate_fee": {"service_id": "001"},
			"lab_fee": {"subinst_id": "005", "service_id": "007"}
		}
	}
}}]}`

// A payee resolves to its service's entry merged over its org's defaults, the
// service's keys winning.
func TestResolvePayee(t *testing.T) {
	registry, err := NewRegistry(parseConfig(t, payeesConfig), map[string]Factory{
		"gw1": func(func(any) error) (Gateway, error) { return new(MockGateway), nil },
	})
	require.NoError(t, err)

	mapping, err := registry.ResolvePayee("gw1", Payee{OrgID: "org_a", ServiceID: "certificate_fee"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"subinst_id":"002","region":"west","service_id":"001"}`, string(mapping))

	mapping, err = registry.ResolvePayee("gw1", Payee{OrgID: "org_a", ServiceID: "lab_fee"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"subinst_id":"005","region":"west","service_id":"007"}`, string(mapping))
}

// A service entry left empty maps to its org's defaults alone, and nested
// objects reach the gateway as JSON objects.
func TestResolvePayee_YAML(t *testing.T) {
	cfg := parseConfig(t, `
methods:
  - id: gw1
    payees:
      org_a:
        defaults:
          account: {bank: "7010", branch: "001"}
        services:
          certificate_fee:
`)
	registry, err := NewRegistry(cfg, map[string]Factory{})
	require.NoError(t, err)

	mapping, err := registry.ResolvePayee("gw1", Payee{OrgID: "org_a", ServiceID: "certificate_fee"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"account":{"bank":"7010","branch":"001"}}`, string(mapping))
}

func TestResolvePayee_NotMapped(t *testing.T) {
	registry, err := NewRegistry(parseConfig(t, payeesConfig), map[string]Factory{})
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		gateway string
		payee   Payee
	}{
		"unknown service": {gateway: "gw1", payee: Payee{OrgID: "org_a", ServiceID: "other"}},
		"unknown org":     {gateway: "gw1", payee: Payee{OrgID: "org_b", ServiceID: "certificate_fee"}},
		"unknown gateway": {gateway: "gw2", payee: Payee{OrgID: "org_a", ServiceID: "certificate_fee"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := registry.ResolvePayee(tc.gateway, tc.payee)
			require.ErrorIs(t, err, ErrPayeeNotMapped)
		})
	}
}

// Every mapping is checked by its gateway at startup; one it rejects fails the
// registry, naming the payee.
func TestNewRegistry_GatewayRejectsPayeeMapping(t *testing.T) {
	var seen []string
	gw := &MockGateway{ValidatePayeeMappingFn: func(mapping json.RawMessage) error {
		seen = append(seen, string(mapping))
		var ids map[string]string
		require.NoError(t, json.Unmarshal(mapping, &ids))
		if ids["subinst_id"] == "005" {
			return errors.New("unknown sub-institution")
		}
		return nil
	}}

	_, err := NewRegistry(parseConfig(t, payeesConfig), map[string]Factory{
		"gw1": func(func(any) error) (Gateway, error) { return gw, nil },
	})

	require.ErrorContains(t, err, "payee org_a/lab_fee: unknown sub-institution")
	assert.NotEmpty(t, seen)
}

func TestNewRegistry_InvalidPayees(t *testing.T) {
	for name, payees := range map[string]map[string]OrgPayees{
		"empty org id":     {"": {Services: map[string]map[string]any{"fee": {}}}},
		"empty service id": {"org_a": {Services: map[string]map[string]any{" ": {}}}},
		"not JSON-encodable": {"org_a": {Services: map[string]map[string]any{
			"fee": {"bad": func() {}},
		}}},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := Config{Methods: []MethodConfig{{GatewayInfo: GatewayInfo{ID: "gw1"}, Payees: payees}}}
			_, err := NewRegistry(cfg, map[string]Factory{})
			require.Error(t, err)
		})
	}
}

// Payee entries must be objects; anything else does not decode.
func TestConfig_PayeesMustBeObjects(t *testing.T) {
	for name, payees := range map[string]string{
		"defaults not an object": `{"org_a": {"defaults": "002", "services": {"fee": {}}}}`,
		"service not an object":  `{"org_a": {"services": {"fee": ["001"]}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var cfg Config
			err := yaml.Unmarshal([]byte(`{"methods":[{"id":"gw1","payees":`+payees+`}]}`), &cfg)
			require.Error(t, err)
		})
	}
}
