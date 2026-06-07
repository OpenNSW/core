package auth

import "net/http"

type APIKeyConfig struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type APIKey struct {
	cfg APIKeyConfig
}

func NewAPIKey(cfg APIKeyConfig) *APIKey {
	return &APIKey{cfg: cfg}
}

func (a *APIKey) Apply(req *http.Request) error {
	req.Header.Set(a.cfg.Key, a.cfg.Value)
	return nil
}
