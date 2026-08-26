// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// ExtensionVersionNotFoundError indicates an extension exists but no published version matches
// the requested version preference.
type ExtensionVersionNotFoundError struct {
	// ExtensionId is the id of the extension without a matching version.
	ExtensionId string
	// Version is the requested version or constraint that could not be matched.
	Version string
	// Source is the requested source, if one was provided.
	Source string
	// Matches contains matching extension metadata from eligible sources.
	Matches []*ExtensionMetadata
	// AzdVersion limits suggested alternatives to releases compatible with azd.
	AzdVersion *semver.Version
}

func (e *ExtensionVersionNotFoundError) Error() string {
	latestVersions := e.latestVersions()
	switch len(latestVersions) {
	case 0:
		if e.AzdVersion != nil {
			return fmt.Sprintf(
				"extension %q version %q was not found, and no published version is compatible with azd %s",
				e.ExtensionId,
				e.Version,
				e.AzdVersion,
			)
		}
		return fmt.Sprintf("extension %q version %q was not found", e.ExtensionId, e.Version)
	case 1:
		return fmt.Sprintf(
			"extension %q version %q was not found; latest compatible version is %q",
			e.ExtensionId,
			e.Version,
			latestVersions[0].Version,
		)
	default:
		displayVersions := make([]string, 0, len(latestVersions))
		for _, latest := range latestVersions {
			displayVersions = append(displayVersions, fmt.Sprintf("%s: %s", latest.Source, latest.Version))
		}
		return fmt.Sprintf(
			"extension %q version %q was not found; latest compatible versions are %s",
			e.ExtensionId,
			e.Version,
			strings.Join(displayVersions, ", "),
		)
	}
}

// Suggestion returns guidance for installing an available compatible version.
// Range constraints (for example from requiredVersions.extensions) skip the install
// command: installing unconstrained latest would still fail the project constraint.
func (e *ExtensionVersionNotFoundError) Suggestion() string {
	if IsVersionRange(e.Version) {
		return fmt.Sprintf(
			"Inspect published versions with 'azd extension show %s' and update "+
				"requiredVersions.extensions in azure.yaml so the constraint matches a published release.",
			e.ExtensionId,
		)
	}

	latestVersions := e.latestVersions()
	if len(latestVersions) == 0 {
		return "Use an azd version compatible with a published release, or choose another extension source."
	}
	if len(latestVersions) > 1 {
		return "Specify the extension source using the --source flag, then choose an available version."
	}

	latest := latestVersions[0]
	command := fmt.Sprintf("azd extension install %s --version %s", e.ExtensionId, latest.Version)
	source := e.Source
	if source == "" && !strings.EqualFold(latest.Source, MainRegistryName) {
		source = latest.Source
	}
	if source != "" {
		command += fmt.Sprintf(" --source %s", source)
	}
	return fmt.Sprintf("Run '%s' to install the latest compatible version.", command)
}

// ExtensionVersionAlternative describes the latest compatible version in one source.
type ExtensionVersionAlternative struct {
	// Source is the configured source publishing the version.
	Source string
	// Version is the latest compatible version in the source.
	Version string
}

// Alternatives returns the latest compatible version available from each matching source.
func (e *ExtensionVersionNotFoundError) Alternatives() []ExtensionVersionAlternative {
	latestVersions := make([]ExtensionVersionAlternative, 0, len(e.Matches))
	for _, match := range e.Matches {
		latestVersion := bestSatisfyingVersionForAzd("", match.Versions, e.AzdVersion)
		if latestVersion == nil {
			continue
		}
		latestVersions = append(latestVersions, ExtensionVersionAlternative{
			Source:  match.Source,
			Version: latestVersion.Version,
		})
	}
	return latestVersions
}

func (e *ExtensionVersionNotFoundError) latestVersions() []ExtensionVersionAlternative {
	return e.Alternatives()
}

// ExtensionAzdVersionIncompatibleError indicates matching extension releases exist, but the
// releases that satisfy the requested install criteria require a different azd version.
type ExtensionAzdVersionIncompatibleError struct {
	// ExtensionId is the requested extension id, when resolution was by id.
	ExtensionId string
	// Namespace is the requested command namespace, when resolution was by namespace.
	Namespace string
	// Capability is the requested extension capability.
	Capability CapabilityType
	// Provider is the requested provider name.
	Provider string
	// Version is the requested extension version or constraint.
	Version string
	// AzdVersion is the running azd version.
	AzdVersion *semver.Version
	// Matches contains extensions rejected only because of azd compatibility.
	Matches []*ExtensionMetadata
}

