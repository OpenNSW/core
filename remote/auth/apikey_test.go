package auth

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAPIKey_Apply(t *testing.T) {
	auth := NewAPIKey(APIKeyConfig{Key: "X-Key", Value: "secret"})
	req, _ := http.NewRequest(http.MethodGet, "http://local", nil)

	err := auth.Apply(req)
	assert.NoError(t, err)
	assert.Equal(t, "secret", req.Header.Get("X-Key"))
}
