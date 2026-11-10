// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"
	"sync"
)

// providerRegistry stores named providers while their registrations are advertised to core.
type providerRegistry[T any] struct {
	mu           sync.RWMutex // protects providers, keyed by provider name
	providers    map[string]T
	providerType string // identifies the provider capability in errors
}

func newProviderRegistry[T any](providerType string) *providerRegistry[T] {
	return &providerRegistry[T]{
		providers:    make(map[string]T),
		providerType: providerType,
	}
}

// Register reserves a provider name and rolls back if advertising it fails.
func (r *providerRegistry[T]) Register(
	ctx context.Context,
	name string,
	provider T,
	advertise func(context.Context, string) error,
) error {
	r.mu.Lock()
	if _, exists := r.providers[name]; exists {
		r.mu.Unlock()
		return fmt.Errorf("%s provider %q already registered", r.providerType, name)
	}
	r.providers[name] = provider
	r.mu.Unlock()

	// Incoming requests may need the provider before the registration acknowledgment arrives.
	if err := advertise(ctx, name); err != nil {
		r.mu.Lock()
		delete(r.providers, name)
		r.mu.Unlock()
		return err
	}

	return nil
}

// Lookup returns the named provider and whether it is registered.
func (r *providerRegistry[T]) Lookup(name string) (T, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, exists := r.providers[name]
	return provider, exists
}
