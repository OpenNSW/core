// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package drivers

import "testing"

func TestValidDisposition(t *testing.T) {
	tests := []struct {
		disposition string
		want        bool
	}{
		{DispositionInline, true},
		{DispositionAttachment, true},
		{"inline", true},
		{"attachment", true},
		{"", false},
		{"Inline", false},
		{"attachment; filename=x.pdf", false},
		{"form-data", false},
	}
	for _, tt := range tests {
		t.Run(tt.disposition, func(t *testing.T) {
			if got := ValidDisposition(tt.disposition); got != tt.want {
				t.Errorf("ValidDisposition(%q) = %v, want %v", tt.disposition, got, tt.want)
			}
		})
	}
}
