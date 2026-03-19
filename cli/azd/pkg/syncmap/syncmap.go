// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package syncmap

import "sync"

// Map is a type-safe wrapper around sync.Map that eliminates
// type assertions at call sites.
//
// A Map must not be copied after first use.
type Map[K comparable, V any] struct {
	noCopy noCopy //nolint:unused
	m      sync.Map
}

// Load returns the stored value for key.
func (s *Map[K, V]) Load(key K) (V, bool) {
	v, ok := s.m.Load(key)
	if !ok {
		var zero V
		return zero, false
	}

	return v.(V), true
}

// Store saves value under key.
func (s *Map[K, V]) Store(key K, value V) {
	s.m.Store(key, value)
}

// LoadOrStore returns the existing value for the key if present.
func (s *Map[K, V]) LoadOrStore(key K, value V) (V, bool) {
	actual, loaded := s.m.LoadOrStore(key, value)
	return actual.(V), loaded
}

// LoadAndDelete deletes the value for key and returns it if
// present.
func (s *Map[K, V]) LoadAndDelete(key K) (V, bool) {
	v, loaded := s.m.LoadAndDelete(key)
	if !loaded {
		var zero V
		return zero, false
	}

	return v.(V), true
}

// Delete removes the value for key.
func (s *Map[K, V]) Delete(key K) {
	s.m.Delete(key)
}

// Range calls f sequentially for each key and value present in
// the map.
func (s *Map[K, V]) Range(f func(key K, value V) bool) {
	s.m.Range(func(k, v any) bool {
		return f(k.(K), v.(V))
	})
}

// noCopy may be added to structs which must not be copied after
// first use. See https://golang.org/issues/8005#issuecomment-190753527
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}
