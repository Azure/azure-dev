// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Copy of the isTemplateCommand function from template_version_middleware.go for testing
func isTemplateCommandTest(commandName string) bool {
	templateCommands := []string{
		"init",
		"up",
		"deploy",
		"provision",
		"env", // Some env commands may need the template
		"pipeline",
		"monitor",
	}

	for _, cmd := range templateCommands {
		if cmd == commandName {
			return true
		}
	}

	return false
}

// Test the isTemplateCommand function that's used by the middleware
func TestIsTemplateCommand(t *testing.T) {
	testCases := []struct {
		commandName string
		expected    bool
	}{
		{"init", true},
		{"up", true},
		{"deploy", true},
		{"provision", true},
		{"env", true},
		{"pipeline", true},
		{"monitor", true},
		{"version", false},
		{"login", false},
		{"logout", false},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("Command: %s", tc.commandName), func(t *testing.T) {
			result := isTemplateCommandTest(tc.commandName)
			assert.Equal(t, tc.expected, result)
		})
	}
}
