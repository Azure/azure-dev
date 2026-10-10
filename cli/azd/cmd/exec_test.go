// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec/scripting"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envSliceToMap converts an env slice ([]string{"KEY=VALUE", ...}) to a map.
// When duplicate keys exist, the last value wins (matching exec.Cmd behavior).
func envSliceToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, entry := range env {
		k, v, _ := strings.Cut(entry, "=")
		m[k] = v
	}
	return m
}

// mockExecKeyVaultService implements keyvault.KeyVaultService for testing.
// Only SecretFromKeyVaultReference is wired; other methods panic if called.
type mockExecKeyVaultService struct {
	secretFromKeyVaultRefFn func(ctx context.Context, ref string, defaultSubID string) (string, error)
}

func (m *mockExecKeyVaultService) GetKeyVault(
	context.Context, string, string, string,
) (*keyvault.KeyVault, error) {
	panic("not implemented")
}

func (m *mockExecKeyVaultService) GetKeyVaultSecret(
	context.Context, string, string, string,
) (*keyvault.Secret, error) {
	panic("not implemented")
}

func (m *mockExecKeyVaultService) PurgeKeyVault(context.Context, string, string, string) error {
	panic("not implemented")
}

func (m *mockExecKeyVaultService) ListSubscriptionVaults(context.Context, string) ([]keyvault.Vault, error) {
	panic("not implemented")
}

func (m *mockExecKeyVaultService) CreateVault(
	context.Context, string, string, string, string, string,
) (keyvault.Vault, error) {
	panic("not implemented")
}

func (m *mockExecKeyVaultService) ListKeyVaultSecrets(context.Context, string, string) ([]string, error) {
	panic("not implemented")
}

func (m *mockExecKeyVaultService) CreateKeyVaultSecret(
	context.Context, string, string, string, string,
) error {
	panic("not implemented")
}

func (m *mockExecKeyVaultService) SecretFromAkvs(context.Context, string) (string, error) {
	panic("not implemented")
}

func (m *mockExecKeyVaultService) SecretFromKeyVaultReference(
	ctx context.Context, ref string, defaultSubID string,
) (string, error) {
	if m.secretFromKeyVaultRefFn != nil {
		return m.secretFromKeyVaultRefFn(ctx, ref, defaultSubID)
	}
	return "", errors.New("mockExecKeyVaultService: secretFromKeyVaultRefFn not set")
}

func TestExecAction_SetsEnvironmentVariables(t *testing.T) {
	const key1 = "AZD_TEST_EXEC_VAR1"
	const key2 = "AZD_TEST_EXEC_VAR2"

	env := environment.NewWithValues("test", map[string]string{
		key1: "value1",
		key2: "value2",
	})

	kvMock := &mockExecKeyVaultService{
		secretFromKeyVaultRefFn: func(_ context.Context, _ string, _ string) (string, error) {
			return "", errors.New("should not be called for plain values")
		},
	}

	action := &execAction{
		env:             env,
		keyvaultService: kvMock,
		flags:           &execFlags{global: &internal.GlobalCommandOptions{}},
		args:            []string{"go", "version"},
	}

	childEnv, err := action.buildChildEnv(t.Context())
	require.NoError(t, err)

	envMap := envSliceToMap(childEnv)
	assert.Equal(t, "value1", envMap[key1])
	assert.Equal(t, "value2", envMap[key2])
}

func TestExecAction_ResolvesSecretReferences(t *testing.T) {
	const secretKey = "AZD_TEST_EXEC_SECRET" //nolint:gosec // G101: test constant, not a credential

	secretRef := "akvs://sub-id/vault-name/secret-name" //nolint:gosec // G101: test fixture, not a credential
	env := environment.NewWithValues("test", map[string]string{
		secretKey: secretRef,
	})

	kvMock := &mockExecKeyVaultService{
		secretFromKeyVaultRefFn: func(_ context.Context, ref string, _ string) (string, error) {
			assert.Equal(t, secretRef, ref)
			return "resolved-secret-value", nil
		},
	}

	action := &execAction{
		env:             env,
		keyvaultService: kvMock,
		flags:           &execFlags{global: &internal.GlobalCommandOptions{}},
		args:            []string{"go", "version"},
	}

	childEnv, err := action.buildChildEnv(t.Context())
	require.NoError(t, err)

	envMap := envSliceToMap(childEnv)
	assert.Equal(t, "resolved-secret-value", envMap[secretKey])
}

