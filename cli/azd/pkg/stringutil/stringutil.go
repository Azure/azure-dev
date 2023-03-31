package stringutil

import "strings"

// IsNilOrEmpty returns true if the string pointer is nil or the trimmed string is empty.
func IsNilOrEmpty(value *string) bool {
	return value == nil || strings.TrimSpace(*value) == ""
}

// PtrValueEquals returns true if the string pointer is not nil and the string value is equal to the expected value.
func PtrValueEquals(actual *string, expected string) bool {
	return actual != nil && *actual == expected
}
