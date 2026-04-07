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

func TestHookConfig_KindField(t *testing.T) {
	tests := []struct {
		name         string
		yamlInput    string
		expectedKind language.HookKind
		expectedDir  string
	}{
		{
			name:         "OmittedKindDefaultsToUnknown",
			yamlInput:    "run: scripts/hook.sh\n",
			expectedKind: language.HookKindUnknown,
			expectedDir:  "",
		},
		{
			name:         "OmittedDirDefaultsToEmpty",
			yamlInput:    "run: scripts/hook.py\nkind: python\n",
			expectedKind: language.HookKindPython,
			expectedDir:  "",
		},
		{
			name:         "KindPython",
			yamlInput:    "run: scripts/hook.py\nkind: python\ndir: src/myapp\n",
			expectedKind: language.HookKindPython,
			expectedDir:  "src/myapp",
		},
		{
			name:         "KindJavaScript",
			yamlInput:    "run: hooks/prebuild.js\nkind: js\ndir: hooks\n",
			expectedKind: language.HookKindJavaScript,
			expectedDir:  "hooks",
		},
		{
			name:         "KindTypeScript",
			yamlInput:    "run: hooks/deploy.ts\nkind: ts\n",
			expectedKind: language.HookKindTypeScript,
			expectedDir:  "",
		},
		{
			name:         "KindDotNet",
			yamlInput:    "run: hooks/validate.csx\nkind: dotnet\ndir: hooks/dotnet\n",
			expectedKind: language.HookKindDotNet,
			expectedDir:  "hooks/dotnet",
		},
		{
			name:         "KindBash",
			yamlInput:    "run: scripts/setup.sh\nkind: sh\n",
			expectedKind: language.HookKindBash,
			expectedDir:  "",
		},
		{
			name:         "KindPowerShell",
			yamlInput:    "run: scripts/setup.ps1\nkind: pwsh\n",
			expectedKind: language.HookKindPowerShell,
			expectedDir:  "",
		},
		{
			name: "AllFieldsTogether",
			yamlInput: "run: src/hooks/predeploy.py\nshell: sh\n" +
				"kind: python\ndir: src/hooks\ncontinueOnError: true\n",
			expectedKind: language.HookKindPython,
			expectedDir:  "src/hooks",
		},
		{
			name:         "LanguageAliasMapsPython",
			yamlInput:    "run: scripts/hook.py\nlanguage: python\n",
			expectedKind: language.HookKindUnknown, // alias only resolved at validate()
			expectedDir:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var config HookConfig
			err := yaml.Unmarshal([]byte(tt.yamlInput), &config)
			require.NoError(t, err)

			require.Equal(t, tt.expectedKind, config.Kind)
			require.Equal(t, tt.expectedDir, config.Dir)
		})
	}
}

func TestHookConfig_KindRoundTrip(t *testing.T) {
	original := HookConfig{
		Run:  "hooks/deploy.py",
		Kind: language.HookKindPython,
		Dir:  "hooks",
	}

	data, err := yaml.Marshal(&original)
	require.NoError(t, err)

	var decoded HookConfig
	err = yaml.Unmarshal(data, &decoded)
	require.NoError(t, err)

	require.Equal(t, original.Kind, decoded.Kind)
	require.Equal(t, original.Dir, decoded.Dir)
	require.Equal(t, original.Run, decoded.Run)
}

func TestHookKind_Constants(t *testing.T) {
	tests := []struct {
		name     string
		kind     language.HookKind
		expected string
	}{
		{"Unknown", language.HookKindUnknown, ""},
		{"Bash", language.HookKindBash, "sh"},
		{"PowerShell", language.HookKindPowerShell, "pwsh"},
		{"JavaScript", language.HookKindJavaScript, "js"},
		{"TypeScript", language.HookKindTypeScript, "ts"},
		{"Python", language.HookKindPython, "python"},
		{"DotNet", language.HookKindDotNet, "dotnet"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, string(tt.kind))
		})
	}
}

