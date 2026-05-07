// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_parseServiceLanguage(t *testing.T) {
	tests := []struct {
		name     string
		input    ServiceLanguageKind
		expected ServiceLanguageKind
	}{
		{
			name:     "dotnet",
			input:    ServiceLanguageDotNet,
			expected: ServiceLanguageDotNet,
		},
		{
			name:     "csharp",
			input:    ServiceLanguageCsharp,
			expected: ServiceLanguageCsharp,
		},
		{
			name:     "fsharp",
			input:    ServiceLanguageFsharp,
			expected: ServiceLanguageFsharp,
		},
		{
			name:     "javascript",
			input:    ServiceLanguageJavaScript,
			expected: ServiceLanguageJavaScript,
		},
		{
			name:     "typescript",
			input:    ServiceLanguageTypeScript,
			expected: ServiceLanguageTypeScript,
		},
		{
			name:     "python",
			input:    ServiceLanguagePython,
			expected: ServiceLanguagePython,
		},
		{
			name:     "java",
			input:    ServiceLanguageJava,
			expected: ServiceLanguageJava,
		},
		{
			name:     "docker",
			input:    ServiceLanguageDocker,
			expected: ServiceLanguageDocker,
		},
		{
			name:     "custom",
			input:    ServiceLanguageCustom,
			expected: ServiceLanguageCustom,
		},
		{
			name:     "empty (none)",
			input:    ServiceLanguageNone,
			expected: ServiceLanguageNone,
		},
		{
			name:     "py alias resolves to python",
			input:    ServiceLanguageKind("py"),
			expected: ServiceLanguagePython,
		},
		{
			name:     "unknown language passes through",
			input:    ServiceLanguageKind("rust"),
			expected: ServiceLanguageKind("rust"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseServiceLanguage(tt.input)
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func Test_ServiceLanguageKind_IsDotNet(t *testing.T) {
	tests := []struct {
		name     string
		kind     ServiceLanguageKind
		expected bool
	}{
		{
			name:     "dotnet",
			kind:     ServiceLanguageDotNet,
			expected: true,
		},
		{
			name:     "csharp",
			kind:     ServiceLanguageCsharp,
			expected: true,
		},
		{
			name:     "fsharp",
			kind:     ServiceLanguageFsharp,
			expected: true,
		},
		{
			name:     "python is not dotnet",
			kind:     ServiceLanguagePython,
			expected: false,
		},
		{
			name:     "javascript is not dotnet",
			kind:     ServiceLanguageJavaScript,
			expected: false,
		},
		{
			name:     "java is not dotnet",
			kind:     ServiceLanguageJava,
			expected: false,
		},
		{
			name:     "docker is not dotnet",
			kind:     ServiceLanguageDocker,
			expected: false,
		},
		{
			name:     "empty is not dotnet",
			kind:     ServiceLanguageNone,
			expected: false,
		},
		{
			name:     "unknown is not dotnet",
			kind:     ServiceLanguageKind("go"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.kind.IsDotNet())
		})
	}
}
