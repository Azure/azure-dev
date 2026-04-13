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

// TestHookConfig_ValidateDirRunResolution verifies that when Dir is
// explicitly set, the run path is resolved relative to Dir (not the
// project root) for file existence checks.
func TestHookConfig_ValidateDirRunResolution(t *testing.T) {
	tests := []struct {
		name         string
		config       HookConfig
		createFiles  []string // paths relative to cwd
		expectPath   string   // expected hc.path (empty = inline)
		expectScript string   // expected hc.script (empty = file)
		expectError  string
	}{
		{
			name: "DirWithRunResolvesRelativeToDir",
			config: HookConfig{
				Name: "preprovision",
				Kind: language.HookKindPython,
				Run:  "main.py",
				Dir:  filepath.Join("hooks", "preprovision"),
			},
			createFiles: []string{
				filepath.Join(
					"hooks", "preprovision", "main.py",
				),
			},
			expectPath:   "main.py",
			expectScript: "",
		},
		{
			name: "DirWithSubdirInRun",
			config: HookConfig{
				Name: "prebuild",
				Kind: language.HookKindPython,
				Run:  filepath.Join("src", "main.py"),
				Dir:  filepath.Join("hooks", "preprovision"),
			},
			createFiles: []string{
				filepath.Join(
					"hooks", "preprovision",
					"src", "main.py",
				),
			},
			expectPath:   filepath.Join("src", "main.py"),
			expectScript: "",
		},
		{
			name: "NoDirRunFullPathFromRoot",
			config: HookConfig{
				Name: "predeploy",
				Kind: language.HookKindPython,
				Run: filepath.Join(
					"hooks", "preprovision", "main.py",
				),
			},
			createFiles: []string{
				filepath.Join(
					"hooks", "preprovision", "main.py",
				),
			},
			expectPath: filepath.Join(
				"hooks", "preprovision", "main.py",
			),
			expectScript: "",
		},
		{
			name: "DirSetFileDoesNotExistNonShell",
			config: HookConfig{
				Name: "preprovision",
				Kind: language.HookKindPython,
				Run:  "nonexistent.py",
				Dir:  filepath.Join("hooks", "preprovision"),
			},
			createFiles: []string{}, // no files
			expectError: "inline scripts are not supported " +
				"for python hooks",
		},
		{
			name: "ShellHookWithDir",
			config: HookConfig{
				Name:  "predeploy",
				Shell: string(language.HookKindBash),
				Run:   "deploy.sh",
				Dir:   "scripts",
			},
			createFiles: []string{
				filepath.Join("scripts", "deploy.sh"),
			},
			expectPath:   "deploy.sh",
			expectScript: "",
		},
		{
			name: "AbsoluteRunIgnoresDir",
			config: HookConfig{
				Name: "test",
				Kind: language.HookKindPython,
				Run:  "main.py",
				Dir:  filepath.Join("hooks", "preprovision"),
			},
			// Put file in dir but also at root — the
			// absolute path case is tested next; here we
			// verify the dir path is used.
			createFiles: []string{
				filepath.Join(
					"hooks", "preprovision", "main.py",
				),
			},
			expectPath:   "main.py",
			expectScript: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := tt.config
			cwd := t.TempDir()
			config.cwd = cwd

			for _, f := range tt.createFiles {
				fp := filepath.Join(cwd, f)
				err := os.MkdirAll(
					filepath.Dir(fp), 0o755,
				)
				require.NoError(t, err)
				err = os.WriteFile(fp, nil, 0o600)
				require.NoError(t, err)
			}

			err := config.validate()

			if tt.expectError != "" {
				require.Error(t, err)
				require.Contains(
					t, err.Error(), tt.expectError,
				)
				return
			}

			require.NoError(t, err)
			require.Equal(
				t, tt.expectPath, config.path,
				"path mismatch",
			)
			require.Equal(
				t, tt.expectScript, config.script,
				"script mismatch",
			)
		})
	}
}

