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

func (mb Baggage) Delete(key attribute.Key) Baggage {
	copied := Baggage{m: make(map[attribute.Key]attribute.Value, len(mb.m))}
	for k, v := range mb.m {
		if k != key {
			copied.m[k] = v
		}
	}

	return copied
}

func (mb Baggage) Lookup(key attribute.Key) (val attribute.Value, ok bool) {
	val, ok = mb.m[key]
	return
}

func (mb Baggage) Get(key attribute.Key) attribute.Value {
	return mb.m[key]
}

func (mb Baggage) Keys() []attribute.Key {
	return maps.Keys(mb.m)
}

func (mb Baggage) Attributes() []attribute.KeyValue {
	res := make([]attribute.KeyValue, mb.Len())
	for k, v := range mb.m {
		res = append(res, attribute.KeyValue{Key: k, Value: v})
	}

	return res
}

func (mb Baggage) Len() int {
	return len(mb.m)
}
