package slice

// Finds a value within a slice
func Find[T any](slice []T, predicate func(value T) bool) *T {
	for _, value := range slice {
		if predicate(value) {
			return &value
		}
	}

	return nil
}
