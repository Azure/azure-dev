// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/language"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestHookConfig_LanguageField(t *testing.T) {
	tests := []struct {
		name             string
		yamlInput        string
		expectedLanguage language.ScriptLanguage
		expectedDir      string
	}{
		{
			name:             "OmittedLanguageDefaultsToUnknown",
			yamlInput:        "run: scripts/hook.sh\n",
			expectedLanguage: language.ScriptLanguageUnknown,
			expectedDir:      "",
		},
		{
			name:             "OmittedDirDefaultsToEmpty",
			yamlInput:        "run: scripts/hook.py\nlanguage: python\n",
			expectedLanguage: language.ScriptLanguagePython,
			expectedDir:      "",
		},
		{
			name:             "LanguagePython",
			yamlInput:        "run: scripts/hook.py\nlanguage: python\ndir: src/myapp\n",
			expectedLanguage: language.ScriptLanguagePython,
			expectedDir:      "src/myapp",
		},
		{
			name:             "LanguageJavaScript",
			yamlInput:        "run: hooks/prebuild.js\nlanguage: js\ndir: hooks\n",
			expectedLanguage: language.ScriptLanguageJavaScript,
			expectedDir:      "hooks",
		},
		{
			name:             "LanguageTypeScript",
			yamlInput:        "run: hooks/deploy.ts\nlanguage: ts\n",
			expectedLanguage: language.ScriptLanguageTypeScript,
			expectedDir:      "",
		},
		{
			name:             "LanguageDotNet",
			yamlInput:        "run: hooks/validate.csx\nlanguage: dotnet\ndir: hooks/dotnet\n",
			expectedLanguage: language.ScriptLanguageDotNet,
			expectedDir:      "hooks/dotnet",
		},
		{
			name:             "LanguageBash",
			yamlInput:        "run: scripts/setup.sh\nlanguage: sh\n",
			expectedLanguage: language.ScriptLanguageBash,
			expectedDir:      "",
		},
		{
			name:             "LanguagePowerShell",
			yamlInput:        "run: scripts/setup.ps1\nlanguage: pwsh\n",
			expectedLanguage: language.ScriptLanguagePowerShell,
			expectedDir:      "",
		},
		{
			name:             "AllFieldsTogether",
			yamlInput:        "run: src/hooks/predeploy.py\nshell: sh\nlanguage: python\ndir: src/hooks\ncontinueOnError: true\n",
			expectedLanguage: language.ScriptLanguagePython,
			expectedDir:      "src/hooks",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var config HookConfig
			err := yaml.Unmarshal([]byte(tt.yamlInput), &config)
			require.NoError(t, err)

			require.Equal(t, tt.expectedLanguage, config.Language)
			require.Equal(t, tt.expectedDir, config.Dir)
		})
	}
}

func TestHookConfig_LanguageRoundTrip(t *testing.T) {
	original := HookConfig{
		Run:      "hooks/deploy.py",
		Shell:    ShellTypeBash,
		Language: language.ScriptLanguagePython,
		Dir:      "hooks",
	}

	data, err := yaml.Marshal(&original)
	require.NoError(t, err)

	var decoded HookConfig
	err = yaml.Unmarshal(data, &decoded)
	require.NoError(t, err)

	require.Equal(t, original.Language, decoded.Language)
	require.Equal(t, original.Dir, decoded.Dir)
	require.Equal(t, original.Run, decoded.Run)
	require.Equal(t, original.Shell, decoded.Shell)
}

func TestScriptLanguage_Constants(t *testing.T) {
	tests := []struct {
		name     string
		lang     language.ScriptLanguage
		expected string
	}{
		{"Unknown", language.ScriptLanguageUnknown, ""},
		{"Bash", language.ScriptLanguageBash, "sh"},
		{"PowerShell", language.ScriptLanguagePowerShell, "pwsh"},
		{"JavaScript", language.ScriptLanguageJavaScript, "js"},
		{"TypeScript", language.ScriptLanguageTypeScript, "ts"},
		{"Python", language.ScriptLanguagePython, "python"},
		{"DotNet", language.ScriptLanguageDotNet, "dotnet"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, string(tt.lang))
		})
	}
}

