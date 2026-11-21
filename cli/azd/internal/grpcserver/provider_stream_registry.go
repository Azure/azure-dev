// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"maps"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// providerStreamRegistry associates provider names with their owning streams.
type providerStreamRegistry[T comparable] struct {
	mu           sync.Mutex   // protects owners and serializes local provider registration
	owners       map[string]T // provider name -> owning stream
	providerType string       // identifies the provider capability in errors
}

func newProviderStreamRegistry[T comparable](providerType string) *providerStreamRegistry[T] {
	return &providerStreamRegistry[T]{
		owners:       make(map[string]T),
		providerType: providerType,
	}
}

// Register associates a name with its owner after local registration succeeds.
func (r *providerStreamRegistry[T]) Register(name string, owner T, register func() error) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.owners[name]; exists {
		return status.Errorf(codes.AlreadyExists, "%s %q is already registered", r.providerType, name)
	}

	// Keep the local registration and ownership update atomic against other streams and cleanup.
	if err := register(); err != nil {
		return status.Errorf(codes.Internal, "registering %s: %v", r.providerType, err)
	}
	r.owners[name] = owner

	return nil
}

// Release removes ownership of every provider registered by the given stream.
func (r *providerStreamRegistry[T]) Release(owner T) {
	r.mu.Lock()
	defer r.mu.Unlock()

	maps.DeleteFunc(r.owners, func(_ string, current T) bool {
		return current == owner
	})
}
