// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"path/filepath"
	"testing"

	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// evalServiceNamed is a declared eval service pointing at one configuration.
func evalServiceNamed(t *testing.T, name, ref string) *azdext.ServiceConfig {
	t.Helper()
	svc := serviceWithRef(t, ref)
	svc.Name = name
	svc.Host = project.EvalHost
	return svc
}

// wiredProject is a project root and the configuration path under it a scaffold
// would be written to.
//
// A real directory rather than a literal like /proj: refTo makes the path
// absolute before rebasing it, and on Windows that prefixes the current drive,
// so a rooted-but-volumeless fixture compares two volumes and never matches.
func wiredProject(t *testing.T, services map[string]*azdext.ServiceConfig) (*azdext.ProjectConfig, string) {
	t.Helper()
	root := t.TempDir()
	return &azdext.ProjectConfig{Path: root, Services: services},
		filepath.Join(root, "evals", "azure.eval.yaml")
}

// The service name is derived from --target, so pointing a second agent at a
// configuration that is already wired asked about a name that did not exist yet
// and `init` added a second service beside the first. Both pointed at the same
// file, so every `azd up` deployed those evals twice.
//
// What decides this is the file a service points at, not the key it was
// declared under.
func TestASecondTargetReusesTheServiceAlreadyPointingAtTheConfiguration(t *testing.T) {
	proj, configPath := wiredProject(t, map[string]*azdext.ServiceConfig{
		"first-agent-evals": evalServiceNamed(t, "first-agent-evals", "./evals/azure.eval.yaml"),
	})

	action, name, err := rootEvalServiceAction(proj, "second-agent-evals", configPath)

	require.NoError(t, err)
	assert.Equal(t, wiringPresent, action,
		"the configuration is already wired, so there is no second service to add")
	assert.Equal(t, "first-agent-evals", name,
		"the reused service keeps its own key; reporting the derived name would name one that does not exist")
}

// The same file written two ways is still the same file, so the reuse survives
// the spellings `init` and a hand-edited azure.yaml each produce.
func TestReuseComparesTheRefAsAPathNotAsText(t *testing.T) {
	for _, declared := range []string{
		"evals/azure.eval.yaml",
		"./evals/azure.eval.yaml",
		"./evals/../evals/azure.eval.yaml",
		"", // stands for the absolute form, which needs the root to spell
	} {
		t.Run(declared, func(t *testing.T) {
			root := t.TempDir()
			configPath := filepath.Join(root, "evals", "azure.eval.yaml")
			ref := declared
			if ref == "" {
				ref = filepath.ToSlash(configPath)
			}
			proj := &azdext.ProjectConfig{
				Path:     root,
				Services: map[string]*azdext.ServiceConfig{"evals": evalServiceNamed(t, "evals", ref)},
			}

			action, name, err := rootEvalServiceAction(proj, "agent-evals", configPath)

			require.NoError(t, err)
			assert.Equal(t, wiringPresent, action)
			assert.Equal(t, "evals", name)
		})
	}
}

// A service pointing at a different configuration is not this one, so it is
// still added rather than silently reused.
func TestAServicePointingSomewhereElseIsNotReused(t *testing.T) {
	proj, configPath := wiredProject(t, map[string]*azdext.ServiceConfig{
		"other-evals": evalServiceNamed(t, "other-evals", "./quality/azure.eval.yaml"),
	})

	action, name, err := rootEvalServiceAction(proj, "agent-evals", configPath)

	require.NoError(t, err)
	assert.Equal(t, wiringAdded, action)
	assert.Equal(t, "agent-evals", name)
}

// An eval service holding its configuration inline points at no file, so it
// cannot be mistaken for the one being scaffolded.
func TestAnInlineEvalServiceIsNotReused(t *testing.T) {
	proj, configPath := wiredProject(t, map[string]*azdext.ServiceConfig{
		"inline-evals": {Name: "inline-evals", Host: project.EvalHost},
	})

	action, name, err := rootEvalServiceAction(proj, "agent-evals", configPath)

	require.NoError(t, err)
	assert.Equal(t, wiringAdded, action)
	assert.Equal(t, "agent-evals", name)
}

// A service under the derived name that already points at this configuration
// keeps that name rather than being traded for whichever other one sorts first.
func TestTheDerivedNameWinsWhenItAlreadyPointsAtTheConfiguration(t *testing.T) {
	proj, configPath := wiredProject(t, map[string]*azdext.ServiceConfig{
		"a-evals":     evalServiceNamed(t, "a-evals", "./evals/azure.eval.yaml"),
		"agent-evals": evalServiceNamed(t, "agent-evals", "./evals/azure.eval.yaml"),
	})

	_, name, err := rootEvalServiceAction(proj, "agent-evals", configPath)

	require.NoError(t, err)
	assert.Equal(t, "agent-evals", name)
}

// A service this extension does not own is never reused, whatever it points at:
// AddService assigns by name, so treating it as ours would replace it.
func TestAForeignServiceIsNeverReusedForItsRef(t *testing.T) {
	foreign := serviceWithRef(t, "./evals/azure.eval.yaml")
	foreign.Name = "web"
	foreign.Host = "containerapp"
	proj, configPath := wiredProject(t, map[string]*azdext.ServiceConfig{"web": foreign})

	action, name, err := rootEvalServiceAction(proj, "agent-evals", configPath)

	require.NoError(t, err)
	assert.Equal(t, wiringAdded, action)
	assert.Equal(t, "agent-evals", name, "the eval service is added under its own name, beside the other host")
}
