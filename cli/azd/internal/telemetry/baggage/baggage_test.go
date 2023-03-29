// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package baggage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestBaggage(t *testing.T) {
	baggage := NewBaggage()
	attr := attribute.String("key1", "val1")
	newBaggage := baggage.Set(attr)
	assert.Equal(t, newBaggage.Get("key1").AsString(), "val1")

	attributes := []attribute.KeyValue{attribute.String("key2", "val2"), attribute.Int("key3", 3)}
	newBaggage = newBaggage.Set(attributes...)
	assert.Equal(t, newBaggage.Len(), 3)
	assert.ElementsMatch(t, newBaggage.Keys(), []attribute.Key{"key1", "key2", "key3"})
	assert.ElementsMatch(t, newBaggage.Attributes(), append(attributes, attr))
	assert.Equal(t, newBaggage.Get("key1").AsString(), "val1")
	assert.Equal(t, newBaggage.Get("key2").AsString(), "val2")
	assert.Equal(t, newBaggage.Get("key3").AsInt64(), int64(3))

	newBaggage = newBaggage.Delete("key2")
	assertNotFound(t, newBaggage, "key2")
	assert.Equal(t, newBaggage.Len(), 2)
	assert.ElementsMatch(t, newBaggage.Keys(), []attribute.Key{"key1", "key3"})

	newBaggage = newBaggage.Delete("key3")
	assertNotFound(t, newBaggage, "key3")
	assert.Equal(t, newBaggage.Len(), 1)
	assert.ElementsMatch(t, newBaggage.Keys(), []attribute.Key{"key1"})

	newBaggage = newBaggage.Delete("key1")
	assertNotFound(t, newBaggage, "key1")
	assert.Equal(t, newBaggage.Len(), 0)
	assert.ElementsMatch(t, newBaggage.Keys(), []attribute.Key{})
}

func TestBaggageMutateCreatesCopy(t *testing.T) {
	empty := NewBaggage()
	withKey := empty.Set(attribute.String("key1", "val1"))

	assertEmptyUnchanged := func() {
		assertNotFound(t, empty, "key1")
		assert.Equal(t, 1, withKey.Len())
	}

	assertWithKeyUnchanged := func() {
		assert.Equal(t, "val1", withKey.Get("key1").AsString())
		assert.Equal(t, 1, withKey.Len())
	}
	assertWithKeyUnchanged()
	assertEmptyUnchanged()

	withoutKey := withKey.Delete("key1")
	assertWithKeyUnchanged()
	assertEmptyUnchanged()
	assertNotFound(t, withoutKey, "key1")

	withKeyModified := withKey.Set(attribute.String("key1", "updated1"), attribute.String("key2", "val2"))
	assertWithKeyUnchanged()
	assertEmptyUnchanged()
	assert.Equal(t, "updated1", withKeyModified.Get("key1").AsString())
	assert.Equal(t, "val2", withKeyModified.Get("key2").AsString())
	assert.Equal(t, 2, withKeyModified.Len())
}

func TestBaggageWhenEmpty(t *testing.T) {
	baggage := NewBaggage()
	newBaggage := baggage.Delete("notFound")
	assert.Equal(t, newBaggage.Len(), 0)

	newBaggage = baggage.Set()
	assert.Equal(t, newBaggage.Len(), 0)
}

func assertNotFound(t *testing.T, baggage Baggage, key attribute.Key) {
	val, ok := baggage.Lookup(key)
	assert.False(t, ok)
	assert.Equal(t, attribute.Value{}, val)
}
