// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
)

type projectExtensionRequirement struct {
	extension         *extensions.ExtensionMetadata
	versionPreference string
	explicit          bool
}

type resolvedExtensionDependency struct {
	parentId     string
	version      string
	capabilities []extensions.CapabilityType
	providers    []extensions.Provider
}

func projectCommandSupportsExtensionAutoInstall(cmd *cobra.Command) bool {
	if _, isExtensionCommand := cmd.Annotations["extension.id"]; isExtensionCommand {
		return false
	}

	path := getCommandPath(cmd)
	if len(path) == 0 {
		return false
	}

	switch path[0] {
	case "up", "provision", "deploy", "package", "restore", "down", "show", "monitor":
		return true
	case "infra":
		return len(path) > 1 && path[1] == "generate"
	case "env":
		return len(path) > 1 && path[1] == "refresh"
	default:
		return false
	}
}

func findExtensionForProvider(
	ctx context.Context,
	console input.Console,
	extensionManager extensionAutoInstallManager,
	installed map[string]*extensions.Extension,
	resolvedDependencies map[string]resolvedExtensionDependency,
	requirementConflicts map[string]error,
	capability extensions.CapabilityType,
	provider string,
) (*extensions.ExtensionMetadata, error) {
	matches, err := extensionManager.FindExtensions(ctx, &extensions.FilterOptions{
		Capability: capability,
		Provider:   provider,
	})
	if err != nil {
		return nil, fmt.Errorf("finding extension for provider %q: %w", provider, err)
	}
	matches = filterExtensionsForProvider(matches, capability, provider)
	matchedRequirementConflicts := map[string]error{}
	matches = slices.DeleteFunc(matches, func(extension *extensions.ExtensionMetadata) bool {
		conflict, hasConflict := requirementConflicts[strings.ToLower(extension.Id)]
		if hasConflict {
			matchedRequirementConflicts[extension.Id] = conflict
		}
		return hasConflict
	})
	dependencyConflicts := map[string]resolvedExtensionDependency{}
	matches = slices.DeleteFunc(matches, func(extension *extensions.ExtensionMetadata) bool {
		dependency, isDependency := resolvedDependencies[strings.ToLower(extension.Id)]
		if isDependency {
			dependencyConflicts[extension.Id] = dependency
		}
		return isDependency
	})
	matches = uninstalledExtensionMatches(matches, installed)
	if len(matches) == 0 {
		if len(matchedRequirementConflicts) > 0 {
			extensionId := slices.Sorted(maps.Keys(matchedRequirementConflicts))[0]
			return nil, matchedRequirementConflicts[extensionId]
		}
		if len(dependencyConflicts) > 0 {
			extensionId := slices.Sorted(maps.Keys(dependencyConflicts))[0]
			dependency := dependencyConflicts[extensionId]
			return nil, fmt.Errorf(
				"extension %s requires dependency %s version %s, which does not provide %s %q",
				dependency.parentId,
				extensionId,
				dependency.version,
				capability,
				provider,
			)
		}
		return nil, nil
	}

	return promptForExtensionChoice(ctx, console, matches)
}

func uninstalledExtensionMatches(
	matches []*extensions.ExtensionMetadata,
	installed map[string]*extensions.Extension,
) []*extensions.ExtensionMetadata {
	return slices.DeleteFunc(slices.Clone(matches), func(extension *extensions.ExtensionMetadata) bool {
		_, isInstalled := installedExtensionById(installed, extension.Id)
		return isInstalled
	})
}

func installedExtensionById(
	installed map[string]*extensions.Extension,
	extensionId string,
) (*extensions.Extension, bool) {
	for installedId, extension := range installed {
		if strings.EqualFold(installedId, extensionId) {
			return extension, true
		}
	}
	return nil, false
}

func validateInstalledExtensionVersion(
	installed *extensions.Extension,
	versionPreference string,
) error {
	if versionPreference == "" {
		return nil
	}

	installedMetadata := &extensions.ExtensionMetadata{
		Id: installed.Id,
		Versions: []extensions.ExtensionVersion{{
			Version: installed.Version,
		}},
	}
	if _, err := extensions.ResolveExtensionVersion(installedMetadata, versionPreference, nil); err != nil {
		return fmt.Errorf(
			"installed extension %s version %s does not satisfy constraint %q",
			installed.Id,
			installed.Version,
			versionPreference,
		)
	}
	return nil
}

