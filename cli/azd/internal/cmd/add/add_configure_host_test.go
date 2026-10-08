// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func TestPromptPort_MultiplePorts_SelectSpecific(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).Respond(1)
	prj := appdetect.Project{
		Language: appdetect.Python,
		Docker: &appdetect.Docker{
			Path:  "/app/Dockerfile",
			Ports: []appdetect.Port{{Number: 3000}, {Number: 8080}},
		},
	}
	port, err := PromptPort(c, t.Context(), "svc", prj)
	require.NoError(t, err)
	assert.Equal(t, 8080, port)
}

func TestPromptPort_MultiplePorts_OtherPrompts(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	// Select 'Other' (last option, index 2 for two ports + Other).
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).Respond(2)
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).Respond("4000")
	prj := appdetect.Project{
		Language: appdetect.Python,
		Docker: &appdetect.Docker{
			Path:  "/app/Dockerfile",
			Ports: []appdetect.Port{{Number: 3000}, {Number: 8080}},
		},
	}
	port, err := PromptPort(c, t.Context(), "svc", prj)
	require.NoError(t, err)
	assert.Equal(t, 4000, port)
}

func TestPromptPort_NoPortsExposed_PromptsNumber(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).Respond("5000")
	prj := appdetect.Project{
		Language: appdetect.Python,
		Docker:   &appdetect.Docker{Path: "/app/Dockerfile"},
	}
	port, err := PromptPort(c, t.Context(), "svc", prj)
	require.NoError(t, err)
	assert.Equal(t, 5000, port)
}

func TestPromptPort_MultiplePorts_SelectError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) { return 0, assertErr() })
	prj := appdetect.Project{
		Language: appdetect.Python,
		Docker: &appdetect.Docker{
			Path:  "/app/Dockerfile",
			Ports: []appdetect.Port{{Number: 3000}, {Number: 8080}},
		},
	}
	_, err := PromptPort(c, t.Context(), "svc", prj)
	require.Error(t, err)
}

func TestPromptPort_MultiplePorts_OtherPromptError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).Respond(2)
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) { return "", assertErr() })
	prj := appdetect.Project{
		Language: appdetect.Python,
		Docker: &appdetect.Docker{
			Path:  "/app/Dockerfile",
			Ports: []appdetect.Port{{Number: 3000}, {Number: 8080}},
		},
	}
	_, err := PromptPort(c, t.Context(), "svc", prj)
	require.Error(t, err)
}

func TestPromptPortNumber_ValidFirstTry(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).Respond("8080")
	p, err := promptPortNumber(c, t.Context(), "port?")
	require.NoError(t, err)
	assert.Equal(t, 8080, p)
}

func TestPromptPortNumber_NonIntegerThenValid(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	responses := []string{"abc", "1234"}
	i := 0
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			v := responses[i]
			i++
			return v, nil
		})
	p, err := promptPortNumber(c, t.Context(), "port?")
	require.NoError(t, err)
	assert.Equal(t, 1234, p)
}

func TestPromptPortNumber_OutOfRangeThenValid(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	responses := []string{"0", "70000", "443"}
	i := 0
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			v := responses[i]
			i++
			return v, nil
		})
	p, err := promptPortNumber(c, t.Context(), "port?")
	require.NoError(t, err)
	assert.Equal(t, 443, p)
}

func TestPromptPortNumber_PromptError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) { return "", assertErr() })
	_, err := promptPortNumber(c, t.Context(), "port?")
	require.Error(t, err)
}

func TestAddServiceAsResource_AppService_Python(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	c := newTestConsole()
	svc := &project.ServiceConfig{
		Name:         "py-svc",
		Host:         project.AppServiceTarget,
		Language:     project.ServiceLanguagePython,
		RelativePath: tempDir,
	}
	prj := appdetect.Project{Language: appdetect.Python}
	r, err := addServiceAsResource(t.Context(), c, svc, prj)
	require.NoError(t, err)
	assert.Equal(t, project.ResourceTypeHostAppService, r.Type)
	props, ok := r.Props.(project.AppServiceProps)
	require.True(t, ok)
	assert.Equal(t, 80, props.Port)
	assert.Equal(t, project.AppServiceRuntimeStackPython, props.Runtime.Stack)
}

