// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mapper

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

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

// ErrConversionFailure is a sentinel error for use with errors.Is() to check for any conversion failure
var ErrConversionFailure = &ConversionError{}

// ConversionError is returned when a mapper function fails during conversion.
// It wraps the original error and provides context about which types were being converted.
// Callers can check for this error in multiple ways:
//
// 1. Using errors.As() for detailed inspection:
//
//	var convErr *ConversionError
//	if errors.As(err, &convErr) {
//		log.Printf("Conversion failed from %v to %v: %v", convErr.SrcType, convErr.DstType, convErr.Err)
//	}
//
// 2. Using the provided helper:
//
//	if IsConversionError(err) {
//		// Handle conversion failure case
//	}
//
// 3. Using errors.Is() for type checking or wrapped error matching:
//
//	if errors.Is(err, ErrConversionFailure) {
//		// Handle any ConversionError using sentinel
//	}
//	if errors.Is(err, &ConversionError{}) {
//		// Handle any ConversionError using type
//	}
//	if errors.Is(err, specificErr) {
//		// Handle when ConversionError wraps specificErr
//	}
//
// 4. Using errors.Unwrap() to access the original error:
//
//	if innerErr := errors.Unwrap(err); innerErr != nil {
//		// Work with the original error
//	}
type ConversionError struct {
	SrcType reflect.Type
	DstType reflect.Type
	Err     error
}

func (e *NoMapperError) Error() string {
	srcName := cleanTypeName(e.SrcType)
	dstName := cleanTypeName(e.DstType)
	return fmt.Sprintf("no mapper registered from %s to %s", srcName, dstName)
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

// cleanTypeName returns a user-friendly type name with package prefixes for clarity
func cleanTypeName(t reflect.Type) string {
	if t == nil {
		return "<nil>"
	}

	switch t.Kind() {
	case reflect.Ptr:
		// For pointers, get the element type and add pointer indicator
		elem := t.Elem()
		return "*" + cleanTypeName(elem)
	case reflect.Slice:
		// For slices, show element type
		elem := t.Elem()
		return "[]" + cleanTypeName(elem)
	case reflect.Array:
		// For arrays, show size and element type
		elem := t.Elem()
		return fmt.Sprintf("[%d]%s", t.Len(), cleanTypeName(elem))
	case reflect.Map:
		// For maps, show key and value types
		key := t.Key()
		elem := t.Elem()
		return fmt.Sprintf("map[%s]%s", cleanTypeName(key), cleanTypeName(elem))
	case reflect.Chan:
		// For channels, show direction and element type
		elem := t.Elem()
		switch t.ChanDir() {
		case reflect.RecvDir:
			return "<-chan " + cleanTypeName(elem)
		case reflect.SendDir:
			return "chan<- " + cleanTypeName(elem)
		default:
			return "chan " + cleanTypeName(elem)
		}
	case reflect.Func:
		// For functions, show a simplified signature
		return "func"
	default:
		// For basic types and structs, show package.TypeName for clarity
		name := t.String()
		// Find the last slash to get the package name
		if lastSlash := strings.LastIndex(name, "/"); lastSlash >= 0 {
			// Get everything after the last slash (package.TypeName)
			return name[lastSlash+1:]
		}
		// If no slash, check for just package.TypeName format
		if strings.Contains(name, ".") {
			return name
		}
		// Built-in types like string, int, etc.
		return name
	}
}

func (e *ConversionError) Error() string {
	srcName := cleanTypeName(e.SrcType)
	dstName := cleanTypeName(e.DstType)
	return fmt.Sprintf("conversion failed from %s to %s: %v", srcName, dstName, e.Err)
}

// Unwrap returns the wrapped error for errors.Unwrap() support
func (e *ConversionError) Unwrap() error {
	return e.Err
}

// Is implements error equality for errors.Is() support.
// It returns true if the target error is also a ConversionError, or if the wrapped error matches.
func (e *ConversionError) Is(target error) bool {
	if _, ok := target.(*ConversionError); ok {
		return true
	}
	return errors.Is(e.Err, target)
}

// IsConversionError returns true if the error is a ConversionError
func IsConversionError(err error) bool {
	var convErr *ConversionError
	return err != nil && errors.As(err, &convErr)
}
