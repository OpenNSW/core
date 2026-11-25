package payment

import (
	"context"
	"fmt"
	"testing"

	corepayment "github.com/OpenNSW/core/payment"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateCall is one Generate call seen by fakeRefIDs.
type generateCall struct {
	issuer, idType string
	params         map[string]string
}

// fakeRefIDs returns "<issuer>/<idType>/<n>" and records every call.
type fakeRefIDs struct {
	calls []generateCall
}

func (f *fakeRefIDs) Generate(_ context.Context, issuer, idType string, params map[string]string) (string, error) {
	f.calls = append(f.calls, generateCall{issuer: issuer, idType: idType, params: params})
	return fmt.Sprintf("%s/%s/%d", issuer, idType, len(f.calls)), nil
}

// testIssuer is the default issuer the tests build a generator with.
const testIssuer = "ACME"

func checkout(metadata map[string]string) corepayment.CreateCheckoutRequest {
	return corepayment.CreateCheckoutRequest{Metadata: metadata}
}

// A checkout naming no format takes the default.
func TestRefGenerator_DefaultFormat(t *testing.T) {
	refIDs := &fakeRefIDs{}

	ref, err := NewRefGenerator(refIDs, testIssuer).GenerateReference(context.Background(), checkout(map[string]string{"task_id": "task-1"}))

	require.NoError(t, err)
	assert.Equal(t, "ACME/payment_ref/1", ref)
	require.Len(t, refIDs.calls, 1)
	assert.Equal(t, testIssuer, refIDs.calls[0].issuer)
	assert.Equal(t, DefaultReferenceIDType, refIDs.calls[0].idType)
	assert.Empty(t, refIDs.calls[0].params)
}

// The format and params ReferenceMetadata writes are the ones the generator
// reads back, alongside the checkout's other metadata.
func TestRefGenerator_FeeFormat(t *testing.T) {
	refIDs := &fakeRefIDs{}
	metadata := ReferenceMetadata("AGENCY", "fee_payment_ref", map[string]string{"exporterId": "0002", "mainCategory": "01"})
	metadata["task_id"] = "task-1"

	ref, err := NewRefGenerator(refIDs, testIssuer).GenerateReference(context.Background(), checkout(metadata))

	require.NoError(t, err)
	assert.Equal(t, "AGENCY/fee_payment_ref/1", ref)
	assert.Equal(t, []generateCall{{issuer: "AGENCY", idType: "fee_payment_ref", params: map[string]string{
		"exporterId": "0002", "mainCategory": "01",
	}}}, refIDs.calls)
}

// A format needs both its issuer and its id_type.
func TestRefGenerator_PartialFormatRejected(t *testing.T) {
	full := ReferenceMetadata("AGENCY", "fee_payment_ref", nil)
	for name, metadata := range map[string]map[string]string{
		"issuer only":  {metadataKeyIssuer: full[metadataKeyIssuer]},
		"id_type only": {metadataKeyIDType: full[metadataKeyIDType]},
	} {
		t.Run(name, func(t *testing.T) {
			refIDs := &fakeRefIDs{}

			_, err := NewRefGenerator(refIDs, testIssuer).GenerateReference(context.Background(), checkout(metadata))

			require.ErrorContains(t, err, "a format needs both")
			assert.Empty(t, refIDs.calls)
		})
	}
}

func TestNewRefGenerator_NilRegistryPanics(t *testing.T) {
	assert.Panics(t, func() { NewRefGenerator(nil, testIssuer) })
}

func TestNewRefGenerator_EmptyIssuerPanics(t *testing.T) {
	assert.Panics(t, func() { NewRefGenerator(&fakeRefIDs{}, "") })
}
