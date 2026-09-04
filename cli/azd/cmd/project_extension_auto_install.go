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
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
)

type projectExtensionRequirement struct {
	extension         *extensions.ExtensionMetadata
	candidates        []*extensions.ExtensionMetadata
	versionPreference string
	explicit          bool
}

type resolvedExtensionDependency struct {
	capabilities []extensions.CapabilityType
	providers    []extensions.Provider
}

// extensionRef identifies an extension within a registry source, normalized for case-insensitive
// comparison. In-flight extensions are tracked by source and id because the same id can be
// published by more than one source, while resolved selections are keyed by id alone to match how
// installation reuses whatever is already installed.
type extensionRef struct {
	source string
	id     string
}

func newExtensionRef(source string, id string) extensionRef {
	return extensionRef{source: strings.ToLower(source), id: strings.ToLower(id)}
}

// projectCommandSupportsExtensionAutoInstall reports whether a command resolves a provider that an
// extension can supply. Commands that only read azure.yaml do not, so they never install anything.
func projectCommandSupportsExtensionAutoInstall(cmd *cobra.Command) bool {
	if _, isExtensionCommand := cmd.Annotations["extension.id"]; isExtensionCommand {
		return false
	}

	path := getCommandPath(cmd)
	if len(path) == 0 {
		return false
	}

	switch path[0] {
	case "up", "provision", "deploy", "package", "restore", "down":
		return true
	case "env":
		return len(path) > 1 && path[1] == "refresh"
	default:
		return false
	}
}

// helpRequested reports whether args ask cobra to render help or reference documentation instead of
// running the command. rootCmd.Find resolves a command path without applying cobra's help
// short-circuit, so extension resolution would otherwise install extensions for `azd up --help`.
func helpRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			// Everything after this is positional.
			return false
		}
		if arg == "-h" || arg == "--help" || arg == "--docs" {
			return true
		}
	}

	return false
}

// providerLookup carries the state needed to decide which of the extensions publishing a provider
// can actually be installed to supply it. The maps are keyed by lowercase extension id, matching
// the case-insensitive comparison the extension manager applies to extension ids.
type providerLookup struct {
	installed map[string]*extensions.Extension
	// resolvedDependencies holds extensions that installation will pull in as pack dependencies.
	resolvedDependencies map[string]resolvedExtensionDependency
	// requirementConflicts holds explicit requirements whose constrained version cannot supply
	// the provider, mapped to the conflict to report.
	requirementConflicts map[string]error
}

// providerCandidates partitions the extensions publishing a provider into those that can be
// installed and the conflicts worth reporting when none can.
type providerCandidates struct {
	installable          []*extensions.ExtensionMetadata
	requirementConflicts map[string]error
}

// partition splits matches into installable candidates and the conflicts that explain the rest.
// Extensions that are already installed, or that installation will pull in as a dependency, are
// dropped silently: installing them cannot supply a provider their selected version does not have.
func (l providerLookup) partition(matches []*extensions.ExtensionMetadata) providerCandidates {
	candidates := providerCandidates{requirementConflicts: map[string]error{}}

	for _, extension := range matches {
		extensionId := strings.ToLower(extension.Id)

		if conflict, hasConflict := l.requirementConflicts[extensionId]; hasConflict {
			candidates.requirementConflicts[extension.Id] = conflict
			continue
		}
		if _, isDependency := l.resolvedDependencies[extensionId]; isDependency {
			continue
		}
		if _, isInstalled := installedExtensionById(l.installed, extension.Id); isInstalled {
			continue
		}

		candidates.installable = append(candidates.installable, extension)
	}

	return candidates
}

// conflictError reports why no candidate can supply the provider, or nil when the provider is
// simply unavailable. Conflicts are reported by lowest extension id so the message is stable.
func (c providerCandidates) conflictError() error {
	if len(c.requirementConflicts) == 0 {
		return nil
	}

	extensionId := slices.Sorted(maps.Keys(c.requirementConflicts))[0]
	return c.requirementConflicts[extensionId]
}

