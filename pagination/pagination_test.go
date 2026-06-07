package pagination

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewPageResult(t *testing.T) {
	page := NewPageResult[string](nil, 12, 5, 25)

	if page.Items == nil {
		t.Fatalf("expected Items to be an empty slice, got nil")
	}
	if len(page.Items) != 0 {
		t.Fatalf("expected empty Items slice, got %d items", len(page.Items))
	}
	if page.Total != 12 || page.Offset != 5 || page.Limit != 25 {
		t.Fatalf("unexpected page metadata: %+v", page)
	}

	items := []string{"a", "b"}
	page = NewPageResult(items, 2, 0, 50)
	if len(page.Items) != 2 || page.Items[0] != "a" || page.Items[1] != "b" {
		t.Fatalf("unexpected page items: %+v", page.Items)
	}
}

func TestResolvePaginationParams(t *testing.T) {
	limit := 25
	offset := 10
	negativeOffset := -1
	zeroLimit := 0
	overLimit := 500

	tests := []struct {
		name       string
		offset     *int
		limit      *int
		wantOffset int
		wantLimit  int
	}{
		{name: "defaults", wantOffset: 0, wantLimit: pageSizeDefault},
		{name: "valid values", offset: &offset, limit: &limit, wantOffset: 10, wantLimit: 25},
		{name: "negative offset ignored", offset: &negativeOffset, limit: &limit, wantOffset: 0, wantLimit: 25},
		{name: "zero limit ignored", offset: &offset, limit: &zeroLimit, wantOffset: 10, wantLimit: pageSizeDefault},
		{name: "limit capped", offset: &offset, limit: &overLimit, wantOffset: 10, wantLimit: pageSizeMax},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOffset, gotLimit := ResolvePaginationParams(tt.offset, tt.limit)
			if gotOffset != tt.wantOffset || gotLimit != tt.wantLimit {
				t.Fatalf("ResolvePaginationParams() = (%d, %d), want (%d, %d)", gotOffset, gotLimit, tt.wantOffset, tt.wantLimit)
			}
		})
	}
}

func TestParsePaginationParams(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantErr    bool
		wantOffset *int
		wantLimit  *int
	}{
		{name: "no params", query: "/items"},
		{name: "valid params", query: "/items?offset=5&limit=25", wantOffset: intPtr(5), wantLimit: intPtr(25)},
		{name: "invalid offset", query: "/items?offset=abc", wantErr: true},
		{name: "negative offset", query: "/items?offset=-1", wantErr: true},
		{name: "invalid limit", query: "/items?limit=abc", wantErr: true},
		{name: "too small limit", query: "/items?limit=0", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.query, nil)
			gotOffset, gotLimit, err := ParsePaginationParams(req)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParsePaginationParams() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePaginationParams() unexpected error = %v", err)
			}
			if !intPtrEq(gotOffset, tt.wantOffset) || !intPtrEq(gotLimit, tt.wantLimit) {
				t.Fatalf("ParsePaginationParams() = (%v, %v), want (%v, %v)", gotOffset, gotLimit, tt.wantOffset, tt.wantLimit)
			}
		})
	}
}

func intPtr(value int) *int {
	return &value
}

func intPtrEq(got, want *int) bool {
	switch {
	case got == nil && want == nil:
		return true
	case got == nil || want == nil:
		return false
	default:
		return *got == *want
	}
}