func (e *ExtensionAzdVersionIncompatibleError) Error() string {
	switch {
	case e.ExtensionId != "" && e.Version != "":
		return fmt.Sprintf(
			"extension %q version %q is not compatible with azd %s",
			e.ExtensionId,
			e.Version,
			e.AzdVersion,
		)
	case e.ExtensionId != "":
		return fmt.Sprintf("no version of extension %q is compatible with azd %s", e.ExtensionId, e.AzdVersion)
	case e.Namespace != "":
		return fmt.Sprintf(
			"command namespace %q is available only from extensions that are incompatible with azd %s",
			e.Namespace,
			e.AzdVersion,
		)
	case e.Provider != "":
		return fmt.Sprintf(
			"no extension compatible with azd %s provides %s %q",
			e.AzdVersion,
			strings.ReplaceAll(string(e.Capability), "-", " "),
			e.Provider,
		)
	default:
		return fmt.Sprintf("matching extensions are not compatible with azd %s", e.AzdVersion)
	}
}

// Suggestion returns guidance for using a compatible azd version.
func (e *ExtensionAzdVersionIncompatibleError) Suggestion() string {
	constraints := make([]string, 0, len(e.Matches))
	for _, match := range e.Matches {
		selected := bestSatisfyingVersion(e.Version, match.Versions)
		if selected == nil || selected.RequiredAzdVersion == "" ||
			slices.Contains(constraints, selected.RequiredAzdVersion) {
			continue
		}
		constraints = append(constraints, selected.RequiredAzdVersion)
	}
	slices.Sort(constraints)
	if len(constraints) == 1 {
		return fmt.Sprintf("Use an azd version that satisfies %q, then retry.", constraints[0])
	}
	return "Use an azd version compatible with the extension, then retry."
}

// InstallResolutionOptions controls compatibility-aware extension discovery.
type InstallResolutionOptions struct {
	FilterOptions
}

// InstallCandidate is an extension and the release installation will pick under the
// manager's azd compatibility policy.
type InstallCandidate struct {
	Extension            *ExtensionMetadata
	Version              *ExtensionVersion
	LatestOverall        *ExtensionVersion
	LatestCompatible     *ExtensionVersion
	HasNewerIncompatible bool
}

// InstallResolutionResult describes installable extensions and why other matching extensions were rejected.
type InstallResolutionResult struct {
	Matches             []*ExtensionMetadata
	VersionMismatches   []*ExtensionMetadata
	IncompatibleMatches []*ExtensionMetadata
	// Options contains the query that produced the result.
	Options InstallResolutionOptions
	// AzdVersion is the version used for compatibility checks.
	AzdVersion *semver.Version

	candidates map[string]*InstallCandidate
}

// Candidate returns the install selection for an extension in Matches.
func (r *InstallResolutionResult) Candidate(extension *ExtensionMetadata) *InstallCandidate {
	if r == nil || extension == nil {
		return nil
	}
	return r.candidates[extensionIdentity(extension)]
}

func extensionIdentity(extension *ExtensionMetadata) string {
	return extension.Source + "\n" + extension.Id
}

// Error returns the most specific resolution error, or nil when no rejected extension explains an empty result.
func (r *InstallResolutionResult) Error() error {
	if len(r.Matches) > 0 {
		return nil
	}
	if len(r.IncompatibleMatches) > 0 {
		return &ExtensionAzdVersionIncompatibleError{
			ExtensionId: r.Options.Id,
			Namespace:   r.Options.Namespace,
			Capability:  r.Options.Capability,
			Provider:    r.Options.Provider,
			Version:     r.Options.Version,
			AzdVersion:  r.AzdVersion,
			Matches:     r.IncompatibleMatches,
		}
	}
	if len(r.VersionMismatches) > 0 {
		return &ExtensionVersionNotFoundError{
			ExtensionId: r.Options.Id,
			Version:     r.Options.Version,
			Source:      r.Options.Source,
			Matches:     r.VersionMismatches,
			AzdVersion:  r.AzdVersion,
		}
	}
	return nil
}

// ResolveExtensions finds extensions that installation can select under the manager's compatibility policy.
// The result retains rejected matches so callers can distinguish missing metadata from version and azd conflicts.
func (m *Manager) ResolveExtensions(
	ctx context.Context,
	options *InstallResolutionOptions,
) (*InstallResolutionResult, error) {
	if options == nil {
		options = &InstallResolutionOptions{}
	}

	baseFilter := FilterOptions{
		Id:           options.Id,
		Namespace:    options.Namespace,
		Source:       options.Source,
		SourceConfig: options.SourceConfig,
		Tags:         slices.Clone(options.Tags),
	}
	catalogue, err := m.FindExtensions(ctx, &baseFilter)
	if err != nil {
		return nil, err
	}

	return ClassifyInstallResolution(catalogue, options, m.azdVersion), nil
}

