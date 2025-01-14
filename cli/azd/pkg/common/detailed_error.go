package common

import "fmt"

type DetailedError struct {
	description string
	err         error
}

func (e *DetailedError) Error() string {
	return fmt.Sprintf("%s\n\nDetails:\n%s", e.description, e.err.Error())
}

func (e *DetailedError) Unwrap() error {
	return e.err
}

func (e *DetailedError) Description() string {
	return e.description
}

// Factory function to create a new DetailedError
func NewDetailedError(description string, err error) *DetailedError {
	return &DetailedError{
		description: description,
		err:         err,
	}
}