func TestExecAction_SecretResolutionFailure(t *testing.T) {
	const secretKey = "AZD_TEST_EXEC_SECRET_FAIL" //nolint:gosec // G101: test constant, not a credential

	secretRef := "akvs://sub-id/vault-name/secret-name" //nolint:gosec // G101: test fixture, not a credential
	env := environment.NewWithValues("test", map[string]string{
		secretKey: secretRef,
	})

	kvMock := &mockExecKeyVaultService{
		secretFromKeyVaultRefFn: func(_ context.Context, _ string, _ string) (string, error) {
			return "", errors.New("vault unavailable")
		},
	}

	action := &execAction{
		env:             env,
		keyvaultService: kvMock,
		flags:           &execFlags{global: &internal.GlobalCommandOptions{}},
		args:            []string{"go", "version"},
	}

	_, err := action.buildChildEnv(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolving secret")
}

func TestExecAction_InvalidShell(t *testing.T) {
	env := environment.NewWithValues("test", nil)

	kvMock := &mockExecKeyVaultService{}

	action := &execAction{
		env:             env,
		keyvaultService: kvMock,
		flags: &execFlags{
			global: &internal.GlobalCommandOptions{},
			shell:  "invalid-shell",
		},
		args: []string{"echo", "hello"},
	}

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid configuration")
}

func TestExecAction_DirectExecMode(t *testing.T) {
	env := environment.NewWithValues("test", nil)

	kvMock := &mockExecKeyVaultService{}

	action := &execAction{
		env:             env,
		keyvaultService: kvMock,
		flags:           &execFlags{global: &internal.GlobalCommandOptions{}},
		args:            []string{"go", "version"},
	}

	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

func TestNewExecCmd(t *testing.T) {
	cmd := newExecCmd()

	assert.Equal(t, "exec [command] [args...] [-- script-args...]", cmd.Use)
	assert.Contains(t, cmd.Short, "Execute commands")
	require.NotNil(t, cmd.Args)

	// Args validator requires at least 1 argument.
	assert.Error(t, cmd.Args(cmd, []string{}))
	assert.NoError(t, cmd.Args(cmd, []string{"echo"}))
	assert.NoError(t, cmd.Args(cmd, []string{"echo", "hello"}))

	// Verify SetInterspersed(false) was called by checking that the flags
	// set does not have interspersed enabled. pflag exposes HasFlags but
	// not interspersed directly; instead we verify the observable behavior
	// by confirming that the whitelist and arg validator work correctly.
	// The FParseErrWhitelist.UnknownFlags should be true.
	assert.True(t, cmd.FParseErrWhitelist.UnknownFlags,
		"unknown flags should be whitelisted so child flags are forwarded")
}

func TestExecAction_ResolvesKeyVaultReferenceFormat(t *testing.T) {
	const secretKey = "AZD_TEST_EXEC_KVREF" //nolint:gosec // G101: test constant, not a credential

	// @Microsoft.KeyVault reference format (used in Azure App Service settings).
	secretRef := "@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.net/secrets/mysecret)"
	env := environment.NewWithValues("test", map[string]string{
		secretKey: secretRef,
	})

	kvMock := &mockExecKeyVaultService{
		secretFromKeyVaultRefFn: func(_ context.Context, ref string, _ string) (string, error) {
			assert.Equal(t, secretRef, ref)
			return "kv-resolved-value", nil
		},
	}

	action := &execAction{
		env:             env,
		keyvaultService: kvMock,
		flags:           &execFlags{global: &internal.GlobalCommandOptions{}},
		args:            []string{"go", "version"},
	}

	childEnv, err := action.buildChildEnv(t.Context())
	require.NoError(t, err)

	envMap := envSliceToMap(childEnv)
	assert.Equal(t, "kv-resolved-value", envMap[secretKey])
}

func TestExecAction_ExitCodePropagation(t *testing.T) {
	env := environment.NewWithValues("test", nil)

	// Build a tiny Go program that calls os.Exit(42) — this is portable across
	// all platforms and gives us a real exit code to verify propagation.
	exitBin := filepath.Join(t.TempDir(), "exit42")
	if runtime.GOOS == "windows" {
		exitBin += ".exe"
	}
	//nolint:gosec // G204: test builds a known fixture
	buildCmd := exec.Command("go", "build", "-o", exitBin, "testdata/exit42.go")
	buildCmd.Dir = "."
	out, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "failed to build exit42: %s", string(out))

	// Test exit code propagation through ExecuteDirect path by using the
	// scripting package directly. This avoids the Execute() shell-dispatch
	// path which doesn't handle raw binaries.
	executor, err := scripting.New(scripting.Config{
		Interactive: false,
		Env:         env.Environ(),
	})
	require.NoError(t, err)

	execErr := executor.ExecuteDirect(t.Context(), exitBin, []string{})
	require.Error(t, execErr)

	var scriptExecErr *scripting.ExecutionError
	if errors.As(execErr, &scriptExecErr) {
		assert.Equal(t, 42, scriptExecErr.ExitCode)
	} else {
		t.Fatalf("expected ExecutionError with exit code 42, got %T: %v", execErr, execErr)
	}
}

func TestLooksLikeFilePath(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"echo hello", false},
		{"go version", false},
		{"npm", false},
		{"./script.sh", true},
		{"scripts/deploy.sh", true},
		{"scripts/my script.sh", true},
		{"my scripts/deploy.sh", true},
		{"\\\\server\\share\\deploy.ps1", true},
		{"C:\\scripts\\deploy.ps1", true},
		{"C:\\Program Files\\deploy.ps1", true},
		{"deploy.sh", true},
		{"deploy.PS1", true},
		{"  deploy.sh  ", true},
		{"build.ps1", true},
		{"run.cmd", true},
		{"setup.bat", true},
		{"app.py", true},
		{"tool.rb", true},
		{"deploy.bash", true},
		{"config.zsh", true},
		{"mycommand", false},
		{"echo $HOME", false},
		{"ls -la", false},
		{"python script.py", false},
		{"echo path/to/file", false},
		{"cat ./config/settings.json", false},
		{"cat<config/settings.json", false},
		{"cat<scripts/deploy.sh", true},
		{"Write-Output 'config\\settings.json'", false},
		{"echo 'scripts/deploy.sh'", false},
		{`echo "scripts/deploy.sh"`, false},
		{"echo scripts/deploy.sh", true},
		{"echo scripts/deploy.SH", true},
		{"$HOME/scripts/deploy.sh", true},
		{"tool --config config\\settings.json", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, looksLikeFilePath(tt.input))
		})
	}
}

