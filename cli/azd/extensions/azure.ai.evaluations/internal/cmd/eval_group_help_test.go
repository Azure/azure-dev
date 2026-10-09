// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEvalCreatePathHelpNamesFileAndDirectory(t *testing.T) {
	usage := newEvalCreateCommand().Flags().Lookup("path").Usage
	assert.Contains(t, usage, "Configuration file or directory")
	assert.Contains(t, usage, "existing directories remain directories")
}
