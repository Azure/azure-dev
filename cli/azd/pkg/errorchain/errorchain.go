// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package errorchain provides dependency-free helpers for inspecting Go error
// chains. The package is public so extensions can preserve safe error-type
// evidence across the azd gRPC boundary without depending on internal APIs.
package errorchain

import (
	"reflect"
	"strings"
)

// MaxChainLen caps the number of types collected from a single error chain.
// The cap bounds telemetry cardinality and protects traversal of pathological
// errors that unwrap to themselves.
const MaxChainLen = 16

const maxTypeNameLen = 256

// genericWrappers names error types that preserve an error chain or attach UX
// metadata but do not identify the error's origin.
var genericWrappers = map[string]bool{
	// Standard library wrappers.
	"*errors.errorString": true,
	"*fmt.wrapError":      true,
	"*fmt.wrapErrors":     true,
	"*errors.joinError":   true,

	// azd UX wrappers.
	"*errorhandler.ErrorWithSuggestion": true,
	"*internal.ErrorWithTraceId":        true,

	// Extension invocation wrapper. It adds producer metadata while preserving
	// the inner error for classification.
	"*extensions.InvocationError": true,
}

// Types returns the wrapped-error type chain (outermost first) as a slice of
// Go type names. The traversal follows Unwrap() error linearly and walks
// Unwrap() []error depth-first in slice order. Nil children are skipped and
// the result is capped at MaxChainLen.
//
// Returns nil for a nil error.
func Types(err error) []string {
	if err == nil {
		return nil
	}

	out := make([]string, 0, 4)
	walk(err, &out, make(map[error]struct{}))
	return out
}

func walk(err error, out *[]string, active map[error]struct{}) {
	if err == nil || len(*out) >= MaxChainLen {
		return
	}

	if isComparableError(err) {
		if _, seen := active[err]; seen {
			return
		}
		active[err] = struct{}{}
		defer delete(active, err)
	}

	*out = append(*out, reflect.TypeOf(err).String())

	//nolint:errorlint // Type switch is intentionally used to check for Unwrap methods.
	switch x := err.(type) {
	case interface{ Unwrap() error }:
		walk(x.Unwrap(), out, active)
	case interface{ Unwrap() []error }:
		for _, child := range x.Unwrap() {
			if len(*out) >= MaxChainLen {
				return
			}
			walk(child, out, active)
		}
	}
}

func isComparableError(err error) bool {
	if err == nil {
		return false
	}

	errType := reflect.TypeOf(err)
	return errType.Comparable()
}

// CauseTypes returns a bounded, de-duplicated list of useful error types for
// transport across an extension boundary. Generic wrappers are omitted and
// type names are limited to the format produced by reflect.Type.String.
func CauseTypes(err error) []string {
	return NormalizeCauseTypes(Types(err))
}

// NormalizeCauseTypes validates and de-duplicates type names received from an
// extension. It returns a fresh slice and never includes generic wrappers.
func NormalizeCauseTypes(types []string) []string {
	if len(types) == 0 {
		return nil
	}

	out := make([]string, 0, min(len(types), MaxChainLen))
	seen := make(map[string]struct{}, len(types))
	for _, typeName := range types {
		if len(out) >= MaxChainLen || !isSafeTypeName(typeName) || IsGenericWrapper(typeName) {
			continue
		}
		if _, ok := seen[typeName]; ok {
			continue
		}
		seen[typeName] = struct{}{}
		out = append(out, typeName)
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// DeepestNamedType returns the deepest non-generic type name from the chain,
// skipping wrappers that do not identify an origin. It falls back to the leaf
// type when every entry is generic and returns "<nil>" for a nil error.
func DeepestNamedType(err error) string {
	if err == nil {
		return "<nil>"
	}

	var deepest string
	var leafType string
	count := 0
	active := make(map[error]struct{})
	visit := func(e error) bool {
		if e == nil || count >= MaxChainLen {
			return false
		}
		if isComparableError(e) {
			if _, seen := active[e]; seen {
				return false
			}
			active[e] = struct{}{}
		}
		count++
		typeName := reflect.TypeOf(e).String()
		leafType = typeName
		if !genericWrappers[typeName] {
			deepest = typeName
		}
		return true
	}

	for err != nil {
		if !visit(err) {
			break
		}
		//nolint:errorlint // Type switch is intentionally used to check for Unwrap methods.
		switch x := err.(type) {
		case interface{ Unwrap() error }:
			err = x.Unwrap()
		case interface{ Unwrap() []error }:
			// For joined errors, use the first non-nil branch so the fallback
			// remains deterministic. Types provides complete branch coverage.
			err = nil
			for _, child := range x.Unwrap() {
				if child != nil {
					err = child
					break
				}
			}
		default:
			err = nil
		}
	}

	if deepest != "" {
		return deepest
	}
	return leafType
}

// DeepestNamedTypeFromTypes returns the deepest useful type from a previously
// captured cause-type list. It returns "<nil>" when no type is available.
func DeepestNamedTypeFromTypes(types []string) string {
	normalized := NormalizeCauseTypes(types)
	if len(normalized) == 0 {
		return "<nil>"
	}
	return normalized[len(normalized)-1]
}

// IsGenericWrapper reports whether typeName is a generic chain or UX wrapper.
func IsGenericWrapper(typeName string) bool {
	return genericWrappers[typeName]
}

// SanitizeTypeName converts a Go type name into a telemetry-safe segment.
func SanitizeTypeName(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, ".", "_"), "*", "")
}

func isSafeTypeName(name string) bool {
	if name == "" || len(name) > maxTypeNameLen {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '*',
			r == '.',
			r == '_',
			r == '[',
			r == ']',
			r == ',':
			continue
		default:
			return false
		}
	}
	return true
}
