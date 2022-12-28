// Package azderr provides application-wide error handling for azd.
package azderr

type Error struct {
	// Operation that resulted in the error.
	Op string
	// Error code of the operation
	Code string

	// Human-readable message of the error
	Message string

	// Nested error
	Err error
}
