// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
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
