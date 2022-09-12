package ux

// Context data that is persisted between steps within an action
type StepContext interface {
	// Sets a value for the specified key
	SetValue(key string, value any)
	// Gets a value for the specified key
	// Boolean value returns whether or not the item exists in the context
	GetValue(key string) (any, bool)
}

// Creates a new StepContext
func NewStepContext() StepContext {
	return &stepContext{
		values: map[string]any{},
	}
}

type stepContext struct {
	values map[string]any
}

func (sc *stepContext) SetValue(key string, value any) {
	sc.values[key] = value
}

func (sc *stepContext) GetValue(key string) (value any, ok bool) {
	value, ok = sc.values[key]
	return
}
