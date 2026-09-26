// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ioc

import "reflect"

// bindingLifetime describes when a resolver's result is reused.
type bindingLifetime int

const (
	singletonLifetime bindingLifetime = iota // One cached result shared with inheriting scopes.
	transientLifetime                        // Invoke the resolver on every resolution.
	scopedLifetime                           // One cached result per scope.
)

// bindingKey identifies a registration by its declared result type and optional name.
type bindingKey struct {
	// The first return type for the resolverFn, e.g. reflect.TypeFor[io.Writer]().
	serviceType reflect.Type
	// Empty for an unnamed registration, or a label such as "bicep".
	name string
}

// binding describes how to resolve a value; it does not hold the resolved instance.
type binding struct {
	// The original function, e.g. func(*Config) (*Client, error), before any wrapping.
	resolver any
	lifetime bindingLifetime
}
