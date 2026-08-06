// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func autoInstallTestExtension(
	id string,
	name string,
	source string,
	category extensions.SourceCategory,
) *extensions.ExtensionMetadata {
	return &extensions.ExtensionMetadata{
		Id:             id,
		DisplayName:    name,
		Description:    name + " description",
		Source:         source,
		SourceCategory: category,
		Versions:       []extensions.ExtensionVersion{{Version: "1.2.3"}},
	}
}

func autoInstallTestRequirement(
	candidates ...*extensions.ExtensionMetadata,
) projectExtensionRequirement {
	return projectExtensionRequirement{
		extension:  candidates[0],
		candidates: candidates,
	}
}

func TestRecommendedSourceCandidate(t *testing.T) {
	t.Parallel()

	officialAlias := autoInstallTestExtension(
		"demo",
		"Demo",
		"official",
		extensions.SourceCategoryAzd,
	)
	azd := autoInstallTestExtension("demo", "Demo", "azd", extensions.SourceCategoryAzd)
	local := autoInstallTestExtension("demo", "Demo", "local", extensions.SourceCategoryLocal)

	tests := []struct {
		name       string
		candidates []*extensions.ExtensionMetadata
		expected   *extensions.ExtensionMetadata
	}{
		{
			name:       "unique official source",
			candidates: []*extensions.ExtensionMetadata{local, officialAlias},
			expected:   officialAlias,
		},
		{
			name:       "literal azd wins among aliases",
			candidates: []*extensions.ExtensionMetadata{officialAlias, azd, local},
			expected:   azd,
		},
		{
			name: "ambiguous official aliases",
			candidates: []*extensions.ExtensionMetadata{
				officialAlias,
				autoInstallTestExtension("demo", "Demo", "mirror", extensions.SourceCategoryAzd),
			},
		},
		{
			name:       "no official source",
			candidates: []*extensions.ExtensionMetadata{local},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			requirement := autoInstallTestRequirement(tt.candidates...)
			assert.Same(t, tt.expected, recommendedSourceCandidate(requirement))
		})
	}
}

func TestCommonRecommendedSourceRequiresSameConfiguredName(t *testing.T) {
	t.Parallel()

	requirements := []projectExtensionRequirement{
		autoInstallTestRequirement(
			autoInstallTestExtension("demo", "Demo", "azd", extensions.SourceCategoryAzd),
		),
		autoInstallTestRequirement(
			autoInstallTestExtension("storage", "Storage", "official", extensions.SourceCategoryAzd),
		),
	}

	source, ok := commonRecommendedSource(requirements)

	assert.False(t, ok)
	assert.Empty(t, source)
}

func TestInteractiveSingleInstallPlan(t *testing.T) {
	t.Parallel()

	azd := autoInstallTestExtension("demo", "Demo Extension", "azd", extensions.SourceCategoryAzd)
	local := autoInstallTestExtension("demo", "Demo Extension", "local", extensions.SourceCategoryLocal)

	t.Run("single source confirms", func(t *testing.T) {
		t.Parallel()
		console := mockinput.NewMockConsole()
		console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return options.Message == "Install Demo Extension?"
		}).Respond(true)

		selections, declined, err := interactiveSingleInstallPlan(
			t.Context(),
			console,
			autoInstallTestRequirement(azd),
		)

		require.NoError(t, err)
		require.False(t, declined)
		require.Len(t, selections, 1)
		assert.Same(t, azd, selections[0].extension)
	})

	t.Run("recommended source selected", func(t *testing.T) {
		t.Parallel()
		console := mockinput.NewMockConsole()
		console.WhenSelect(func(options input.ConsoleOptions) bool {
			return options.Message == "Install Demo Extension from 'azd'" &&
				options.EnableFiltering != nil && !*options.EnableFiltering
		}).Respond(0)

		selections, declined, err := interactiveSingleInstallPlan(
			t.Context(),
			console,
			autoInstallTestRequirement(local, azd),
		)

		require.NoError(t, err)
		require.False(t, declined)
		require.Len(t, selections, 1)
		assert.Same(t, azd, selections[0].extension)
	})

	t.Run("different source selected", func(t *testing.T) {
		t.Parallel()
		console := mockinput.NewMockConsole()
		console.WhenSelect(func(options input.ConsoleOptions) bool {
			return options.Message == "Install Demo Extension from 'azd'"
		}).Respond(1)
		console.WhenSelect(func(options input.ConsoleOptions) bool {
			return options.Message == "Select a source for Demo Extension" &&
				assert.Equal(t, []string{"azd", "local"}, options.Options)
		}).Respond(1)

		selections, declined, err := interactiveSingleInstallPlan(
			t.Context(),
			console,
			autoInstallTestRequirement(local, azd),
		)

		require.NoError(t, err)
		require.False(t, declined)
		require.Len(t, selections, 1)
		assert.Same(t, local, selections[0].extension)
	})

	t.Run("cancel stops the plan", func(t *testing.T) {
		t.Parallel()
		console := mockinput.NewMockConsole()
		console.WhenSelect(func(options input.ConsoleOptions) bool {
			return options.Message == "Install Demo Extension from 'azd'"
		}).Respond(2)

		selections, declined, err := interactiveSingleInstallPlan(
			t.Context(),
			console,
			autoInstallTestRequirement(local, azd),
		)

		require.NoError(t, err)
		assert.True(t, declined)
		assert.Empty(t, selections)
	})
}

