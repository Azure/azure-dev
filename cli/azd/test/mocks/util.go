package mocks

// Returns a pointer for the specified value
func RefOf[T any](value T) *T {
	return &value
}
