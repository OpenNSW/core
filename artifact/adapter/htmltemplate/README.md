# htmltemplate

An [artifact adapter](../README.md) for HTML document templates. It registers the `html_template` kind, so a template
ships as an artifact like any other configuration, and loads a template's source by ID.

A template is stored as the HTML file itself, not wrapped in JSON. It is the input [`htmlgen`](../../../htmlgen/README.md)
renders, but this package doesn't use `htmlgen`, and `htmlgen` takes template bytes from any source. The application
connects the two.

```go
reg.RegisterArtifact("phyto-certificate", htmltemplate.Kind, "", "npqs/phyto-certificate.gohtml")

src, err := htmltemplate.Load(ctx, reg, "phyto-certificate") // the latest version's bytes
html, err := htmlgen.Generate(ctx, src, data)
```

`Load` returns an error matching `artifact.ErrNotFound` for an unknown ID. Loading doesn't check the template:
rendering compiles and checks it every time, and which resolvers it may call is up to the renderer.