func TestShouldFailOnMissingScript(t *testing.T) {
	tests := []struct {
		name  string
		input string
		shell string
		want  bool
	}{
		{"missing path", "my scripts/deploy.sh", "", true},
		{"missing UNC path", "\\\\server\\share\\deploy.ps1", "", true},
		{"explicit inline shell", "echo scripts/deploy.sh", "pwsh", false},
		{"explicit shell path with spaces", "my scripts/deploy.sh", "bash", false},
		{"explicit shell environment path", "$HOME/scripts/deploy.sh", "bash", false},
		{"explicit shell quoted path", `echo "scripts/deploy.sh"`, "bash", false},
		{
			"explicit shell operator after path argument",
			"Write-Output config\\settings.json | Out-String",
			"pwsh",
			false,
		},
		{"explicit shell glob", "if exist *.txt rem deploy.cmd", "cmd", false},
		{
			"explicit shell absolute path argument",
			"if exist C:\\config\\settings.json rem deploy.cmd",
			"cmd",
			false,
		},
		{"explicit shell pipeline with leading path", "./deploy.sh | tee output.log", "bash", false},
		{"explicit shell compact pipeline with leading path", "./deploy.sh|tee output.log", "bash", false},
		{"explicit shell extensionless leading path", "./deploy | tee output.log", "bash", false},
		{"explicit shell compact extensionless path", "./deploy|tee", "bash", false},
		{"explicit shell leading path glob", "./scripts/*.sh", "bash", false},
		{"explicit shell leading path single-character glob", "./scripts/?.sh", "bash", false},
		{"explicit shell leading path bracket glob", "./scripts/[ab].sh", "bash", false},
		{"explicit shell leading path brace expansion", "./scripts/{a,b}.sh", "bash", false},
		{"explicit shell home expansion", "~/scripts/*.sh", "bash", false},
		{"explicit cmd environment expansion", "%TEMP%\\deploy.cmd", "cmd", false},
		{"explicit cmd delayed expansion", "!SCRIPT!", "cmd", false},
		{"explicit cmd escape", "^deploy.cmd", "cmd", false},
		{"explicit shell assignment", "PATH=./bin", "bash", false},
		{"glob without explicit shell", "./scripts/*.sh", "", true},
		{"home path without explicit shell", "~/scripts/deploy", "", true},
		{"environment path without explicit shell", "%TEMP%\\deploy", "", true},
		{"assignment path without explicit shell", "PATH=./bin", "", true},
		{
			"compact pipeline with extensionless leading path",
			".\\deploy|.\\cleanup.ps1",
			"pwsh",
			false,
		},
		{
			"explicit PowerShell pipeline with leading path",
			".\\deploy.ps1 | Out-String",
			"pwsh",
			false,
		},
		{"explicit shell path", "./missing.ps1", "pwsh", true},
		{"inline redirection", "cat<config/settings.json", "", false},
		{"explicit shell redirection", "cat<scripts/deploy.sh", "bash", false},
		{"explicit shell operator without separator", "cat<deploy.sh", "bash", false},
		{"explicit shell path with later operator", "./deploy&test.sh", "bash", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, shouldFailOnMissingScript(tt.input, tt.shell))
		})
	}
}

