# UI Projector

`uiprojector` is a metadata-driven engine that transforms raw workflow state and business data into a structured UI payload. It uses a **Zone-Based Architecture** to map transformation logic (Projectors) and layout rules (Blueprints) into named UI slots.

## Core Concepts

### 1. Blueprint (The Layout)
A `Blueprint` defines the structural rules for a view. It maps named **Zones** (e.g., "main", "sidebar") to specific components.
- **TemplateID**: The identifier for the raw template content.
- **Projector**: The strategy used to transform the data (e.g., `FORM`, `MARKDOWN`).
- **VisibleWhen**: Declarative rules that hide/show zones based on current state or data presence.

### 2. Facts (The Input)
`Facts` represent the current context of the business entity being rendered.
- **State**: The logical status (e.g., `PENDING`, `APPROVED`).
- **Data**: A map of raw data plucked into sections via `DataKey`.

### 3. Projector (The Strategy)
Projectors are transformation strategies. Built-in projectors include:
- **FORM**: Transforms JSON Schema templates into interactive forms.
- **MARKDOWN**: Renders Go `text/template` markdown.
- **RAW**: Returns data as-is.

### 4. Assembler (The Engine)
The `Assembler` orchestrates the lifecycle:
1. Validates visibility rules via `ShouldRender`.
2. Resolves and fetches templates via a `TemplateProvider`.
3. Selects the appropriate `Projector`.
4. Plucks specific data via `DataKey`.
5. Projects the final content into the designated **Zone**.

## Usage

```go
// 1. Setup dependencies
tp := MyTemplateProvider{} 
projectors := uiprojector.DefaultProjectors()

// 2. Initialize Assembler
asm, err := uiprojector.NewAssembler(tp, projectors)
if err != nil {
    // handle error
}

// 3. Assemble a view
blueprint := &uiprojector.Blueprint{
    Sections: map[string]uiprojector.SectionBlueprint{
        "main": {
            ID: "my-form",
            Projector: "FORM",
            TemplateID: "form-v1",
        },
    },
}
facts := uiprojector.Facts{State: "DRAFT", Data: map[string]any{}}

zones, err := asm.Assemble(ctx, blueprint, facts)
if err != nil {
    // handle error
}

// Access rendered content by zone
mainContent := zones["main"].Content
```

## Architecture Features

- **Zone-Based**: Named slots instead of simple lists allow for complex, shell-driven layouts.
- **Stateless Visibility**: Visibility logic is decoupled and testable through pure functions.
- **Immutability**: The `Assembler` uses defensive copying for its projector registry to prevent external side effects.
- **Storage Agnostic**: Templates can be fetched from S3, local disk, or databases via the `TemplateProvider` interface.

## Extensibility
You can register custom projectors by adding them to the map passed to `NewAssembler`:

```go
projectors := uiprojector.DefaultProjectors()
projectors["CHARTS"] = &ChartProjector{}
asm, err := uiprojector.NewAssembler(tp, projectors)
```
