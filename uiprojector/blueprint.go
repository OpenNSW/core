package uiprojector

// Blueprint defines the layout and rules for a UI view.
type Blueprint struct {
	ID       string                      `json:"id"`
	Sections map[string]SectionBlueprint `json:"sections"`
}

// SectionBlueprint defines an individual component within a layout. The
// section's slot key in the surrounding Blueprint.Sections map is the
// authoritative identifier; SectionBlueprint deliberately has no own ID.
type SectionBlueprint struct {
	TemplateID  string       `json:"templateId"`
	Title       string       `json:"title"`
	Projector   string       `json:"projector"` // e.g., FORM, MARKDOWN
	DataKey     string       `json:"dataKey"`   // The key in Facts.Data to pluck for this section
	VisibleWhen *VisibleWhen `json:"visibleWhen,omitempty"`
}

// VisibleWhen defines declarative visibility rules based on Facts.
type VisibleWhen struct {
	States         []string `json:"states,omitempty"`         // Required Facts.State values
	RequireDataKey string   `json:"requireDataKey,omitempty"` // Section only visible if this key exists in data
}

// Facts represents the current state of a business entity to be rendered.
type Facts struct {
	State string         `json:"state"` // Logical status (e.g., "PENDING", "COMPLETED")
	Data  map[string]any `json:"data"`  // The snapshot/registry of business data
}

// SectionType identifies the projector used for a section.
type SectionType string

// Section represents a rendered component. Sections are returned in a
// slot-keyed map; the slot key is the identifier, so Section carries none.
type Section struct {
	Type    SectionType `json:"type"`
	Title   string      `json:"title"`
	Content any         `json:"content"`
}

// FormContent is the payload for a FORM projector.
type FormContent struct {
	Schema   any `json:"schema"`
	UISchema any `json:"uiSchema,omitempty"`
	Data     any `json:"data,omitempty"`
}
