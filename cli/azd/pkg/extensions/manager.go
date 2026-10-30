// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"log"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Masterminds/semver/v3"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/rzip"
)

const (
	extensionRegistryUrl = "https://aka.ms/azd/extensions/registry"
)

var (
	ErrExtensionNotFound          = errors.New("extension not found")
	ErrInstalledExtensionNotFound = errors.New("extension not found")
	ErrRegistryExtensionNotFound  = errors.New("extension not found in registry")
	ErrExtensionInstalled         = errors.New("extension already installed")
)

// DependencyNotFoundError indicates that a required dependency of an extension
// could not be located in the parent source or the main azd registry.
type DependencyNotFoundError struct {
	// DependencyId is the id of the dependency that could not be resolved.
	DependencyId string
	// ParentId is the id of the extension that declares the dependency.
	ParentId string
}

func (e *DependencyNotFoundError) Error() string {
	return fmt.Sprintf("dependency %s required by %s was not found", e.DependencyId, e.ParentId)
}

// Suggestion returns actionable guidance for installing the missing dependency.
func (e *DependencyNotFoundError) Suggestion() string {
	return fmt.Sprintf(
		"Install the required dependency first with azd extension install %s, then retry.",
		e.DependencyId,
	)
}

// DependencyVersionNotFoundError indicates that a required dependency exists
// but none of its versions satisfy the constraint declared by its parent.
type DependencyVersionNotFoundError struct {
	// DependencyId is the id of the dependency without a matching version.
	DependencyId string
	// ParentId is the id of the extension that declares the dependency.
	ParentId string
	// Constraint is the version constraint that could not be satisfied.
	Constraint string
}

func (e *DependencyVersionNotFoundError) Error() string {
	return fmt.Sprintf(
		"dependency %s required by %s was found, but no version satisfies constraint %q",
		e.DependencyId, e.ParentId, e.Constraint,
	)
}

// Suggestion returns actionable guidance for resolving the dependency constraint.
func (e *DependencyVersionNotFoundError) Suggestion() string {
	return fmt.Sprintf(
		"Install a version of %s that satisfies constraint %q before retrying, include a compatible version "+
			"with %s, or update %s's dependency constraint.",
		e.DependencyId, e.Constraint, e.ParentId, e.ParentId,
	)
}

// DependencyAzdVersionIncompatibleError indicates that dependency versions
// satisfy the parent's constraint, but none support the running azd version.
type DependencyAzdVersionIncompatibleError struct {
	// DependencyId is the id of the incompatible dependency.
	DependencyId string
	// ParentId is the id of the extension that declares the dependency.
	ParentId string
	// Constraint is the dependency version constraint declared by the parent.
	Constraint string
	// RequiredAzdVersion is the azd version constraint declared by the dependency.
	RequiredAzdVersion string
}

func (e *DependencyAzdVersionIncompatibleError) Error() string {
	return fmt.Sprintf(
		"dependency %s required by %s has versions satisfying constraint %q, "+
			"but none are compatible with the current azd version",
		e.DependencyId, e.ParentId, e.Constraint,
	)
}

// Suggestion returns actionable guidance for installing a compatible azd version.
func (e *DependencyAzdVersionIncompatibleError) Suggestion() string {
	if e.RequiredAzdVersion == "" {
		return "Use an azd version compatible with the dependency, then retry."
	}
	return fmt.Sprintf(
		"Use an azd version that satisfies %q, then retry.",
		e.RequiredAzdVersion,
	)
}

// DependencyAmbiguousSourceError indicates that a required dependency of an
// extension was found in more than one configured source, so azd cannot decide
// which one to use. The caller must disambiguate by specifying an exact source.
type DependencyAmbiguousSourceError struct {
	// DependencyId is the id of the dependency that matched multiple sources.
	DependencyId string
	// ParentId is the id of the extension that declares the dependency.
	ParentId string
	// Sources lists the source names the dependency was found in.
	Sources []string
}

func (e *DependencyAmbiguousSourceError) Error() string {
	if len(e.Sources) > 0 {
		return fmt.Sprintf(
			"dependency %s required by %s was found in multiple sources (%s); specify an exact source",
			e.DependencyId, e.ParentId, strings.Join(e.Sources, ", "),
		)
	}
	return fmt.Sprintf(
		"dependency %s required by %s was found in multiple sources; specify an exact source",
		e.DependencyId, e.ParentId,
	)
}

