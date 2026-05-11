// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package errchain provides low-level, dependency-free helpers for
// inspecting Go error chains. It is intentionally separate from the
// classifier in internal/cmd so telemetry sub-spans in low-level
// packages can emit chain context without pulling in the full typed
// classifier graph.
package errchain

import (
	"reflect"
	"strings"
)

// MaxChainLen caps the number of types collected from a single error
// chain. The cap is global to bound telemetry cardinality and to
// stop pathological errors that unwrap to themselves.
const MaxChainLen = 16

// genericWrappers names Go types that preserve an error chain or attach UX metadata but tell engineers nothing
// about origin. Adding a type here makes fallback classification skip it, which can move spans from a
// type-specific ResultCode to internal.unclassified when no deeper non-generic type exists.
var genericWrappers = map[string]bool{
	// Standard library wrappers
	"*errors.errorString": true,
	"*fmt.wrapError":      true,
	"*fmt.wrapErrors":     true,
	"*errors.joinError":   true,

	// azd UX wrappers — these attach a suggestion or trace ID for
	// presentation only; the inner is the meaningful classification.
	"*errorhandler.ErrorWithSuggestion": true,
	"*internal.ErrorWithTraceId":        true,
}

// Types returns the wrapped-error type chain (outermost first) as a
// slice of Go type names (e.g. "*fmt.wrapError",
// "*azcore.ResponseError"). The traversal:
//   - follows Unwrap() error linearly,
//   - depth-first walks Unwrap() []error in slice order,
//   - skips nil children,
//   - is globally capped at MaxChainLen.
//
// Returns nil for a nil error.
func Types(err error) []string {
	if err == nil {
		return nil
	}
	out := make([]string, 0, 4)
	walk(err, &out)
	return out
}

func walk(err error, out *[]string) {
	if err == nil || len(*out) >= MaxChainLen {
		return
	}

	*out = append(*out, reflect.TypeOf(err).String())

	//nolint:errorlint // Type switch is intentionally used to check for Unwrap() methods.
	switch x := err.(type) {
	case interface{ Unwrap() error }:
		walk(x.Unwrap(), out)
	case interface{ Unwrap() []error }:
		for _, child := range x.Unwrap() {
			if len(*out) >= MaxChainLen {
				return
			}
			walk(child, out)
		}
	}
}

// DeepestNamedType returns the deepest non-generic type name from the
// chain, skipping the wrappers in genericWrappers so a fmt.Errorf or
// suggestion wrap doesn't mask the real error. Falls back to the leaf
// type when nothing in the chain is non-generic. Returns "<nil>" for
// a nil error.
func DeepestNamedType(err error) string {
	if err == nil {
		return "<nil>"
	}

	var deepest string  // last non-generic type observed
	var leafType string // last type observed regardless

	count := 0
	visit := func(e error) bool {
		if e == nil || count >= MaxChainLen {
			return false
		}
		count++
		t := reflect.TypeOf(e).String()
		leafType = t
		if !genericWrappers[t] {
			deepest = t
		}
		return true
	}

	for err != nil {
		if !visit(err) {
			break
		}
		//nolint:errorlint // Type switch is intentionally used to check for Unwrap() methods.
		switch x := err.(type) {
		case interface{ Unwrap() error }:
			err = x.Unwrap()
		case interface{ Unwrap() []error }:
			// For joined errors, classify only the first non-nil
			// child. Picking a single branch keeps "deepest named"
			// well-defined; full chain coverage is in Types().
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

// IsGenericWrapper reports whether the given Go type name (as
// produced by reflect.TypeOf(err).String()) is a generic chain
// wrapper. Exposed so the typed classifier in internal/cmd can apply
// the same logic when picking a fallback ResultCode.
func IsGenericWrapper(typeName string) bool {
	return genericWrappers[typeName]
}

// SanitizeTypeName converts a Go type name (e.g. "*azcore.ResponseError") into a telemetry-safe segment
// ("azcore_ResponseError").
func SanitizeTypeName(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, ".", "_"), "*", "")
}