func TestInteractiveMultipleInstallPlanDifferentSourceShortcut(t *testing.T) {
	t.Parallel()

	requirements := []projectExtensionRequirement{
		autoInstallTestRequirement(
			autoInstallTestExtension("demo", "Demo Extension", "azd", extensions.SourceCategoryAzd),
			autoInstallTestExtension("demo", "Demo Extension", "local", extensions.SourceCategoryLocal),
		),
		autoInstallTestRequirement(
			autoInstallTestExtension("storage", "Storage Helper", "azd", extensions.SourceCategoryAzd),
			autoInstallTestExtension("storage", "Storage Helper", "local", extensions.SourceCategoryLocal),
		),
		autoInstallTestRequirement(
			autoInstallTestExtension("monitor", "Monitoring Tools", "azd", extensions.SourceCategoryAzd),
			autoInstallTestExtension("monitor", "Monitoring Tools", "local", extensions.SourceCategoryLocal),
		),
	}
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Install all 3 required extensions from 'azd'" &&
			options.EnableFiltering != nil && !*options.EnableFiltering
	}).Respond(1)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select a source for Demo Extension"
	}).Respond(1)
	console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return options.Message == "Install remaining extensions from 'local'?"
	}).Respond(true)

	selections, declined, err := interactiveMultipleInstallPlan(t.Context(), console, requirements)

	require.NoError(t, err)
	require.False(t, declined)
	require.Len(t, selections, 3)
	for _, selection := range selections {
		assert.Equal(t, "local", selection.extension.Source)
	}
}

func TestInteractiveMultipleInstallPlanFallsBackToIndividualSources(t *testing.T) {
	t.Parallel()

	requirements := []projectExtensionRequirement{
		autoInstallTestRequirement(
			autoInstallTestExtension("demo", "Demo Extension", "azd", extensions.SourceCategoryAzd),
			autoInstallTestExtension("demo", "Demo Extension", "local", extensions.SourceCategoryLocal),
		),
		autoInstallTestRequirement(
			autoInstallTestExtension("storage", "Storage Helper", "azd", extensions.SourceCategoryAzd),
			autoInstallTestExtension("storage", "Storage Helper", "private", extensions.SourceCategoryOther),
		),
	}
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Install all 2 required extensions from 'azd'"
	}).Respond(1)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select a source for Demo Extension"
	}).Respond(1)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select a source for Storage Helper"
	}).Respond(1)

	selections, declined, err := interactiveMultipleInstallPlan(t.Context(), console, requirements)

	require.NoError(t, err)
	require.False(t, declined)
	require.Len(t, selections, 2)
	assert.Equal(t, "local", selections[0].extension.Source)
	assert.Equal(t, "private", selections[1].extension.Source)
}

func TestAutoInstallExtensionRequirementsDeclined(t *testing.T) {
	clearAgentEnvVarsForTest(t)

	extension := autoInstallTestExtension("demo", "Demo Extension", "azd", extensions.SourceCategoryAzd)
	console := mockinput.NewMockConsole()
	console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return options.Message == "Install Demo Extension?"
	}).Respond(false)
	manager := &fakeExtensionAutoInstallManager{installed: map[string]*extensions.Extension{}}

	result, err := autoInstallExtensionRequirements(
		t.Context(),
		console,
		manager,
		[]projectExtensionRequirement{autoInstallTestRequirement(extension)},
		autoInstallDisplayContext{requiredByProject: true},
	)

	require.NoError(t, err)
	assert.True(t, result.declined)
	assert.False(t, result.installed)
	assert.Contains(t, strings.Join(console.Output(), "\n"), "Canceled: required extension isn't installed.")
	assert.Empty(t, manager.installed)
}

