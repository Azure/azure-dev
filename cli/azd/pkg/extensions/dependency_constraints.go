// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// DependencyConstraint is a version constraint that one extension declares on another.
type DependencyConstraint struct {
	// ParentId is the id of the extension that declares the constraint.
	ParentId string
	// Constraint is the declared version constraint.
	Constraint string
}

// describeConstraints renders constraints for user-facing messages.
func describeConstraints(constraints []DependencyConstraint) string {
	parts := make([]string, 0, len(constraints))
	for _, constraint := range constraints {
		parts = append(parts, fmt.Sprintf("%s requires %q", constraint.ParentId, constraint.Constraint))
	}
	return strings.Join(parts, ", ")
}

// unsatisfiedConstraints returns the constraints that version does not satisfy.
func unsatisfiedConstraints(constraints []DependencyConstraint, version string) []DependencyConstraint {
	return slices.DeleteFunc(slices.Clone(constraints), func(constraint DependencyConstraint) bool {
		return matchesVersionConstraint(constraint.Constraint, version)
	})
}

// satisfiedConstraints returns the constraints that version satisfies.
func satisfiedConstraints(constraints []DependencyConstraint, version string) []DependencyConstraint {
	return slices.DeleteFunc(slices.Clone(constraints), func(constraint DependencyConstraint) bool {
		return !matchesVersionConstraint(constraint.Constraint, version)
	})
}

// satisfiesConstraints reports whether version satisfies every constraint. Each constraint is checked
// separately because joining expressions that contain "||" would change their meaning.
func satisfiesConstraints(constraints []DependencyConstraint, version string) bool {
	return len(unsatisfiedConstraints(constraints, version)) == 0
}

// bestVersionForConstraints returns the highest release that supports azdVersion and satisfies every
// constraint, or nil when no release does. A nil azdVersion skips the compatibility check.
func bestVersionForConstraints(
	constraints []DependencyConstraint,
	versions []ExtensionVersion,
	azdVersion *semver.Version,
) *ExtensionVersion {
	candidates := make([]ExtensionVersion, 0, len(versions))
	for i := range versions {
		if azdVersion != nil && !VersionIsCompatible(&versions[i], azdVersion) {
			continue
		}
		if satisfiesConstraints(constraints, versions[i].Version) {
			candidates = append(candidates, versions[i])
		}
	}
	return LatestVersion(candidates)
}

// withConstraint adds constraint unless an equal constraint is already present.
func withConstraint(constraints []DependencyConstraint, constraint DependencyConstraint) []DependencyConstraint {
	if constraint.Constraint == "" || strings.EqualFold(constraint.Constraint, "latest") {
		return constraints
	}
	if slices.ContainsFunc(constraints, func(existing DependencyConstraint) bool {
		return strings.EqualFold(existing.ParentId, constraint.ParentId) && existing.Constraint == constraint.Constraint
	}) {
		return constraints
	}
	// Sorting keeps messages and results deterministic.
	constraints = append(slices.Clone(constraints), constraint)
	slices.SortFunc(constraints, func(a, b DependencyConstraint) int {
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.ParentId), strings.ToLower(b.ParentId)),
			cmp.Compare(a.Constraint, b.Constraint),
		)
	})
	return constraints
}

// DependencyVersionConflictError indicates that an installed dependency violates its parent's constraint.
type DependencyVersionConflictError struct {
	// DependencyId is the id of the installed dependency.
	DependencyId string
	// ParentId is the id of the extension that declares the constraint.
	ParentId string
	// Constraint is the constraint that the installed version does not satisfy.
	Constraint string
	// InstalledVersion is the installed version of the dependency.
	InstalledVersion string
	// Source is the source of CompatibleVersion. It is empty when CompatibleVersion is empty.
	Source string
	// CompatibleVersion is the highest release that satisfies every constraint on the dependency,
	// when one is known.
	CompatibleVersion string
}