// dependencySources returns the de-duplicated, sorted source names present in a
// set of extension matches. It is used to report which sources a dependency was
// found in when the match is ambiguous.
func dependencySources(matches []*ExtensionMetadata) []string {
	seen := map[string]struct{}{}
	for _, m := range matches {
		if m.Source != "" {
			seen[m.Source] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// ResolveDependency selects metadata for a dependency using the running azd version, preferring
// the parent extension's source and falling back to the main azd registry.
func (m *Manager) ResolveDependency(
	ctx context.Context,
	parent *ExtensionMetadata,
	dependency ExtensionDependency,
) (*ExtensionMetadata, error) {
	return m.resolveDependency(ctx, parent, dependency, true, m.azdVersion)
}

func (m *Manager) resolveDependency(
	ctx context.Context,
	parent *ExtensionMetadata,
	dependency ExtensionDependency,
	allowMainRegistryFallback bool,
	azdVersion *semver.Version,
) (*ExtensionMetadata, error) {
	parentSource := parent.Source
	if parentSource == "" {
		parentSource = MainRegistryName
	}

	sources := []string{parentSource}
	if allowMainRegistryFallback && !strings.EqualFold(parentSource, MainRegistryName) {
		sources = append(sources, MainRegistryName)
	}

	foundWithoutMatchingVersion := false
	var incompatibleVersion *ExtensionVersion
	for _, source := range sources {
		matches, err := m.FindExtensions(ctx, &FilterOptions{
			Id:     dependency.Id,
			Source: source,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to find dependency %s: %w", dependency.Id, err)
		}
		if len(matches) > 1 {
			return nil, &DependencyAmbiguousSourceError{
				DependencyId: dependency.Id,
				ParentId:     parent.Id,
				Sources:      dependencySources(matches),
			}
		}
		if len(matches) == 0 {
			continue
		}

		publishedVersion := bestSatisfyingVersion(dependency.Version, matches[0].Versions)
		if publishedVersion == nil {
			foundWithoutMatchingVersion = true
			continue
		}
		if bestSatisfyingVersionForAzd(dependency.Version, matches[0].Versions, azdVersion) != nil {
			return matches[0], nil
		}
		incompatibleVersion = publishedVersion
	}

	if incompatibleVersion != nil {
		return nil, &DependencyAzdVersionIncompatibleError{
			DependencyId:       dependency.Id,
			ParentId:           parent.Id,
			Constraint:         dependency.Version,
			RequiredAzdVersion: incompatibleVersion.RequiredAzdVersion,
		}
	}

	if foundWithoutMatchingVersion {
		return nil, &DependencyVersionNotFoundError{
			DependencyId: dependency.Id,
			ParentId:     parent.Id,
			Constraint:   dependency.Version,
		}
	}
	return nil, &DependencyNotFoundError{DependencyId: dependency.Id, ParentId: parent.Id}
}

// FilterOptions controls raw catalogue lookup. It does not apply the manager's azd compatibility policy.
// Install-capable callers should use [InstallResolutionOptions].
type FilterOptions struct {
	// Id filters by extension id.
	Id string
	// Namespace filters by extension namespace.
	Namespace string
	// Version requires any published version to match the version preference.
	Version string
	// Source filters by configured source name.
	Source string
	// SourceConfig restricts lookup to one source that is not persisted or cached.
	// It takes precedence over Source.
	SourceConfig *SourceConfig
	// Tags requires all specified tags.
	Tags []string
	// Capability requires any published version to declare the capability.
	Capability CapabilityType
	// Provider requires the release selected without azd compatibility filtering to declare the provider.
	Provider string
}

type sourceFilterPredicate func(config *SourceConfig) bool
type extensionFilterPredicate func(extension *ExtensionMetadata) bool

// IsVersionRange reports whether expr is a semver constraint.
// Exact versions, non-semver tags, an empty value, and "latest" are not ranges.
func IsVersionRange(expr string) bool {
	if expr == "" || strings.EqualFold(expr, "latest") {
		return false
	}
	if _, err := semver.NewVersion(expr); err == nil {
		return false
	}

	hasWildcardPart := slices.ContainsFunc(strings.Split(expr, "."), func(part string) bool {
		return part == "x" || part == "X" || part == "*"
	})
	return strings.ContainsAny(expr, "<>=^~*, ") ||
		strings.Contains(expr, "||") ||
		hasWildcardPart
}

// SatisfiesConstraint reports whether an installed version satisfies a declared dependency
// constraint. Empty, "latest", semver constraints, and exact non-semver tags are supported.
func SatisfiesConstraint(constraint, version string) bool {
	return matchesVersionConstraint(constraint, version)
}

// matchesVersionConstraint reports whether candidate satisfies expr.
// Empty, "latest", semver constraints, and exact non-semver tags are supported.
func matchesVersionConstraint(expr, candidate string) bool {
	if expr == "" || strings.EqualFold(expr, "latest") {
		return true
	}

	constraint, err := semver.NewConstraint(expr)
	if err != nil {
		return strings.EqualFold(expr, candidate)
	}

	parsedVersion, err := semver.NewVersion(candidate)
	if err != nil {
		return strings.EqualFold(expr, candidate)
	}

	return constraint.Check(parsedVersion)
}

// compareVersions orders two versions. ok is false when either is not valid semver, because
// non-semver tags have no defined order.
func compareVersions(a, b string) (order int, ok bool) {
	aSemver, err := semver.NewVersion(a)
	if err != nil {
		return 0, false
	}
	bSemver, err := semver.NewVersion(b)
	if err != nil {
		return 0, false
	}
	return aSemver.Compare(bSemver), true
}

// isDowngrade reports whether moving from current to target is a version regression.
// Returns false if either version is not valid semver.
func isDowngrade(current, target string) bool {
	order, ok := compareVersions(target, current)
	return ok && order < 0
}

// isNewerVersion reports whether candidate is newer than current. A different non-semver tag counts
// as newer, consistent with isDowngrade never treating such a change as a regression.
func isNewerVersion(candidate, current string) bool {
	if order, ok := compareVersions(candidate, current); ok {
		return order > 0
	}
	return !strings.EqualFold(candidate, current)
}

// bestSatisfyingVersion returns the highest published version satisfying expr.
// Empty or "latest" selects the latest version; non-semver tags use exact match.
func bestSatisfyingVersion(expr string, versions []ExtensionVersion) *ExtensionVersion {
	if len(versions) == 0 {
		return nil
	}

	if expr == "" || strings.EqualFold(expr, "latest") {
		return LatestVersion(versions)
	}

	constraint, err := semver.NewConstraint(expr)
	if err != nil {
		// Non-semver expression: fall back to an exact tag match.
		for i := range versions {
			if strings.EqualFold(versions[i].Version, expr) {
				return &versions[i]
			}
		}
		return nil
	}

	var best *semver.Version
	var bestIdx int
	for i := range versions {
		v, err := semver.NewVersion(versions[i].Version)
		if err != nil {
			continue
		}
		if !constraint.Check(v) {
			continue
		}
		if best == nil || v.GreaterThan(best) {
			best = v
			bestIdx = i
		}
	}
	if best == nil {
		return nil
	}
	return &versions[bestIdx]
}

func bestSatisfyingVersionForAzd(
	expr string,
	versions []ExtensionVersion,
	azdVersion *semver.Version,
) *ExtensionVersion {
	if azdVersion == nil {
		return bestSatisfyingVersion(expr, versions)
	}

	compatible := make([]ExtensionVersion, 0, len(versions))
	for i := range versions {
		if VersionIsCompatible(&versions[i], azdVersion) {
			compatible = append(compatible, versions[i])
		}
	}

	return bestSatisfyingVersion(expr, compatible)
}

// ResolveExtensionVersion selects the highest release that matches versionPreference and azdVersion.
func ResolveExtensionVersion(
	extension *ExtensionMetadata,
	versionPreference string,
	azdVersion *semver.Version,
) (*ExtensionVersion, error) {
	if extension == nil {
		return nil, fmt.Errorf("extension metadata cannot be nil")
	}

	selected := bestSatisfyingVersionForAzd(versionPreference, extension.Versions, azdVersion)
	if selected != nil {
		return selected, nil
	}

	published := bestSatisfyingVersion(versionPreference, extension.Versions)
	if published != nil {
		return nil, &ExtensionAzdVersionIncompatibleError{
			ExtensionId: extension.Id,
			Version:     versionPreference,
			AzdVersion:  azdVersion,
			Matches:     []*ExtensionMetadata{extension},
		}
	}

	return nil, &ExtensionVersionNotFoundError{
		ExtensionId: extension.Id,
		Version:     versionPreference,
		Matches:     []*ExtensionMetadata{extension},
		AzdVersion:  azdVersion,
	}
}

// createExtensionFilter creates a comprehensive filter that checks ALL criteria with AND logic
func createExtensionFilter(options *FilterOptions) extensionFilterPredicate {
	return func(extension *ExtensionMetadata) bool {
		// Check Id filter
		if options.Id != "" {
			if !strings.EqualFold(extension.Id, options.Id) {
				return false
			}
		}

		// Check Namespace filter
		if options.Namespace != "" {
			if !strings.EqualFold(extension.Namespace, options.Namespace) {
				return false
			}
		}

		// Check Version filter - extension must have at least one matching version.
		if options.Version != "" && !strings.EqualFold(options.Version, "latest") {
			hasVersion := slices.ContainsFunc(extension.Versions, func(version ExtensionVersion) bool {
				return matchesVersionConstraint(options.Version, version.Version)
			})
			if !hasVersion {
				return false
			}
		}

		// Check Source filter
		if options.Source != "" {
			if !strings.EqualFold(extension.Source, options.Source) {
				return false
			}
		}

		// Check Tags filter - extension must have ALL specified tags
		if len(options.Tags) > 0 {
			for _, optionTag := range options.Tags {
				hasTag := slices.ContainsFunc(extension.Tags, func(extensionTag string) bool {
					return strings.EqualFold(optionTag, extensionTag)
				})
				if !hasTag {
					return false
				}
			}
		}

		// Catalogue capability queries match any published version.
		if options.Capability != "" {
			hasCapability := slices.ContainsFunc(extension.Versions, func(version ExtensionVersion) bool {
				return slices.Contains(version.Capabilities, options.Capability)
			})
			if !hasCapability {
				return false
			}
		}

		// Provider queries use the release selected without azd compatibility filtering.
		if options.Provider != "" {
			selectedVersion, err := ResolveExtensionVersion(extension, options.Version, nil)
			if err != nil {
				return false
			}
			if !VersionProvidesProvider(selectedVersion, options.Capability, options.Provider) {
				return false
			}
		}

		// All criteria passed
		return true
	}
}

// Manager is responsible for managing extensions
type Manager struct {
	sourceManager *SourceManager
	sources       []Source
	installed     map[string]*Extension
	configManager config.UserConfigManager
	userConfig    config.Config
	pipeline      azruntime.Pipeline
	azdVersion    *semver.Version

	// Lazy runner to avoid circular dependency issues since extension manager is used during command bootstrapping
	lazyRunner *lazy.Lazy[*Runner]
}

// ManagerOptions controls extension manager compatibility policy.
type ManagerOptions struct {
	// AzdVersion overrides the running azd version used for install resolution.
	AzdVersion *semver.Version
	// IgnoreAzdCompatibility disables azd compatibility checks.
	IgnoreAzdCompatibility bool
}

// NewManager creates a new extension manager
func NewManager(
	configManager config.UserConfigManager,
	sourceManager *SourceManager,
	lazyRunner *lazy.Lazy[*Runner],
	transport policy.Transporter,
) (*Manager, error) {
	return NewManagerWithOptions(
		configManager,
		sourceManager,
		lazyRunner,
		transport,
		ManagerOptions{},
	)
}

// NewManagerWithOptions creates an extension manager with explicit compatibility policy.
func NewManagerWithOptions(
	configManager config.UserConfigManager,
	sourceManager *SourceManager,
	lazyRunner *lazy.Lazy[*Runner],
	transport policy.Transporter,
	options ManagerOptions,
) (*Manager, error) {
	var azdVersion *semver.Version
	if !options.IgnoreAzdCompatibility {
		azdVersion = CurrentAzdVersion()
		if options.AzdVersion != nil {
			azdVersion = options.AzdVersion
		}
	}

	userConfig, err := configManager.Load()
	if err != nil {
		return nil, err
	}

	pipeline := azruntime.NewPipeline("azd-extensions", "1.0.0", azruntime.PipelineOptions{}, &policy.ClientOptions{
		Transport: transport,
	})

	return &Manager{
		userConfig:    userConfig,
		configManager: configManager,
		sourceManager: sourceManager,
		lazyRunner:    lazyRunner,
		pipeline:      pipeline,
		azdVersion:    azdVersion,
	}, nil
}

// AzdVersion returns the version used for extension compatibility checks.
func (m *Manager) AzdVersion() *semver.Version {
	return m.azdVersion
}

// ResolveVersion selects an extension release using the manager's compatibility policy.
func (m *Manager) ResolveVersion(
	extension *ExtensionMetadata,
	versionPreference string,
) (*ExtensionVersion, error) {
	return ResolveExtensionVersion(extension, versionPreference, m.azdVersion)
}

// ListInstalled retrieves a list of installed extensions
func (m *Manager) ListInstalled() (map[string]*Extension, error) {
	var extensions map[string]*Extension

	if m.installed != nil {
		return m.installed, nil
	}

	ok, err := m.userConfig.GetSection(installedConfigKey, &extensions)
	if err != nil {
		return nil, fmt.Errorf("failed to get extensions section: %w", err)
	}

	if !ok || extensions == nil {
		extensions = map[string]*Extension{}
	}

	m.installed = extensions

	return m.installed, nil
}

// GetInstalled retrieves an installed extension by filter criteria
func (m *Manager) GetInstalled(options FilterOptions) (*Extension, error) {
	extensions, err := m.ListInstalled()
	if err != nil {
		return nil, err
	}

	isExtensionMatch := createExtensionFilter(&options)

	// Convert installed extensions to ExtensionMetadata for filtering
	for _, extension := range extensions {
		// Create metadata representation for filtering
		metadata := &ExtensionMetadata{
			Id:        extension.Id,
			Namespace: extension.Namespace,
			Source:    extension.Source,
			Tags:      []string{}, // Installed extensions don't store tags
		}

		// Apply the same filter logic as other methods
		if isExtensionMatch(metadata) {
			return extension, nil
		}
	}

	return nil, ErrInstalledExtensionNotFound
}

// IsOfficialRegistrySource verifies the configured source used by an
// installed extension before allowing it to report telemetry.
func (m *Manager) IsOfficialRegistrySource(ctx context.Context, name string) (bool, error) {
	if strings.TrimSpace(name) == "" {
		return false, nil
	}

	source, err := m.sourceManager.Get(ctx, name)
	if err != nil {
		if errors.Is(err, ErrSourceNotFound) {
			return false, nil
		}
		return false, err
	}

	return IsOfficialMainRegistrySource(source), nil
}

// UpdateInstalled updates an installed extension's metadata in the config
func (m *Manager) UpdateInstalled(extension *Extension) error {
	return m.updateInstalled(extension.Id, func(*Extension) *Extension { return extension })
}

// updateInstalled transforms freshly decoded metadata and invalidates the cache after saving.
func (m *Manager) updateInstalled(id string, update func(*Extension) *Extension) error {
	var installed map[string]*Extension
	_, err := m.userConfig.GetSection(installedConfigKey, &installed)
	if err != nil {
		return fmt.Errorf("failed to list installed extensions: %w", err)
	}

	current, exists := installed[id]
	if !exists {
		return ErrInstalledExtensionNotFound
	}

	installed[id] = update(current)
	previous, _ := m.userConfig.Get(installedConfigKey)
	if err := m.userConfig.Set(installedConfigKey, installed); err != nil {
		return fmt.Errorf("failed to set extensions section: %w", err)
	}

	if err := m.configManager.Save(m.userConfig); err != nil {
		if restoreErr := m.userConfig.Set(installedConfigKey, previous); restoreErr != nil {
			return errors.Join(
				fmt.Errorf("failed to save user config: %w", err),
				fmt.Errorf("failed to restore installed extension metadata: %w", restoreErr),
			)
		}
		return fmt.Errorf("failed to save user config: %w", err)
	}

	// Invalidate cache so subsequent calls reflect the updated extension
	m.installed = nil

	return nil
}

// FindExtensions performs a raw catalogue lookup without applying the manager's azd compatibility policy.
func (m *Manager) FindExtensions(ctx context.Context, options *FilterOptions) ([]*ExtensionMetadata, error) {
	allExtensions := []*ExtensionMetadata{}

	if options == nil {
		options = &FilterOptions{}
	}

	var sourceFilterPredicate sourceFilterPredicate
	if options.Source != "" {
		sourceFilterPredicate = func(config *SourceConfig) bool {
			return strings.EqualFold(config.Name, options.Source)
		}
	}

	filterOptions := options
	if options.SourceConfig != nil {
		filterOptionsCopy := *options
		filterOptionsCopy.Source = ""
		filterOptions = &filterOptionsCopy
	}

	// Use the centralized extension filter
	extensionFilter := createExtensionFilter(filterOptions)

	var sources []Source
	var err error
	if options.SourceConfig != nil {
		source, err := m.sourceManager.CreateSource(ctx, options.SourceConfig)
		if err != nil {
			if schemaErr, ok := errors.AsType[*ErrUnsupportedRegistrySchema](err); ok {
				return nil, NewUnsupportedRegistrySchemaError(schemaErr)
			}
			return nil, fmt.Errorf("failed initializing extension source: %w", err)
		}
		sources = []Source{source}
	} else {
		sources, err = m.getSources(ctx, sourceFilterPredicate)
	}
	if err != nil {
		return nil, fmt.Errorf("failed listing extensions: %w", err)
	}

	for _, source := range sources {
		filteredExtensions := []*ExtensionMetadata{}
		sourceExtensions, err := source.ListExtensions(ctx)
		if err != nil {
			return nil, fmt.Errorf("unable to list extension: %w", err)
		}

		for _, extension := range sourceExtensions {
			if extensionFilter(extension) {
				filteredExtensions = append(filteredExtensions, extension)
			}
		}

		// Sort by source, then repository path and finally name
		slices.SortFunc(filteredExtensions, func(a *ExtensionMetadata, b *ExtensionMetadata) int {
			if a.Source != b.Source {
				return strings.Compare(a.Source, b.Source)
			}

			return strings.Compare(a.Id, b.Id)
		})

		allExtensions = append(allExtensions, filteredExtensions...)
	}

	return allExtensions, nil
}

// Install installs an extension using the running azd version.
func (m *Manager) Install(
	ctx context.Context,
	extension *ExtensionMetadata,
	versionPreference string,
) (*ExtensionVersion, error) {
	return m.InstallWithOptions(ctx, extension, InstallOptions{
		VersionPreference: versionPreference,
	})
}

// InstallOptions controls how Manager.InstallWithOptions behaves.
type InstallOptions struct {
	// VersionPreference is the version constraint or exact tag to install.
	// Empty or "latest" selects the highest available version.
	VersionPreference string
	// SkipDependencies installs only the target extension, without resolving or
	// installing its declared dependencies and without enforcing the installed
	// dependency version constraints. It is used when the caller only needs the
	// extension's own binary (e.g. generating command snapshots) and cannot
	// guarantee that the registry's dependency graph is internally consistent.
	SkipDependencies bool
	// SkipMainRegistryDependencyFallback prevents dependencies missing from the
	// parent source from falling back to the main azd registry. Self-contained
	// bundle installs use this to remain isolated from network sources.
	SkipMainRegistryDependencyFallback bool
	// SkipDependencyUpdates prevents the install from updating dependencies that are already
	// installed. An installed dependency that does not satisfy a declared constraint then fails
	// the install.
	SkipDependencyUpdates bool
}

// InstallWithOptions installs an extension using the supplied options.
func (m *Manager) InstallWithOptions(
	ctx context.Context,
	extension *ExtensionMetadata,
	opts InstallOptions,
) (*ExtensionVersion, error) {
	version, _, err := m.installInternal(ctx, extension, opts, false, false, newDependencyWalk())
	return version, err
}

// installInternal installs an extension and its dependencies. It returns the results of updates to
// dependencies that were already installed, including updates made by nested dependency installs.
// deferDependencyUpdates defers installed-dependency reconciliation; it is set by the
// upgrade flow so dependency reconciliation can run after the parent has been reinstalled.
// asDependency records that the extension is being installed only because another extension
// requires it; the flag is preserved across upgrades and consulted by PlanUninstall.
func (m *Manager) installInternal(
	ctx context.Context,
	extension *ExtensionMetadata,
	opts InstallOptions,
	deferDependencyUpdates bool,
	asDependency bool,
	walk *dependencyWalk,
) (extVersion *ExtensionVersion, dependencyResults []UpgradeResult, err error) {
	if extension == nil {
		return nil, nil, fmt.Errorf("extension metadata cannot be nil")
	}

	if !walk.startInstall(extension.Id) {
		return nil, nil, fmt.Errorf(
			"dependency cycle detected involving extension %s",
			extension.Id,
		)
	}
	defer walk.finishInstall(extension.Id)

	ctx, span := tracing.Start(ctx, events.ExtensionInstallEvent)
	// Set the extension id immediately so failure spans can be correlated to the
	// extension being installed. The version is added later, once it has been resolved.
	span.SetAttributes(
		fields.ExtensionId.String(extension.Id),
		fields.ExtensionSourceCategory.String(string(extension.SourceCategoryOrUnknown())),
	)
	defer func() {
		span.EndWithStatus(err)
	}()

	installed, err := m.GetInstalled(FilterOptions{Id: extension.Id})
	if err == nil && installed != nil {
		return nil, nil, fmt.Errorf("%s %w", extension.Id, ErrExtensionInstalled)
	}

	// Resolve to the latest published version that satisfies the preference.
	selectedVersion, err := ResolveExtensionVersion(extension, opts.VersionPreference, m.azdVersion)
	if err != nil {
		return nil, nil, err
	}

	// Record the resolved version on the span so failures during install
	// (artifact download, checksum, copy, config save) still emit it.
	span.SetAttributes(fields.ExtensionVersion.String(selectedVersion.Version))

	// Binaries are optional as long as dependencies are provided
	// This allows for extensions that are just extension packs
	if len(selectedVersion.Artifacts) == 0 && len(selectedVersion.Dependencies) == 0 {
		return nil, nil, fmt.Errorf("no binaries or dependencies available for this version")
	}

	// Install dependencies unless the caller opted out. Skipping bypasses both the
	// dependency install loop and the installed-dependency constraint check, so the
	// target extension installs even when the registry's dependency graph is
	// transiently inconsistent (e.g. during a coordinated multi-extension bump).
	if !opts.SkipDependencies && len(selectedVersion.Dependencies) > 0 {
		dependencyResults, err = m.installDependencies(
			ctx, extension, selectedVersion.Dependencies, opts, deferDependencyUpdates, walk,
		)
		if err != nil {
			return nil, dependencyResults, err
		}
	}

	hasArtifact := len(selectedVersion.Artifacts) > 0
	var relativeExtensionPath string
	var targetPath string

	// Install the artifacts
	if hasArtifact {
		// Step 3: Find the artifact for the current OS
		artifact, err := findArtifactForCurrentOS(selectedVersion)
		if err != nil {
			return nil, dependencyResults, fmt.Errorf("failed to find artifact for current OS: %w", err)
		}

		// Step 4: Download the artifact to a temp location
		tempFilePath, err := m.downloadArtifact(ctx, artifact.URL)
		if err != nil {
			return nil, dependencyResults, fmt.Errorf("failed to download artifact: %w", err)
		}

		// Clean up the temp file after all scenarios
		defer os.Remove(tempFilePath)

		// Step 5: Validate the checksum if provided
		if err := validateChecksum(tempFilePath, artifact.Checksum); err != nil {
			return nil, dependencyResults, fmt.Errorf("checksum validation failed: %w", err)
		}

		userConfigDir, err := config.GetUserConfigDir()
		if err != nil {
			return nil, dependencyResults, fmt.Errorf("failed to get user config directory: %w", err)
		}

		targetDir := filepath.Join(userConfigDir, "extensions", extension.Id)
		if err := os.MkdirAll(targetDir, os.ModePerm); err != nil {
			return nil, dependencyResults, fmt.Errorf("failed to create target directory: %w", err)
		}

		// Step 6: Copy the artifact to the target directory
		// Check if artifact is a zip file, if so extract it to the target directory
		if strings.HasSuffix(tempFilePath, ".zip") {
			if err := rzip.ExtractToDirectory(tempFilePath, targetDir); err != nil {
				return nil, dependencyResults, fmt.Errorf("failed to extract zip file: %w", err)
			}
		} else if strings.HasSuffix(tempFilePath, ".tar.gz") {
			if err := rzip.ExtractTarGzToDirectory(tempFilePath, targetDir); err != nil {
				return nil, dependencyResults, fmt.Errorf("failed to extract tar.gz file: %w", err)
			}
		} else {
			targetPath = filepath.Join(targetDir, filepath.Base(tempFilePath))
			if err := copyFile(tempFilePath, targetPath); err != nil {
				return nil, dependencyResults, fmt.Errorf("failed to copy artifact to target location: %w", err)
			}
		}

		entryPoint := selectedVersion.EntryPoint
		if platformEntryPoint, has := artifact.AdditionalMetadata["entryPoint"]; has {
			entryPoint = fmt.Sprint(platformEntryPoint)
		}
		if entryPoint == "" {
			switch runtime.GOOS {
			case "windows":
				entryPoint = fmt.Sprintf("%s.exe", extension.Id)
			default:
				entryPoint = extension.Id
			}
		}

		targetPath := filepath.Join(targetDir, entryPoint)

		// Prevent path traversal attacks by ensuring entryPoint stays within the extension install directory
		if !osutil.IsPathContained(targetDir, targetPath) {
			return nil, dependencyResults, fmt.Errorf(
				"invalid entry point path: entry point %q resolves outside extension directory. "+
					"Use relative paths (e.g., 'bin/myext' or 'bin\\myext') without '..' sequences", entryPoint)
		}

		// Need to set the executable permission for the binary
		// This change is specifically required for Linux but will apply consistently across all platforms
		if err := os.Chmod(targetPath, osutil.PermissionExecutableFile); err != nil {
			return nil, dependencyResults, fmt.Errorf("failed to set executable permission: %w", err)
		}

		relativeExtensionPath, err = filepath.Rel(userConfigDir, targetPath)
		if err != nil {
			return nil, dependencyResults, fmt.Errorf("failed to get relative path: %w", err)
		}
	}

	// Step 7: Update the user config with the installed extension
	extensions, err := m.ListInstalled()
	if err != nil {
		return nil, dependencyResults, fmt.Errorf("failed to list installed extensions: %w", err)
	}

	extensions[extension.Id] = &Extension{
		Id:             extension.Id,
		Capabilities:   selectedVersion.Capabilities,
		Namespace:      extension.Namespace,
		DisplayName:    extension.DisplayName,
		Description:    extension.Description,
		Version:        selectedVersion.Version,
		Usage:          selectedVersion.Usage,
		Path:           relativeExtensionPath,
		Source:         extension.Source,
		SourceCategory: extension.SourceCategoryOrUnknown(),
		Providers:      selectedVersion.Providers,
		McpConfig:      selectedVersion.McpConfig,
		// Declared dependencies are recorded even when SkipDependencies is set: they describe
		// the installed version, and uninstall planning relies on them to protect dependencies.
		Dependencies:          slices.Clone(selectedVersion.Dependencies),
		InstalledAsDependency: asDependency,
	}

	if err := m.userConfig.Set(installedConfigKey, extensions); err != nil {
		return nil, dependencyResults, fmt.Errorf("failed to set extensions section: %w", err)
	}

	if err := m.configManager.Save(m.userConfig); err != nil {
		return nil, dependencyResults, fmt.Errorf("failed to save user config: %w", err)
	}

	log.Printf(
		"Extension '%s' (version %s) installed successfully to %s\n",
		extension.Id,
		selectedVersion.Version,
		targetPath,
	)

	// Fetch and cache metadata if extension supports it
	installedExtension := extensions[extension.Id]
	if installedExtension.HasCapability(MetadataCapability) {
		if err := m.fetchAndCacheMetadata(ctx, installedExtension); err != nil {
			// Log warning but don't fail installation
			log.Printf("Warning: Failed to fetch extension metadata for '%s': %v\n", extension.Id, err)
		}
	}

	return selectedVersion, dependencyResults, nil
}

// installDependencies installs missing dependencies of parent and, unless deferDependencyUpdates is
// set, updates installed dependencies that do not satisfy their constraints. Versions are selected
// against every constraint known to the operation, so a dependency chosen for one extension stays
// within the ranges declared by its siblings, its ancestors, and installed extensions.
func (m *Manager) installDependencies(
	ctx context.Context,
	parent *ExtensionMetadata,
	dependencies []ExtensionDependency,
	opts InstallOptions,
	deferDependencyUpdates bool,
	walk *dependencyWalk,
) ([]UpgradeResult, error) {
	// Record every constraint before processing any dependency, so nested installs and updates
	// also respect constraints on siblings that are processed later.
	walk.setPending(parent.Id, dependencies)

	allowFallback := !opts.SkipMainRegistryDependencyFallback
	conflictError := func(dependency ExtensionDependency, installed *Extension) error {
		// Metadata only improves the suggestion, so a lookup failure keeps the generic one.
		metadata, _ := m.resolveDependency(ctx, parent, dependency, allowFallback, m.azdVersion)
		return m.dependencyConflictError(parent.Id, dependency, installed, metadata, walk)
	}

	var results []UpgradeResult
	for _, dependency := range dependencies {
		installedDependency, err := m.GetInstalled(FilterOptions{Id: dependency.Id})
		if err == nil && installedDependency != nil {
			if deferDependencyUpdates || matchesVersionConstraint(dependency.Version, installedDependency.Version) {
				continue
			}
			if opts.SkipDependencyUpdates {
				return results, fmt.Errorf(
					"dependency updates are disabled: %w", conflictError(dependency, installedDependency),
				)
			}
			updates := m.evaluateDependencyChanges(ctx, parent, []ExtensionDependency{dependency}, UpgradeOptions{
				UpgradeDependencies:                true,
				SkipMainRegistryDependencyFallback: opts.SkipMainRegistryDependencyFallback,
			}, walk)
			results = append(results, updates...)
			if err := DependencyUpgradeError(updates); err != nil {
				return results, fmt.Errorf("failed to update dependencies for extension %s: %w", parent.Id, err)
			}
			continue
		}

		dependencyMetadata, err := m.resolveDependency(ctx, parent, dependency, allowFallback, m.azdVersion)
		if err != nil {
			return results, err
		}
		constraints, err := m.dependencyConstraints(dependency.Id, walk)
		if err != nil {
			return results, err
		}
		// resolveDependency found a compatible release for this constraint alone, so no match
		// means the constraints conflict.
		version := bestVersionForConstraints(constraints, dependencyMetadata.Versions, m.azdVersion)
		if version == nil {
			return results, &DependencyConstraintsConflictError{
				DependencyId: dependencyMetadata.Id,
				Constraints:  constraints,
			}
		}

		dependencyOpts := InstallOptions{
			VersionPreference:                  version.Version,
			SkipMainRegistryDependencyFallback: opts.SkipMainRegistryDependencyFallback,
			SkipDependencyUpdates:              opts.SkipDependencyUpdates,
		}
		_, nested, err := m.installInternal(ctx, dependencyMetadata, dependencyOpts, false, true, walk)
		results = append(results, nested...)
		if err != nil && !errors.Is(err, ErrExtensionInstalled) {
			return results, fmt.Errorf("failed to install dependency: %w", err)
		}
	}

	if deferDependencyUpdates {
		return results, nil
	}
	// A nested install or update can change a dependency that was checked earlier. Validate the
	// final versions before the parent is recorded as installed.
	for _, dependency := range dependencies {
		installedDependency, err := m.GetInstalled(FilterOptions{Id: dependency.Id})
		if err != nil || matchesVersionConstraint(dependency.Version, installedDependency.Version) {
			continue
		}
		return results, fmt.Errorf(
			"dependencies of %s are not satisfied: %w", parent.Id, conflictError(dependency, installedDependency),
		)
	}
	return results, nil
}

// Uninstall uninstalls an extension by name.
func (m *Manager) Uninstall(ctx context.Context, id string) error {
	// An empty id would match an arbitrary installed record.
	if strings.TrimSpace(id) == "" {
		return ErrEmptyExtensionId
	}

	// Get the installed extension
	extension, err := m.GetInstalled(FilterOptions{Id: id})
	if err != nil {
		return fmt.Errorf("failed to get installed extension: %w", err)
	}

	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get user config directory: %w", err)
	}

	extensionDir := filepath.Join(userConfigDir, "extensions", extension.Id)
	if err := osutil.RemoveAll(ctx, extensionDir); err != nil {
		return fmt.Errorf("failed to remove extension: %w", err)
	}

	// Update the user config
	extensions, err := m.ListInstalled()
	if err != nil {
		return fmt.Errorf("failed to list installed extensions: %w", err)
	}

	delete(extensions, id)

	if err := m.userConfig.Set(installedConfigKey, extensions); err != nil {
		return fmt.Errorf("failed to set extensions section: %w", err)
	}

	if err := m.configManager.Save(m.userConfig); err != nil {
		return fmt.Errorf("failed to save user config: %w", err)
	}

	log.Printf("Extension '%s' uninstalled successfully\n", id)
	return nil
}

// UpgradeOptions controls how Manager.Upgrade behaves.
type UpgradeOptions struct {
	// VersionPreference is the version constraint or exact tag to install.
	// Empty or "latest" selects the highest available version.
	VersionPreference string
	// UpgradeDependencies enables automatic upgrades for installed dependencies.
	UpgradeDependencies bool
	// SkipDependencies reinstalls only the target extension, without resolving or
	// installing its declared dependencies, without enforcing the installed
	// dependency version constraints, and without reconciling installed dependency
	// versions. It mirrors InstallOptions.SkipDependencies for the reinstall an
	// upgrade performs, so `--no-dependencies` behaves the same whether the
	// extension is being installed fresh or over an existing install.
	SkipDependencies bool
	// SkipMainRegistryDependencyFallback mirrors the InstallOptions behavior for
	// the reinstall performed during upgrade.
	SkipMainRegistryDependencyFallback bool
	// PromoteToExplicit clears dependency ownership for an explicit reinstall.
	// Updates leave it false to preserve ownership.
	PromoteToExplicit bool
}

// DefaultUpgradeOptions returns UpgradeOptions with dependency upgrades enabled.
func DefaultUpgradeOptions(versionPreference string) UpgradeOptions {
	return UpgradeOptions{
		VersionPreference:   versionPreference,
		UpgradeDependencies: true,
	}
}

// Upgrade upgrades the extension to the specified version.
// Empty or "latest" selects the latest available version.
// The returned slice contains dependency upgrade results.
func (m *Manager) Upgrade(
	ctx context.Context,
	extension *ExtensionMetadata,
	opts UpgradeOptions,
) (*ExtensionVersion, []UpgradeResult, error) {
	if extension == nil {
		return nil, nil, fmt.Errorf("extension metadata cannot be nil")
	}

	return m.upgradeInternal(ctx, extension, opts, newDependencyWalk(extension.Id))
}

// ReconcileDependencies upgrades installed dependencies for the selected extension version.
func (m *Manager) ReconcileDependencies(
	ctx context.Context,
	extension *ExtensionMetadata,
	opts UpgradeOptions,
) (*ExtensionVersion, []UpgradeResult, error) {
	if extension == nil {
		return nil, nil, fmt.Errorf("extension metadata cannot be nil")
	}

	selectedVersion, err := ResolveExtensionVersion(extension, opts.VersionPreference, m.azdVersion)
	if err != nil {
		return nil, nil, err
	}

	if len(selectedVersion.Dependencies) == 0 {
		return selectedVersion, nil, nil
	}

	if err := m.BackfillDependencies(extension.Id, extension.Source, selectedVersion); err != nil {
		return nil, nil, err
	}

	results := m.evaluateDependencyChanges(
		ctx, extension, selectedVersion.Dependencies, opts, newDependencyWalk(extension.Id),
	)
	return selectedVersion, results, nil
}

// BackfillDependencies records the dependency snapshot on an installed record that predates
// dependency tracking. It only applies to metadata from the installed source and version when
// the record has no snapshot. Ownership is never inferred.
func (m *Manager) BackfillDependencies(id, source string, version *ExtensionVersion) error {
	if version == nil {
		return nil
	}
	installed, err := m.GetInstalled(FilterOptions{Id: id})
	if errors.Is(err, ErrInstalledExtensionNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get installed extension %s: %w", id, err)
	}
	if len(installed.Dependencies) > 0 ||
		!strings.EqualFold(installed.Source, source) ||
		installed.Version != version.Version ||
		len(version.Dependencies) == 0 {
		return nil
	}

	if err := m.updateInstalled(installed.Id, func(record *Extension) *Extension {
		record.Dependencies = slices.Clone(version.Dependencies)
		return record
	}); err != nil {
		return fmt.Errorf("failed to record dependencies for %s: %w", id, err)
	}
	return nil
}

// BackfillInstalledDependencies records dependency snapshots for every installed record that
// predates dependency tracking, so their constraints are known before a command selects versions.
// It is best effort: records whose metadata cannot be found or saved are left unchanged, and the
// commands that update them report their own failures.
func (m *Manager) BackfillInstalledDependencies(ctx context.Context) {
	installed, err := m.ListInstalled()
	if err != nil {
		log.Printf("skipping dependency backfill: %v", err)
		return
	}
	candidates := slices.DeleteFunc(slices.Sorted(maps.Keys(installed)), func(id string) bool {
		record := installed[id]
		return len(record.Dependencies) > 0 || record.Source == "" || record.Source == BundleSourceName
	})
	if len(candidates) == 0 {
		return
	}

	// An unfiltered lookup loads every source once and caches them for the rest of the command;
	// filtered lookups would fetch a source again for each record.
	available, err := m.FindExtensions(ctx, &FilterOptions{})
	if err != nil {
		log.Printf("skipping dependency backfill: %v", err)
		return
	}
	bySourceAndId := make(map[[2]string]*ExtensionMetadata, len(available))
	for _, extension := range available {
		bySourceAndId[[2]string{strings.ToLower(extension.Source), strings.ToLower(extension.Id)}] = extension
	}
	for _, id := range candidates {
		record := installed[id]
		metadata := bySourceAndId[[2]string{strings.ToLower(record.Source), strings.ToLower(record.Id)}]
		if metadata == nil {
			continue
		}
		version := FindVersion(metadata.Versions, record.Version)
		if err := m.BackfillDependencies(record.Id, metadata.Source, version); err != nil {
			log.Printf("skipping dependency backfill for %s: %v", record.Id, err)
		}
	}
}

// MarkExplicitlyInstalled prevents automatic removal as an unused dependency.
// Repeated calls are a no-op.
func (m *Manager) MarkExplicitlyInstalled(id string) error {
	installed, err := m.GetInstalled(FilterOptions{Id: id})
	if err != nil {
		return err
	}
	if !installed.InstalledAsDependency {
		return nil
	}

	if err := m.updateInstalled(installed.Id, func(record *Extension) *Extension {
		record.InstalledAsDependency = false
		return record
	}); err != nil {
		return fmt.Errorf("failed to mark %s as explicitly installed: %w", id, err)
	}
	return nil
}

// upgradeInternal performs the reinstall and any dependency upgrades. The returned results include
// updates to installed dependencies that new dependencies required during the reinstall.
func (m *Manager) upgradeInternal(
	ctx context.Context,
	extension *ExtensionMetadata,
	opts UpgradeOptions,
	walk *dependencyWalk,
) (version *ExtensionVersion, results []UpgradeResult, err error) {
	installed, err := m.GetInstalled(FilterOptions{Id: extension.Id})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get installed extension: %w", err)
	}
	asDependency := installed.InstalledAsDependency && !opts.PromoteToExplicit

	finish, err := m.prepareUpgradeRecovery(ctx, installed, extension.Id)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		err = errors.Join(err, finish(recoveryCtx, err != nil))
	}()

	if err := m.Uninstall(ctx, installed.Id); err != nil {
		return nil, nil, fmt.Errorf("failed to uninstall extension: %w", err)
	}

	// Skip the installed-dependency constraint check: the previous parent has just been
	// uninstalled and any stale dependency will be reconciled by evaluateDependencyChanges below.
	extensionVersion, installResults, err := m.installInternal(ctx, extension, InstallOptions{
		VersionPreference:                  opts.VersionPreference,
		SkipDependencies:                   opts.SkipDependencies,
		SkipMainRegistryDependencyFallback: opts.SkipMainRegistryDependencyFallback,
		SkipDependencyUpdates:              !opts.UpgradeDependencies,
	}, true, asDependency, walk)
	if err != nil {
		return nil, installResults, fmt.Errorf("failed to install extension: %w", err)
	}

	// When dependencies are skipped, the reinstall above installs only the target
	// extension; do not resolve or reconcile any dependency versions.
	if opts.SkipDependencies {
		return extensionVersion, installResults, nil
	}

	if extensionVersion == nil || len(extensionVersion.Dependencies) == 0 {
		return extensionVersion, installResults, nil
	}

	depUpgrades := m.evaluateDependencyChanges(ctx, extension, extensionVersion.Dependencies, opts, walk)
	return extensionVersion, append(installResults, depUpgrades...), nil
}

// prepareUpgradeRecovery preserves the target's files and installed record
// until its replacement succeeds. Completed dependency changes are retained.
func (m *Manager) prepareUpgradeRecovery(
	ctx context.Context, installed *Extension, replacementID string,
) (func(context.Context, bool) error, error) {
	if installed.Id == "" || installed.Id == "." || installed.Id == ".." ||
		strings.ContainsAny(installed.Id, `/\`) {
		return nil, fmt.Errorf("invalid installed extension directory for %q", installed.Id)
	}
	if !strings.EqualFold(installed.Id, replacementID) ||
		replacementID == "" || replacementID == "." || replacementID == ".." ||
		strings.ContainsAny(replacementID, `/\`) {
		return nil, fmt.Errorf("invalid replacement extension directory for %q", replacementID)
	}
	// Snapshot persisted metadata without copying Extension's runtime locks.
	data, err := json.Marshal(installed)
	if err != nil {
		return nil, fmt.Errorf("failed to snapshot installed extension metadata: %w", err)
	}
	previous := new(Extension)
	if err := json.Unmarshal(data, previous); err != nil {
		return nil, fmt.Errorf("failed to snapshot installed extension metadata: %w", err)
	}
	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user config directory: %w", err)
	}
	extensionRoot := filepath.Join(userConfigDir, "extensions")
	extensionDir := filepath.Join(extensionRoot, installed.Id)
	if installed.Id == "" || extensionDir == extensionRoot || !osutil.IsPathContained(extensionRoot, extensionDir) {
		return nil, fmt.Errorf("invalid installed extension directory for %q", installed.Id)
	}
	replacementDir := filepath.Join(extensionRoot, replacementID)
	if replacementDir == extensionRoot || !osutil.IsPathContained(extensionRoot, replacementDir) {
		return nil, fmt.Errorf("invalid replacement extension directory for %q", replacementID)
	}
	if err := os.MkdirAll(extensionRoot, osutil.PermissionDirectory); err != nil {
		return nil, fmt.Errorf("failed to create extension directory: %w", err)
	}
	backupDir, err := os.MkdirTemp(extensionRoot, ".upgrade-backup-")
	if err != nil {
		return nil, fmt.Errorf("failed to create extension backup: %w", err)
	}
	backupPath := filepath.Join(backupDir, "installed")
	metadataPath := filepath.Join(backupDir, "metadata.json")
	if err := os.WriteFile(metadataPath, data, osutil.PermissionFileOwnerOnly); err != nil {
		return nil, errors.Join(
			fmt.Errorf("failed to preserve installed extension metadata: %w", err),
			os.RemoveAll(backupDir),
		)
	}
	hasFiles := false
	if err := osutil.Rename(ctx, extensionDir, backupPath); err == nil {
		hasFiles = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.Join(
			fmt.Errorf("failed to preserve installed extension files: %w", err),
			os.RemoveAll(backupDir),
		)
	}
	return func(ctx context.Context, failed bool) error {
		if failed {
			if err := osutil.RemoveAll(ctx, replacementDir); err != nil {
				return fmt.Errorf("failed to remove unsuccessful replacement; backup retained at %q: %w", backupDir, err)
			}
			if hasFiles {
				if err := osutil.Rename(ctx, backupPath, extensionDir); err != nil {
					return fmt.Errorf("failed to restore installed files; backup retained at %q: %w", backupDir, err)
				}
			}
			records, err := m.ListInstalled()
			if err != nil {
				return fmt.Errorf(
					"failed to load installed metadata during recovery; backup retained at %q; installed files at %q: %w",
					backupDir, extensionDir, err,
				)
			}
			records = maps.Clone(records)
			for id := range records {
				if strings.EqualFold(id, replacementID) {
					delete(records, id)
				}
			}
			records[previous.Id] = previous
			if err := m.userConfig.Set(installedConfigKey, records); err != nil {
				return fmt.Errorf(
					"failed to restore installed extension metadata; backup retained at %q; installed files at %q: %w",
					backupDir, extensionDir, err,
				)
			}
			m.installed = nil
			if err := m.configManager.Save(m.userConfig); err != nil {
				return fmt.Errorf(
					"failed to save restored installed extension metadata; backup retained at %q; installed files at %q: %w",
					backupDir, extensionDir, err,
				)
			}
		}
		if err := osutil.RemoveAll(ctx, backupDir); err != nil {
			return fmt.Errorf("failed to clean up extension backup %q: %w", backupDir, err)
		}
		return nil
	}, nil
}

// evaluateDependencyChanges reconciles installed dependencies against their declared constraints.
// It selects the highest published version that satisfies every constraint known to the operation:
// the parent's, those of other installed extensions, and those of extensions pending in walk. It
// never downgrades a dependency.
func (m *Manager) evaluateDependencyChanges(
	ctx context.Context,
	parentExtension *ExtensionMetadata,
	dependencies []ExtensionDependency,
	opts UpgradeOptions,
	walk *dependencyWalk,
) []UpgradeResult {
	var results []UpgradeResult

	for _, dep := range dependencies {
		installed, err := m.GetInstalled(FilterOptions{Id: dep.Id})
		if err != nil || installed == nil {
			// Not installed — handled by the parent's Install dependency loop.
			continue
		}

		// These failures occur before an actual upgrade starts its own span.
		recordFailure := func(err error) {
			result := dependencyFailure(installed, err)
			TraceDependencyFailure(ctx, parentExtension.Id, result)
			results = append(results, result)
		}

		// Dependency upgrades use the same parent-source then main-registry
		// resolution policy as fresh dependency installs.
		childMetadata, findErr := m.resolveDependency(
			ctx,
			parentExtension,
			dep,
			!opts.SkipMainRegistryDependencyFallback,
			m.azdVersion,
		)

		// Direct children that predate dependency tracking learn their snapshot here, before
		// any version logic. Deeper legacy records are backfilled when they are directly
		// reconciled, updated, or reinstalled.
		if findErr == nil {
			installedRelease := FindVersion(childMetadata.Versions, installed.Version)
			if err := m.BackfillDependencies(dep.Id, childMetadata.Source, installedRelease); err != nil {
				recordFailure(err)
				continue
			}
		}

		// An unconstrained dependency has nothing to reconcile against.
		if dep.Version == "" {
			continue
		}

		// Respect the version this operation already chose; otherwise surface a conflict.
		if walk.isSettled(dep.Id) {
			if matchesVersionConstraint(dep.Version, installed.Version) {
				continue
			}
			recordFailure(m.dependencyConflictError(parentExtension.Id, dep, installed, childMetadata, walk))
			continue
		}

		// Settle up front so that any extension processed later in this operation validates
		// against the resulting version, whether this iteration upgrades, skips, or fails.
		walk.settle(dep.Id)

		if findErr != nil {
			// Without registry data, only fail if the installed version violates the constraint.
			if matchesVersionConstraint(dep.Version, installed.Version) {
				continue
			}
			recordFailure(findErr)
			continue
		}

		constraints, err := m.dependencyConstraints(dep.Id, walk)
		if err != nil {
			recordFailure(err)
			continue
		}
		parentConstraint := DependencyConstraint{ParentId: parentExtension.Id, Constraint: dep.Version}
		constraints = withConstraint(constraints, parentConstraint)

		bestVersion := bestVersionForConstraints(constraints, childMetadata.Versions, m.azdVersion)
		if bestVersion == nil {
			// No release satisfies every constraint. Keep an installed version that satisfies this
			// parent; conflicts with other constraints are reported when the extensions declaring
			// them are reconciled.
			if matchesVersionConstraint(dep.Version, installed.Version) {
				continue
			}
			// Otherwise satisfy this parent if that breaks no constraint the installed version
			// satisfies.
			bestVersion = bestVersionForConstraints(
				withConstraint(satisfiedConstraints(constraints, installed.Version), parentConstraint),
				childMetadata.Versions,
				m.azdVersion,
			)
			// resolveDependency found a compatible release for this parent's constraint alone, so
			// no match means the constraints conflict.
			if bestVersion == nil {
				recordFailure(&DependencyConstraintsConflictError{DependencyId: dep.Id, Constraints: constraints})
				continue
			}
		}

		// Already at the highest matching version — nothing to do.
		if bestVersion.Version == installed.Version {
			continue
		}

		// Refuse to silently downgrade: a user (or sibling pack) may have moved the dependency
		// past this pack's declared range deliberately.
		if isDowngrade(installed.Version, bestVersion.Version) {
			if matchesVersionConstraint(dep.Version, installed.Version) {
				// Installed is newer but still satisfies the constraint — keep it, no-op.
				continue
			}
			recordFailure(&DependencyVersionConflictError{
				DependencyId:      dep.Id,
				ParentId:          parentExtension.Id,
				Constraint:        dep.Version,
				InstalledVersion:  installed.Version,
				Source:            childMetadata.Source,
				CompatibleVersion: bestVersion.Version,
			})
			continue
		}

		// Surface disabled dependency updates as Skipped entries.
		if !opts.UpgradeDependencies {
			results = append(results, UpgradeResult{
				ExtensionId:        dep.Id,
				Status:             UpgradeStatusSkipped,
				FromVersion:        installed.Version,
				FromSource:         installed.Source,
				FromSourceCategory: installed.SourceCategoryOrUnknown(),
				SkipReason: fmt.Sprintf(
					"dependency updates disabled; %s available",
					bestVersion.Version,
				),
			})
			continue
		}

		childResult := UpgradeResult{
			ExtensionId:        dep.Id,
			FromVersion:        installed.Version,
			FromSource:         installed.Source,
			FromSourceCategory: installed.SourceCategoryOrUnknown(),
			ToSource:           childMetadata.Source,
			ToSourceCategory:   childMetadata.SourceCategoryOrUnknown(),
		}

		// Correlate the child update with its triggering parent.
		childCtx, span := tracing.Start(ctx, events.ExtensionUpdateEvent)
		span.SetAttributes(
			fields.ExtensionId.String(dep.Id),
			fields.ExtensionDependencyOf.String(parentExtension.Id),
			fields.ExtensionSourceCategory.String(string(childMetadata.SourceCategoryOrUnknown())),
		)

		childOpts := UpgradeOptions{
			VersionPreference:                  bestVersion.Version,
			UpgradeDependencies:                opts.UpgradeDependencies,
			SkipMainRegistryDependencyFallback: opts.SkipMainRegistryDependencyFallback,
		}

		childVersion, nested, upErr := m.upgradeInternal(childCtx, childMetadata, childOpts, walk)
		childResult.DependencyUpgrades = nested
		if upErr != nil {
			childResult.Status = UpgradeStatusFailed
			childResult.Error = upErr
			span.EndWithStatus(upErr)
			results = append(results, childResult)
			continue
		}

		childResult.Status = UpgradeStatusUpgraded
		childResult.ToVersion = childVersion.Version
		span.SetAttributes(
			fields.ExtensionVersionFrom.String(installed.Version),
			fields.ExtensionVersionTo.String(childVersion.Version),
		)
		span.EndWithStatus(nil)
		results = append(results, childResult)
	}

	return results
}

// FindVersion returns the release with the given version tag, or nil.
func FindVersion(versions []ExtensionVersion, version string) *ExtensionVersion {
	for i := range versions {
		if versions[i].Version == version {
			return &versions[i]
		}
	}
	return nil
}

// Helper function to find the artifact for the current OS
func findArtifactForCurrentOS(version *ExtensionVersion) (*ExtensionArtifact, error) {
	if version.Artifacts == nil {
		return nil, fmt.Errorf("no binaries available for this version")
	}

	artifactVersions := []string{
		fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		runtime.GOOS,
	}

	for _, artifactVersion := range artifactVersions {
		artifact, exists := version.Artifacts[artifactVersion]
		if exists {
			if artifact.URL == "" {
				return nil, fmt.Errorf("artifact URL is missing for platform: %s", artifactVersion)
			}

			return &artifact, nil
		}
	}

	return nil, fmt.Errorf("no artifact available for platform: %s", strings.Join(artifactVersions, ", "))
}

// downloadFile downloads a file from the given URL and saves it to a temporary directory using the filename from the URL.
func (m *Manager) downloadArtifact(ctx context.Context, artifactUrl string) (string, error) {
	if strings.HasPrefix(artifactUrl, "http://") || strings.HasPrefix(artifactUrl, "https://") {
		return m.downloadFromRemote(ctx, artifactUrl)
	}
	return m.copyFromLocalPath(artifactUrl)
}

// Handles downloading artifacts from HTTP/HTTPS URLs
func (m *Manager) downloadFromRemote(ctx context.Context, artifactUrl string) (string, error) {
	req, err := azruntime.NewRequest(ctx, http.MethodGet, artifactUrl)
	if err != nil {
		return "", err
	}

	resp, err := m.pipeline.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download file, status code: %d", resp.StatusCode)
	}

	filename := filepath.Base(artifactUrl)
	tempFilePath := filepath.Join(os.TempDir(), filename)

	tempFile, err := os.Create(tempFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer tempFile.Close()

	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to write to temporary file: %w", err)
	}

	return tempFilePath, nil
}

// Handles copying artifacts from local or network file paths
func (m *Manager) copyFromLocalPath(artifactPath string) (string, error) {
	// If the path is relative, resolve it against the userConfigDir
	if !filepath.IsAbs(artifactPath) {
		userConfigDir, err := config.GetUserConfigDir()
		if err != nil {
			return "", fmt.Errorf("failed to get user config directory: %w", err)
		}

		artifactPath = filepath.Join(userConfigDir, artifactPath)

		// Prevent path traversal attacks by ensuring artifact path stays within the user config directory.
		// This validation only applies to relative paths resolved against userConfigDir.
		// Absolute paths are trusted since they are explicitly configured by the user/admin.
		if !osutil.IsPathContained(userConfigDir, artifactPath) {
			return "", fmt.Errorf(
				"invalid artifact path: path %q resolves outside user config directory %q. "+
					"Use an absolute path or a path relative to %q", artifactPath, userConfigDir, userConfigDir)
		}
	}

	if _, err := os.Stat(artifactPath); os.IsNotExist(err) {
		return "", fmt.Errorf("file does not exist at path: %s", artifactPath)
	}

	filename := filepath.Base(artifactPath)
	tempFilePath := filepath.Join(os.TempDir(), filename)

	if err := copyFile(artifactPath, tempFilePath); err != nil {
		return "", fmt.Errorf("failed to copy file to temporary location: %w", err)
	}

	return tempFilePath, nil
}

// InvalidateSourceCache clears the in-memory extension source cache so that
// subsequent source lookups re-read the configured sources. This is required
// when a source is added within the same process (e.g. registering a bundle
// source during install) after the source list may already have been cached.
func (tm *Manager) InvalidateSourceCache() {
	tm.sources = nil
}

// ReloadUserConfig re-reads the user configuration from disk into the manager's
// cached copy. This is required when the configuration is mutated out-of-band
// within the same process (e.g. registering a bundle source during install);
// without it, a subsequent install save would persist the manager's stale
// snapshot and clobber the externally added changes.
func (tm *Manager) ReloadUserConfig() error {
	userConfig, err := tm.configManager.Load()
	if err != nil {
		return fmt.Errorf("reloading user configuration: %w", err)
	}

	tm.userConfig = userConfig
	tm.installed = nil
	return nil
}

func (tm *Manager) getSources(ctx context.Context, filter sourceFilterPredicate) ([]Source, error) {
	if tm.sources != nil {
		if filter == nil {
			return tm.sources, nil
		}
		return slices.Collect(func(yield func(Source) bool) {
			for _, source := range tm.sources {
				if filter(&SourceConfig{Name: source.Name()}) && !yield(source) {
					return
				}
			}
		}), nil
	}
	configs, err := tm.sourceManager.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed parsing extension sources: %w", err)
	}

	sources, err := tm.createSourcesFromConfig(ctx, configs, filter)
	if err != nil {
		return nil, fmt.Errorf("failed initializing extension sources: %w", err)
	}

	if filter != nil {
		return sources, nil
	}
	tm.sources = sources

	return tm.sources, nil
}

func (tm *Manager) createSourcesFromConfig(
	ctx context.Context,
	configs []*SourceConfig,
	filter sourceFilterPredicate,
) ([]Source, error) {
	sources := []Source{}
	var schemaErrors []*ErrUnsupportedRegistrySchema

	for _, config := range configs {
		if filter != nil && !filter(config) {
			continue
		}

		source, err := tm.sourceManager.CreateSource(ctx, config)
		if err != nil {
			if schemaErr, ok := errors.AsType[*ErrUnsupportedRegistrySchema](err); ok {
				log.Printf(
					"WARNING: source '%s' has incompatible "+
						"schema version %s, skipping",
					config.Name, schemaErr.SchemaVersion,
				)
				schemaErrors = append(schemaErrors, schemaErr)
				continue
			}
			log.Printf("failed to create source: %v", err)
			continue
		}

		sources = append(sources, source)
	}

	// Only hard-fail when every source had an incompatible schema and
	// no usable sources remain.
	if len(sources) == 0 && len(schemaErrors) > 0 {
		return nil, NewUnsupportedRegistrySchemaError(schemaErrors[0])
	}

	return sources, nil
}

// validateChecksum validates the file at the given path against the expected checksum using the specified algorithm.
func validateChecksum(filePath string, checksum ExtensionChecksum) error {
	// Check if checksum or required fields are nil
	if checksum.Algorithm == "" && checksum.Value == "" {
		log.Println("Checksum algorithm and value is missing, skipping checksum validation")
		return nil
	}

	var hashAlgo hash.Hash

	// Select the hashing algorithm based on the input
	switch checksum.Algorithm {
	case "sha256":
		hashAlgo = sha256.New()
	case "sha512":
		hashAlgo = sha512.New()
	default:
		return fmt.Errorf("unsupported checksum algorithm: %s", checksum.Algorithm)
	}

	// Open the file for reading
	//nolint:gosec // G703: filePath from extension install
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file for checksum validation: %w", err)
	}
	defer file.Close()

	// Compute the checksum
	if _, err := io.Copy(hashAlgo, file); err != nil {
		return fmt.Errorf("failed to compute checksum: %w", err)
	}

	// Convert the computed checksum to a hexadecimal string
	computedChecksum := hex.EncodeToString(hashAlgo.Sum(nil))

	// Compare the computed checksum with the expected checksum
	if computedChecksum != checksum.Value {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", checksum.Value, computedChecksum)
	}

	return nil
}

// Helper function to copy a file to the target directory
func copyFile(src, dst string) error {
	input, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer input.Close()

	output, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer output.Close()

	_, err = io.Copy(output, input)
	if err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	return nil
}

const (
	metadataFileName    = "metadata.json"
	metadataCommandName = "metadata"
	metadataTimeout     = 10 * time.Second
)

// fetchAndCacheMetadata fetches metadata from an extension and caches it to disk.
// Caller must verify that extension has MetadataCapability before calling.
// Returns nil error if metadata was successfully fetched and cached.
func (m *Manager) fetchAndCacheMetadata(
	ctx context.Context,
	extension *Extension,
) error {
	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get user config directory: %w", err)
	}

	extensionDir := filepath.Join(userConfigDir, "extensions", extension.Id)
	metadataPath := filepath.Join(extensionDir, metadataFileName)

	// Check if metadata.json already exists (pre-packaged)
	if _, err := os.Stat(metadataPath); err == nil {
		log.Printf("Extension '%s' has pre-packaged metadata.json, skipping metadata command", extension.Id)
		return nil
	}

	// Execute metadata command with timeout using the runner
	cmdCtx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()

	runner, err := m.lazyRunner.GetValue()
	if err != nil {
		return fmt.Errorf("failed to resolve extension runner: %w", err)
	}

	runResult, err := runner.Invoke(cmdCtx, extension, &InvokeOptions{
		Args: []string{metadataCommandName},
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("metadata command timed out after %v", metadataTimeout)
		}
		return fmt.Errorf("metadata command failed: %w", err)
	}
	if runResult.ExitCode != 0 {
		return fmt.Errorf("metadata command exited with code %d", runResult.ExitCode)
	}

	// Parse metadata JSON from stdout
	var metadata ExtensionCommandMetadata
	if err := json.Unmarshal([]byte(runResult.Stdout), &metadata); err != nil {
		return fmt.Errorf("failed to parse metadata JSON: %w", err)
	}

	// Validate metadata
	if metadata.ID != extension.Id {
		return fmt.Errorf(
			"metadata ID '%s' does not match extension ID '%s'",
			metadata.ID,
			extension.Id,
		)
	}

	// Write metadata to cache
	metadataJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := os.WriteFile(metadataPath, metadataJSON, 0600); err != nil {
		return fmt.Errorf("failed to write metadata file: %w", err)
	}

	log.Printf("Extension '%s' metadata cached successfully", extension.Id)
	return nil
}

// LoadMetadata loads cached metadata for an extension
func (m *Manager) LoadMetadata(extensionId string) (*ExtensionCommandMetadata, error) {
	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user config directory: %w", err)
	}

	extensionDir := filepath.Join(userConfigDir, "extensions", extensionId)
	metadataPath := filepath.Join(extensionDir, metadataFileName)

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("metadata not found for extension '%s'", extensionId)
		}
		return nil, fmt.Errorf("failed to read metadata file: %w", err)
	}

	var metadata ExtensionCommandMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse metadata JSON: %w", err)
	}

	return &metadata, nil
}

// DeleteMetadata removes cached metadata for an extension
func (m *Manager) DeleteMetadata(extensionId string) error {
	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get user config directory: %w", err)
	}

	extensionDir := filepath.Join(userConfigDir, "extensions", extensionId)
	metadataPath := filepath.Join(extensionDir, metadataFileName)

	if err := os.Remove(metadataPath); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove metadata file: %w", err)
		}
		// File doesn't exist, which is fine
	}

	return nil
}

// MetadataExists checks if cached metadata exists for an extension
func (m *Manager) MetadataExists(extensionId string) bool {
	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return false
	}

	extensionDir := filepath.Join(userConfigDir, "extensions", extensionId)
	metadataPath := filepath.Join(extensionDir, metadataFileName)

	_, err = os.Stat(metadataPath)
	return err == nil
}

// HasMetadataCapability checks if the extension with the given ID has the metadata capability.
func (m *Manager) HasMetadataCapability(extensionId string) bool {
	extension, err := m.GetInstalled(FilterOptions{Id: extensionId})
	if err != nil || extension == nil {
		return false
	}
	return extension.HasCapability(MetadataCapability)
}
