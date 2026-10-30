// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package temporal

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/proto"
)

// testEncryptionConfig returns an EncryptionConfig with a fresh random key.
func testEncryptionConfig(t *testing.T, keyID string) EncryptionConfig {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return EncryptionConfig{KeyID: keyID, Key: base64.StdEncoding.EncodeToString(key)}
}

func newTestCodec(t *testing.T, cfg EncryptionConfig) *encryptionCodec {
	t.Helper()
	codec, err := newEncryptionCodec(cfg)
	if err != nil {
		t.Fatalf("newEncryptionCodec() error = %v", err)
	}
	return codec
}

func plainPayloads(t *testing.T, values ...any) []*commonpb.Payload {
	t.Helper()
	payloads, err := converter.GetDefaultDataConverter().ToPayloads(values...)
	if err != nil {
		t.Fatalf("ToPayloads() error = %v", err)
	}
	return payloads.GetPayloads()
}

func TestEncryptionCodecRoundTrip(t *testing.T) {
	codec := newTestCodec(t, testEncryptionConfig(t, "k1"))
	original := plainPayloads(t, "TIN 104-555-0001", map[string]string{"consignee": "Hamburg Fine Foods"}, nil)

	encoded, err := codec.Encode(original)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	for i, p := range encoded {
		if got := string(p.GetMetadata()[converter.MetadataEncoding]); got != encodingEncrypted {
			t.Fatalf("payload %d encoding = %q, want %q", i, got, encodingEncrypted)
		}
		if got := string(p.GetMetadata()[metadataKeyID]); got != "k1" {
			t.Fatalf("payload %d key ID = %q, want %q", i, got, "k1")
		}
		if bytes.Contains(p.GetData(), []byte("104-555-0001")) || bytes.Contains(p.GetData(), []byte("Hamburg")) {
			t.Fatalf("payload %d carries its plaintext", i)
		}
	}

	decoded, err := codec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	for i := range original {
		if !proto.Equal(decoded[i], original[i]) {
			t.Fatalf("payload %d after the round trip = %v, want %v", i, decoded[i], original[i])
		}
	}
}

func TestEncryptionCodecUsesAFreshNonce(t *testing.T) {
	codec := newTestCodec(t, testEncryptionConfig(t, "k1"))
	payload := plainPayloads(t, "same value")

	first, err := codec.Encode(payload)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	second, err := codec.Encode(payload)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if bytes.Equal(first[0].GetData(), second[0].GetData()) {
		t.Fatalf("encoding the same payload twice gave the same ciphertext")
	}
}

func TestEncryptionCodecRefuses(t *testing.T) {
	cfg := testEncryptionConfig(t, "k1")
	codec := newTestCodec(t, cfg)
	encrypt := func(t *testing.T, codec *encryptionCodec) *commonpb.Payload {
		t.Helper()
		encoded, err := codec.Encode(plainPayloads(t, "value"))
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
		return encoded[0]
	}

	tests := []struct {
		name    string
		payload func(t *testing.T) *commonpb.Payload
		wantErr string
	}{
		{"an unencrypted payload", func(t *testing.T) *commonpb.Payload { return plainPayloads(t, "value")[0] }, "not encrypted"},
		{"a tampered ciphertext", func(t *testing.T) *commonpb.Payload {
			p := encrypt(t, codec)
			p.Data[len(p.Data)-1] ^= 0x01
			return p
		}, "failed authentication"},
		{"another key ID", func(t *testing.T) *commonpb.Payload {
			p := encrypt(t, codec)
			p.Metadata[metadataKeyID] = []byte("k2")
			return p
		}, `encrypted with key "k2"`},
		{"another key under the same ID", func(t *testing.T) *commonpb.Payload {
			return encrypt(t, newTestCodec(t, testEncryptionConfig(t, "k1")))
		}, "failed authentication"},
		{"a truncated ciphertext", func(t *testing.T) *commonpb.Payload {
			p := encrypt(t, codec)
			p.Data = p.Data[:4]
			return p
		}, "too short"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := codec.Decode([]*commonpb.Payload{tt.payload(t)})
			if err == nil {
				t.Fatalf("Decode() error = nil, want one containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Decode() error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestEncryptingConverters(t *testing.T) {
	dataConverter, failureConverter, err := encryptingConverters(testEncryptionConfig(t, "k1"))
	if err != nil {
		t.Fatalf("encryptingConverters() error = %v", err)
	}

	payloads, err := dataConverter.ToPayloads("TIN 104-555-0001", 42)
	if err != nil {
		t.Fatalf("ToPayloads() error = %v", err)
	}
	for i, p := range payloads.GetPayloads() {
		if got := string(p.GetMetadata()[converter.MetadataEncoding]); got != encodingEncrypted {
			t.Fatalf("payload %d encoding = %q, want %q", i, got, encodingEncrypted)
		}
	}
	var text string
	var number int
	if err := dataConverter.FromPayloads(payloads, &text, &number); err != nil {
		t.Fatalf("FromPayloads() error = %v", err)
	}
	if text != "TIN 104-555-0001" || number != 42 {
		t.Fatalf("FromPayloads() = %q, %d, want %q, %d", text, number, "TIN 104-555-0001", 42)
	}

	failure := failureConverter.ErrorToFailure(errors.New("company TIN 104-555-0001 not found"))
	if strings.Contains(failure.GetMessage(), "104-555-0001") || strings.Contains(failure.GetStackTrace(), "104-555-0001") {
		t.Fatalf("failure carries its message in clear: %q", failure.GetMessage())
	}
	if got := string(failure.GetEncodedAttributes().GetMetadata()[converter.MetadataEncoding]); got != encodingEncrypted {
		t.Fatalf("failure attributes encoding = %q, want %q", got, encodingEncrypted)
	}
	if err := failureConverter.FailureToError(failure); !strings.Contains(err.Error(), "company TIN 104-555-0001 not found") {
		t.Fatalf("FailureToError() = %q, want the original message", err)
	}
}

func echoActivity(_ context.Context, in string) (string, error) {
	return "echo: " + in, nil
}

func echoWorkflow(ctx workflow.Context, in string) (string, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second})
	var out string
	err := workflow.ExecuteActivity(ctx, echoActivity, in).Get(ctx, &out)
	return out, err
}

func TestEncryptingDataConverterRunsAWorkflow(t *testing.T) {
	dataConverter, _, err := encryptingConverters(testEncryptionConfig(t, "k1"))
	if err != nil {
		t.Fatalf("encryptingConverters() error = %v", err)
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetDataConverter(dataConverter)
	env.RegisterActivity(echoActivity)
	env.RegisterWorkflow(echoWorkflow)

	env.ExecuteWorkflow(echoWorkflow, "consignment 42")
	if !env.IsWorkflowCompleted() {
		t.Fatalf("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error = %v", err)
	}
	var out string
	if err := env.GetWorkflowResult(&out); err != nil {
		t.Fatalf("GetWorkflowResult() error = %v", err)
	}
	if out != "echo: consignment 42" {
		t.Fatalf("workflow result = %q, want %q", out, "echo: consignment 42")
	}
}