func TestAddServiceAsResource_AppService_JavaScript(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	c := newTestConsole()
	svc := &project.ServiceConfig{
		Name:         "js-svc",
		Host:         project.AppServiceTarget,
		Language:     project.ServiceLanguageJavaScript,
		RelativePath: tempDir,
	}
	prj := appdetect.Project{Language: appdetect.JavaScript}
	r, err := addServiceAsResource(t.Context(), c, svc, prj)
	require.NoError(t, err)
	props, ok := r.Props.(project.AppServiceProps)
	require.True(t, ok)
	assert.Equal(t, project.AppServiceRuntimeStackNode, props.Runtime.Stack)
	assert.Equal(t, 80, props.Port)
}

func TestAddServiceAsResource_UnsupportedHost(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	c := newTestConsole()
	svc := &project.ServiceConfig{
		Name:         "svc",
		Host:         project.ServiceTargetKind("bogus"),
		Language:     project.ServiceLanguageJavaScript,
		RelativePath: tempDir,
	}
	prj := appdetect.Project{Language: appdetect.JavaScript}
	_, err := addServiceAsResource(t.Context(), c, svc, prj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported service target")
}

func TestAddServiceAsResource_FunctionApp(t *testing.T) {
	for language, runtime := range functionRuntimeByLanguage {
		t.Run(string(language), func(t *testing.T) {
			svc := &project.ServiceConfig{
				Name: "func", Host: project.AzureFunctionTarget, Language: language,
			}
			resource, err := addServiceAsResource(t.Context(), newTestConsole(), svc, appdetect.Project{})
			require.NoError(t, err)
			assert.Equal(t, project.ResourceTypeHostFunctionApp, resource.Type)
			assert.Equal(t, project.FunctionAppProps{Runtime: runtime}, resource.Props)
		})
	}
}

func TestValidateFunctionCodeProject(t *testing.T) {
	tests := []struct {
		name     string
		language appdetect.Language
		project  string
		wantErr  string
	}{
		{"Python", appdetect.Python, "", ""},
		{"isolated .NET", appdetect.DotNet,
			`<Project Sdk="Microsoft.NET.Sdk"><PackageReference Include="Microsoft.Azure.Functions.Worker" /></Project>`,
			""},
		{"in-process .NET", appdetect.DotNet,
			`<Project Sdk="Microsoft.NET.Sdk.Functions"></Project>`, "requires a .NET isolated Function App"},
		{"unsupported", appdetect.Language("unknown"), "", "unsupported Function App language"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "host.json"), []byte("{}"), 0o600))
			if tt.project != "" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "func.csproj"), []byte(tt.project), 0o600))
			}
			err := validateFunctionCodeProject(&appdetect.Project{Path: dir, Language: tt.language})
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
	t.Run("missing host.json", func(t *testing.T) {
		err := validateFunctionCodeProject(&appdetect.Project{Path: t.TempDir(), Language: appdetect.Go})
		require.ErrorContains(t, err, "no host.json")
	})
	t.Run("host.json is a directory", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "host.json"), 0o700))
		err := validateFunctionCodeProject(&appdetect.Project{Path: dir, Language: appdetect.Go})
		require.ErrorContains(t, err, "host.json must be a file")
	})
	t.Run("in-process .NET with host.json", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "host.json"), []byte("{}"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "func.csproj"),
			[]byte(`<Project Sdk="Microsoft.NET.Sdk.Functions"></Project>`), 0o600))
		err := validateFunctionCodeProject(&appdetect.Project{Path: dir, Language: appdetect.DotNet})
		require.ErrorContains(t, err, "requires a .NET isolated Function App")
	})
}

