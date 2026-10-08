// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
//go:build !windows

package cmd

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsInvalidFilenameError(t *testing.T) {
	assert.False(t, isInvalidFilenameError(errors.New("invalid filename")))
}
