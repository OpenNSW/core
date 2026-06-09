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

func (m *MockGateway) ParseWebhook(ctx context.Context, body []byte, headers map[string][]string) (*WebhookPayload, *WebhookResponse, error) {
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
				"config": {"secret": "keep-away"}
			},
			{
				"id": "method1",
				"is_active": true,
				"render_info": {
					"display_name": "Method 1",
					"display_order": 1
				}
			},
			{
				"id": "inactive",
				"is_active": false,
				"render_info": {
					"display_name": "Inactive",
					"display_order": 3
				}
			}
		]
	}`
	tmpFile, err := os.CreateTemp("", "payment_methods_*.json")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	os.WriteFile(tmpFile.Name(), []byte(configContent), 0644)

	registry, _ := NewRegistry(tmpFile.Name(), map[string]Factory{})
	infos := registry.ListInfo()

	// 1. Should only contain active methods
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
