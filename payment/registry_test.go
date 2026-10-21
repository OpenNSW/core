// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package payment

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockGateway is a mock implementation of PaymentGateway
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

func TestNewRegistry(t *testing.T) {
	// Setup temporary config file
	configContent := `{
		"version": "1.0",
		"methods": [
			{
				"id": "lankapay",
				"is_active": true,
				"render_info": {
					"display_name": "LankaPay",
					"display_order": 1
				},
				"config": {"apiKey": "secret"}
			}
		]
	}`
	tmpFile, err := os.CreateTemp("", "payment_methods_*.json")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	_, err = tmpFile.WriteString(configContent)
	assert.NoError(t, err)
	tmpFile.Close()

	mockG := new(MockGateway)

	var gotConfig json.RawMessage
	factories := map[string]Factory{
		"lankapay": func(cfg json.RawMessage) (PaymentGateway, error) {
			gotConfig = cfg
			return mockG, nil
		},
	}

	registry, err := NewRegistry(tmpFile.Name(), factories)
	assert.NoError(t, err)
	assert.NotNil(t, registry)

	// The factory receives the gateway's config verbatim from the file.
	assert.JSONEq(t, `{"apiKey": "secret"}`, string(gotConfig))
}

func TestListInfo(t *testing.T) {
	configContent := `{
		"version": "1.0",
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
	}`
	tmpFile, err := os.CreateTemp("", "payment_methods_*.json")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	os.WriteFile(tmpFile.Name(), []byte(configContent), 0644)

	registry, _ := NewRegistry(tmpFile.Name(), map[string]Factory{})
	infos := registry.ListInfo(Payee{OrgID: "org_a", ServiceID: "fee"})

	// 1. Should only contain active methods that map the payee
	assert.Equal(t, 2, len(infos))

	// 2. Should be sorted by DisplayOrder (Method 1 should come before Method 2)
	assert.Equal(t, "method1", infos[0].ID)
	assert.Equal(t, "method2", infos[1].ID)

	// 3. Should be sanitized (Config should be empty)
	assert.Nil(t, infos[1].Config)
}

func TestGet(t *testing.T) {
	configContent := `{
		"version": "1.0",
		"methods": [
			{
				"id": "gw1",
				"is_active": true
			}
		]
	}`
	tmpFile, _ := os.CreateTemp("", "payment_methods_*.json")
	defer os.Remove(tmpFile.Name())
	os.WriteFile(tmpFile.Name(), []byte(configContent), 0644)

	mockG := new(MockGateway)
	factories := map[string]Factory{
		"gw1": func(cfg json.RawMessage) (PaymentGateway, error) { return mockG, nil },
	}
	registry, _ := NewRegistry(tmpFile.Name(), factories)

	gateway, err := registry.Get("gw1")
	assert.NoError(t, err)
	assert.Equal(t, mockG, gateway)

	_, err = registry.Get("non-existent")
	assert.Error(t, err)
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "pm-*.json")
	require.NoError(t, err)
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

func TestNewRegistry_FileNotFound(t *testing.T) {
	_, err := NewRegistry("/no/such/file.json", map[string]Factory{})
	require.Error(t, err)
}

func TestNewRegistry_InvalidJSON(t *testing.T) {
	path := writeTempConfig(t, `{ not valid json`)
	_, err := NewRegistry(path, map[string]Factory{})
	require.Error(t, err)
}

func TestNewRegistry_FactoryError(t *testing.T) {
	path := writeTempConfig(t, `{"version":"1.0","methods":[{"id":"gw1","is_active":true,"config":{"k":"v"}}]}`)
	factories := map[string]Factory{
		"gw1": func(cfg json.RawMessage) (PaymentGateway, error) {
			return nil, errors.New("bad config")
		},
	}

	_, err := NewRegistry(path, factories)
	require.Error(t, err)
}

const payeesConfig = `{"version":"1.0","methods":[{"id":"gw1","is_active":true,"payees":{
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
	path := writeTempConfig(t, payeesConfig)
	registry, err := NewRegistry(path, map[string]Factory{
		"gw1": func(json.RawMessage) (PaymentGateway, error) { return new(MockGateway), nil },
	})
	require.NoError(t, err)

	mapping, err := registry.ResolvePayee("gw1", Payee{OrgID: "org_a", ServiceID: "certificate_fee"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"subinst_id":"002","region":"west","service_id":"001"}`, string(mapping))

	mapping, err = registry.ResolvePayee("gw1", Payee{OrgID: "org_a", ServiceID: "lab_fee"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"subinst_id":"005","region":"west","service_id":"007"}`, string(mapping))
}

func TestResolvePayee_NotMapped(t *testing.T) {
	path := writeTempConfig(t, payeesConfig)
	registry, err := NewRegistry(path, map[string]Factory{})
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
	path := writeTempConfig(t, payeesConfig)
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

	_, err := NewRegistry(path, map[string]Factory{
		"gw1": func(json.RawMessage) (PaymentGateway, error) { return gw, nil },
	})

	require.ErrorContains(t, err, "payee org_a/lab_fee: unknown sub-institution")
	assert.NotEmpty(t, seen)
}

func TestNewRegistry_InvalidPayees(t *testing.T) {
	for name, payees := range map[string]string{
		"defaults not an object": `{"org_a": {"defaults": "002", "services": {"fee": {}}}}`,
		"service not an object":  `{"org_a": {"services": {"fee": ["001"]}}}`,
		"empty org id":           `{"": {"services": {"fee": {}}}}`,
		"empty service id":       `{"org_a": {"services": {" ": {}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := writeTempConfig(t, `{"methods":[{"id":"gw1","is_active":true,"payees":`+payees+`}]}`)
			_, err := NewRegistry(path, map[string]Factory{})
			require.Error(t, err)
		})
	}
}
