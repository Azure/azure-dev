// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/resource"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type autoInstallDisplayContext struct {
	requiredByProject bool
}

type autoInstallResult struct {
	installed bool
	declined  bool
}

type extensionInstallSelection struct {
	requirement projectExtensionRequirement
	extension   *extensions.ExtensionMetadata
}

func autoInstallCommandMatches(
	ctx context.Context,
	console input.Console,
	extensionManager extensionAutoInstallManager,
	matches []*extensions.ExtensionMetadata,
	intro string,
) (autoInstallResult, error) {
	console.Message(ctx, intro)
	console.Message(ctx, "")
	candidates, err := chooseLogicalExtensionCandidates(ctx, console, matches)
	if err != nil {
		return autoInstallResult{}, err
	}

	return autoInstallExtensionRequirements(
		ctx,
		console,
		extensionManager,
		[]projectExtensionRequirement{{
			extension:  candidates[0],
			candidates: candidates,
		}},
		autoInstallDisplayContext{},
	)
}

func autoInstallExtensionRequirements(
	ctx context.Context,
	console input.Console,
	extensionManager extensionAutoInstallManager,
	requirements []projectExtensionRequirement,
	display autoInstallDisplayContext,
) (autoInstallResult, error) {
	if len(requirements) == 0 {
		return autoInstallResult{}, nil
	}

	displayExtensionRequirements(ctx, console, requirements, display)

	if resource.IsRunningOnCI() {
		return autoInstallResult{}, manualInstallError(
			requirements,
			"Auto-installation is not supported in CI/CD environments.",
		)
	}

	var selections []extensionInstallSelection
	var declined bool
	var err error
	if console.IsNoPromptMode() {
		selections, err = noPromptInstallPlan(requirements)
		if err == nil {
			console.Message(ctx, "\nNo-prompt mode: installing required extensions automatically.")
		}
	} else {
		selections, declined, err = interactiveInstallPlan(ctx, console, requirements)
	}
	if err != nil {
		return autoInstallResult{}, err
	}
	if declined {
		console.Message(ctx, "\nCanceled: required extension isn't installed.")
		return autoInstallResult{declined: true}, nil
	}

	selections, err = orderInstallSelections(selections)
	if err != nil {
		return autoInstallResult{}, err
	}

	console.Message(ctx, "")
	installedAny := false
	for _, selection := range selections {
		installed, err := tryAutoInstallExtensionVersion(
			ctx,
			console,
			extensionManager,
			*selection.extension,
			selection.requirement.versionPreference,
		)
		if err != nil {
			return autoInstallResult{installed: installedAny}, err
		}
		installedAny = installedAny || installed
	}
	if installedAny {
		console.Message(ctx, "")
	}

	return autoInstallResult{installed: installedAny}, nil
}

