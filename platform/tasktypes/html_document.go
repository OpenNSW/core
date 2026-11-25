package tasktypes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/htmlgen"
	corestorage "github.com/OpenNSW/core/storage"
)

// TaskTypeHTMLDocumentGenerator renders a print-ready HTML document from an
// html_template artifact and the task's inputs, and stores it.
const TaskTypeHTMLDocumentGenerator = "HTML_DOCUMENT_GENERATOR"

// htmlDocumentMIME is the type the document is stored and served as.
const htmlDocumentMIME = "text/html; charset=utf-8"

// HTMLTemplateSource returns an html_template's source by id.
type HTMLTemplateSource interface {
	HTMLTemplate(ctx context.Context, id string) ([]byte, error)
}

// DocumentSaver stores a document this service produced. The storage service
// satisfies it.
type DocumentSaver interface {
	Save(ctx context.Context, filename, mime string, body io.Reader, size int64) (*corestorage.FileMetadata, error)
}

// StaticData names a code by its row in a static_data artifact: the title of
// the row of artifact id whose const is value, in that artifact as manifest
// version manifestVersion lists it, and whether there is such a row. An id that
// manifest doesn't list matches artifact.ErrNotFound and a malformed artifact
// artifact.ErrInvalid, which fail the step for an admin; any other error is
// retried.
type StaticData interface {
	Title(ctx context.Context, manifestVersion int, id, value string) (title string, found bool, err error)
}

// staticDataResolver is the name a template calls the static data resolver by.
const staticDataResolver = "staticdata"

// errStaticDataCall marks a staticdata call the template itself gets wrong.
var errStaticDataCall = errors.New("staticdata takes a static_data id and a value")

// HTMLDocumentGeneratorPlugin is a synchronous plugin that renders the
// html_template named in the subtask template's plugin_properties against the
// task's inputs, stores the document, and records where it went:
//
//	{"task_type": "HTML_DOCUMENT_GENERATOR", "output_namespace": "permit_doc",
//	 "plugin_properties": {"template_id": "permit--html", "filename": "permit.html"}}
//
// The node's input_mapping assembles everything the document prints into the
// inputs — form submissions, reviewer decisions, a reference ID generated
// earlier — and the template addresses them by input name, e.g.
// {{ .application.applicant_name }}. The template only arranges and formats
// what it is given.
//
// A template names a code by its static_data row with the staticdata resolver:
// {{ staticdata "declaration--port-of-entry" .port }}
// prints the title of the row whose const is .port, in that artifact as the
// manifest version the task started on lists it, the manifest version the
// template itself comes from. An empty or missing value prints nothing, and a
// value no row has prints as it is, as htmlgen's lookup helper does.
//
// The stored file's metadata is written to the task's own state at
// <output_namespace> as {key, name, mime_type, size}; the workflow reads the
// key from there through the node's output_mapping, and a client fetches the
// document through the storage API with it.
type HTMLDocumentGeneratorPlugin struct {
	templates  HTMLTemplateSource
	files      DocumentSaver
	staticData StaticData
}

// NewHTMLDocumentGeneratorPlugin builds the plugin. All dependencies must be
// non-nil.
func NewHTMLDocumentGeneratorPlugin(templates HTMLTemplateSource, files DocumentSaver, staticData StaticData) *HTMLDocumentGeneratorPlugin {
	if templates == nil {
		panic("templates is nil")
	}
	if files == nil {
		panic("files is nil")
	}
	if staticData == nil {
		panic("staticData is nil")
	}
	return &HTMLDocumentGeneratorPlugin{templates: templates, files: files, staticData: staticData}
}

type htmlDocumentGeneratorConfig struct {
	TemplateID string `json:"template_id"`
	// Filename names the stored document; it defaults to <template_id>.html.
	Filename string `json:"filename,omitempty"`
}

