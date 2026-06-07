package artifact

// Kind identifies an artifact's category. It is an OPEN string: core defines its
// own built-ins below, but any package (including a country repo) may define new
// kinds without editing core. The registry treats Kind as an opaque key — it
// never validates against a known set.
type Kind string

// Artifact is the constraint for everything the registry can return. Kind MUST be
// a value-receiver method returning a constant (independent of field values),
// because the registry calls it on a zero value (see kindOf).
type Artifact interface {
	Kind() Kind
}

// Parser is implemented by *T for each artifact type. Parse populates the value
// from raw bytes AND validates its shape, turning a wrong-shaped artifact into a
// returned error instead of a silent zero value discovered later.
//
// Parse is EXPORTED on purpose: a country's artifact type, defined outside core,
// must be able to satisfy it. An unexported method could not be implemented from
// another package.
type Parser interface {
	Parse(raw []byte) error
}