func TestPromptCodeProject_GoFunctionApp(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/func\n"), 0o600))
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) { return dir, nil }
	prj, err := (&AddAction{console: c}).promptCodeProject(t.Context(), project.ResourceTypeHostFunctionApp)
	require.NoError(t, err)
	require.Equal(t, appdetect.Go, prj.Language)
}

func TestValidateFunctionCodeProject_DotNetProjectTypes(t *testing.T) {
	for _, extension := range []string{".csproj", ".fsproj", ".vbproj"} {
		for _, inProcess := range []bool{false, true} {
			name := extension + "/isolated"
			packageName := "Microsoft.Azure.Functions.Worker"
			if inProcess {
				name = extension + "/in-process"
				packageName = "Microsoft.NET.Sdk.Functions"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "host.json"), []byte("{}"), 0o600))
				content := `<Project Sdk="Microsoft.NET.Sdk"><ItemGroup><PackageReference Include="` +
					packageName + `" /></ItemGroup></Project>`
				require.NoError(t, os.WriteFile(filepath.Join(dir, "func"+extension), []byte(content), 0o600))
				err := validateFunctionCodeProject(&appdetect.Project{Path: dir, Language: appdetect.DotNet})
				if inProcess {
					require.ErrorContains(t, err, "requires a .NET isolated Function App")
					require.ErrorContains(t, err, "func"+extension)
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
}

func TestPromptCodeProject_FallbackLanguageSelection(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	// Write a requirements.txt so Python selection succeeds.
	writeFile(t, filepath_join(tempDir, "requirements.txt"), "flask\n")
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		return tempDir, nil
	}
	// Respond Select with index 0 — whatever language first lands alphabetically.
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			// Pick the first Python-tagged option to get requirements.txt path exercised;
			// fall back to 0 if not found.
			for i, o := range opts.Options {
				if containsCI(o, "Python") {
					return i, nil
				}
			}
			return 0, nil
		})
	a := &AddAction{console: c}
	prj, err := a.promptCodeProject(t.Context())
	require.NoError(t, err)
	require.NotNil(t, prj)
	assert.Equal(t, tempDir, prj.Path)
}

func TestPromptCodeProject_PromptDirError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		return "", assertErr()
	}
	a := &AddAction{console: c}
	_, err := a.promptCodeProject(t.Context())
	require.Error(t, err)
}

func TestPromptCodeProject_ManualFallback_Java(t *testing.T) {
	t.Parallel()
	// Empty dir so appdetect returns nil.
	tempDir := t.TempDir()
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		return tempDir, nil
	}
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			// Pick a non-Python language to avoid requirements.txt check.
			for i, o := range opts.Options {
				if containsCI(o, "Java") && !containsCI(o, "JavaScript") {
					return i, nil
				}
			}
			return 0, nil
		})
	a := &AddAction{console: c}
	prj, err := a.promptCodeProject(t.Context())
	require.NoError(t, err)
	require.NotNil(t, prj)
	assert.Equal(t, "Manual", prj.DetectionRule)
}

func TestPromptCodeProject_ManualFallback_InteractiveTabAlign(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	c := newTestConsole()
	c.MockConsole.SetTerminal(true)
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		return tempDir, nil
	}
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			return 0, nil
		})
	a := &AddAction{console: c}
	prj, err := a.promptCodeProject(t.Context())
	// Either Python without requirements.txt (error) or non-Python success.
	// Both exercise the TabAlign path.
	if err == nil {
		require.NotNil(t, prj)
	} else {
		assert.Contains(t, err.Error(), "requirements.txt")
	}
}

func TestPromptCodeProject_ManualFallback_SelectError(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		return tempDir, nil
	}
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) { return 0, assertErr() })
	a := &AddAction{console: c}
	_, err := a.promptCodeProject(t.Context())
	require.Error(t, err)
}
