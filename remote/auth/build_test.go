// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestBuild(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name: "api_key success",
			cfg:  Config{Type: TypeAPIKey, APIKey: &APIKeyConfig{Key: "X-API", Value: "secret"}},
		},
		{
			name: "bearer success",
			cfg:  Config{Type: TypeBearer, Bearer: &BearerConfig{Token: "my-token"}},
		},
		{
			name: "oauth2 success",
			cfg: Config{Type: TypeOAuth2, OAuth2: &OAuth2Config{
				TokenURL: "http://auth", ClientID: "id", ClientSecret: "secret", InsecureSkipTLSVerify: true,
			}},
		},
		{
			name: "oauth2 with endpoint params",
			cfg: Config{Type: TypeOAuth2, OAuth2: &OAuth2Config{
				TokenURL: "http://auth", ClientID: "id", ClientSecret: "secret",
				EndpointParams: EndpointParams{"resource": {"https://api.example"}, "audience": {"a", "b"}},
			}},
		},
		{
			name: "oauth2 rejects a reserved endpoint param",
			cfg: Config{Type: TypeOAuth2, OAuth2: &OAuth2Config{
				TokenURL: "http://auth", ClientID: "id", ClientSecret: "secret",
				EndpointParams: EndpointParams{"grant_type": {"password"}},
			}},
			wantErr: `"grant_type"`,
		},
		{
			name:    "unsupported type",
			cfg:     Config{Type: "biometric"},
			wantErr: "unsupported auth type",
		},
		{
			name:    "missing type",
			cfg:     Config{Bearer: &BearerConfig{Token: "my-token"}},
			wantErr: "missing auth type",
		},
		{
			name:    "missing settings for the type",
			cfg:     Config{Type: TypeBearer},
			wantErr: `missing "bearer" settings`,
		},
		{
			name:    "settings for a different type only",
			cfg:     Config{Type: TypeBearer, APIKey: &APIKeyConfig{Key: "X-API", Value: "secret"}},
			wantErr: `missing "bearer" settings`,
		},
		{
			name: "stray settings for another type",
			cfg: Config{
				Type:   TypeBearer,
				Bearer: &BearerConfig{Token: "my-token"},
				APIKey: &APIKeyConfig{Key: "X-API", Value: "secret"},
			},
			wantErr: "must set only its own",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authn, err := Build(tt.cfg)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, authn)
		})
	}
}

func TestBuild_ResolvesSecretReference(t *testing.T) {
	t.Setenv("BUILD_TOKEN", "resolved")
	authn, err := Build(Config{Type: TypeBearer, Bearer: &BearerConfig{Token: "env:BUILD_TOKEN"}})
	require.NoError(t, err)

	req, _ := http.NewRequest(http.MethodGet, "http://local", nil)
	require.NoError(t, authn.Apply(req))
	assert.Equal(t, "Bearer resolved", req.Header.Get("Authorization"))
}

func TestBuild_FailsLoudOnUnsetEnv(t *testing.T) {
	_, err := Build(Config{Type: TypeBearer, Bearer: &BearerConfig{Token: "env:DEFINITELY_UNSET"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not set or is empty")
}

func TestConfig_DecodesFromJSONAndYAML(t *testing.T) {
	// The same configuration written in each format decodes to the same Config.
	const jsonDoc = `{
		"type": "oauth2",
		"oauth2": {
			"token_url": "https://idp.example/token",
			"client_id": "my-client",
			"client_secret": "env:CLIENT_SECRET",
			"scopes": ["read"],
			"insecure_skip_tls_verify": true,
			"endpoint_params": {"resource": "https://api.example", "audience": ["a", "b"]}
		}
	}`
	const yamlDoc = `
type: oauth2
oauth2:
  tokenURL: https://idp.example/token
  clientID: my-client
  clientSecret: env:CLIENT_SECRET
  scopes: [read]
  insecureSkipTLSVerify: true
  endpointParams:
    resource: https://api.example
    audience: [a, b]
`
	want := Config{Type: TypeOAuth2, OAuth2: &OAuth2Config{
		TokenURL:              "https://idp.example/token",
		ClientID:              "my-client",
		ClientSecret:          "env:CLIENT_SECRET",
		Scopes:                []string{"read"},
		InsecureSkipTLSVerify: true,
		EndpointParams:        EndpointParams{"resource": {"https://api.example"}, "audience": {"a", "b"}},
	}}

	var fromJSON Config
	require.NoError(t, json.Unmarshal([]byte(jsonDoc), &fromJSON))
	assert.Equal(t, want, fromJSON)

	var fromYAML Config
	require.NoError(t, yaml.Unmarshal([]byte(yamlDoc), &fromYAML))
	assert.Equal(t, want, fromYAML)
}
