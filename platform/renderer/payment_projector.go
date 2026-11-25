package renderer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"text/template"

	"github.com/OpenNSW/core/uiprojector"
)

// ProjectorPayment defines the payment instructions projector type.
const ProjectorPayment uiprojector.ProjectorType = "PAYMENT"

// PaymentProjector renders payment instructions for a PENDING_PAYMENT task.
//
// The payment facts (reference, amount, instructions, flow type) are populated
// on the task record by the payment plugin when it creates the checkout session
// via core/payment. The projector renders those facts; it does not call the
// payment service. If the task section carries its own markdown template
// (templateContent) it is executed with the payment data, otherwise the
// gateway-supplied instructions are rendered as-is.
type PaymentProjector struct{}

// NewPaymentProjector creates a new PaymentProjector.
func NewPaymentProjector() *PaymentProjector {
	return &PaymentProjector{}
}

// Type returns the projector type.
func (p *PaymentProjector) Type() uiprojector.ProjectorType {
	return ProjectorPayment
}

// Project renders the payment instructions. When the payment facts haven't been
// populated yet (entering PENDING_PAYMENT before CreateCheckoutSession
// completes), it returns a placeholder so the page still loads instead of 500ing.
func (p *PaymentProjector) Project(ctx context.Context, templateContent []byte, data any) (uiprojector.Projection, error) {
	if data == nil {
		return uiprojector.Projection{
			Type:    uiprojector.SectionTypeMarkdown,
			Content: "Preparing payment details...",
		}, nil
	}
	dataMap, ok := data.(map[string]any)
	if !ok {
		return uiprojector.Projection{}, fmt.Errorf("payment_projector: expected map data, got %T", data)
	}

	instructions, _ := dataMap["instructions"].(string)

	// templateContent is the section's instructions wrapper, a JSON object of the
	// form {"id": "...", "template": "{{ .instructions }}"}. Extract the inner
	// template and render it against the payment data map (whose keys are the
	// snake_case fields the plugin stored: instructions, reference_number,
	// amount, currency, checkout_url, ...). Fall back to the raw instructions
	// text if no template is configured.
	content := instructions
	if len(bytes.TrimSpace(templateContent)) > 0 {
		var wrapper struct {
			ID       string `json:"id"`
			Template string `json:"template"`
		}
		if err := json.Unmarshal(templateContent, &wrapper); err != nil {
			return uiprojector.Projection{}, fmt.Errorf("payment_projector: parse instructions wrapper: %w", err)
		}
		if t := wrapper.Template; t != "" {
			tmpl, err := template.New("instructions").Parse(t)
			if err != nil {
				return uiprojector.Projection{}, fmt.Errorf("payment_projector: parse template: %w", err)
			}
			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, dataMap); err != nil {
				return uiprojector.Projection{}, fmt.Errorf("payment_projector: execute template: %w", err)
			}
			content = buf.String()
		}
	}

	// REDIRECT-flow gateways return a hosted checkout URL the UI must navigate to.
	if flowType, _ := dataMap["flow_type"].(string); flowType == "REDIRECT" {
		checkoutURL, _ := dataMap["checkout_url"].(string)
		return uiprojector.Projection{
			Type: uiprojector.SectionType("REDIRECT"),
			Content: map[string]any{
				"checkout_url": checkoutURL,
				"content":      content,
			},
		}, nil
	}

	return uiprojector.Projection{
		Type:    uiprojector.SectionTypeMarkdown,
		Content: content,
	}, nil
}
