// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mapper

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

type Resolver func(key string) string

type MapperFunc func(ctx context.Context, src any, dst any) error

// NoMapperError is returned when no mapper is registered for the given types.
// Callers can check for this error in multiple ways:
//
// 1. Using errors.Is() with the sentinel:
//
//	if errors.Is(err, ErrNoMapper) {
//		// Handle missing mapper case
//	}
//
// 2. Using errors.As() for detailed inspection:
//
//	var noMapperErr *NoMapperError
//	if errors.As(err, &noMapperErr) {
//		log.Printf("Missing mapper from %v to %v", noMapperErr.SrcType, noMapperErr.DstType)
//	}
//
// 3. Using the provided helper:
//
//	if IsNoMapperError(err) {
//		// Handle missing mapper case
//	}
type NoMapperError struct {
	SrcType reflect.Type
	DstType reflect.Type
}

// ErrNoMapper is a sentinel error for use with errors.Is()
var ErrNoMapper = &NoMapperError{}

// ErrDuplicateRegistration is returned when trying to register a mapper for types that already have a mapper
var ErrDuplicateRegistration = errors.New("mapper already registered for these types")

// ErrInvalidRegistration is returned when trying to register a nil function
var ErrInvalidRegistration = errors.New("cannot register nil mapper function")

func (e *NoMapperError) Error() string {
	return fmt.Sprintf("no mapper registered from %v to %v", e.SrcType, e.DstType)
}

// Is implements error equality for errors.Is() support.
// It returns true if the target error is also a NoMapperError.
func (e *NoMapperError) Is(target error) bool {
	_, ok := target.(*NoMapperError)
	return ok
}

// IsNoMapperError returns true if the error is a NoMapperError
func IsNoMapperError(err error) bool {
	var noMapperErr *NoMapperError
	return err != nil && errors.As(err, &noMapperErr)
}

type resolverKeyType struct{}

var resolverKey = resolverKeyType{}

var (
	registry = make(map[[2]reflect.Type]MapperFunc)
	mu       sync.RWMutex
)

// Mapper provides a fluent interface for type conversion
type Mapper struct {
	ctx context.Context
}

// Default mapper instance for convenience functions
var defaultMapper = &Mapper{ctx: context.Background()}

// Register a converter between two types.
// Returns an error if fn is nil or if a mapper is already registered for these types.
// Use this when you need to handle registration errors programmatically.
func Register[S, T any](fn func(context.Context, S) (T, error)) error {
	if fn == nil {
		return ErrInvalidRegistration
	}

	key := [2]reflect.Type{
		reflect.TypeOf((*S)(nil)).Elem(),
		reflect.TypeOf((*T)(nil)).Elem(),
	}

	mu.Lock()
	defer mu.Unlock()

	// Check for duplicate registration
	if _, exists := registry[key]; exists {
		return fmt.Errorf("%w: from %v to %v", ErrDuplicateRegistration, key[0], key[1])
	}

	registry[key] = func(ctx context.Context, src any, dst any) error {
		res, err := fn(ctx, src.(S))
		if err != nil {
			return err
		}
		reflect.ValueOf(dst).Elem().Set(reflect.ValueOf(res))
		return nil
	}
	return nil
}

// MustRegister a converter between two types.
// Panics if fn is nil or if a mapper is already registered for these types.
// This is convenient for init() functions where registration errors should halt startup.
func MustRegister[S, T any](fn func(context.Context, S) (T, error)) {
	if err := Register[S, T](fn); err != nil {
		panic(fmt.Sprintf("mapper registration failed: %v", err))
	}
}

// Convert performs type conversion using the default mapper
func Convert(src any, dst any) error {
	return defaultMapper.Convert(src, dst)
}

// WithResolver returns a mapper with the resolver attached.
// If resolver is nil, returns a mapper with default background context.
func WithResolver(resolver Resolver) *Mapper {
	if resolver == nil {
		return &Mapper{ctx: context.Background()}
	}

	ctx := context.WithValue(context.Background(), resolverKey, func(key string) string {
		return resolver(key)
	})
	return &Mapper{ctx: ctx}
}

// Convert performs type conversion using this mapper's context
func (m *Mapper) Convert(src any, dst any) error {
	srcType := reflect.TypeOf(src)
	dstType := reflect.TypeOf(dst).Elem()

	key := [2]reflect.Type{srcType, dstType}

	mu.RLock()
	fn, ok := registry[key]
	mu.RUnlock()
	if !ok {
		return &NoMapperError{
			SrcType: srcType,
			DstType: dstType,
		}
	}

	return fn(m.ctx, src, dst)
}

// GetResolver retrieves the resolver from context, returns nil if not present
func GetResolver(ctx context.Context) Resolver {
	if resolver, ok := ctx.Value(resolverKey).(func(string) string); ok {
		return Resolver(resolver)
	}
	return nil
}

// ResolverFromContext retrieves the resolver from context with a boolean indicating presence
func ResolverFromContext(ctx context.Context) (Resolver, bool) {
	if resolver, ok := ctx.Value(resolverKey).(func(string) string); ok {
		return Resolver(resolver), true
	}
	return nil, false
}

// Clear removes all registered mappers (useful for testing)
func Clear() {
	mu.Lock()
	defer mu.Unlock()
	registry = make(map[[2]reflect.Type]MapperFunc)
}