func orderInstallSelections(
	selections []extensionInstallSelection,
) ([]extensionInstallSelection, error) {
	byID := make(map[string]extensionInstallSelection, len(selections))
	for _, selection := range selections {
		byID[strings.ToLower(selection.extension.Id)] = selection
	}

	const (
		selectionVisiting = iota + 1
		selectionVisited
	)
	state := make(map[string]int, len(selections))
	ordered := make([]extensionInstallSelection, 0, len(selections))
	var visit func(extensionInstallSelection) error
	visit = func(selection extensionInstallSelection) error {
		id := strings.ToLower(selection.extension.Id)
		switch state[id] {
		case selectionVisited:
			return nil
		case selectionVisiting:
			// Installation reports dependency cycles. Avoid recursing forever while
			// preserving a deterministic plan for the manager to validate.
			return nil
		}
		state[id] = selectionVisiting

		version, err := extensions.ResolveExtensionVersion(
			selection.extension,
			selection.requirement.versionPreference,
			nil,
		)
		if err != nil {
			return fmt.Errorf("resolving required extension %s: %w", selection.extension.Id, err)
		}
		for _, dependency := range version.Dependencies {
			if dependencySelection, selected := byID[strings.ToLower(dependency.Id)]; selected {
				if err := visit(dependencySelection); err != nil {
					return err
				}
			}
		}

		state[id] = selectionVisited
		ordered = append(ordered, selection)
		return nil
	}

	for _, selection := range selections {
		if err := visit(selection); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func displayExtensionRequirements(
	ctx context.Context,
	console input.Console,
	requirements []projectExtensionRequirement,
	display autoInstallDisplayContext,
) {
	if len(requirements) == 1 {
		requirement := requirements[0]
		extension := requirement.extension
		header := "Extension required: %s"
		if display.requiredByProject {
			header = "Extension required by azure.yaml: %s"
		}
		console.Message(ctx, output.WithHighLightFormat(header, extension.DisplayName))

		var details strings.Builder
		tabs := tabwriter.NewWriter(&details, 0, 4, 2, ' ', 0)
		fmt.Fprintf(tabs, "  ID:\t%s\n", extension.Id)
		sourceLabel := "Source"
		if len(requirementCandidates(requirement)) > 1 {
			sourceLabel = "Sources"
		}
		fmt.Fprintf(tabs, "  %s:\t%s\n", sourceLabel, sourceSummary(requirement, false))
		fmt.Fprintf(tabs, "  Description:\t%s\n", extension.Description)
		_ = tabs.Flush()
		console.Message(ctx, strings.TrimRight(details.String(), "\n"))
		console.Message(ctx, "")
		return
	}

	if display.requiredByProject {
		console.Message(
			ctx,
			output.WithHighLightFormat("%d extensions required by azure.yaml:", len(requirements)),
		)
	} else {
		console.Message(ctx, output.WithHighLightFormat("%d extensions required:", len(requirements)))
	}
	console.Message(ctx, "")

	usePluralSource := slices.ContainsFunc(requirements, func(requirement projectExtensionRequirement) bool {
		return len(requirementCandidates(requirement)) > 1
	})
	sourceHeading := "Source"
	if usePluralSource {
		sourceHeading = "Sources"
	}

	var table strings.Builder
	tabs := tabwriter.NewWriter(&table, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tabs, "  Extension\tID\t%s\n", sourceHeading)
	for _, requirement := range requirements {
		fmt.Fprintf(
			tabs,
			"  %s\t%s\t%s\n",
			requirement.extension.DisplayName,
			requirement.extension.Id,
			sourceSummary(requirement, true),
		)
	}
	_ = tabs.Flush()
	tableOutput := strings.TrimRight(table.String(), "\n")
	if headerEnd := strings.IndexByte(tableOutput, '\n'); headerEnd >= 0 {
		tableOutput = output.WithGrayFormat(tableOutput[:headerEnd]) + tableOutput[headerEnd:]
	} else {
		tableOutput = output.WithGrayFormat(tableOutput)
	}
	console.Message(ctx, tableOutput)
	console.Message(ctx, "")
}

func sourceSummary(requirement projectExtensionRequirement, compact bool) string {
	candidates := sortedRequirementCandidates(requirement)
	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		names = append(names, candidate.Source)
	}
	if !compact || len(names) <= 3 {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s %s", names[0], output.WithGrayFormat("(+%d more)", len(names)-1))
}

func sortedRequirementCandidates(
	requirement projectExtensionRequirement,
) []*extensions.ExtensionMetadata {
	candidates := slices.Clone(requirementCandidates(requirement))
	recommended := recommendedSourceCandidate(requirement)
	slices.SortFunc(candidates, func(a, b *extensions.ExtensionMetadata) int {
		switch {
		case recommended != nil && a == recommended:
			return -1
		case recommended != nil && b == recommended:
			return 1
		default:
			return strings.Compare(strings.ToLower(a.Source), strings.ToLower(b.Source))
		}
	})
	return candidates
}

func recommendedSourceCandidate(
	requirement projectExtensionRequirement,
) *extensions.ExtensionMetadata {
	official := slices.DeleteFunc(
		slices.Clone(requirementCandidates(requirement)),
		func(candidate *extensions.ExtensionMetadata) bool {
			return candidate.SourceCategoryOrUnknown() != extensions.SourceCategoryAzd
		},
	)
	if len(official) == 1 {
		return official[0]
	}

	namedAzd := slices.DeleteFunc(official, func(candidate *extensions.ExtensionMetadata) bool {
		return !strings.EqualFold(candidate.Source, "azd")
	})
	if len(namedAzd) == 1 {
		return namedAzd[0]
	}
	return nil
}

func noPromptInstallPlan(
	requirements []projectExtensionRequirement,
) ([]extensionInstallSelection, error) {
	for _, requirement := range requirements {
		if len(requirementCandidates(requirement)) != 1 {
			return nil, manualInstallError(
				requirements,
				"Required extensions are available from more than one source.",
			)
		}
	}

	selections := make([]extensionInstallSelection, 0, len(requirements))
	for _, requirement := range requirements {
		selections = append(selections, extensionInstallSelection{
			requirement: requirement,
			extension:   requirementCandidates(requirement)[0],
		})
	}
	return selections, nil
}

func manualInstallError(
	requirements []projectExtensionRequirement,
	message string,
) error {
	var suggestion strings.Builder
	suggestion.WriteString("Install the required extensions manually, then run this command again:")
	for _, requirement := range requirements {
		candidates := sortedRequirementCandidates(requirement)
		if len(candidates) > 1 {
			fmt.Fprintf(&suggestion, "\n\nChoose one source for %s:", requirement.extension.Id)
		}
		for _, candidate := range candidates {
			versionArg := ""
			if requirement.versionPreference != "" {
				version, err := extensions.ResolveExtensionVersion(
					candidate,
					requirement.versionPreference,
					nil,
				)
				if err != nil {
					return fmt.Errorf("resolving required extension %s: %w", candidate.Id, err)
				}
				versionArg = fmt.Sprintf(" --version %s", version.Version)
			}
			fmt.Fprintf(
				&suggestion,
				"\n  azd extension install %s --source %s%s",
				candidate.Id,
				candidate.Source,
				versionArg,
			)
		}
	}

	return &internal.ErrorWithSuggestion{
		Err:        fmt.Errorf("required extension installation needs manual action"),
		Message:    message,
		Suggestion: suggestion.String(),
	}
}

func interactiveInstallPlan(
	ctx context.Context,
	console input.Console,
	requirements []projectExtensionRequirement,
) ([]extensionInstallSelection, bool, error) {
	if len(requirements) == 1 {
		return interactiveSingleInstallPlan(ctx, console, requirements[0])
	}
	return interactiveMultipleInstallPlan(ctx, console, requirements)
}

func interactiveSingleInstallPlan(
	ctx context.Context,
	console input.Console,
	requirement projectExtensionRequirement,
) ([]extensionInstallSelection, bool, error) {
	candidates := requirementCandidates(requirement)
	if len(candidates) == 1 {
		confirmed, err := console.Confirm(ctx, input.ConsoleOptions{
			Message:      fmt.Sprintf("Install %s?", requirement.extension.DisplayName),
			DefaultValue: true,
		})
		if err != nil {
			return nil, false, err
		}
		if !confirmed {
			return nil, true, nil
		}
		return []extensionInstallSelection{{requirement: requirement, extension: candidates[0]}}, false, nil
	}

	recommended := recommendedSourceCandidate(requirement)
	if recommended != nil {
		choice, err := console.Select(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf(
				"Install %s from '%s'",
				requirement.extension.DisplayName,
				recommended.Source,
			),
			Options: []string{
				fmt.Sprintf("Install from '%s' (recommended)", recommended.Source),
				"Install from a different source",
				"Cancel",
			},
			DefaultValue:    fmt.Sprintf("Install from '%s' (recommended)", recommended.Source),
			EnableFiltering: new(false),
		})
		if err != nil {
			return nil, false, err
		}
		switch choice {
		case 0:
			return []extensionInstallSelection{{requirement: requirement, extension: recommended}}, false, nil
		case 1:
			selected, err := selectRequirementSource(ctx, console, requirement)
			if err != nil {
				return nil, false, err
			}
			return []extensionInstallSelection{{requirement: requirement, extension: selected}}, false, nil
		default:
			return nil, true, nil
		}
	}

	confirmed, err := console.Confirm(ctx, input.ConsoleOptions{
		Message:      fmt.Sprintf("Install %s?", requirement.extension.DisplayName),
		DefaultValue: true,
	})
	if err != nil {
		return nil, false, err
	}
	if !confirmed {
		return nil, true, nil
	}
	selected, err := selectRequirementSource(ctx, console, requirement)
	if err != nil {
		return nil, false, err
	}
	return []extensionInstallSelection{{requirement: requirement, extension: selected}}, false, nil
}

func interactiveMultipleInstallPlan(
	ctx context.Context,
	console input.Console,
	requirements []projectExtensionRequirement,
) ([]extensionInstallSelection, bool, error) {
	allSingleSource := !slices.ContainsFunc(requirements, func(requirement projectExtensionRequirement) bool {
		return len(requirementCandidates(requirement)) > 1
	})
	if allSingleSource {
		confirmed, err := confirmInstallAll(ctx, console, len(requirements))
		if err != nil || !confirmed {
			return nil, !confirmed, err
		}
		return soleSourceSelections(requirements), false, nil
	}

	if source, hasCommonRecommendedSource := commonRecommendedSource(requirements); hasCommonRecommendedSource {
		choice, err := console.Select(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf(
				"Install all %d required extensions from '%s'",
				len(requirements),
				source,
			),
			Options: []string{
				fmt.Sprintf("Install all from '%s' (recommended)", source),
				"Install all from a different source",
				"Cancel",
			},
			DefaultValue:    fmt.Sprintf("Install all from '%s' (recommended)", source),
			EnableFiltering: new(false),
		})
		if err != nil {
			return nil, false, err
		}
		switch choice {
		case 0:
			selections := make([]extensionInstallSelection, 0, len(requirements))
			for _, requirement := range requirements {
				selections = append(selections, extensionInstallSelection{
					requirement: requirement,
					extension:   recommendedSourceCandidate(requirement),
				})
			}
			return selections, false, nil
		case 1:
			selections, err := selectDifferentSources(ctx, console, requirements)
			return selections, false, err
		default:
			return nil, true, nil
		}
	}

	confirmed, err := confirmInstallAll(ctx, console, len(requirements))
	if err != nil || !confirmed {
		return nil, !confirmed, err
	}
	selections, err := selectSourcesIndividually(ctx, console, requirements)
	return selections, false, err
}