func TestExecAction_FileNotFoundNoInlineFallback(t *testing.T) {
	env := environment.NewWithValues("test", nil)
	kvMock := &mockExecKeyVaultService{}

	tests := [][]string{
		{"nonexistent.sh"},
		{"nonexistent.sh", "--verbose"},
		{"my scripts/deploy.sh"},
	}

	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			action := &execAction{
				env:             env,
				keyvaultService: kvMock,
				flags:           &execFlags{global: &internal.GlobalCommandOptions{}},
				args:            args,
			}

			_, err := action.Run(t.Context())
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not found")
		})
	}
}

func TestExecAction_ExplicitShellExpressionBypassesInvalidPathProbe(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows rejects shell operators as invalid filename characters")
	}

	tests := []string{
		"echo success>nul&rem deploy.cmd",
		"if exist *.txt rem deploy.cmd",
		`if exist C:\config\settings.json rem deploy.cmd`,
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			action := &execAction{
				env:             environment.NewWithValues("test", nil),
				keyvaultService: &mockExecKeyVaultService{},
				flags: &execFlags{
					global: &internal.GlobalCommandOptions{},
					shell:  "cmd",
				},
				args: []string{input},
			}

			_, err := action.Run(t.Context())
			require.NoError(t, err)
		})
	}
}

func TestExecAction_ExplicitShellExpressionWithPathArgumentBypassesInvalidPathProbe(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows rejects shell operators as invalid filename characters")
	}

	action := &execAction{
		env:             environment.NewWithValues("test", nil),
		keyvaultService: &mockExecKeyVaultService{},
		flags: &execFlags{
			global: &internal.GlobalCommandOptions{},
			shell:  "cmd",
		},
		args: []string{`echo config\settings.json|findstr settings>nul&rem deploy.cmd`},
	}

	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

func TestExecAction_ExplicitShellPipelineWithLeadingPathBypassesInvalidPathProbe(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows rejects shell operators as invalid filename characters")
	}

	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("emit.cmd", []byte("@echo success\r\n"), 0o600))

	action := &execAction{
		env:             environment.NewWithValues("test", nil),
		keyvaultService: &mockExecKeyVaultService{},
		flags: &execFlags{
			global: &internal.GlobalCommandOptions{},
			shell:  "cmd",
		},
		args: []string{`.\emit.cmd|findstr success>nul&rem deploy.cmd`},
	}

	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