// TestHookConfig_ValidateDirRunAbsolutePath verifies that an
// absolute run path is not joined with Dir.
func TestHookConfig_ValidateDirRunAbsolutePath(t *testing.T) {
	cwd := t.TempDir()

	// Create an "absolute" script inside a temp location.
	absScript := filepath.Join(cwd, "abs-scripts", "run.py")
	require.NoError(
		t,
		os.MkdirAll(filepath.Dir(absScript), 0o755),
	)
	require.NoError(
		t, os.WriteFile(absScript, nil, 0o600),
	)

	config := HookConfig{
		Name: "test",
		Kind: language.HookKindPython,
		Run:  absScript,
		Dir:  filepath.Join("hooks", "preprovision"),
		cwd:  cwd,
	}

	err := config.validate()
	require.NoError(t, err)
	// Absolute run paths are resolved without Dir prefix.
	require.Equal(t, absScript, config.path)
	require.Equal(t, "", config.script)
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

// TestHookConfig_ResolvedPaths verifies that validate() populates
// resolvedScriptPath and resolvedDir correctly for various
// combinations of Dir, run path, and hook kind.
func TestHookConfig_ResolvedPaths(t *testing.T) {
	tests := []struct {
		name               string
		config             HookConfig
		createFiles        []string
		expectedDir        string // suffix of resolvedDir (checked via HasSuffix)
		expectedScriptPath string // suffix of resolvedScriptPath
	}{
		{
			name: "PythonWithExplicitDir",
			config: HookConfig{
				Name: "test",
				Kind: language.HookKindPython,
				Run:  "main.py",
				Dir:  filepath.Join("hooks", "preprovision"),
			},
			createFiles: []string{
				filepath.Join(
					"hooks", "preprovision", "main.py",
				),
			},
			expectedDir: filepath.Join(
				"hooks", "preprovision",
			),
			expectedScriptPath: filepath.Join(
				"hooks", "preprovision", "main.py",
			),
		},
		{
			name: "PythonWithInferredDir",
			config: HookConfig{
				Name: "test",
				Kind: language.HookKindPython,
				Run: filepath.Join(
					"hooks", "preprovision", "main.py",
				),
			},
			createFiles: []string{
				filepath.Join(
					"hooks", "preprovision", "main.py",
				),
			},
			expectedDir: filepath.Join(
				"hooks", "preprovision",
			),
			expectedScriptPath: filepath.Join(
				"hooks", "preprovision", "main.py",
			),
		},
		{
			name: "ShellHookWithDir",
			config: HookConfig{
				Name:  "test",
				Shell: string(language.HookKindBash),
				Run:   "deploy.sh",
				Dir:   "scripts",
			},
			createFiles: []string{
				filepath.Join("scripts", "deploy.sh"),
			},
			expectedDir: "scripts",
			expectedScriptPath: filepath.Join(
				"scripts", "deploy.sh",
			),
		},
		{
			name: "ShellHookNoDirUsesProjectRoot",
			config: HookConfig{
				Name:  "test",
				Shell: string(language.HookKindBash),
				Run:   filepath.Join("scripts", "deploy.sh"),
			},
			createFiles: []string{
				filepath.Join("scripts", "deploy.sh"),
			},
			expectedDir: "", // resolvedDir == cwd
			expectedScriptPath: filepath.Join(
				"scripts", "deploy.sh",
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := tt.config
			cwd := t.TempDir()
			config.cwd = cwd

			for _, f := range tt.createFiles {
				fp := filepath.Join(cwd, f)
				require.NoError(
					t,
					os.MkdirAll(
						filepath.Dir(fp), 0o755,
					),
				)
				require.NoError(
					t,
					os.WriteFile(fp, nil, 0o600),
				)
			}

			err := config.validate()
			require.NoError(t, err)

			// resolvedDir should be an absolute path
			// ending with the expected suffix.
			require.True(
				t,
				filepath.IsAbs(config.resolvedDir),
				"resolvedDir should be absolute: %s",
				config.resolvedDir,
			)

			if tt.expectedDir == "" {
				// Should resolve to cwd itself.
				absCwd, _ := filepath.Abs(cwd)
				require.Equal(
					t, absCwd, config.resolvedDir,
				)
			} else {
				expectedFull := filepath.Join(
					cwd, tt.expectedDir,
				)
				absExpected, _ := filepath.Abs(
					expectedFull,
				)
				require.Equal(
					t, absExpected, config.resolvedDir,
				)
			}

			// resolvedScriptPath should be absolute and
			// end with the expected suffix.
			if tt.expectedScriptPath != "" {
				require.True(
					t,
					filepath.IsAbs(
						config.resolvedScriptPath,
					),
					"resolvedScriptPath should be "+
						"absolute: %s",
					config.resolvedScriptPath,
				)
				expectedFull := filepath.Join(
					cwd, tt.expectedScriptPath,
				)
				require.Equal(
					t,
					expectedFull,
					config.resolvedScriptPath,
				)
			}
		})
	}
}

// TestHookConfig_ValidatePathTraversal verifies that validate()
// rejects Dir values that escape the project root via "..".
func TestHookConfig_ValidatePathTraversal(t *testing.T) {
	cwd := t.TempDir()

	config := HookConfig{
		Name:  "test",
		Shell: string(language.HookKindBash),
		Run:   "echo hello",
		Dir:   filepath.Join("..", "..", "escape"),
		cwd:   cwd,
	}

	err := config.validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "escapes project root")
}

