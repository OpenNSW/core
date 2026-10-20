// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package callbacktoken

import (
	"errors"
	"strings"
	"testing"
)

const (
	task = "5f0c9b1e-3d2a-4c6b-8e7f-0a1b2c3d4e5f"
	step = "9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d"
)

func TestRoundTrip(t *testing.T) {
	token, err := Encode(task, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != Length || token[0] != Version {
		t.Fatalf("token %q: length %d, first %q", token, len(token), token[0])
	}
	gotTask, gotStep, err := Decode(token)
	if err != nil {
		t.Fatal(err)
	}
	if gotTask != task || gotStep != step {
		t.Errorf("Decode = (%s, %s), want (%s, %s)", gotTask, gotStep, task, step)
	}
}

// Upper-case UUIDs are the same UUIDs; the token and the decoded IDs are canonical.
func TestEncodeIsCaseInsensitiveOnInput(t *testing.T) {
	a, _ := Encode(task, step)
	b, err := Encode(strings.ToUpper(task), strings.ToUpper(step))
	if err != nil || a != b {
		t.Errorf("Encode of upper-case IDs = %q, %v; want %q", b, err, a)
	}
}

func TestTokenIsURLSafeAndDistinguishesSteps(t *testing.T) {
	a, _ := Encode(task, step)
	b, _ := Encode(task, "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d")
	if a == b {
		t.Error("different steps of one task must have different tokens")
	}
	if strings.ContainsAny(a, "+/=") {
		t.Errorf("token %q is not URL-safe and unpadded", a)
	}
}

func TestEncodeRejectsNonUUIDs(t *testing.T) {
	if _, err := Encode("task-1", step); err == nil {
		t.Error("expected an error for a task ID that is not a UUID")
	}
	if _, err := Encode(task, "step-1"); err == nil {
		t.Error("expected an error for a step ID that is not a UUID")
	}
}

func TestDecodeRejectsMalformedTokens(t *testing.T) {
	good, _ := Encode(task, step)
	cases := map[string]string{
		"empty":           "",
		"too short":       good[:Length-1],
		"too long":        good + "A",
		"unknown version": "2" + good[1:],
		"not base64url":   "1" + strings.Repeat("!", Length-1),
		"padding":         good[:Length-1] + "=",
		"standard base64": "1" + strings.Repeat("+", Length-1),
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Decode(token); !errors.Is(err, ErrInvalid) {
				t.Errorf("Decode(%q) error = %v, want ErrInvalid", token, err)
			}
		})
	}
}
