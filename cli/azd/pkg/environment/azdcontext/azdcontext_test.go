package azdcontext

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetAzdContextFails(t *testing.T) {
	ctx := context.Background()
	azdCtx, err := GetAzdContext(ctx)
	assert.Nil(t, azdCtx)
	assert.NotNil(t, err)
	assert.Equal(t, "cannot find AzdContext on go context", err.Error())
}

func TestGetAzdContextSuccess(t *testing.T) {
	expectedContext, _ := NewAzdContext()
	ctx := context.Background()
	ctx = WithAzdContext(ctx, expectedContext)

	actualContext, err := GetAzdContext(ctx)
	assert.Nil(t, err)
	assert.NotNil(t, actualContext)
	assert.Same(t, expectedContext, actualContext)
}