// findExtensionForProvider selects an installable extension that supplies the given provider,
// prompting when more than one is available. It returns a nil extension and a nil error when
// nothing can be installed to supply the provider, and an error when a required extension is
// pinned to a version that cannot.
func findExtensionForProvider(
	ctx context.Context,
	console input.Console,
	extensionManager extensionAutoInstallManager,
	lookup providerLookup,
	capability extensions.CapabilityType,
	provider string,
) ([]*extensions.ExtensionMetadata, error) {
	matches, err := extensionManager.FindInstallableExtensions(ctx, &extensions.InstallResolutionOptions{
		FilterOptions: extensions.FilterOptions{
			Capability: capability,
			Provider:   provider,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("finding extension for provider %q: %w", provider, err)
	}

	candidates := lookup.partition(matches)
	if len(candidates.installable) == 0 {
		return nil, candidates.conflictError()
	}

	return chooseLogicalExtensionCandidates(ctx, console, candidates.installable)
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

// versionSatisfiesConstraint reports whether an already selected extension version satisfies a
// declared semver constraint. An empty constraint matches any version.
func versionSatisfiesConstraint(extensionId string, version string, constraint string) bool {
	if constraint == "" {
		return true
	}

	metadata := &extensions.ExtensionMetadata{
		Id:       extensionId,
		Versions: []extensions.ExtensionVersion{{Version: version}},
	}
	_, err := extensions.ResolveExtensionVersion(metadata, constraint, nil)
	return err == nil
}

func validateInstalledExtensionVersion(
	installed *extensions.Extension,
	versionPreference string,
) error {
	if versionSatisfiesConstraint(installed.Id, installed.Version, versionPreference) {
		return nil
	}

	// --version only accepts an exact version, so the constraint cannot be passed through.
	return &internal.ErrorWithSuggestion{
		Err: fmt.Errorf(
			"installed extension %s version %s does not satisfy constraint %q",
			installed.Id,
			installed.Version,
			versionPreference,
		),
		Suggestion: fmt.Sprintf(
			"Run 'azd extension update %s' to move to the latest version, or "+
				"'azd extension install %s --version <version>' to select an exact version "+
				"that satisfies %q.",
			installed.Id,
			installed.Id,
			versionPreference,
		),
	}
}

// resolveExtensionRequirementDependencies walks the dependencies of the required extensions so
// resolution can tell that installing one of them will pull in another. It is best effort: a
// dependency it cannot resolve is left out, which at worst prompts for an extension installation
// would have supplied anyway, and installation reports any genuine problem itself.
func resolveExtensionRequirementDependencies(
	ctx context.Context,
	extensionManager extensionAutoInstallManager,
	requirements map[string]projectExtensionRequirement,
) map[string]resolvedExtensionDependency {
	resolved := map[string]resolvedExtensionDependency{}

	for _, requirement := range sortedProjectExtensionRequirements(requirements) {
		var common map[string]resolvedExtensionDependency
		for _, candidate := range requirementCandidates(requirement) {
			version, err := extensionManager.ResolveVersion(
				candidate,
				requirement.versionPreference,
			)
			if err != nil {
				common = map[string]resolvedExtensionDependency{}
				break
			}

			candidateResolved := map[string]resolvedExtensionDependency{}
			key := newExtensionRef(candidate.Source, candidate.Id)
			resolveExtensionDependencies(
				ctx,
				extensionManager,
				candidate,
				version.Dependencies,
				candidateResolved,
				map[extensionRef]struct{}{key: {}},
			)
			if common == nil {
				common = candidateResolved
			} else {
				common = intersectResolvedDependencies(common, candidateResolved)
			}
		}
		for id, dependency := range common {
			if _, exists := resolved[id]; !exists {
				resolved[id] = dependency
			}
		}
	}

	return resolved
}

func intersectResolvedDependencies(
	left map[string]resolvedExtensionDependency,
	right map[string]resolvedExtensionDependency,
) map[string]resolvedExtensionDependency {
	intersection := map[string]resolvedExtensionDependency{}
	for id, leftDependency := range left {
		rightDependency, exists := right[id]
		if !exists {
			continue
		}

		intersection[id] = resolvedExtensionDependency{
			capabilities: slices.DeleteFunc(
				slices.Clone(leftDependency.capabilities),
				func(capability extensions.CapabilityType) bool {
					return !slices.Contains(rightDependency.capabilities, capability)
				},
			),
			providers: slices.DeleteFunc(
				slices.Clone(leftDependency.providers),
				func(provider extensions.Provider) bool {
					return !slices.ContainsFunc(
						rightDependency.providers,
						func(candidate extensions.Provider) bool {
							return candidate.Type == provider.Type &&
								strings.EqualFold(candidate.Name, provider.Name)
						},
					)
				},
			),
		}
	}
	return intersection
}

func resolveExtensionDependencies(
	ctx context.Context,
	extensionManager extensionAutoInstallManager,
	parent *extensions.ExtensionMetadata,
	dependencies []extensions.ExtensionDependency,
	resolved map[string]resolvedExtensionDependency,
	resolving map[extensionRef]struct{},
) {
	for _, dependency := range dependencies {
		key := newExtensionRef(parent.Source, dependency.Id)
		// Guards against a cycle in the registry metadata.
		if _, isResolving := resolving[key]; isResolving {
			continue
		}
		dependencyId := strings.ToLower(dependency.Id)
		if _, isResolved := resolved[dependencyId]; isResolved {
			continue
		}

		// Installation reuses a compatible installed dependency instead of the registry selection.
		installedDependency, err := extensionManager.GetInstalled(extensions.FilterOptions{Id: dependency.Id})
		if err == nil && installedDependency != nil &&
			versionSatisfiesConstraint(dependency.Id, installedDependency.Version, dependency.Version) {
			resolved[dependencyId] = resolvedExtensionDependency{
				capabilities: installedDependency.Capabilities,
				providers:    installedDependency.Providers,
			}
			continue
		}

		dependencyExtension, err := extensionManager.ResolveDependency(ctx, parent, dependency)
		if err != nil {
			continue
		}

		version, err := extensionManager.ResolveVersion(dependencyExtension, dependency.Version)
		if err != nil {
			continue
		}
		resolved[dependencyId] = resolvedExtensionDependency{
			capabilities: version.Capabilities,
			providers:    version.Providers,
		}

		resolving[key] = struct{}{}
		resolveExtensionDependencies(
			ctx,
			extensionManager,
			dependencyExtension,
			version.Dependencies,
			resolved,
			resolving,
		)
		delete(resolving, key)
	}
}

// installedProvidesProvider reports whether an installed extension already supplies the provider,
// in which case nothing needs to be installed for it.
// installedProviderExtensions returns the installed extensions that publish the provider,
// sorted by id.
func installedProviderExtensions(
	installed map[string]*extensions.Extension,
	capability extensions.CapabilityType,
	providerName string,
) []*extensions.Extension {
	var providers []*extensions.Extension
	for _, extension := range installed {
		if extensionProvidesProvider(extension.Capabilities, extension.Providers, capability, providerName) {
			providers = append(providers, extension)
		}
	}
	slices.SortFunc(providers, func(a, b *extensions.Extension) int {
		return strings.Compare(a.Id, b.Id)
	})
	return providers
}

// promoteProjectRequiredExtension marks an installed extension the project requires as an
// explicit install, so a record that only a pack pulled in survives when that pack is
// uninstalled. Explicit records are left untouched.
func promoteProjectRequiredExtension(
	extensionManager extensionAutoInstallManager,
	installed *extensions.Extension,
) error {
	if !installed.InstalledAsDependency {
		return nil
	}
	if err := extensionManager.MarkExplicitlyInstalled(installed.Id); err != nil {
		return fmt.Errorf("marking extension %s as explicitly installed: %w", installed.Id, err)
	}
	return nil
}

func extensionProvidesProvider(
	capabilities []extensions.CapabilityType,
	providers []extensions.Provider,
	capability extensions.CapabilityType,
	providerName string,
) bool {
	return extensions.VersionProvidesProvider(
		&extensions.ExtensionVersion{Capabilities: capabilities, Providers: providers},
		capability,
		providerName,
	)
}

// providerIsBuiltIn reports whether azd itself implements the named provider. Core registers these
// unconditionally, so no extension is ever required to supply them and the registry need not be
// consulted, which would otherwise let an unreachable source fail an ordinary project. Matching is
// case sensitive because the runtime resolves providers by their exact name.
func providerIsBuiltIn(capability extensions.CapabilityType, provider string) bool {
	switch capability {
	case extensions.ServiceTargetProviderCapability:
		return slices.Contains(project.BuiltInServiceTargetKinds(), project.ServiceTargetKind(provider))
	case extensions.ProvisioningProviderCapability:
		return slices.Contains(provisioning.BuiltInProviderKinds(), provisioning.ProviderKind(provider))
	default:
		return false
	}
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

func extensionCandidateProvidesProvider(
	ctx context.Context,
	extensionManager extensionAutoInstallManager,
	candidate *extensions.ExtensionMetadata,
	versionPreference string,
	capability extensions.CapabilityType,
	provider string,
) (bool, error) {
	version, err := extensionManager.ResolveVersion(candidate, versionPreference)
	if err != nil {
		return false, err
	}
	if extensionVersionProvidesProvider(version, capability, provider) {
		return true, nil
	}

	resolved := map[string]resolvedExtensionDependency{}
	key := newExtensionRef(candidate.Source, candidate.Id)
	resolveExtensionDependencies(
		ctx,
		extensionManager,
		candidate,
		version.Dependencies,
		resolved,
		map[extensionRef]struct{}{key: {}},
	)
	for dependency := range maps.Values(resolved) {
		if resolvedDependencyProvidesProvider(dependency, capability, provider) {
			return true, nil
		}
	}
	return false, nil
}

// extensionForProvider returns every release that supplies the requested provider.
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
				// The project requires this extension in its own right, so a record that only
				// a pack pulled in becomes explicit and survives when that pack is uninstalled.
				if err := promoteProjectRequiredExtension(extensionManager, installedExtension); err != nil {
					return nil, err
				}
				continue
			}

			matches, err := extensionManager.FindInstallableExtensions(
				ctx,
				&extensions.InstallResolutionOptions{FilterOptions: extensions.FilterOptions{
					Id:      extensionId,
					Version: versionPreference,
				}},
			)
			if err != nil {
				return nil, fmt.Errorf("finding required extension %s: %w", extensionId, err)
			}
			if len(matches) == 0 {
				return nil, &internal.ErrorWithSuggestion{
					Err: fmt.Errorf("required extension %s not found", extensionId),
					Suggestion: fmt.Sprintf(
						"Check requiredVersions.extensions in azure.yaml, then run "+
							"'azd extension source list' to verify that a configured source publishes %s.",
						extensionId,
					),
				}
			}

			candidates, err := chooseLogicalExtensionCandidates(ctx, console, matches)
			if err != nil {
				return nil, fmt.Errorf("selecting required extension %s: %w", extensionId, err)
			}
			extension := candidates[0]

			requirements[extension.Id] = projectExtensionRequirement{
				extension:         extension,
				candidates:        candidates,
				versionPreference: versionPreference,
				explicit:          true,
			}
		}
	}

	addProvider := func(capability extensions.CapabilityType, provider string) error {
		if provider == "" || providerIsBuiltIn(capability, provider) {
			return nil
		}
		// An installed provider satisfies the requirement; the project needs that extension
		// in its own right, so a dependency-installed record becomes explicit.
		if providers := installedProviderExtensions(installed, capability, provider); len(providers) > 0 {
			for _, extension := range providers {
				if err := promoteProjectRequiredExtension(extensionManager, extension); err != nil {
					return err
				}
			}
			return nil
		}

		requirementConflicts := map[string]error{}
		for _, extensionId := range slices.Sorted(maps.Keys(requirements)) {
			requirement := requirements[extensionId]
			var providingCandidates []*extensions.ExtensionMetadata
			for _, candidate := range requirementCandidates(requirement) {
				provides, err := extensionCandidateProvidesProvider(
					ctx,
					extensionManager,
					candidate,
					requirement.versionPreference,
					capability,
					provider,
				)
				if err != nil {
					return fmt.Errorf("resolving required extension %s: %w", extensionId, err)
				}
				if provides {
					providingCandidates = append(providingCandidates, candidate)
				}
			}
			if len(providingCandidates) > 0 {
				requirement.candidates = providingCandidates
				requirement.extension = providingCandidates[0]
				requirements[extensionId] = requirement
				return nil
			}

			hasProviderVersion := slices.ContainsFunc(
				requirementCandidates(requirement),
				func(candidate *extensions.ExtensionMetadata) bool {
					return len(extensionForProvider(candidate, capability, provider).Versions) > 0
				},
			)
			if !hasProviderVersion {
				continue
			}
			selectedVersion, err := extensionManager.ResolveVersion(
				requirement.extension,
				requirement.versionPreference,
			)
			if err != nil {
				return fmt.Errorf("resolving required extension %s: %w", extensionId, err)
			}
			requirementConflicts[strings.ToLower(extensionId)] = fmt.Errorf(
				"required extension %s version %s does not provide %s %q",
				extensionId,
				selectedVersion.Version,
				capability,
				provider,
			)
		}

		resolvedDependencies := resolveExtensionRequirementDependencies(ctx, extensionManager, requirements)
		for dependency := range maps.Values(resolvedDependencies) {
			if resolvedDependencyProvidesProvider(dependency, capability, provider) {
				return nil
			}
		}

		candidates, err := findExtensionForProvider(
			ctx,
			console,
			extensionManager,
			providerLookup{
				installed:            installed,
				resolvedDependencies: resolvedDependencies,
				requirementConflicts: requirementConflicts,
			},
			capability,
			provider,
		)
		if err != nil || len(candidates) == 0 {
			return err
		}
		extension := candidates[0]
		if requirement, alreadyRequired := requirements[extension.Id]; alreadyRequired {
			requirement.candidates = slices.DeleteFunc(
				requirementCandidates(requirement),
				func(candidate *extensions.ExtensionMetadata) bool {
					return !slices.ContainsFunc(candidates, func(match *extensions.ExtensionMetadata) bool {
						return strings.EqualFold(candidate.Source, match.Source)
					})
				},
			)
			if len(requirement.candidates) == 0 {
				return fmt.Errorf(
					"required extension %s does not provide %s %q",
					extension.Id,
					capability,
					provider,
				)
			}
			requirement.extension = requirement.candidates[0]
			requirements[extension.Id] = requirement
		} else {
			requirements[extension.Id] = projectExtensionRequirement{
				extension:  extension,
				candidates: candidates,
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

// projectExtensionResult reports what resolution did, so the caller knows whether to rebuild the
// command tree and whether the legacy unsupported-host fallback still has work to do.
type projectExtensionResult struct {
	// handled reports that resolution owned the project's provider requirements, so the legacy
	// unsupported-host fallback must not prompt for them again.
	handled bool
	// installed reports that an extension was installed, so the command tree is out of date.
	installed bool
	// declined reports that the user intentionally stopped before installation.
	declined bool
}

func tryAutoInstallProjectExtensions(
	ctx context.Context,
	rootContainer *ioc.NestedContainer,
	foundCmd *cobra.Command,
	args []string,
) (projectExtensionResult, error) {
	if !projectCommandSupportsExtensionAutoInstall(foundCmd) {
		return projectExtensionResult{}, nil
	}

	if helpRequested(args) {
		return projectExtensionResult{}, nil
	}

	var projectConfig *project.ProjectConfig
	if err := rootContainer.Resolve(&projectConfig); err != nil {
		log.Printf("skipping project extension auto-install: %v", err)
		return projectExtensionResult{}, nil
	}

	var extensionManager *extensions.Manager
	if err := rootContainer.Resolve(&extensionManager); err != nil {
		return projectExtensionResult{}, fmt.Errorf("resolving extension manager: %w", err)
	}
	var console input.Console
	if err := rootContainer.Resolve(&console); err != nil {
		return projectExtensionResult{}, fmt.Errorf("resolving console: %w", err)
	}

	requirements, err := missingProjectExtensions(ctx, console, extensionManager, projectConfig)
	if err != nil {
		return projectExtensionResult{}, err
	}
	if len(requirements) == 0 {
		return projectExtensionResult{}, nil
	}

	result, err := autoInstallExtensionRequirements(
		ctx,
		console,
		extensionManager,
		requirements,
		autoInstallDisplayContext{requiredByProject: true},
	)
	if err != nil {
		return projectExtensionResult{handled: true, installed: result.installed}, err
	}

	return projectExtensionResult{
		handled:   true,
		installed: result.installed,
		declined:  result.declined,
	}, nil
}

func displayAutoInstallError(ctx context.Context, console input.Console, err error) {
	err = internal.WrapErrorWithSuggestion(err)
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
