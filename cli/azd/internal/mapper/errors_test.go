// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mapper

import (
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test that error types are properly exported and accessible
func TestErrorTypes(t *testing.T) {
	t.Run("NoMapperError", func(t *testing.T) {
		err := &NoMapperError{
			SrcType: reflect.TypeOf(""),
			DstType: reflect.TypeOf(0),
		}

		assert.Contains(t, err.Error(), "no mapper registered from string to int")
		assert.True(t, IsNoMapperError(err))
		assert.True(t, errors.Is(err, &NoMapperError{}))
	})

	t.Run("ConversionError", func(t *testing.T) {
		innerErr := errors.New("test error")
		err := &ConversionError{
			SrcType: reflect.TypeOf(""),
			DstType: reflect.TypeOf(0),
			Err:     innerErr,
		}

		assert.Contains(t, err.Error(), "conversion failed from string to int: test error")
		assert.True(t, IsConversionError(err))
		assert.True(t, errors.Is(err, &ConversionError{}))
		assert.Equal(t, innerErr, errors.Unwrap(err))
	})

	t.Run("Sentinel errors", func(t *testing.T) {
		assert.NotNil(t, ErrNoMapper)
		assert.NotNil(t, ErrConversionFailure)
		assert.NotNil(t, ErrDuplicateRegistration)
		assert.NotNil(t, ErrInvalidRegistration)
	})

	t.Run("Helper functions", func(t *testing.T) {
		// Test with nil
		assert.False(t, IsNoMapperError(nil))
		assert.False(t, IsConversionError(nil))

		// Test with unrelated error
		unrelated := errors.New("unrelated")
		assert.False(t, IsNoMapperError(unrelated))
		assert.False(t, IsConversionError(unrelated))
	})
}