func TestExecAction_ExplicitShellLeadingPathExpansionFallsBackInline(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is required for shell expansion coverage")
	}

	t.Chdir(t.TempDir())
	require.NoError(t, os.Mkdir("scripts", 0o750))
	for _, name := range []string{"a.sh", "b.sh"} {
		//nolint:gosec // G306: expanded shell fixtures must be directly executable.
		require.NoError(t, os.WriteFile(
			filepath.Join("scripts", name),
			[]byte("#!/usr/bin/env bash\nexit 0\n"),
			0o700,
		))
	}
	//nolint:gosec // G306: pipeline fixtures must be directly executable.
	require.NoError(t, os.WriteFile("emit", []byte("#!/usr/bin/env bash\necho success\n"), 0o700))
	//nolint:gosec // G306: pipeline fixtures must be directly executable.
	require.NoError(t, os.WriteFile("cleanup.sh", []byte("#!/usr/bin/env bash\ncat >/dev/null\n"), 0o700))

	for _, input := range []string{
		"./scripts/*.sh",
		"./scripts/{a,b}.sh",
		"./emit|./cleanup.sh",
	} {
		t.Run(input, func(t *testing.T) {
			action := &execAction{
				env:             environment.NewWithValues("test", nil),
				keyvaultService: &mockExecKeyVaultService{},
				flags: &execFlags{
					global: &internal.GlobalCommandOptions{},
					shell:  "bash",
				},
				args: []string{input},
			}

			_, err := action.Run(t.Context())
			require.NoError(t, err)
		})
	}
}

func TestExecAction_ExplicitShellAssignmentWithPathFallsBackInline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bare assignment syntax is POSIX-shell specific")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is required for assignment coverage")
	}

	action := &execAction{
		env:             environment.NewWithValues("test", nil),
		keyvaultService: &mockExecKeyVaultService{},
		flags: &execFlags{
			global: &internal.GlobalCommandOptions{},
			shell:  "bash",
		},
		args: []string{"PATH=./bin"},
	}

	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

func TestExecAction_ExplicitShellPreservesExistingFilePrecedence(t *testing.T) {
	shell := "bash"
	extension := ".sh"
	content := []byte("exit 0\n")
	if runtime.GOOS == "windows" {
		shell = "cmd"
		extension = ".cmd"
		content = []byte("@exit /b 0\r\n")
	}

	tempDir := t.TempDir()
	t.Chdir(tempDir)

	filenameCharacters := []string{
		"&", "(", ")", ";", "$", "'", "`", "^", "!", "[", "]", "{", "}", "~",
	}
	if runtime.GOOS != "windows" {
		// cmd file execution intentionally neutralizes %VAR% expansion, so a
		// percent sign is not a supported Windows filename case.
		filenameCharacters = append(filenameCharacters, "<", ">", "|", `"`, "%", "*", "?")
	}

	tests := []string{
		"deploy test" + extension,
		filepath.Join("scripts&tools", "deploy"+extension),
		filepath.Join("scripts(test)", "deploy"+extension),
		filepath.Join("scripts tools", "deploy"+extension),
	}
	for _, character := range filenameCharacters {
		tests = append(tests, "deploy"+character+"test"+extension)
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			scriptPath := filepath.Join(tempDir, input)
			require.NoError(t, os.MkdirAll(filepath.Dir(scriptPath), 0o750))
			require.NoError(t, os.WriteFile(scriptPath, content, 0o600))

			action := &execAction{
				env:             environment.NewWithValues("test", nil),
				keyvaultService: &mockExecKeyVaultService{},
				flags: &execFlags{
					global: &internal.GlobalCommandOptions{},
					shell:  shell,
				},
				args: []string{input},
			}

			_, err := action.Run(t.Context())
			require.NoError(t, err)
		})
	}
}

