package convert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Converts a pointer to a value type
// If the ptr is nil returns default value, otherwise the value of value of the pointer
func ToValueWithDefault[T any](ptr *T, defaultValue T) T {
	if ptr == nil {
		return defaultValue
	}

	return *ptr
}

// Returns a pointer for the specified value
func RefOf[T any](value T) *T {
	return &value
}

// Creates a JSON serialized HTTP request body
func ToHttpRequestBody(value any) (io.ReadCloser, error) {
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed serializing JSON: %w", err)
	}

	return io.NopCloser(bytes.NewBuffer(jsonBytes)), nil
}
