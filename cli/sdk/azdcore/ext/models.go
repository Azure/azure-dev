package ext

type ErrorWithSuggestion struct {
	Err        error
	Suggestion string
}

func (e *ErrorWithSuggestion) Error() string {
	return e.Err.Error()
}

func (e *ErrorWithSuggestion) Unwrap() error {
	return e.Err
}
