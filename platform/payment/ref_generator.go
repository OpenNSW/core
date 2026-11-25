// Package payment adapts core/payment to the refid registry and the task engine.
//
// It issues the references payers pay against, from the refid formats in the
// server config. RefGenerator implements core/payment's ReferenceGenerator: a
// checkout takes the fee's own format when its metadata names one
// (ReferenceMetadata), and the default format otherwise. TaskCompleter is the
// task completer the payment service settles a payment step through.
package payment

import (
	"context"
	"fmt"
	"strings"

	corepayment "github.com/OpenNSW/core/payment"
	"github.com/OpenNSW/core/refid"
)

// DefaultReferenceIDType is the id_type of the default payment reference
// format, for fees without a format of their own. The deployment names the
// format's issuer (NewRefGenerator), and its refid config defines the format.
const DefaultReferenceIDType = "payment_ref"

// Checkout metadata keys that carry a fee's reference format: the format's
// issuer and id_type, and one key per param, named metadataKeyParamPrefix plus
// the param's name. They are stored with the transaction.
const (
	metadataKeyIssuer      = "reference_issuer"
	metadataKeyIDType      = "reference_id_type"
	metadataKeyParamPrefix = "reference_param."
)

// ReferenceMetadata returns the checkout metadata that names a fee's reference
// format and the values of its params.
func ReferenceMetadata(issuer, idType string, params map[string]string) map[string]string {
	metadata := make(map[string]string, len(params)+2)
	metadata[metadataKeyIssuer] = issuer
	metadata[metadataKeyIDType] = idType
	for name, value := range params {
		metadata[metadataKeyParamPrefix+name] = value
	}
	return metadata
}

// RefGenerator issues payment references from the refid registry: in the format
// a checkout's metadata names (ReferenceMetadata), and in the default format
// (its default issuer, DefaultReferenceIDType) for a checkout that names none.
type RefGenerator struct {
	refIDs        refid.Registry
	defaultIssuer string
}

// NewRefGenerator builds the generator. refIDs must be non-nil, and
// defaultIssuer, the issuer of the default format, non-empty.
func NewRefGenerator(refIDs refid.Registry, defaultIssuer string) *RefGenerator {
	if refIDs == nil {
		panic("refIDs is nil")
	}
	if defaultIssuer == "" {
		panic("defaultIssuer is empty")
	}
	return &RefGenerator{refIDs: refIDs, defaultIssuer: defaultIssuer}
}

// GenerateReference implements corepayment.ReferenceGenerator.
func (g *RefGenerator) GenerateReference(ctx context.Context, req corepayment.CreateCheckoutRequest) (string, error) {
	issuer, idType := req.Metadata[metadataKeyIssuer], req.Metadata[metadataKeyIDType]
	switch {
	case issuer == "" && idType == "":
		issuer, idType = g.defaultIssuer, DefaultReferenceIDType
	case issuer == "" || idType == "":
		return "", fmt.Errorf("payment reference: the checkout names %s=%q and %s=%q; a format needs both",
			metadataKeyIssuer, issuer, metadataKeyIDType, idType)
	}
	params := make(map[string]string)
	for key, value := range req.Metadata {
		if name, ok := strings.CutPrefix(key, metadataKeyParamPrefix); ok {
			params[name] = value
		}
	}
	return g.refIDs.Generate(ctx, issuer, idType, params)
}
