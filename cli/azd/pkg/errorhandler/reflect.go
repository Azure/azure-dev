// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

import (
	"fmt"
	"reflect"
	"strings"
)

// findErrorByTypeName walks the error chain and returns the first error
// whose struct type name matches the given name. Uses errors.As-like
// unwrapping semantics to traverse the error chain.
func findErrorByTypeName(err error, typeName string) (any, bool) {
	for err != nil {
		t := reflect.TypeOf(err)
		// Dereference pointer to get the struct type name
		if t.Kind() == reflect.Ptr {
			t = t.Elem()
		}
		if t.Name() == typeName {
			return err, true
		}

		// Unwrap the error chain
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return nil, false
		}
		err = unwrapper.Unwrap()
	}
	return nil, false
}

// matchProperties checks if all property paths resolve to the expected values
// on the given target object using reflection (AND logic).
//
// When useRegex is false, property values are matched as case-insensitive substrings.
// When useRegex is true, property values are treated as regular expressions.
func matchProperties(target any, properties map[string]string, matcher *PatternMatcher, useRegex bool) bool {
	for path, expected := range properties {
		actual, ok := resolvePropertyPath(target, path)
		if !ok {
			return false
		}
		if !matcher.MatchSingle(actual, expected, useRegex) {
			return false
		}
	}
	return true
}

// resolvePropertyPath resolves a dot-separated property path on the target
// using reflection. Returns the string representation of the value.
//
// Examples:
//
//	"Code" → target.Code
//	"Details.Code" → target.Details.Code
func resolvePropertyPath(target any, path string) (string, bool) {
	parts := strings.Split(path, ".")
	v := reflect.ValueOf(target)

	for _, part := range parts {
		// Dereference pointers
		for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
			if v.IsNil() {
				return "", false
			}
			v = v.Elem()
		}

		if v.Kind() != reflect.Struct {
			return "", false
		}

		v = v.FieldByName(part)
		if !v.IsValid() {
			return "", false
		}
	}

	// Dereference final pointer
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}

	return fmt.Sprint(v.Interface()), true
}
