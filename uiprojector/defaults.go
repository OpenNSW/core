package uiprojector

// Built-in projector keys. These are the names returned by each projector's
// Type() method, and they match the SectionBlueprint.Projector values used in
// blueprints. Consumers may register additional projectors whose Type() returns
// any other unique string.
const (
	ProjectorForm     ProjectorType = "FORM"
	ProjectorMarkdown ProjectorType = "MARKDOWN"
	ProjectorRaw      ProjectorType = "RAW"
)

// Built-in render types emitted by the projectors shipped with this package.
// These are the SectionType values carried on Projection.Type and copied into
// Section.Type for the frontend. Custom projectors may emit any other string,
// and a single projector may emit more than one render type per invocation.
const (
	SectionTypeForm     SectionType = "FORM"
	SectionTypeMarkdown SectionType = "MARKDOWN"
	SectionTypeRaw      SectionType = "RAW"
)

// DefaultProjectors returns a fresh slice containing the projectors shipped
// with this package. The returned slice is owned by the caller and safe to
// mutate — append, replace, or drop entries before passing it to NewAssembler.
func DefaultProjectors() []Projector {
	return []Projector{
		NewFormProjector(),
		NewMarkdownProjector(),
		NewRawProjector(),
	}
}
