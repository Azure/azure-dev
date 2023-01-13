// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package baggage

import (
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/exp/maps"
)

// An immutable object safe for concurrent use.
type Baggage struct {
	// Use a map to avoid duplicates as OpenTelemetry does not allow for duplicate attributes

	m map[attribute.Key]attribute.Value
}

// NewBaggage creates a properly-constructed Baggage.
func NewBaggage() Baggage {
	return Baggage{m: map[attribute.Key]attribute.Value{}}
}

func (mb Baggage) copy() Baggage {
	copied := Baggage{m: make(map[attribute.Key]attribute.Value, len(mb.m))}
	for k, v := range mb.m {
		copied.m[k] = v
	}

	return copied
}

// Set sets the provided key value pairs, returning a new copy of Baggage.
// For any existing keys, the value is overridden.
func (mb Baggage) Set(keyValue ...attribute.KeyValue) Baggage {
	if len(keyValue) == 0 {
		return mb
	}

	copied := mb.copy()
	for _, kv := range keyValue {
		copied.m[kv.Key] = kv.Value
	}

	return copied
}

// Delete removes the provided key, returning a new copy of Baggage.
// The new Baggage copy is returned that should be used.
func (mb Baggage) Delete(key attribute.Key) Baggage {
	copied := Baggage{m: make(map[attribute.Key]attribute.Value, len(mb.m))}
	for k, v := range mb.m {
		if k != key {
			copied.m[k] = v
		}
	}

	return copied
}

// Lookup returns the value of the given key.
// If the key does not exist, the boolean value returned is false.
func (mb Baggage) Lookup(key attribute.Key) (val attribute.Value, ok bool) {
	val, ok = mb.m[key]
	return
}

// Get returns the value of the given key.
// If the key does not exist, the default value is returned.
func (mb Baggage) Get(key attribute.Key) attribute.Value {
	return mb.m[key]
}

// Keys returns a copy of the keys contained.
func (mb Baggage) Keys() []attribute.Key {
	return maps.Keys(mb.m)
}

// Attributes returns a copy of the key-value attributes contained.
func (mb Baggage) Attributes() []attribute.KeyValue {
	res := make([]attribute.KeyValue, mb.Len())
	i := 0
	for k, v := range mb.m {
		res[i] = attribute.KeyValue{Key: k, Value: v}
		i++
	}

	return res
}

// Len returns the number of attributes contained.
func (mb Baggage) Len() int {
	return len(mb.m)
}
