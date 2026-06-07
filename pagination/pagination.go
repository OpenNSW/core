package pagination

import (
	"fmt"
	"net/http"
	"strconv"
)

const pageSizeDefault = 50
const pageSizeMax = 100

// Page is the standard pagination envelope returned by all list endpoints.
type Page[T any] struct {
	Items  []T   `json:"items"`
	Total  int64 `json:"total"`
	Offset int   `json:"offset"`
	Limit  int   `json:"limit"`
}

// NewPageResult constructs a Page from items and pagination metadata, ensuring Items is never nil.
func NewPageResult[T any](items []T, total int64, offset, limit int) Page[T] {
	if items == nil {
		items = []T{}
	}
	return Page[T]{Items: items, Total: total, Offset: offset, Limit: limit}
}

// ResolvePaginationParams calculates the offset and limit for pagination based on the provided values.
// If offset or limit are nil, default values are used. The limit is capped at a maximum value.
func ResolvePaginationParams(offset *int, limit *int) (int, int) {
	finalOffset := 0
	finalLimit := pageSizeDefault

	if offset != nil && *offset >= 0 {
		finalOffset = *offset
	}

	if limit != nil && *limit > 0 {
		finalLimit = min(*limit, pageSizeMax)
	}

	return finalOffset, finalLimit
}

// ParsePaginationParams extracts offset and limit from query parameters.
func ParsePaginationParams(r *http.Request) (*int, *int, error) {
	var offset, limit *int

	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		offsetVal, err := strconv.Atoi(offsetStr)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid 'offset' query parameter, must be an integer")
		}
		if offsetVal < 0 {
			return nil, nil, fmt.Errorf("invalid 'offset' query parameter, must be a non-negative integer")
		}
		offset = &offsetVal
	}

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		limitVal, err := strconv.Atoi(limitStr)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid 'limit' query parameter, must be an integer")
		}
		if limitVal < 1 {
			return nil, nil, fmt.Errorf("invalid 'limit' query parameter, must be at least 1")
		}
		limit = &limitVal
	}

	return offset, limit, nil
}