func commonRecommendedSource(requirements []projectExtensionRequirement) (string, bool) {
	if len(requirements) == 0 {
		return "", false
	}

	first := recommendedSourceCandidate(requirements[0])
	if first == nil {
		return "", false
	}
	for _, requirement := range requirements[1:] {
		candidate := recommendedSourceCandidate(requirement)
		if candidate == nil || !strings.EqualFold(candidate.Source, first.Source) {
			return "", false
		}
	}
	return first.Source, true
}

func confirmInstallAll(ctx context.Context, console input.Console, count int) (bool, error) {
	return console.Confirm(ctx, input.ConsoleOptions{
		Message:      fmt.Sprintf("Install all %d required extensions?", count),
		DefaultValue: true,
	})
}

func soleSourceSelections(requirements []projectExtensionRequirement) []extensionInstallSelection {
	selections := make([]extensionInstallSelection, 0, len(requirements))
	for _, requirement := range requirements {
		selections = append(selections, extensionInstallSelection{
			requirement: requirement,
			extension:   requirementCandidates(requirement)[0],
		})
	}
	return selections
}

func selectDifferentSources(
	ctx context.Context,
	console input.Console,
	requirements []projectExtensionRequirement,
) ([]extensionInstallSelection, error) {
	selections := make([]extensionInstallSelection, 0, len(requirements))
	firstAmbiguous := slices.IndexFunc(requirements, func(requirement projectExtensionRequirement) bool {
		return len(requirementCandidates(requirement)) > 1
	})
	for _, requirement := range requirements[:firstAmbiguous] {
		selections = append(selections, extensionInstallSelection{
			requirement: requirement,
			extension:   requirementCandidates(requirement)[0],
		})
	}

	first := requirements[firstAmbiguous]
	selected, err := selectRequirementSource(
		ctx,
		console,
		first,
	)
	if err != nil {
		return nil, err
	}
	selections = append(selections, extensionInstallSelection{requirement: first, extension: selected})

	remaining := requirements[firstAmbiguous+1:]
	if len(remaining) == 0 {
		return selections, nil
	}
	if slices.ContainsFunc(remaining, func(requirement projectExtensionRequirement) bool {
		return candidateForSource(requirement, selected.Source) == nil
	}) {
		individual, err := selectSourcesIndividually(ctx, console, remaining)
		return append(selections, individual...), err
	}

	useForRemaining, err := console.Confirm(ctx, input.ConsoleOptions{
		Message:      fmt.Sprintf("Install remaining extensions from '%s'?", selected.Source),
		DefaultValue: true,
	})
	if err != nil {
		return nil, err
	}
	if useForRemaining {
		for _, requirement := range remaining {
			selections = append(selections, extensionInstallSelection{
				requirement: requirement,
				extension:   candidateForSource(requirement, selected.Source),
			})
		}
		return selections, nil
	}

	individual, err := selectSourcesIndividually(ctx, console, remaining)
	return append(selections, individual...), err
}

