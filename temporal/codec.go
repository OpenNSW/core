// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package temporal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	sdktemporal "go.temporal.io/sdk/temporal"
	"google.golang.org/protobuf/proto"
)

const (
	encodingEncrypted = "binary/encrypted"
	metadataKeyID     = "encryption-key-id"
)

// encryptingConverters returns a data converter that encrypts every payload
// with the configured key, and a failure converter that moves failure messages
// and stack traces into encrypted payloads too.
func encryptingConverters(c EncryptionConfig) (converter.DataConverter, converter.FailureConverter, error) {
	codec, err := newEncryptionCodec(c)
	if err != nil {
		return nil, nil, err
	}
	dataConverter := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), codec)
	failureConverter := sdktemporal.NewDefaultFailureConverter(sdktemporal.DefaultFailureConverterOptions{
		DataConverter:          dataConverter,
		EncodeCommonAttributes: true,
	})
	return dataConverter, failureConverter, nil
}

// encryptionCodec encrypts each payload, metadata included, with AES-256-GCM.
// The key ID is authenticated with the ciphertext, so a payload can't be moved
// to another key's name. Decode refuses any payload this codec didn't encrypt.
type encryptionCodec struct {
	keyID string
	aead  cipher.AEAD
}

func newEncryptionCodec(c EncryptionConfig) (*encryptionCodec, error) {
	key, err := c.decodeKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("temporal encryption: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("temporal encryption: %w", err)
	}
	return &encryptionCodec{keyID: c.KeyID, aead: aead}, nil
}

// Encode implements converter.PayloadCodec.
func (c *encryptionCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		plaintext, err := proto.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("temporal encryption: marshal payload: %w", err)
		}
		nonce := make([]byte, c.aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return nil, fmt.Errorf("temporal encryption: generate nonce: %w", err)
		}
		result[i] = &commonpb.Payload{
			Metadata: map[string][]byte{
				converter.MetadataEncoding: []byte(encodingEncrypted),
				metadataKeyID:              []byte(c.keyID),
			},
			Data: c.aead.Seal(nonce, nonce, plaintext, []byte(c.keyID)),
		}
	}
	return result, nil
}

// Decode implements converter.PayloadCodec.
func (c *encryptionCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		metadata := p.GetMetadata()
		if string(metadata[converter.MetadataEncoding]) != encodingEncrypted {
			return nil, errors.New("temporal encryption: payload is not encrypted")
		}
		if keyID := string(metadata[metadataKeyID]); keyID != c.keyID {
			return nil, fmt.Errorf("temporal encryption: payload is encrypted with key %q, not %q", keyID, c.keyID)
		}
		data := p.GetData()
		nonceSize := c.aead.NonceSize()
		if len(data) < nonceSize {
			return nil, errors.New("temporal encryption: payload is too short")
		}
		plaintext, err := c.aead.Open(nil, data[:nonceSize], data[nonceSize:], []byte(c.keyID))
		if err != nil {
			return nil, errors.New("temporal encryption: payload failed authentication")
		}
		decoded := &commonpb.Payload{}
		if err := proto.Unmarshal(plaintext, decoded); err != nil {
			return nil, fmt.Errorf("temporal encryption: unmarshal payload: %w", err)
		}
		result[i] = decoded
	}
	return result, nil
}