func (e *DependencyVersionConflictError) Error() string {
	return fmt.Sprintf(
		"%s %s is installed, but %s requires %q",
		e.DependencyId, e.InstalledVersion, e.ParentId, e.Constraint,
	)
}

// Suggestion returns guidance without authorizing an automatic downgrade.
func (e *DependencyVersionConflictError) Suggestion() string {
	if e.CompatibleVersion == "" {
		return fmt.Sprintf(
			"Install a version of %s that satisfies %q, or update %s to a compatible release.",
			e.DependencyId, e.Constraint, e.ParentId,
		)
	}
	command := fmt.Sprintf("azd extension install %s --version %s --force", e.DependencyId, e.CompatibleVersion)
	if e.Source != "" {
		command = fmt.Sprintf(
			"azd extension install %s --source %s --version %s --force",
			e.DependencyId, e.Source, e.CompatibleVersion,
		)
	}
	return fmt.Sprintf(
		"Run %s to install a version that %s accepts, or update %s to a compatible release.",
		output.WithHighLightFormat("%s", command), e.ParentId, e.ParentId,
	)
}

// DependencyConstraintsConflictError indicates that no published release of a dependency satisfies
// every constraint that extensions declare on it.
type DependencyConstraintsConflictError struct {
	// DependencyId is the id of the dependency.
	DependencyId string
	// Constraints are the constraints declared on the dependency.
	Constraints []DependencyConstraint
}

func (e *DependencyConstraintsConflictError) Error() string {
	return fmt.Sprintf(
		"no compatible version of %s satisfies all of its constraints: %s",
		e.DependencyId, describeConstraints(e.Constraints),
	)
}

// Suggestion returns guidance for resolving incompatible constraints.
func (e *DependencyConstraintsConflictError) Suggestion() string {
	parents := make([]string, 0, len(e.Constraints))
	for _, constraint := range e.Constraints {
		parents = append(parents, constraint.ParentId)
	}
	return fmt.Sprintf(
		"Update %s to releases that accept a common version of %s.",
		strings.Join(parents, " or "), e.DependencyId,
	)
}

// IsDependencyConflict reports whether every cause of err is an incompatible dependency constraint.
// Retrying the same command does not resolve these errors. An error that joins a conflict with
// another failure is not a conflict, because retrying might resolve the other failure.
func IsDependencyConflict(err error) bool {
	//nolint:errorlint // Inspect every cause before treating a joined failure as a conflict.
	switch err := err.(type) {
	case *DependencyVersionConflictError, *DependencyConstraintsConflictError:
		return true
	case interface{ Unwrap() []error }:
		causes := err.Unwrap()
		return len(causes) > 0 && !slices.ContainsFunc(causes, func(cause error) bool {
			return !IsDependencyConflict(cause)
		})
	case interface{ Unwrap() error }:
		return IsDependencyConflict(err.Unwrap())
	default:
		return false
	}
}

// dependencyFailure returns a failed result for an installed dependency, carrying the suggestion
// from err.
func dependencyFailure(installed *Extension, err error) UpgradeResult {
	result := UpgradeResult{
		ExtensionId:        installed.Id,
		Status:             UpgradeStatusFailed,
		FromVersion:        installed.Version,
		FromSource:         installed.Source,
		FromSourceCategory: installed.SourceCategoryOrUnknown(),
		Error:              err,
	}
	if suggestionErr, ok := errors.AsType[*internal.ErrorWithSuggestion](internal.WrapErrorWithSuggestion(err)); ok {
		result.Suggestion = suggestionErr.Suggestion
	}
	return result
}

// TraceDependencyFailure records a dependency failure that occurred before an update of the
// dependency started.
func TraceDependencyFailure(ctx context.Context, parentId string, result UpgradeResult) {
	_, span := tracing.Start(ctx, events.ExtensionUpdateEvent)
	span.SetAttributes(
		fields.ExtensionId.String(result.ExtensionId),
		fields.ExtensionDependencyOf.String(parentId),
		fields.ExtensionSourceCategory.String(string(result.FromSourceCategory)),
	)
	span.EndWithStatus(result.Error)
}

