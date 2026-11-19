// Package htmltemplate registers htmlgen templates as the "html_template" kind
// of the core artifact registry.
package htmltemplate

import (
	"context"

	"github.com/OpenNSW/core/artifact"
)

// Kind is the manifest kind of an htmlgen template: the target HTML with
// html/template actions in it, stored as the file itself rather than wrapped
// in JSON.
const Kind artifact.Kind = "html_template"

type loadable struct {
	src []byte
}

func (loadable) Kind() artifact.Kind { return Kind }

// Parse keeps the source as it is. A template is loaded each time it is
// rendered, and rendering compiles and checks it, so checking it here as well
// would only repeat that. Which resolvers it may call is up to whoever renders
// it.
func (l *loadable) Parse(raw []byte) error {
	l.src = raw
	return nil
}

// Load returns the source of the latest version of the template id. An
// unknown id returns an error matching artifact.ErrNotFound.
func Load(ctx context.Context, reg *artifact.Registry, id string) ([]byte, error) {
	l, err := artifact.Latest[loadable](ctx, reg, id)
	if err != nil {
		return nil, err
	}
	return l.src, nil
}