func resolveExtensionRequirementDependencies(
	ctx context.Context,
	extensionManager extensionAutoInstallManager,
	requirements map[string]projectExtensionRequirement,
) (map[string]resolvedExtensionDependency, error) {
	resolved := map[string]resolvedExtensionDependency{}
	resolving := map[string]struct{}{}

	for _, requirement := range sortedProjectExtensionRequirements(requirements) {
		version, err := extensions.ResolveExtensionVersion(
			requirement.extension,
			requirement.versionPreference,
			nil,
		)
		if err != nil {
			return nil, fmt.Errorf("resolving required extension %s: %w", requirement.extension.Id, err)
		}

		key := strings.ToLower(requirement.extension.Source + "\x00" + requirement.extension.Id)
		resolving[key] = struct{}{}
		err = resolveExtensionDependencies(
			ctx,
			extensionManager,
			requirement.extension,
			version.Dependencies,
			resolved,
			resolving,
		)
		delete(resolving, key)
		if err != nil {
			return nil, err
		}
	}

	return resolved, nil
}

func resolveExtensionDependencies(
	ctx context.Context,
	extensionManager extensionAutoInstallManager,
	parent *extensions.ExtensionMetadata,
	dependencies []extensions.ExtensionDependency,
	resolved map[string]resolvedExtensionDependency,
	resolving map[string]struct{},
) error {
	for _, dependency := range dependencies {
		key := strings.ToLower(parent.Source + "\x00" + dependency.Id)
		if _, isResolving := resolving[key]; isResolving {
			return fmt.Errorf("dependency cycle detected involving extension %s", dependency.Id)
		}
		dependencyId := strings.ToLower(dependency.Id)
		if _, isResolved := resolved[dependencyId]; isResolved {
			continue
		}

		// Installation reuses a compatible installed dependency instead of replacing it with the registry selection.
		installedDependency, err := extensionManager.GetInstalled(extensions.FilterOptions{Id: dependency.Id})
		if err == nil && installedDependency != nil {
			if dependency.Version != "" {
				installedMetadata := &extensions.ExtensionMetadata{
					Id: dependency.Id,
					Versions: []extensions.ExtensionVersion{{
						Version: installedDependency.Version,
					}},
				}
				if _, err := extensions.ResolveExtensionVersion(installedMetadata, dependency.Version, nil); err != nil {
					return fmt.Errorf(
						"installed dependency %s version %s does not satisfy constraint %q",
						dependency.Id,
						installedDependency.Version,
						dependency.Version,
					)
				}
			}

			resolved[dependencyId] = resolvedExtensionDependency{
				parentId:     parent.Id,
				version:      installedDependency.Version,
				capabilities: installedDependency.Capabilities,
				providers:    installedDependency.Providers,
			}
			continue
		}

		matches, err := extensionManager.FindExtensions(ctx, &extensions.FilterOptions{
			Id:      dependency.Id,
			Version: dependency.Version,
			Source:  parent.Source,
		})
		if err != nil {
			return fmt.Errorf("finding dependency %s: %w", dependency.Id, err)
		}
		if len(matches) == 0 {
			return &extensions.DependencyNotFoundError{
				DependencyId: dependency.Id,
				ParentId:     parent.Id,
			}
		}
		if len(matches) > 1 {
			sources := make([]string, 0, len(matches))
			for _, match := range matches {
				sources = append(sources, match.Source)
			}
			slices.Sort(sources)
			sources = slices.Compact(sources)
			return &extensions.DependencyAmbiguousSourceError{
				DependencyId: dependency.Id,
				ParentId:     parent.Id,
				Sources:      sources,
			}
		}

		dependencyExtension := matches[0]
		version, err := extensions.ResolveExtensionVersion(dependencyExtension, dependency.Version, nil)
		if err != nil {
			return fmt.Errorf("resolving dependency %s: %w", dependency.Id, err)
		}
		resolved[dependencyId] = resolvedExtensionDependency{
			parentId:     parent.Id,
			version:      version.Version,
			capabilities: version.Capabilities,
			providers:    version.Providers,
		}

		resolving[key] = struct{}{}
		err = resolveExtensionDependencies(
			ctx,
			extensionManager,
			dependencyExtension,
			version.Dependencies,
			resolved,
			resolving,
		)
		delete(resolving, key)
		if err != nil {
			return err
		}
	}

	return nil
}

