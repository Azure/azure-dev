// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_consoleTruncate(t *testing.T) {
	t.Run("no truncate", func(t *testing.T) {
		console := &AskerConsole{
			consoleWidth: 100,
		}
		expected := "sample text"
		produced := console.spinnerText(expected, "|   |")
		require.Equal(t, expected, produced)
	})

	t.Run("no truncate - too small", func(t *testing.T) {
		console := &AskerConsole{
			consoleWidth: cMinConsoleWidth,
		}
		expected := "sample text"
		produced := console.spinnerText(expected, "|   |")
		require.Equal(t, expected, produced)
	})

	t.Run("truncate", func(t *testing.T) {
		console := &AskerConsole{
			consoleWidth: cMinConsoleWidth + 1,
		}
		original := "sample text which should be truncated because it is too long"
		expected := original[:cMinConsoleWidth-len(cPostfix)-len(customCharSet[0])] + cPostfix
		produced := console.spinnerText(original, customCharSet[0])
		require.Equal(t, expected, produced)
	})
}