func TestHookConfig_ValidateKindResolution(t *testing.T) {
	tests := []struct {
		name         string
		config       HookConfig
		createFile   string // relative path to create under cwd
		expectedKind language.HookKind
		isShell      bool
		expectError  string
	}{
		{
			name: "ExplicitKindPythonFromFile",
			config: HookConfig{
				Name: "test",
				Kind: language.HookKindPython,
				Run:  "script.py",
			},
			createFile:   "script.py",
			expectedKind: language.HookKindPython,
			isShell:      false,
		},
		{
			name: "ExplicitKindOverridesExtension",
			config: HookConfig{
				Name: "test",
				Kind: language.HookKindPython,
				Run:  "script.js",
			},
			createFile:   "script.js",
			expectedKind: language.HookKindPython,
			isShell:      false,
		},
		{
			name: "LanguageAliasMapsToKind",
			config: HookConfig{
				Name:     "test",
				Language: string(language.HookKindPython),
				Run:      "script.py",
			},
			createFile:   "script.py",
			expectedKind: language.HookKindPython,
			isShell:      false,
		},
		{
			name: "ShellAliasBashMapsToKind",
			config: HookConfig{
				Name:  "test",
				Shell: string(language.HookKindBash),
				Run:   "echo hello",
			},
			expectedKind: language.HookKindBash,
			isShell:      true,
		},
		{
			name: "InferPythonFromExtension",
			config: HookConfig{
				Name: "test",
				Run:  "hooks/seed.py",
			},
			createFile:   "hooks/seed.py",
			expectedKind: language.HookKindPython,
			isShell:      false,
		},
		{
			name: "InferJavaScriptFromExtension",
			config: HookConfig{
				Name: "test",
				Run:  "hooks/setup.js",
			},
			createFile:   "hooks/setup.js",
			expectedKind: language.HookKindJavaScript,
			isShell:      false,
		},
		{
			name: "InferTypeScriptFromExtension",
			config: HookConfig{
				Name: "test",
				Run:  "hooks/test.ts",
			},
			createFile:   "hooks/test.ts",
			expectedKind: language.HookKindTypeScript,
			isShell:      false,
		},
		{
			name: "InferDotNetFromExtension",
			config: HookConfig{
				Name: "test",
				Run:  "hooks/run.cs",
			},
			createFile:   "hooks/run.cs",
			expectedKind: language.HookKindDotNet,
			isShell:      false,
		},
		{
			name: "InferBashFromExtension",
			config: HookConfig{
				Name: "test",
				Run:  "hooks/deploy.sh",
			},
			createFile:   "hooks/deploy.sh",
			expectedKind: language.HookKindBash,
			isShell:      true,
		},
		{
			name: "InferPowerShellFromExtension",
			config: HookConfig{
				Name: "test",
				Run:  "hooks/deploy.ps1",
			},
			createFile:   "hooks/deploy.ps1",
			expectedKind: language.HookKindPowerShell,
			isShell:      true,
		},
		{
			name: "InlineScriptDefaultsToOSShell",
			config: HookConfig{
				Name: "test",
				Run:  "echo hello",
			},
			expectedKind: defaultKindForOS(),
			isShell:      true,
		},
		{
			name: "InlineScriptWithKindPythonErrors",
			config: HookConfig{
				Name: "test",
				Kind: language.HookKindPython,
				Run:  "print('hello')",
			},
			expectError: "inline scripts are not supported " +
				"for python hooks",
		},
		{
			name: "InlineScriptWithShellBashIsOK",
			config: HookConfig{
				Name:  "test",
				Shell: string(language.HookKindBash),
				Run:   "echo hello",
			},
			expectedKind: language.HookKindBash,
			isShell:      true,
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
				err = os.WriteFile(filePath, nil, 0o600)
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
				t, tt.expectedKind, config.Kind,
			)
			require.Equal(
				t, tt.isShell,
				config.Kind.IsShell(),
			)
		})
	}
}

func TestHookConfig_ValidateDirInference(t *testing.T) {
	tests := []struct {
		name        string
		config      HookConfig
		createFile  string
		expectedDir string
	}{
		{
			name: "InferDirFromPythonRunPath",
			config: HookConfig{
				Name: "test",
				Run:  filepath.Join("hooks", "preprovision", "main.py"),
			},
			createFile:  filepath.Join("hooks", "preprovision", "main.py"),
			expectedDir: filepath.Join("hooks", "preprovision"),
		},
		{
			name: "InferDirFromNestedPath",
			config: HookConfig{
				Name: "test",
				Run:  filepath.Join("src", "tools", "setup.py"),
			},
			createFile:  filepath.Join("src", "tools", "setup.py"),
			expectedDir: filepath.Join("src", "tools"),
		},
		{
			name: "InferDirForScriptInRoot",
			config: HookConfig{
				Name: "test",
				Run:  "setup.py",
			},
			createFile:  "setup.py",
			expectedDir: ".",
		},
		{
			name: "ExplicitDirOverridesInferred",
			config: HookConfig{
				Name: "test",
				Run:  filepath.Join("hooks", "deploy-tool", "src", "main.py"),
				Dir:  filepath.Join("hooks", "deploy-tool"),
			},
			createFile:  filepath.Join("hooks", "deploy-tool", "src", "main.py"),
			expectedDir: filepath.Join("hooks", "deploy-tool"),
		},
		{
			name: "ShellHookDirUnchanged",
			config: HookConfig{
				Name:  "test",
				Shell: string(language.HookKindBash),
				Run:   filepath.Join("hooks", "setup.sh"),
			},
			createFile:  filepath.Join("hooks", "setup.sh"),
			expectedDir: "",
		},
		{
			name: "InlineScriptDirUnchanged",
			config: HookConfig{
				Name:  "test",
				Shell: string(language.HookKindBash),
				Run:   "echo hello",
			},
			expectedDir: "",
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
				err = os.WriteFile(filePath, nil, 0o600)
				require.NoError(t, err)
			}

			err := config.validate()
			require.NoError(t, err)
			require.Equal(t, tt.expectedDir, config.Dir)
		})
	}
}

func TestHookKind_IsShell(t *testing.T) {
	tests := []struct {
		name     string
		kind     language.HookKind
		expected bool
	}{
		{"Python", language.HookKindPython, false},
		{"JavaScript", language.HookKindJavaScript, false},
		{"TypeScript", language.HookKindTypeScript, false},
		{"DotNet", language.HookKindDotNet, false},
		{"Bash", language.HookKindBash, true},
		{"PowerShell", language.HookKindPowerShell, true},
		{"Unknown", language.HookKindUnknown, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(
				t, tt.expected,
				tt.kind.IsShell(),
			)
		})
	}
}