// ClassifyInstallResolution applies azd-compatibility classification to catalogue rows.
func ClassifyInstallResolution(
	catalogue []*ExtensionMetadata,
	options *InstallResolutionOptions,
	azdVersion *semver.Version,
) *InstallResolutionResult {
	if options == nil {
		options = &InstallResolutionOptions{}
	}

	result := &InstallResolutionResult{
		Options:    *options,
		AzdVersion: azdVersion,
		candidates: map[string]*InstallCandidate{},
	}

	for _, extension := range catalogue {
		publishedVersion := bestSatisfyingVersion(options.Version, extension.Versions)
		if publishedVersion == nil {
			if options.Version != "" && !strings.EqualFold(options.Version, "latest") {
				result.VersionMismatches = append(result.VersionMismatches, extension)
			}
			continue
		}

		selectedVersion := bestSatisfyingVersionForAzd(options.Version, extension.Versions, azdVersion)
		if selectedVersion == nil {
			if extensionVersionMatchesResolution(publishedVersion, options) {
				result.IncompatibleMatches = append(result.IncompatibleMatches, extension)
			}
			continue
		}

		if extensionVersionMatchesResolution(selectedVersion, options) {
			result.Matches = append(result.Matches, extension)
			result.candidates[extensionIdentity(extension)] = newInstallCandidate(
				extension,
				selectedVersion,
				azdVersion,
			)
			continue
		}

		if azdVersion != nil &&
			!VersionIsCompatible(publishedVersion, azdVersion) &&
			extensionVersionMatchesResolution(publishedVersion, options) {
			result.IncompatibleMatches = append(result.IncompatibleMatches, extension)
		}
	}

	return result
}

func newInstallCandidate(
	extension *ExtensionMetadata,
	selectedVersion *ExtensionVersion,
	azdVersion *semver.Version,
) *InstallCandidate {
	candidate := &InstallCandidate{
		Extension: extension,
		Version:   selectedVersion,
	}
	if azdVersion == nil || len(extension.Versions) == 0 {
		return candidate
	}

	compat := FilterCompatibleVersions(extension.Versions, azdVersion)
	candidate.LatestOverall = compat.LatestOverall
	candidate.LatestCompatible = compat.LatestCompatible
	candidate.HasNewerIncompatible = compat.HasNewerIncompatible
	return candidate
}

// FindInstallableExtensions returns compatibility-aware extension matches or a typed resolution error.
func (m *Manager) FindInstallableExtensions(
	ctx context.Context,
	options *InstallResolutionOptions,
) ([]*ExtensionMetadata, error) {
	result, err := m.ResolveExtensions(ctx, options)
	if err != nil {
		return nil, err
	}
	if resolutionErr := result.Error(); resolutionErr != nil {
		return nil, resolutionErr
	}
	return result.Matches, nil
}

func extensionVersionMatchesResolution(
	version *ExtensionVersion,
	options *InstallResolutionOptions,
) bool {
	if options.Capability != "" && !slices.Contains(version.Capabilities, options.Capability) {
		return false
	}
	if options.Provider != "" && !VersionProvidesProvider(version, options.Capability, options.Provider) {
		return false
	}
	return true
}

// ProviderTypeForCapability returns the provider type a capability is expected to register.
func ProviderTypeForCapability(capability CapabilityType) (ProviderType, bool) {
	switch capability {
	case ServiceTargetProviderCapability:
		return ServiceTargetProviderType, true
	case ProvisioningProviderCapability:
		return ProvisioningProviderType, true
	default:
		return "", false
	}
}

// VersionProvidesProvider reports whether the release supplies the named provider.
// When capability maps to a provider type, the provider entry must use that type.
func VersionProvidesProvider(
	version *ExtensionVersion,
	capability CapabilityType,
	providerName string,
) bool {
	if version == nil || providerName == "" {
		return false
	}
	if capability != "" && !slices.Contains(version.Capabilities, capability) {
		return false
	}
	expectedType, requireType := ProviderTypeForCapability(capability)
	return slices.ContainsFunc(version.Providers, func(provider Provider) bool {
		if !strings.EqualFold(provider.Name, providerName) {
			return false
		}
		return !requireType || provider.Type == expectedType
	})
}
