package convert

// Converts a pointer to a value type
// If the ptr is nil returns default value, otherwise the value of value of the pointer
func ToValueWithDefault[T any](ptr *T, defaultValue T) T {
	if ptr == nil {
		return defaultValue
	}

	if str, ok := any(ptr).(*string); ok && *str == "" {
		return defaultValue
	}

	return *ptr
}
