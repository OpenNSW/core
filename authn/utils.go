// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package authn

import (
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"math/big"
)

func parseRSAPublicKey(key jwk) (*rsa.PublicKey, error) {
	if key.Kty != "RSA" {
		return nil, fmt.Errorf("unsupported jwk key type: %s", key.Kty)
	}

	modulusBytes, err := base64.RawURLEncoding.DecodeString(key.N)
	if err != nil {
		return nil, fmt.Errorf("failed to decode jwk modulus: %w", err)
	}
	exponentBytes, err := base64.RawURLEncoding.DecodeString(key.E)
	if err != nil {
		return nil, fmt.Errorf("failed to decode jwk exponent: %w", err)
	}

	if len(modulusBytes) == 0 || len(exponentBytes) == 0 {
		return nil, fmt.Errorf("invalid jwk key data")
	}

	exponentInt := new(big.Int).SetBytes(exponentBytes).Int64()
	if exponentInt <= 0 {
		return nil, fmt.Errorf("invalid jwk exponent")
	}

	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(modulusBytes),
		E: int(exponentInt),
	}, nil
}