func TestAutoInstallExtensionRequirementsNoPrompt(t *testing.T) {
	clearAgentEnvVarsForTest(t)

	requirements := []projectExtensionRequirement{
		autoInstallTestRequirement(
			autoInstallTestExtension("demo", "Demo Extension", "azd", extensions.SourceCategoryAzd),
		),
		autoInstallTestRequirement(
			autoInstallTestExtension("storage", "Storage Helper", "local", extensions.SourceCategoryLocal),
		),
	}
	console := mockinput.NewMockConsole()
	console.SetNoPromptMode(true)
	manager := &fakeExtensionAutoInstallManager{installed: map[string]*extensions.Extension{}}

	result, err := autoInstallExtensionRequirements(
		t.Context(),
		console,
		manager,
		requirements,
		autoInstallDisplayContext{requiredByProject: true},
	)

	require.NoError(t, err)
	assert.True(t, result.installed)
	assert.Contains(
		t,
		strings.Join(console.Output(), "\n"),
		"No-prompt mode: installing required extensions automatically.",
	)
	require.Len(t, console.SpinnerOps(), 4)
	assert.Equal(t, input.StepDone, console.SpinnerOps()[1].Format)
	assert.Contains(t, console.SpinnerOps()[1].Message, "(1.2.3)")
	assert.NotContains(t, console.SpinnerOps()[1].Message, " from ")
	assert.Equal(t, input.StepDone, console.SpinnerOps()[3].Format)
	require.NotEmpty(t, console.Output())
	assert.Empty(t, console.Output()[len(console.Output())-1])
}

func TestAutoInstallExtensionRequirementsInstallFailure(t *testing.T) {
	clearAgentEnvVarsForTest(t)

	extension := autoInstallTestExtension("demo", "Demo Extension", "azd", extensions.SourceCategoryAzd)
	console := mockinput.NewMockConsole()
	console.SetNoPromptMode(true)
	manager := &fakeExtensionAutoInstallManager{
		installed:  map[string]*extensions.Extension{},
		installErr: errors.New("download failed"),
	}

	result, err := autoInstallExtensionRequirements(
		t.Context(),
		console,
		manager,
		[]projectExtensionRequirement{autoInstallTestRequirement(extension)},
		autoInstallDisplayContext{},
	)

	require.ErrorContains(t, err, "failed to install extension: download failed")
	assert.False(t, result.installed)
	require.Len(t, console.SpinnerOps(), 2)
	assert.Equal(t, input.StepFailed, console.SpinnerOps()[1].Format)
	assert.Equal(t, "Installing extension 'demo'", console.SpinnerOps()[1].Message)
}

func TestAutoInstallExtensionRequirementsNoPromptAmbiguous(t *testing.T) {
	clearAgentEnvVarsForTest(t)

	azd := autoInstallTestExtension("demo", "Demo Extension", "azd", extensions.SourceCategoryAzd)
	local := autoInstallTestExtension("demo", "Demo Extension", "local", extensions.SourceCategoryLocal)
	console := mockinput.NewMockConsole()
	console.SetNoPromptMode(true)
	manager := &fakeExtensionAutoInstallManager{installed: map[string]*extensions.Extension{}}

	result, err := autoInstallExtensionRequirements(
		t.Context(),
		console,
		manager,
		[]projectExtensionRequirement{autoInstallTestRequirement(azd, local)},
		autoInstallDisplayContext{},
	)

	assert.False(t, result.installed)
	suggestionErr, ok := errors.AsType[*internal.ErrorWithSuggestion](err)
	require.True(t, ok)
	assert.Contains(t, suggestionErr.Suggestion, "Choose one source for demo:")
	assert.Contains(t, suggestionErr.Suggestion, "azd extension install demo --source azd --version 1.2.3")
	assert.Contains(t, suggestionErr.Suggestion, "azd extension install demo --source local --version 1.2.3")
	assert.Empty(t, manager.installed)
}

