// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package lazy

import "sync"

type InitializerFn[T comparable] func() (T, error)

// A data structure that will lazily load an instance of the underlying type
// from the specified initializer
type Lazy[T comparable] struct {
	initialized     bool
	initializer     InitializerFn[T]
	value           T
	error           error
	initializerLock sync.Mutex
	valueLock       sync.Mutex
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
	l.valueLock.Lock()
	if l.initialized {
		value, err := l.value, l.error
		l.valueLock.Unlock()
		return value, err
	}
	l.valueLock.Unlock()

	// Only one caller runs the initializer. SetValue remains available while the
	// initializer runs, including when the initializer itself calls SetValue.
	l.initializerLock.Lock()
	defer l.initializerLock.Unlock()

	l.valueLock.Lock()
	if l.initialized {
		value, err := l.value, l.error
		l.valueLock.Unlock()
		return value, err
	}
	l.valueLock.Unlock()

	value, err := l.initializer()

	l.valueLock.Lock()
	defer l.valueLock.Unlock()
	if l.initialized {
		return l.value, l.error
	}
	if err == nil {
		l.setValue(value)
	} else {
		l.error = err
	}
	return l.value, l.error
}

// SetValue sets the resolved value and clears any prior error. It may be called
// while the initializer is running; the explicit value then takes precedence
// over that initializer's result.
func (l *Lazy[T]) SetValue(value T) {
	l.valueLock.Lock()
	defer l.valueLock.Unlock()
	l.setValue(value)
}

func (l *Lazy[T]) setValue(value T) {
	l.value = value
	l.error = nil
	l.initialized = true
}