func TestExecAction_ExplicitShellPreservesDirectoryError(t *testing.T) {
	shell := "bash"
	directoryName := "deploy&test.sh"
	if runtime.GOOS == "windows" {
		shell = "cmd"
		directoryName = "deploy&test.cmd"
	}

	t.Chdir(t.TempDir())
	require.NoError(t, os.Mkdir(directoryName, 0o750))

	action := &execAction{
		env:             environment.NewWithValues("test", nil),
		keyvaultService: &mockExecKeyVaultService{},
		flags: &execFlags{
			global: &internal.GlobalCommandOptions{},
			shell:  shell,
		},
		args: []string{directoryName},
	}

	_, err := action.Run(t.Context())
	require.Error(t, err)
	valErr, ok := errors.AsType[*scripting.ValidationError](err)
	require.True(t, ok)
	assert.Contains(t, valErr.Reason, "must be a file")
}

func TestExecAction_InvalidFilenameRequiresExplicitShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows rejects shell operators as invalid filename characters")
	}

	action := &execAction{
		env:             environment.NewWithValues("test", nil),
		keyvaultService: &mockExecKeyVaultService{},
		flags:           &execFlags{global: &internal.GlobalCommandOptions{}},
		args:            []string{"cat<deploy.sh"},
	}

	_, err := action.Run(t.Context())
	require.Error(t, err)
	_, ok := errors.AsType[*scripting.ValidationError](err)
	require.True(t, ok)
}

func TestExecAction_ExplicitShellClearInvalidPathDoesNotFallback(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows rejects shell operators as invalid filename characters")
	}

	action := &execAction{
		env:             environment.NewWithValues("test", nil),
		keyvaultService: &mockExecKeyVaultService{},
		flags: &execFlags{
			global: &internal.GlobalCommandOptions{},
			shell:  "cmd",
		},
		args: []string{`scripts\deploy.cmd`},
	}

	_, err := action.Run(t.Context())
	require.Error(t, err)
	_, inline := errors.AsType[*internal.ExitCodeError](err)
	assert.False(t, inline, "clear script paths must not fall back to inline execution")
}

func TestExecAction_ExplicitShellLongCommandBypassesPathProbe(t *testing.T) {
	shell := "bash"
	inlineCommand := ": " + strings.Repeat("x", 300)
	missingScript := strings.Repeat("x", 300) + ".sh"
	if runtime.GOOS == "windows" {
		shell = "cmd"
		inlineCommand = "rem " + strings.Repeat("x", 300)
		missingScript = strings.Repeat("x", 300) + ".cmd"
	}

	t.Run("inline command", func(t *testing.T) {
		action := &execAction{
			env:             environment.NewWithValues("test", nil),
			keyvaultService: &mockExecKeyVaultService{},
			flags: &execFlags{
				global: &internal.GlobalCommandOptions{},
				shell:  shell,
			},
			args: []string{inlineCommand},
		}

		_, err := action.Run(t.Context())
		require.NoError(t, err)
	})

	t.Run("clear missing script", func(t *testing.T) {
		action := &execAction{
			env:             environment.NewWithValues("test", nil),
			keyvaultService: &mockExecKeyVaultService{},
			flags: &execFlags{
				global: &internal.GlobalCommandOptions{},
				shell:  shell,
			},
			args: []string{missingScript},
		}

		_, err := action.Run(t.Context())
		require.Error(t, err)
		_, inline := errors.AsType[*internal.ExitCodeError](err)
		assert.False(t, inline, "clear long script paths must not fall back to inline execution")
	})
}

func TestExecAction_ExecutionErrorDoesNotFallbackInline(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows command-line length errors are platform-specific")
	}

	tempDir := t.TempDir()
	t.Chdir(tempDir)
	scriptName := "deploy & test.cmd"
	require.NoError(t, os.WriteFile(scriptName, []byte("@exit /b 0\r\n"), 0o600))

	action := &execAction{
		env:             environment.NewWithValues("test", nil),
		keyvaultService: &mockExecKeyVaultService{},
		flags: &execFlags{
			global: &internal.GlobalCommandOptions{},
			shell:  "cmd",
		},
		args: []string{scriptName, strings.Repeat("x", 40_000)},
	}

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to execute script")
	assert.NotContains(t, err.Error(), "inline script")
}