func extensionProvidesProvider(
	capabilities []extensions.CapabilityType,
	providers []extensions.Provider,
	capability extensions.CapabilityType,
	providerName string,
) bool {
	expectedType, hasProviderType := providerTypeForCapability(capability)
	if !hasProviderType || !slices.Contains(capabilities, capability) {
		return false
	}

	return slices.ContainsFunc(providers, func(provider extensions.Provider) bool {
		return provider.Type == expectedType && strings.EqualFold(provider.Name, providerName)
	})
}

func providerTypeForCapability(capability extensions.CapabilityType) (extensions.ProviderType, bool) {
	switch capability {
	case extensions.ServiceTargetProviderCapability:
		return extensions.ServiceTargetProviderType, true
	case extensions.ProvisioningProviderCapability:
		return extensions.ProvisioningProviderType, true
	default:
		return "", false
	}
}

func filterExtensionsForProvider(
	matches []*extensions.ExtensionMetadata,
	capability extensions.CapabilityType,
	providerName string,
) []*extensions.ExtensionMetadata {
	filtered := make([]*extensions.ExtensionMetadata, 0, len(matches))
	for _, extension := range matches {
		providerExtension := extensionForProvider(extension, capability, providerName)
		if len(providerExtension.Versions) > 0 {
			filtered = append(filtered, providerExtension)
		}
	}
	return filtered
}

func extensionVersionProvidesProvider(
	version *extensions.ExtensionVersion,
	capability extensions.CapabilityType,
	providerName string,
) bool {
	return extensionProvidesProvider(version.Capabilities, version.Providers, capability, providerName)
}

func resolvedDependencyProvidesProvider(
	dependency resolvedExtensionDependency,
	capability extensions.CapabilityType,
	providerName string,
) bool {
	return extensionProvidesProvider(
		dependency.capabilities,
		dependency.providers,
		capability,
		providerName,
	)
}

func extensionForProvider(
	extension *extensions.ExtensionMetadata,
	capability extensions.CapabilityType,
	providerName string,
) *extensions.ExtensionMetadata {
	filtered := *extension
	filtered.Versions = slices.DeleteFunc(slices.Clone(extension.Versions), func(version extensions.ExtensionVersion) bool {
		return !extensionVersionProvidesProvider(&version, capability, providerName)
	})
	return &filtered
}

func missingProjectExtensions(
	ctx context.Context,
	console input.Console,
	extensionManager extensionAutoInstallManager,
	projectConfig *project.ProjectConfig,
) ([]projectExtensionRequirement, error) {
	installed, err := extensionManager.ListInstalled()
	if err != nil {
		return nil, fmt.Errorf("listing installed extensions: %w", err)
	}

	requirements := map[string]projectExtensionRequirement{}
	if projectConfig.RequiredVersions != nil {
		for _, extensionId := range slices.Sorted(maps.Keys(projectConfig.RequiredVersions.Extensions)) {
			versionPreference := ""
			if constraint := projectConfig.RequiredVersions.Extensions[extensionId]; constraint != nil {
				versionPreference = *constraint
			}
			if installedExtension, isInstalled := installedExtensionById(installed, extensionId); isInstalled {
				if err := validateInstalledExtensionVersion(installedExtension, versionPreference); err != nil {
					return nil, err
				}
				continue
			}

			matches, err := extensionManager.FindExtensions(ctx, &extensions.FilterOptions{
				Id:      extensionId,
				Version: versionPreference,
			})
			if err != nil {
				return nil, fmt.Errorf("finding required extension %s: %w", extensionId, err)
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("required extension %s not found", extensionId)
			}

			extension, err := promptForExtensionChoice(ctx, console, matches)
			if err != nil {
				return nil, fmt.Errorf("selecting required extension %s: %w", extensionId, err)
			}

			requirements[extension.Id] = projectExtensionRequirement{
				extension:         extension,
				versionPreference: versionPreference,
				explicit:          true,
			}
		}
	}

	addProvider := func(capability extensions.CapabilityType, provider string) error {
		if provider == "" {
			return nil
		}

		requirementConflicts := map[string]error{}
		for _, extensionId := range slices.Sorted(maps.Keys(requirements)) {
			requirement := requirements[extensionId]
			selectedVersion, err := extensions.ResolveExtensionVersion(
				requirement.extension,
				requirement.versionPreference,
				nil,
			)
			if err != nil {
				return fmt.Errorf("resolving required extension %s: %w", extensionId, err)
			}
			if extensionVersionProvidesProvider(selectedVersion, capability, provider) {
				return nil
			}

			if len(extensionForProvider(requirement.extension, capability, provider).Versions) == 0 {
				continue
			}
			requirementConflicts[strings.ToLower(extensionId)] = fmt.Errorf(
				"required extension %s version %s does not provide %s %q",
				extensionId,
				selectedVersion.Version,
				capability,
				provider,
			)
		}

		resolvedDependencies, err := resolveExtensionRequirementDependencies(ctx, extensionManager, requirements)
		if err != nil {
			return err
		}
		for dependency := range maps.Values(resolvedDependencies) {
			if resolvedDependencyProvidesProvider(dependency, capability, provider) {
				return nil
			}
		}

		extension, err := findExtensionForProvider(
			ctx,
			console,
			extensionManager,
			installed,
			resolvedDependencies,
			requirementConflicts,
			capability,
			provider,
		)
		if err != nil || extension == nil {
			return err
		}
		if requirement, alreadyRequired := requirements[extension.Id]; alreadyRequired {
			requirement.extension = extensionForProvider(requirement.extension, capability, provider)
			if len(requirement.extension.Versions) == 0 {
				return fmt.Errorf(
					"required extension %s does not provide %s %q",
					extension.Id,
					capability,
					provider,
				)
			}
			requirements[extension.Id] = requirement
		} else {
			requirements[extension.Id] = projectExtensionRequirement{
				extension: extension,
			}
		}
		return nil
	}

	for _, serviceName := range slices.Sorted(maps.Keys(projectConfig.Services)) {
		if err := addProvider(
			extensions.ServiceTargetProviderCapability,
			string(projectConfig.Services[serviceName].Host),
		); err != nil {
			return nil, err
		}
	}

	for _, infra := range projectConfig.Infra.GetLayers() {
		if err := addProvider(extensions.ProvisioningProviderCapability, string(infra.Provider)); err != nil {
			return nil, err
		}
	}

	return sortedProjectExtensionRequirements(requirements), nil
}

