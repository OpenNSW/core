package auth

import "net/http"

type BearerConfig struct {
	Token string `json:"token"`
}

type Bearer struct {
	cfg BearerConfig
}

func NewBearer(cfg BearerConfig) *Bearer {
	return &Bearer{cfg: cfg}
}

func (a *Bearer) Apply(req *http.Request) error {
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	return nil
}
