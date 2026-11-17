package authn

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	coreauthn "github.com/OpenNSW/core/authn"
)

// Config is the authentication configuration this application supplies, loaded
// from config.yaml's authn section by cmd/server/config.
//
// It deliberately omits core/authn's claim-declaration fields: the claims
// Principal flattens onto named fields are this package's own business, not a
// per-deployment setting, so coreConfig declares them from builtinUserClaims. A
// deployment that needs further token attributes (for attribute-based rules that
// differ per agency) lists them in ExtraUserClaims / ExtraClientClaims.
type Config struct {
	JWKSURL               string `yaml:"jwksURL"`
	Issuer                string `yaml:"issuer"`
	Audience              string `yaml:"audience"`
	InsecureSkipTLSVerify bool   `yaml:"insecureSkipTLSVerify"`

	// ExtraUserClaims names further claims to read from user
	// (authorization_code) tokens, surfaced on Principal.ExtraClaims.
	ExtraUserClaims ClaimSpec `yaml:"extraUserClaims"`
	// ExtraClientClaims is the machine (client_credentials) counterpart. A name
	// declared for one kind is never read from the other kind's tokens.
	ExtraClientClaims ClaimSpec `yaml:"extraClientClaims"`
}

// ClaimSpec lists top-level token claims by name. Names are matched exactly
// (JWT claim names are case-sensitive), and a name in both lists is required.
type ClaimSpec struct {
	// Required claims must carry a usable value (a non-blank string, or a
	// non-empty array of non-blank strings): a token without one is rejected
	// with 401, on every request that deployment serves.
	Required []string `yaml:"required"`
	// Optional claims are read when present and skipped when absent.
	Optional []string `yaml:"optional"`
}

func (s ClaimSpec) core() coreauthn.ClaimSpec {
	return coreauthn.ClaimSpec{Required: s.Required, Optional: s.Optional}
}

// names lists every claim in the spec.
func (s ClaimSpec) names() []string {
	return slices.Concat(s.Required, s.Optional)
}

// coreConfig maps Config onto core/authn's Config and declares the claims this
// package reads: builtinUserClaims, then the deployment's extras. A claim
// surfaced on Principal but not declared here is silently never extracted.
func (c Config) coreConfig() coreauthn.Config {
	return coreauthn.Config{
		JWKSURL:               c.JWKSURL,
		Issuer:                c.Issuer,
		Audience:              c.Audience,
		InsecureSkipTLSVerify: c.InsecureSkipTLSVerify,
		UserClaims: coreauthn.ClaimSpec{
			Required: slices.Concat(builtinUserClaims.Required, c.ExtraUserClaims.Required),
			Optional: slices.Concat(builtinUserClaims.Optional, c.ExtraUserClaims.Optional),
		},
		ClientClaims: c.ExtraClientClaims.core(),
	}
}

// Validate reports whether the configuration is usable, including the claim
// declarations coreConfig adds. A claim name that is blank, or already bound to
// a dedicated field (by this package or by core/authn), is an error here, at
// startup, so no claim silently shadows or double-binds another.
//
// The names core/authn binds (its fixed schema and the roles claim) are checked
// by core/authn when it validates the declaration, so that list lives there
// alone.
func (c Config) Validate() error {
	if err := errors.Join(
		c.ExtraUserClaims.validate("authn.extraUserClaims", builtinUserClaimNames),
		c.ExtraClientClaims.validate("authn.extraClientClaims", nil),
	); err != nil {
		return err
	}
	return c.coreConfig().Validate()
}

// validate rejects blank names and names in builtin, matched case-insensitively
// like core/authn's own guard. key is the section's yaml path. Every offending
// name is reported together, so a config is fixed in one pass.
func (s ClaimSpec) validate(key string, builtin []string) error {
	var errs []error
	for _, list := range []struct {
		name  string
		names []string
	}{{"required", s.Required}, {"optional", s.Optional}} {
		var blank bool
		var reserved []string
		for _, name := range list.names {
			trimmed := strings.TrimSpace(name)
			switch {
			case trimmed == "":
				blank = true
			case slices.ContainsFunc(builtin, func(b string) bool { return strings.EqualFold(b, trimmed) }):
				reserved = append(reserved, trimmed)
			}
		}
		if blank {
			errs = append(errs, fmt.Errorf("%s.%s contains an empty claim name", key, list.name))
		}
		if len(reserved) > 0 {
			slices.Sort(reserved)
			errs = append(errs, fmt.Errorf("%s.%s lists claim(s) %v that the server already reads for every user (%s); remove them",
				key, list.name, slices.Compact(reserved), strings.Join(builtin, ", ")))
		}
	}
	return errors.Join(errs...)
}
