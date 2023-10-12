package convert

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

// Attempts to convert the specified value to a string, otherwise returns the default value
func ToStringWithDefault(value any, defaultValue string) string {
	if str, ok := value.(string); ok {
		return str
	}

	return defaultValue
}
