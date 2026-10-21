// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
)

// ErrPayeeNotMapped indicates a payment method has no mapping for a payee, so
// it cannot collect payments for it.
var ErrPayeeNotMapped = errors.New("payee not mapped for payment method")

// Payee identifies who is being paid and for what, in the host's own terms. It
// never carries a gateway's account ids: each payment method maps a payee to
// its own ids in its "payees" config, and the registry resolves that mapping.
type Payee struct {
	OrgID     string `json:"org_id"`
	ServiceID string `json:"service_id"`
}

func (p Payee) validate() error {
	if strings.TrimSpace(p.OrgID) == "" {
		return errors.New("payee.org_id is required")
	}
	if strings.TrimSpace(p.ServiceID) == "" {
		return errors.New("payee.service_id is required")
	}
	return nil
}

func (p Payee) String() string {
	return p.OrgID + "/" + p.ServiceID
}

// PayeeLookup returns the calling gateway's resolved payee mapping for the
// transaction holding referenceNumber, so a webhook can be checked against the
// payee its reference was issued for. found is false for an unknown reference.
type PayeeLookup func(ctx context.Context, referenceNumber string) (mapping json.RawMessage, found bool, err error)

// orgPayees is one org's entry in a payment method's "payees" config. Each
// service's entry is merged over the org's defaults, keys in the service entry
// winning. Both are JSON objects whose keys the method's gateway defines.
type orgPayees struct {
	Defaults json.RawMessage            `json:"defaults,omitempty"`
	Services map[string]json.RawMessage `json:"services"`
}

// resolvePayees flattens a method's "payees" config into one merged mapping per
// payee. Only a service listed under its org is mapped: the defaults alone map
// nothing.
func resolvePayees(payees map[string]orgPayees) (map[Payee]json.RawMessage, error) {
	resolved := make(map[Payee]json.RawMessage)
	for orgID, org := range payees {
		if strings.TrimSpace(orgID) == "" {
			return nil, errors.New("payees: org id is empty")
		}
		defaults, err := jsonObject(org.Defaults)
		if err != nil {
			return nil, fmt.Errorf("payees.%s.defaults: %w", orgID, err)
		}
		for serviceID, raw := range org.Services {
			if strings.TrimSpace(serviceID) == "" {
				return nil, fmt.Errorf("payees.%s.services: service id is empty", orgID)
			}
			entry, err := jsonObject(raw)
			if err != nil {
				return nil, fmt.Errorf("payees.%s.services.%s: %w", orgID, serviceID, err)
			}
			merged := maps.Clone(defaults)
			maps.Copy(merged, entry)
			mapping, err := json.Marshal(merged)
			if err != nil {
				return nil, fmt.Errorf("payees.%s.services.%s: %w", orgID, serviceID, err)
			}
			resolved[Payee{OrgID: orgID, ServiceID: serviceID}] = mapping
		}
	}
	return resolved, nil
}

// jsonObject decodes raw as a JSON object. Absent or null decodes to an empty
// object; anything else that is not an object is an error.
func jsonObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	obj := map[string]json.RawMessage{}
	if len(raw) == 0 {
		return obj, nil
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, errors.New("must be a JSON object")
	}
	if obj == nil {
		obj = map[string]json.RawMessage{}
	}
	return obj, nil
}
