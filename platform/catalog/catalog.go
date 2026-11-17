// Package catalog holds the deployment's global catalog: the single source of
// truth mapping the stable logical names used across configuration onto one
// deployment's concrete identifiers — IdP token roles and OAuth2 client ids
// today, further sections later.
//
// It is the catalog section of config.yaml, decoded with the rest of the config
// and handed to consumers as data. Unknown keys are ignored, so the section may
// grow new entries ahead of the code that reads them.
package catalog

import (
	"errors"
	"fmt"
	"strings"
)

// Catalog is the catalog section of config.yaml.
type Catalog struct {
	// Roles maps a logical principal name ("trader") to the IdP token role
	// carried in a user's access token ("Trader").
	Roles map[string]string `yaml:"roles"`
	// Clients maps a logical principal name ("fcau") to the OAuth2 client id
	// presented by an M2M caller ("FCAU_TO_NSW").
	Clients map[string]string `yaml:"clients"`
}

// RequireRoles reports an error unless roles — the catalog's Roles map — defines
// every one of names with a non-empty token role.
//
// It exists because some callers depend on specific logical names rather than on
// whatever the catalog happens to define: consignment scopes queries by "trader"
// and "cha", and task ownership resolution maps each of those to its own database
// column. For them a missing name is not a narrower catalog but a silent denial of
// every request in that role, so they call this at construction to fail loudly
// instead. An empty mapping counts as missing: it can never match a real JWT role
// claim, and honoring it would let a comparison against an empty string succeed.
func RequireRoles(roles map[string]string, names ...string) error {
	var missing []string
	for _, name := range names {
		if role, ok := roles[name]; !ok || role == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("catalog is missing required role(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

// Validate reports whether the catalog is internally consistent: it must define
// at least one entry, and every logical name must map to a non-empty identifier.
// Messages are unprefixed so the config can qualify them with the section.
func (c *Catalog) Validate() error {
	if c == nil {
		return errors.New("nil catalog")
	}
	if len(c.Roles) == 0 && len(c.Clients) == 0 {
		return errors.New("defines no roles or clients")
	}
	for name, role := range c.Roles {
		if role == "" {
			return fmt.Errorf("roles[%q] must map to a non-empty token role", name)
		}
	}
	for name, clientID := range c.Clients {
		if clientID == "" {
			return fmt.Errorf("clients[%q] must map to a non-empty client id", name)
		}
	}
	return nil
}
