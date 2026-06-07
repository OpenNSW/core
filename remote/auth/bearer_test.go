package auth

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBearer_Apply(t *testing.T) {
	auth := NewBearer(BearerConfig{Token: "my-token"})
	req, _ := http.NewRequest(http.MethodGet, "http://local", nil)

	err := auth.Apply(req)
	assert.NoError(t, err)
	assert.Equal(t, "Bearer my-token", req.Header.Get("Authorization"))
}