func sortedProjectExtensionRequirements(
	requirements map[string]projectExtensionRequirement,
) []projectExtensionRequirement {
	result := slices.Collect(maps.Values(requirements))
	slices.SortFunc(result, func(a, b projectExtensionRequirement) int {
		if a.explicit != b.explicit {
			if a.explicit {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.extension.Id, b.extension.Id)
	})

	return result
}

func tryAutoInstallProjectExtensions(
	ctx context.Context,
	rootContainer *ioc.NestedContainer,
	foundCmd *cobra.Command,
) (handled bool, installed bool, err error) {
	if !projectCommandSupportsExtensionAutoInstall(foundCmd) {
		return false, false, nil
	}

	var projectConfig *project.ProjectConfig
	if err := rootContainer.Resolve(&projectConfig); err != nil {
		log.Printf("skipping project extension auto-install: %v", err)
		return false, false, nil
	}

	var extensionManager *extensions.Manager
	if err := rootContainer.Resolve(&extensionManager); err != nil {
		return false, false, fmt.Errorf("resolving extension manager: %w", err)
	}
	var console input.Console
	if err := rootContainer.Resolve(&console); err != nil {
		return false, false, fmt.Errorf("resolving console: %w", err)
	}

	requirements, err := missingProjectExtensions(ctx, console, extensionManager, projectConfig)
	if err != nil {
		return false, false, err
	}
	if len(requirements) == 0 {
		return false, false, nil
	}

	installedAny := false
	for _, requirement := range requirements {
		installed, err := tryAutoInstallExtensionVersion(
			ctx,
			console,
			extensionManager,
			*requirement.extension,
			requirement.versionPreference,
		)
		if err != nil {
			return true, installedAny, err
		}
		installedAny = installedAny || installed
	}

	return true, installedAny, nil
}

func displayAutoInstallError(ctx context.Context, console input.Console, err error) {
	if suggestionErr, ok := errors.AsType[*internal.ErrorWithSuggestion](err); ok {
		console.Message(ctx, "")
		console.MessageUxItem(ctx, &ux.ErrorWithSuggestion{
			Err:        suggestionErr.Err,
			Message:    suggestionErr.Message,
			Suggestion: suggestionErr.Suggestion,
			Links:      suggestionErr.Links,
		})
		return
	}

	console.Message(ctx, output.WithErrorFormat("\nERROR: %s", err.Error()))
}