func TestHookConfig_ValidateLanguageResolution(t *testing.T) {
	tests := []struct {
		name             string
		config           HookConfig
		createFile       string // relative path to create under cwd
		expectedLanguage language.ScriptLanguage
		isLanguageHook   bool
		expectError      string
	}{
		{
			name: "ExplicitLanguagePythonFromFile",
			config: HookConfig{
				Name:     "test",
				Language: language.ScriptLanguagePython,
				Run:      "script.py",
			},
			createFile:       "script.py",
			expectedLanguage: language.ScriptLanguagePython,
			isLanguageHook:   true,
		},
		{
			name: "ExplicitLanguageOverridesExtension",
			config: HookConfig{
				Name:     "test",
				Language: language.ScriptLanguagePython,
				Run:      "script.js",
			},
			createFile:       "script.js",
			expectedLanguage: language.ScriptLanguagePython,
			isLanguageHook:   true,
		},
		{
			name: "ShellBashMapsToLanguage",
			config: HookConfig{
				Name:  "test",
				Shell: ShellTypeBash,
				Run:   "echo hello",
			},
			expectedLanguage: language.ScriptLanguageBash,
			isLanguageHook:   false,
		},
		{
			name: "InferPythonFromExtension",
			config: HookConfig{
				Name: "test",
				Run:  "hooks/seed.py",
			},
			createFile:       "hooks/seed.py",
			expectedLanguage: language.ScriptLanguagePython,
			isLanguageHook:   true,
		},
		{
			name: "InferJavaScriptFromExtension",
			config: HookConfig{
				Name: "test",
				Run:  "hooks/setup.js",
			},
			createFile:       "hooks/setup.js",
			expectedLanguage: language.ScriptLanguageJavaScript,
			isLanguageHook:   true,
		},
		{
			name: "InferTypeScriptFromExtension",
			config: HookConfig{
				Name: "test",
				Run:  "hooks/test.ts",
			},
			createFile:       "hooks/test.ts",
			expectedLanguage: language.ScriptLanguageTypeScript,
			isLanguageHook:   true,
		},
		{
			name: "InferDotNetFromExtension",
			config: HookConfig{
				Name: "test",
				Run:  "hooks/run.cs",
			},
			createFile:       "hooks/run.cs",
			expectedLanguage: language.ScriptLanguageDotNet,
			isLanguageHook:   true,
		},
		{
			name: "InferBashFromExtension",
			config: HookConfig{
				Name: "test",
				Run:  "hooks/deploy.sh",
			},
			createFile:       "hooks/deploy.sh",
			expectedLanguage: language.ScriptLanguageBash,
			isLanguageHook:   false,
		},
		{
			name: "InferPowerShellFromExtension",
			config: HookConfig{
				Name: "test",
				Run:  "hooks/deploy.ps1",
			},
			createFile:       "hooks/deploy.ps1",
			expectedLanguage: language.ScriptLanguagePowerShell,
			isLanguageHook:   false,
		},
		{
			name: "InlineScriptDefaultsToOSShell",
			config: HookConfig{
				Name: "test",
				Run:  "echo hello",
			},
			expectedLanguage: shellToLanguage(
				getDefaultShellForOS(),
			),
			isLanguageHook: false,
		},
		{
			name: "InlineScriptWithLanguagePythonErrors",
			config: HookConfig{
				Name:     "test",
				Language: language.ScriptLanguagePython,
				Run:      "print('hello')",
			},
			expectError: "inline scripts are not supported " +
				"for python hooks",
		},
		{
			name: "InlineScriptWithShellBashIsOK",
			config: HookConfig{
				Name:  "test",
				Shell: ShellTypeBash,
				Run:   "echo hello",
			},
			expectedLanguage: language.ScriptLanguageBash,
			isLanguageHook:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := tt.config
			cwd := t.TempDir()
			config.cwd = cwd

			if tt.createFile != "" {
				filePath := filepath.Join(cwd, tt.createFile)
				err := os.MkdirAll(filepath.Dir(filePath), 0o755)
				require.NoError(t, err)
				err = os.WriteFile(filePath, nil, 0o644)
				require.NoError(t, err)
			}

			err := config.validate()

			if tt.expectError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectError)
				return
			}

			require.NoError(t, err)
			require.Equal(
				t, tt.expectedLanguage, config.Language,
			)
			require.Equal(
				t, tt.isLanguageHook, config.IsLanguageHook(),
			)
		})
	}
}

func TestHookConfig_IsLanguageHook(t *testing.T) {
	tests := []struct {
		name     string
		lang     language.ScriptLanguage
		expected bool
	}{
		{"Python", language.ScriptLanguagePython, true},
		{"JavaScript", language.ScriptLanguageJavaScript, true},
		{"TypeScript", language.ScriptLanguageTypeScript, true},
		{"DotNet", language.ScriptLanguageDotNet, true},
		{"Bash", language.ScriptLanguageBash, false},
		{"PowerShell", language.ScriptLanguagePowerShell, false},
		{"Unknown", language.ScriptLanguageUnknown, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &HookConfig{Language: tt.lang}
			require.Equal(t, tt.expected, config.IsLanguageHook())
		})
	}
}
