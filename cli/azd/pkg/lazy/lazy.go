package lazy

import "sync"

type InitializerFn[T comparable] func() (T, error)

// A data structure that will lazily load an instance of the underlying type
// from the specified initializer
type Lazy[T comparable] struct {
	initialized  bool
	initializer  InitializerFn[T]
	value        T
	error        error
	getValueLock sync.Mutex
	setValueLock sync.Mutex
}

// Creates a new Lazy[T]
func NewLazy[T comparable](initializerFn InitializerFn[T]) *Lazy[T] {
	return &Lazy[T]{
		initializer: initializerFn,
	}
}

// From creates a lazy that resolves to the specified value.
func From[T comparable](value T) *Lazy[T] {
	return NewLazy(func() (T, error) { return value, nil })
}

// Gets the value of the configured initializer
// Initializer will only run once on success
func (l *Lazy[T]) GetValue() (T, error) {
	// Only allow a single caller to get a value at one time.
	// Additional calls will block until current call is complete
	l.getValueLock.Lock()
	defer l.getValueLock.Unlock()

	if !l.initialized {
		value, err := l.initializer()
		if err == nil {
			l.SetValue(value)
		} else {
			l.error = err
		}
	}

	return l.value, l.error
}

// Sets a value on the lazy type
func (l *Lazy[T]) SetValue(value T) {
	// Only allow a single caller to get a value at one time.
	// Additional calls will block until current call is complete
	l.setValueLock.Lock()
	defer l.setValueLock.Unlock()

	l.value = value
	l.error = nil
	l.initialized = true
}
