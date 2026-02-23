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
// findErrorByTypeName walks the error chain (including multi-unwrap trees)
// and returns the first error whose struct type name matches the given name
// AND whose properties match (if any are specified).
func findErrorByTypeName(
	err error,
	typeName string,
	properties map[string]string,
	matcher *PatternMatcher,
	useRegex bool,
) (any, bool) {
	// Use a stack for depth-first traversal to support multi-unwrap error trees
	stack := []error{err}

	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if current == nil {
			continue
		}

		t := reflect.TypeOf(current)
		if t.Kind() == reflect.Ptr {
			t = t.Elem()
		}
		if t.Name() == typeName {
			// Type matches — check properties if specified
			if len(properties) == 0 || matchProperties(current, properties, matcher, useRegex) {
				return current, true
			}
			// Type matched but properties didn't — keep searching
		}

		// Multi-unwrap (Go 1.20+): Unwrap() []error
		if mu, ok := current.(interface{ Unwrap() []error }); ok {
			stack = append(stack, mu.Unwrap()...)
			continue
		}

		// Single unwrap: Unwrap() error
		if su, ok := current.(interface{ Unwrap() error }); ok {
			stack = append(stack, su.Unwrap())
		}
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
