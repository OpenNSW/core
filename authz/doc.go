// Package authz provides generic, transport-agnostic authorization for HTTP
// services, driven by the OAuth2 scopes carried on a request's authenticated
// principal.
//
// It is intentionally decoupled from any authentication implementation: it
// defines a minimal Principal interface (Subject/Roles/Scopes) and receives the
// principal through an injected Extractor, so it imports nothing from the authn
// layer (or any other internal package). In this codebase *auth.AuthContext
// satisfies Principal structurally; the composition root wires
// auth.GetAuthContext into the Extractor.
//
// Authorization decisions use the scopes already granted on the token (for
// example "nsw:consignment:read"). Application-specific scope strings are defined
// by the caller, never here.
package authz
