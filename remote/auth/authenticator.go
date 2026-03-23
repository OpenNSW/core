package auth

import "net/http"

// Authenticator defines an interface for applying authentication to outgoing requests.
type Authenticator interface {
	Apply(req *http.Request) error
}