// TestHookConfig_ServiceHookRelativePathWithinProject verifies that
// service-level hooks with relative paths that reference parent
// directories are accepted when they resolve within the project
// root. This is the regression scenario from issue #7666 where a
// service in src/logicApp has hooks at ../../hooks/prepackage.ps1.
func TestHookConfig_ServiceHookRelativePathWithinProject(
	t *testing.T,
) {
	// Structure:
	//   projectRoot/
	//     hooks/prepackage.sh
	//     hooks/prepackage.ps1
	//     hooks/prepackage.py
	//     hooks/prepackage.js
	//     hooks/prepackage.ts
	//     hooks/prepackage.cs
	//     src/logicApp/  (service cwd)
	projectRoot := t.TempDir()
	serviceCwd := filepath.Join(
		projectRoot, "src", "logicApp",
	)
	hooksDir := filepath.Join(projectRoot, "hooks")

	require.NoError(t, os.MkdirAll(serviceCwd, 0o755))
	require.NoError(t, os.MkdirAll(hooksDir, 0o755))

	// Create script files for all hook kinds.
	scripts := []string{
		"prepackage.sh",
		"prepackage.ps1",
		"prepackage.py",
		"prepackage.js",
		"prepackage.ts",
		"prepackage.cs",
	}
	for _, s := range scripts {
		require.NoError(t, os.WriteFile(
			filepath.Join(hooksDir, s), nil, 0o600,
		))
	}

	tests := []struct {
		name string
		run  string
		kind language.HookKind
	}{
		{
			name: "BashHookFromService",
			run: filepath.Join(
				"..", "..", "hooks", "prepackage.sh",
			),
			kind: language.HookKindBash,
		},
		{
			name: "PowerShellHookFromService",
			run: filepath.Join(
				"..", "..", "hooks", "prepackage.ps1",
			),
			kind: language.HookKindPowerShell,
		},
		{
			name: "PythonHookFromService",
			run: filepath.Join(
				"..", "..", "hooks", "prepackage.py",
			),
			kind: language.HookKindPython,
		},
		{
			name: "JavaScriptHookFromService",
			run: filepath.Join(
				"..", "..", "hooks", "prepackage.js",
			),
			kind: language.HookKindJavaScript,
		},
		{
			name: "TypeScriptHookFromService",
			run: filepath.Join(
				"..", "..", "hooks", "prepackage.ts",
			),
			kind: language.HookKindTypeScript,
		},
		{
			name: "DotNetHookFromService",
			run: filepath.Join(
				"..", "..", "hooks", "prepackage.cs",
			),
			kind: language.HookKindDotNet,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := HookConfig{
				Name:       "prepackage",
				Run:        tt.run,
				cwd:        serviceCwd,
				projectDir: projectRoot,
			}

			err := config.validate()
			require.NoError(t, err,
				"hook within project root must not "+
					"be rejected")
			require.Equal(t, tt.kind, config.Kind)
		})
	}
}

