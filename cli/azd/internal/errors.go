package internal

// ErrorWithSuggestion is a custom error type that includes a suggestion for the user
type ErrorWithSuggestion struct {
	Suggestion string
	Err        error
}

// NewErrorWithSuggestion creates a new instance of the ErrorWithSuggestion
func NewErrorWithSuggestion(err error, suggestion string) *ErrorWithSuggestion {
	return &ErrorWithSuggestion{
		Suggestion: suggestion,
		Err:        err,
	}
}

// Error returns the error message
func (es *ErrorWithSuggestion) Error() string {
	return es.Err.Error()
}

// Unwrap returns the wrapped error
func (es *ErrorWithSuggestion) Unwrap() error {
	return es.Err
}

// ErrorWithTraceId is a custom error type that includes a trace ID for the current operation
type ErrorWithTraceId struct {
	TraceId string
	Err     error
}

// NewErrorWithTraceId creates a new instance of the ErrorWithTraceId
func NewErrorWithTraceId(err error, traceId string) *ErrorWithTraceId {
	return &ErrorWithTraceId{
		TraceId: traceId,
		Err:     err,
	}
}

// Error returns the error message
func (et *ErrorWithTraceId) Error() string {
	return et.Err.Error()
}

// Unwrap returns the wrapped error
func (et *ErrorWithTraceId) Unwrap() error {
	return et.Err
}