func TestAutoInstallExtensionRequirementsShowsSourceForMultiSourcePlan(t *testing.T) {
	clearAgentEnvVarsForTest(t)

	azd := autoInstallTestExtension("demo", "Demo Extension", "azd", extensions.SourceCategoryAzd)
	local := autoInstallTestExtension("demo", "Demo Extension", "local", extensions.SourceCategoryLocal)
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Install Demo Extension from 'azd'"
	}).Respond(0)
	manager := &fakeExtensionAutoInstallManager{installed: map[string]*extensions.Extension{}}

	result, err := autoInstallExtensionRequirements(
		t.Context(),
		console,
		manager,
		[]projectExtensionRequirement{autoInstallTestRequirement(azd, local)},
		autoInstallDisplayContext{},
	)

	require.NoError(t, err)
	assert.True(t, result.installed)
	require.Len(t, console.SpinnerOps(), 2)
	assert.Contains(t, console.SpinnerOps()[1].Message, "(1.2.3) from 'azd'")
}

func TestDisplayExtensionRequirements(t *testing.T) {
	t.Parallel()

	t.Run("single project requirement", func(t *testing.T) {
		t.Parallel()
		console := mockinput.NewMockConsole()
		displayExtensionRequirements(
			t.Context(),
			console,
			[]projectExtensionRequirement{autoInstallTestRequirement(
				autoInstallTestExtension("demo", "Demo Extension", "azd", extensions.SourceCategoryAzd),
			)},
			autoInstallDisplayContext{requiredByProject: true},
		)

		output := strings.Join(console.Output(), "\n")
		require.NotEmpty(t, console.Output())
		assert.NotEmpty(t, console.Output()[0])
		assert.Contains(t, output, "Extension required by azure.yaml: Demo Extension")
		assert.Contains(t, output, "ID:")
		assert.Contains(t, output, "Source:")
		assert.NotContains(t, output, "\nRequired by azure.yaml.")
		assert.Empty(t, console.Output()[len(console.Output())-1])
	})

	t.Run("multiple requirements use sources heading", func(t *testing.T) {
		t.Parallel()
		console := mockinput.NewMockConsole()
		displayExtensionRequirements(
			t.Context(),
			console,
			[]projectExtensionRequirement{
				autoInstallTestRequirement(
					autoInstallTestExtension("demo", "Demo Extension", "azd", extensions.SourceCategoryAzd),
					autoInstallTestExtension("demo", "Demo Extension", "local", extensions.SourceCategoryLocal),
				),
				autoInstallTestRequirement(
					autoInstallTestExtension("storage", "Storage Helper", "azd", extensions.SourceCategoryAzd),
				),
			},
			autoInstallDisplayContext{requiredByProject: true},
		)

		output := strings.Join(console.Output(), "\n")
		require.NotEmpty(t, console.Output())
		assert.NotEmpty(t, console.Output()[0])
		assert.Contains(t, output, "2 extensions required by azure.yaml:")
		assert.Contains(t, output, "Extension")
		assert.Contains(t, output, "ID")
		assert.Contains(t, output, "Sources")
		assert.Contains(t, output, "Demo Extension")
		assert.Contains(t, output, "azd, local")
		assert.Empty(t, console.Output()[len(console.Output())-1])
	})
}

func TestAutoInstallExtensionRequirementsCIListsAllRequirements(t *testing.T) {
	clearAgentEnvVarsForTest(t)
	t.Setenv("CI", "true")

	requirements := []projectExtensionRequirement{
		autoInstallTestRequirement(
			autoInstallTestExtension("demo", "Demo Extension", "azd", extensions.SourceCategoryAzd),
		),
		autoInstallTestRequirement(
			autoInstallTestExtension("storage", "Storage Helper", "local", extensions.SourceCategoryLocal),
		),
	}
	console := mockinput.NewMockConsole()
	manager := &fakeExtensionAutoInstallManager{installed: map[string]*extensions.Extension{}}

	_, err := autoInstallExtensionRequirements(
		t.Context(),
		console,
		manager,
		requirements,
		autoInstallDisplayContext{requiredByProject: true},
	)

	suggestionErr, ok := errors.AsType[*internal.ErrorWithSuggestion](err)
	require.True(t, ok)
	assert.Equal(t, "Auto-installation is not supported in CI/CD environments.", suggestionErr.Message)
	assert.Contains(t, suggestionErr.Suggestion, "azd extension install demo --source azd --version 1.2.3")
	assert.Contains(t, suggestionErr.Suggestion, "azd extension install storage --source local --version 1.2.3")
	assert.Empty(t, manager.installed)
}
