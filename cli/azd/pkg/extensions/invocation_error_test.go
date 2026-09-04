// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWrapInvocationError_PreservesErrorAndMetadata(t *testing.T) {
	t.Parallel()

	inner := errors.New("extension failed")
	err := WrapInvocationError(inner, "test.extension", "1.2.3", "prepackage")

	require.Equal(t, inner.Error(), err.Error())
	require.ErrorIs(t, err, inner)

	metadata, ok := errors.AsType[InvocationMetadataProvider](err)
	require.True(t, ok)
	require.Equal(t, "test.extension", metadata.InvocationExtensionId())
	require.Equal(t, "1.2.3", metadata.InvocationExtensionVersion())
	require.Equal(t, "prepackage", metadata.InvocationEvent())
}

func TestWrapInvocationError_PreservesExistingMetadataWhenUnset(t *testing.T) {
	t.Parallel()

	inner := WrapInvocationError(errors.New("extension failed"), "test.extension", "1.2.3", "prepackage")
	err := WrapInvocationError(inner, "", "", "")

	metadata, ok := errors.AsType[InvocationMetadataProvider](err)
	require.True(t, ok)
	require.Equal(t, "test.extension", metadata.InvocationExtensionId())
	require.Equal(t, "1.2.3", metadata.InvocationExtensionVersion())
	require.Equal(t, "prepackage", metadata.InvocationEvent())
}