// pendingDependencies holds the dependencies declared by an extension that is being installed.
type pendingDependencies struct {
	id           string
	dependencies []ExtensionDependency
}

// dependencyWalk holds the state shared by the nested installs and updates of one operation.
type dependencyWalk struct {
	// inFlight holds the extensions being installed, to detect dependency cycles.
	inFlight map[string]struct{}
	// settled holds dependencies whose versions the operation already chose. Later constraints are
	// validated against these versions instead of changing them again.
	settled map[string]struct{}
	// pending holds the dependencies declared by extensions that the operation is installing. They
	// replace the installed records of those extensions until the installs complete.
	pending map[string]pendingDependencies
}

func newDependencyWalk(settled ...string) *dependencyWalk {
	walk := &dependencyWalk{
		inFlight: map[string]struct{}{},
		settled:  map[string]struct{}{},
		pending:  map[string]pendingDependencies{},
	}
	for _, id := range settled {
		walk.settle(id)
	}
	return walk
}

func (w *dependencyWalk) settle(id string) {
	w.settled[strings.ToLower(id)] = struct{}{}
}

func (w *dependencyWalk) isSettled(id string) bool {
	_, ok := w.settled[strings.ToLower(id)]
	return ok
}

// startInstall marks id as in flight and returns false when it already is.
func (w *dependencyWalk) startInstall(id string) bool {
	key := strings.ToLower(id)
	if _, ok := w.inFlight[key]; ok {
		return false
	}
	w.inFlight[key] = struct{}{}
	return true
}

func (w *dependencyWalk) finishInstall(id string) {
	key := strings.ToLower(id)
	delete(w.inFlight, key)
	delete(w.pending, key)
}

func (w *dependencyWalk) setPending(id string, dependencies []ExtensionDependency) {
	w.pending[strings.ToLower(id)] = pendingDependencies{id: id, dependencies: dependencies}
}

// dependencyConstraints returns the version constraints that installed extensions and the extensions
// pending in walk declare on id.
func (m *Manager) dependencyConstraints(id string, walk *dependencyWalk) ([]DependencyConstraint, error) {
	installed, err := m.ListInstalled()
	if err != nil {
		return nil, fmt.Errorf("failed to list installed extensions: %w", err)
	}

	var constraints []DependencyConstraint
	add := func(parentId string, dependencies []ExtensionDependency) {
		for _, dependency := range dependencies {
			if strings.EqualFold(dependency.Id, id) {
				constraints = withConstraint(constraints, DependencyConstraint{
					ParentId:   parentId,
					Constraint: dependency.Version,
				})
			}
		}
	}
	for _, record := range installed {
		if _, pending := walk.pending[strings.ToLower(record.Id)]; !pending && !strings.EqualFold(record.Id, id) {
			add(record.Id, record.Dependencies)
		}
	}
	for _, parent := range walk.pending {
		if !strings.EqualFold(parent.id, id) {
			add(parent.id, parent.dependencies)
		}
	}
	return constraints, nil
}

// VersionHold describes installed extensions whose constraints keep an extension below the release
// that would otherwise be selected.
type VersionHold struct {
	// Latest is the release that would otherwise be selected.
	Latest string
	// Version is the version to select instead: the highest release that satisfies every
	// constraint, or the installed version when no such release is newer. It is empty for an
	// extension without an installed version when no release satisfies the constraints.
	Version string
	// Constraints are the constraints that Latest does not satisfy.
	Constraints []DependencyConstraint
}

// Reason explains the hold for skip reasons and notes.
func (h *VersionHold) Reason() string {
	return fmt.Sprintf("%s is available, but %s", h.Latest, describeConstraints(h.Constraints))
}

