// Package staticdata exposes large, versioned JSON reference data by id,
// sourced through the artifact registry instead of being bundled into the SPA
// build or embedded in this binary: to the frontend through Handler, and to
// server-side callers, such as a document naming a stored code, through
// LoadAt and Find.
package staticdata

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/OpenNSW/core/artifact"
)

// Kind is the artifact kind for static JSON reference data. It is kept
// separate from core's generic_template/workflow_definition kinds so
// registering an id here can never make an internal-only artifact reachable
// from the public lookup endpoint.
const Kind artifact.Kind = "static_data"

type loadable struct {
	json.RawMessage
}

func (loadable) Kind() artifact.Kind { return Kind }

// Parse requires the artifact to be a JSON object with a top-level "data"
// array, never a bare array — so a future field (a version tag, a source
// timestamp) can be added to the envelope without becoming a breaking change
// for every consumer.
func (l *loadable) Parse(raw []byte) error {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("static data artifact must be a JSON object: %w", err)
	}
	if len(envelope.Data) == 0 {
		return fmt.Errorf(`static data artifact must have a top-level "data" field`)
	}
	var probe []json.RawMessage
	if err := json.Unmarshal(envelope.Data, &probe); err != nil {
		return fmt.Errorf(`static data artifact's "data" field must be a JSON array: %w`, err)
	}
	// Unmarshaling JSON null into a slice succeeds with a nil slice and no
	// error — distinct from "[]", which unmarshals to a non-nil empty slice —
	// so a null "data" field must be rejected explicitly.
	if probe == nil {
		return fmt.Errorf(`static data artifact's "data" field must be a JSON array, got null`)
	}
	l.RawMessage = raw
	return nil
}

// LoadAt fetches the static data artifact id as manifest version manifestVersion lists it: the
// row without a version, else its highest explicit version (see artifact.Latest). A case pins
// the manifest version it starts on, so its forms keep the data they opened with while a later
// manifest ships a new file under the same id. A manifest version that is not published returns
// an error matching artifact.ErrUnknownManifestVersion.
func LoadAt(ctx context.Context, set *artifact.Set, id string, manifestVersion int) (json.RawMessage, error) {
	reg, err := set.Registry(ctx, manifestVersion)
	if err != nil {
		return nil, err
	}
	w, err := artifact.Latest[loadable](ctx, reg, id)
	return w.RawMessage, err
}