// Execute renders and stores the document and returns nil (not ErrSuspended),
// so the engine advances immediately.
func (p *HTMLDocumentGeneratorPlugin) Execute(ctx pluginContext, configRaw json.RawMessage) error {
	var cfg htmlDocumentGeneratorConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return configError(fmt.Errorf("html_document_generator: invalid config: %w", err))
	}
	if strings.TrimSpace(cfg.TemplateID) == "" {
		return configError(errors.New("html_document_generator: plugin_properties.template_id is required"))
	}
	if ctx.OutputNamespace == "" {
		return configError(errors.New("html_document_generator: output_namespace is required to hold the document's storage key"))
	}
	if ctx.Record == nil {
		return inputError(errors.New("html_document_generator: task record is nil"))
	}

	tmpl, err := p.templates.HTMLTemplate(ctx.Context, cfg.TemplateID)
	if err != nil {
		err = fmt.Errorf("html_document_generator: load template %q: %w", cfg.TemplateID, err)
		if errors.Is(err, artifact.ErrNotFound) || errors.Is(err, artifact.ErrInvalid) {
			return configError(err)
		}
		return err
	}

	// Inputs go to htmlgen as JSON, which it decodes with UseNumber, so a
	// number prints as plain digits rather than in exponent form. That keeps
	// only the text the number still has here: inputs arrive through Temporal
	// as float64, so 1.50 is already 1.5. A template formats money and
	// quantities with decimal.
	data, err := json.Marshal(ctx.Inputs)
	if err != nil {
		return inputError(fmt.Errorf("html_document_generator: encode inputs: %w", err))
	}
	manifestVersion := ctx.Record.ManifestVersion
	doc, err := htmlgen.Generate(ctx.Context, tmpl, data, htmlgen.WithResolvers(htmlgen.Resolvers{
		staticDataResolver: func(rctx context.Context, args ...any) (any, error) {
			return p.resolveStaticData(rctx, manifestVersion, args...)
		},
	}))
	if err != nil {
		err = fmt.Errorf("html_document_generator: render template %q: %w", cfg.TemplateID, err)
		switch {
		// The template asks for static data that isn't there, or asks wrongly.
		case errors.Is(err, artifact.ErrNotFound), errors.Is(err, artifact.ErrInvalid), errors.Is(err, errStaticDataCall):
			return configError(err)
		// The static data could not be read this time; a retry may.
		case errors.Is(err, htmlgen.ErrResolver):
			return err
		default:
			return inputError(err)
		}
	}

	// Every run stores what it rendered, including a Temporal retry and a
	// workflow loop that brings the step back. A loop may arrive with changed
	// inputs, and reusing an earlier document would leave the task pointing at
	// one rendered from the old data. A retry leaves at worst an extra copy in
	// storage that nothing refers to.
	meta, err := p.files.Save(ctx.Context, documentFilename(cfg), htmlDocumentMIME, bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		return fmt.Errorf("html_document_generator: store document: %w", err)
	}

	if ctx.Record.Data == nil {
		ctx.Record.Data = make(map[string]any)
	}
	ctx.Record.Data[ctx.OutputNamespace] = map[string]any{
		"key":       meta.Key,
		"name":      meta.Name,
		"mime_type": meta.MimeType,
		"size":      meta.Size,
	}
	return nil
}

// resolveStaticData is the staticdata resolver for a task on manifest version
// manifestVersion: the version of the manifest
// title of the row of static_data artifact args[0] whose
// const is the last argument.
//
// A template in a published manifest may name a version between the two, as
// in {{ staticdata "<id>" "1" .port }}. Published manifests never change, so
// the resolver accepts that form and ignores the version: the manifest that
// holds such a template lists the static data at the version it names.
// Once every in-flight task of a manifest version whose template calls
// staticdata with a version has finished, the resolver can accept only two
// arguments: the static data id and the value.
func (p *HTMLDocumentGeneratorPlugin) resolveStaticData(ctx context.Context, manifestVersion int, args ...any) (any, error) {
	if len(args) != 2 && len(args) != 3 {
		return nil, fmt.Errorf("%w, got %d arguments", errStaticDataCall, len(args))
	}
	id, ok := args[0].(string)
	if !ok || strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: the id must be non-empty text", errStaticDataCall)
	}
	var code string
	switch v := args[len(args)-1].(type) {
	case nil:
	case string:
		code = v
	case json.Number:
		code = v.String()
	default:
		return nil, fmt.Errorf("%w: the value must be text or a number, got %T", errStaticDataCall, v)
	}
	if code == "" {
		return "", nil
	}

	title, found, err := p.staticData.Title(ctx, manifestVersion, id, code)
	if err != nil {
		return nil, err
	}
	if !found {
		return code, nil
	}
	return title, nil
}

// documentFilename is the stored document's name. Storage takes the key's
// extension from it, so it always ends in .html.
func documentFilename(cfg htmlDocumentGeneratorConfig) string {
	name := strings.TrimSpace(cfg.Filename)
	if name == "" {
		name = cfg.TemplateID
	}
	if !strings.EqualFold(filepath.Ext(name), ".html") {
		name += ".html"
	}
	return name
}
