// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package baggage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestContext(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, NewBaggage(), BaggageFromContext(ctx))

	b := NewBaggage()
	b.Set(attribute.String("key1", "val1"))
	ctx = ContextWithBaggage(ctx, b)
	assert.Equal(t, b, BaggageFromContext(ctx))

	ctx = ContextWithoutBaggage(ctx)
	assert.Equal(t, NewBaggage(), BaggageFromContext(ctx))
}
