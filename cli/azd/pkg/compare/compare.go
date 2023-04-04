package compare

import "strings"

// IsStringNilOrEmpty returns true if the string pointer is nil or the trimmed string is empty.
func IsStringNilOrEmpty(value *string) bool {
	return value == nil || strings.TrimSpace(*value) == ""
}

// PtrValueEquals returns true if the pointer is not nil and the value is equal to the expected value.
func PtrValueEquals[T comparable](actual *T, expected T) bool {
	return actual != nil && *actual == expected
}