// TestHookConfig_ServiceHookEscapesProjectRoot verifies that
// service-level hooks that escape the project root are still
// rejected even when projectDir is set separately from cwd.
func TestHookConfig_ServiceHookEscapesProjectRoot(
	t *testing.T,
) {
	projectRoot := t.TempDir()
	serviceCwd := filepath.Join(
		projectRoot, "src", "logicApp",
	)
	require.NoError(t, os.MkdirAll(serviceCwd, 0o755))

	// Script file outside the project root — the
	// boundary check must reject this.
	outsideDir := t.TempDir()
	scriptPath := filepath.Join(outsideDir, "evil.sh")
	require.NoError(t, os.WriteFile(
		scriptPath, nil, 0o600,
	))

	config := HookConfig{
		Name:       "prepackage",
		Shell:      string(language.HookKindBash),
		Run:        scriptPath,
		cwd:        serviceCwd,
		projectDir: projectRoot,
	}

	err := config.validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "escapes project root")
}

// TestHookConfig_ProjectDirFallbackToCwd verifies that when
// projectDir is empty, cwd is used as the boundary (preserving
// backward compatibility).
func TestHookConfig_ProjectDirFallbackToCwd(t *testing.T) {
	cwd := t.TempDir()

	config := HookConfig{
		Name:  "test",
		Shell: string(language.HookKindBash),
		Run:   "echo hello",
		Dir:   filepath.Join("..", "..", "escape"),
		cwd:   cwd,
		// projectDir intentionally empty
	}

	err := config.validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "escapes project root")
}

// TestHookConfig_ServiceDirWithinProjectBoundary verifies that
// an explicit Dir field that resolves within the project root is
// accepted for service-level hooks with a separate projectDir.
func TestHookConfig_ServiceDirWithinProjectBoundary(
	t *testing.T,
) {
	projectRoot := t.TempDir()
	serviceCwd := filepath.Join(
		projectRoot, "src", "logicApp",
	)
	require.NoError(t, os.MkdirAll(serviceCwd, 0o755))

	scriptDir := filepath.Join(projectRoot, "hooks")
	require.NoError(t, os.MkdirAll(scriptDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(scriptDir, "deploy.sh"),
		nil, 0o600,
	))

	config := HookConfig{
		Name:       "predeploy",
		Shell:      string(language.HookKindBash),
		Run:        "deploy.sh",
		Dir:        filepath.Join("..", "..", "hooks"),
		cwd:        serviceCwd,
		projectDir: projectRoot,
	}

	err := config.validate()
	require.NoError(t, err,
		"Dir within project root must be accepted")
}

// TestHookConfig_DirRunKindInference verifies that when both Dir
// and Run are set without an explicit Kind, validate() correctly
// infers the hook kind from the file extension and resolves the
// script path to the actual file on disk.
func TestHookConfig_DirRunKindInference(t *testing.T) {
	tests := []struct {
		name         string
		run          string
		expectedKind language.HookKind
	}{
		{
			name:         "Bash",
			run:          "build.sh",
			expectedKind: language.HookKindBash,
		},
		{
			name:         "PowerShell",
			run:          "build.ps1",
			expectedKind: language.HookKindPowerShell,
		},
		{
			name:         "Python",
			run:          "main.py",
			expectedKind: language.HookKindPython,
		},
		{
			name:         "JavaScript",
			run:          "index.js",
			expectedKind: language.HookKindJavaScript,
		},
		{
			name:         "TypeScript",
			run:          "index.ts",
			expectedKind: language.HookKindTypeScript,
		},
		{
			name:         "DotNet",
			run:          "Program.cs",
			expectedKind: language.HookKindDotNet,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cwd := t.TempDir()

			hookDir := filepath.Join(
				cwd, "hooks", "pre",
			)
			require.NoError(
				t,
				os.MkdirAll(hookDir, 0o755),
			)
			require.NoError(t, os.WriteFile(
				filepath.Join(hookDir, tt.run),
				nil, 0o600,
			))

			config := HookConfig{
				Name: "preprovision",
				Run:  tt.run,
				Dir:  filepath.Join("hooks", "pre"),
				cwd:  cwd,
			}

			err := config.validate()
			require.NoError(t, err)

			require.Equal(
				t,
				tt.expectedKind,
				config.Kind,
				"Kind should be inferred from "+
					"file extension",
			)

			expectedScript := filepath.Join(
				hookDir, tt.run,
			)
			require.Equal(
				t,
				expectedScript,
				config.resolvedScriptPath,
				"resolvedScriptPath should point "+
					"to the actual file",
			)
		})
	}
}