// HoldForDependents returns a hold when constraints that installed extensions declare on extension
// exclude latest, a release newer than installedVersion. A hold never selects a version older than
// installedVersion; pass an empty installedVersion when the installed version must not limit the
// selection. It returns nil when no hold applies.
func (m *Manager) HoldForDependents(
	extension *ExtensionMetadata,
	latest string,
	installedVersion string,
) (*VersionHold, error) {
	if installedVersion != "" && !isNewerVersion(latest, installedVersion) {
		return nil, nil
	}
	constraints, err := m.dependencyConstraints(extension.Id, newDependencyWalk())
	if err != nil {
		return nil, err
	}
	unsatisfied := unsatisfiedConstraints(constraints, latest)
	if len(unsatisfied) == 0 {
		return nil, nil
	}
	hold := &VersionHold{Latest: latest, Version: installedVersion, Constraints: unsatisfied}
	best := bestVersionForConstraints(constraints, extension.Versions, m.azdVersion)
	if best != nil && (installedVersion == "" || isNewerVersion(best.Version, installedVersion)) {
		hold.Version = best.Version
	}
	return hold, nil
}

// DependencyConflict is an unsatisfied constraint between installed extensions.
type DependencyConflict struct {
	// ParentId is the id of the extension that declares the constraint.
	ParentId string
	// Result is the failed result for the dependency, with a *DependencyVersionConflictError.
	Result UpgradeResult
}

// DependencyConflicts returns the unsatisfied constraints between installed extensions where the
// declaring extension or the dependency is one of ids. Ids are compared case-insensitively.
func (m *Manager) DependencyConflicts(ids []string) ([]DependencyConflict, error) {
	installed, err := m.ListInstalled()
	if err != nil {
		return nil, fmt.Errorf("failed to list installed extensions: %w", err)
	}

	involved := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		involved[strings.ToLower(id)] = struct{}{}
	}
	isInvolved := func(id string) bool {
		_, ok := involved[strings.ToLower(id)]
		return ok
	}
	byId := make(map[string]*Extension, len(installed))
	for id, record := range installed {
		byId[strings.ToLower(id)] = record
	}

	var conflicts []DependencyConflict
	for _, parentId := range slices.Sorted(maps.Keys(installed)) {
		parent := installed[parentId]
		for _, dependency := range parent.Dependencies {
			child := byId[strings.ToLower(dependency.Id)]
			if child == nil || (!isInvolved(parent.Id) && !isInvolved(child.Id)) ||
				matchesVersionConstraint(dependency.Version, child.Version) {
				continue
			}
			conflicts = append(conflicts, DependencyConflict{
				ParentId: parent.Id,
				Result: dependencyFailure(child, &DependencyVersionConflictError{
					DependencyId:     child.Id,
					ParentId:         parent.Id,
					Constraint:       dependency.Version,
					InstalledVersion: child.Version,
				}),
			})
		}
	}
	return conflicts, nil
}

// dependencyConflictError explains why the installed version of dependency cannot satisfy parent's
// constraint. When metadata is available, it names a release that satisfies every known constraint,
// or reports that no release does.
func (m *Manager) dependencyConflictError(
	parentId string,
	dependency ExtensionDependency,
	installed *Extension,
	metadata *ExtensionMetadata,
	walk *dependencyWalk,
) error {
	conflict := &DependencyVersionConflictError{
		DependencyId:     installed.Id,
		ParentId:         parentId,
		Constraint:       dependency.Version,
		InstalledVersion: installed.Version,
	}
	if metadata == nil {
		return conflict
	}
	constraints, err := m.dependencyConstraints(dependency.Id, walk)
	if err != nil {
		return conflict
	}
	constraints = withConstraint(constraints, DependencyConstraint{ParentId: parentId, Constraint: dependency.Version})
	best := bestVersionForConstraints(constraints, metadata.Versions, m.azdVersion)
	if best == nil {
		return &DependencyConstraintsConflictError{DependencyId: installed.Id, Constraints: constraints}
	}
	if best.Version != installed.Version {
		conflict.Source = metadata.Source
		conflict.CompatibleVersion = best.Version
	}
	return conflict
}