func selectSourcesIndividually(
	ctx context.Context,
	console input.Console,
	requirements []projectExtensionRequirement,
) ([]extensionInstallSelection, error) {
	selections := make([]extensionInstallSelection, 0, len(requirements))
	for _, requirement := range requirements {
		candidates := requirementCandidates(requirement)
		selected := candidates[0]
		var err error
		if len(candidates) > 1 {
			selected, err = selectRequirementSource(ctx, console, requirement)
			if err != nil {
				return nil, err
			}
		}
		selections = append(selections, extensionInstallSelection{
			requirement: requirement,
			extension:   selected,
		})
	}
	return selections, nil
}

func selectRequirementSource(
	ctx context.Context,
	console input.Console,
	requirement projectExtensionRequirement,
) (*extensions.ExtensionMetadata, error) {
	candidates := sortedRequirementCandidates(requirement)
	options := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		options = append(options, candidate.Source)
	}

	choice, err := console.Select(ctx, input.ConsoleOptions{
		Message:         fmt.Sprintf("Select a source for %s", requirement.extension.DisplayName),
		Options:         options,
		EnableFiltering: new(false),
	})
	if err != nil {
		return nil, err
	}
	return candidates[choice], nil
}

func candidateForSource(
	requirement projectExtensionRequirement,
	source string,
) *extensions.ExtensionMetadata {
	for _, candidate := range requirementCandidates(requirement) {
		if strings.EqualFold(candidate.Source, source) {
			return candidate
		}
	}
	return nil
}
