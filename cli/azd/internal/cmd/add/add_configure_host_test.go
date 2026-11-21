// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
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
