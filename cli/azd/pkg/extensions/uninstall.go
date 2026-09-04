// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// ErrEmptyExtensionId indicates that an extension id was blank. A blank id would otherwise match
// an arbitrary installed record, because the installed filter treats an empty id as a wildcard.
var ErrEmptyExtensionId = errors.New("extension id cannot be empty")

// ExtensionRequiredError indicates that one or more extensions cannot be uninstalled because
// other installed extensions declare a dependency on them.
type ExtensionRequiredError struct {
	// Blocked maps each requested extension id to the sorted ids of the installed extensions
	// that require it.
	Blocked map[string][]string
}

func (e *ExtensionRequiredError) Error() string {
	ids := slices.Sorted(maps.Keys(e.Blocked))
	if len(ids) == 1 {
		return fmt.Sprintf(
			"extension %s is required by installed extensions: %s",
			ids[0], strings.Join(e.Blocked[ids[0]], ", "),
		)
	}

	var builder strings.Builder
	builder.WriteString("extensions are required by other installed extensions:")
	for _, id := range ids {
		fmt.Fprintf(&builder, "\n  %s: required by %s", id, strings.Join(e.Blocked[id], ", "))
	}
	return builder.String()
}

// Suggestion returns actionable guidance for removing the dependents first.
func (e *ExtensionRequiredError) Suggestion() string {
	return fmt.Sprintf(
		"Run 'azd extension uninstall %s' to remove the dependents first, or pass --force to remove it anyway.",
		strings.Join(e.dependents(), " "),
	)
}

// dependents returns the sorted, de-duplicated ids of every extension that blocks the request.
func (e *ExtensionRequiredError) dependents() []string {
	seen := map[string]struct{}{}
	for _, dependents := range e.Blocked {
		for _, dependent := range dependents {
			seen[dependent] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// UninstallPlanOptions controls how PlanUninstall treats dependencies and dependents.
type UninstallPlanOptions struct {
	// KeepDependencies leaves dependencies that were installed only for the removed
	// extensions in place instead of removing them once nothing requires them.
	KeepDependencies bool
	// IgnoreDependents allows removal even when other installed extensions require a target.
	IgnoreDependents bool
}

// RetainedDependency is a dependency of the removed extensions that stays installed.
type RetainedDependency struct {
	Extension *Extension
	// RequiredBy lists the remaining installed extensions that still require the dependency,
	// sorted by id. It is empty when the dependency stays because its record is not marked as
	// a dependency install (installed by name, or written before dependency tracking existed).
	RequiredBy []string
}

// UninstallPlan describes the effect of uninstalling a set of extensions.
type UninstallPlan struct {
	// Targets are the requested extensions, in request order.
	Targets []*Extension
	// Orphaned lists dependencies that were installed only for the removed extensions and
	// that nothing requires once they are gone, parents before children.
	Orphaned []*Extension
	// Retained lists dependencies of the removed extensions that stay installed, sorted by id.
	Retained []RetainedDependency
	// Blocked maps a target id to the sorted ids of installed extensions outside the removal
	// set that require it. It is only populated when UninstallPlanOptions.IgnoreDependents is
	// set; otherwise a blocked request fails with ExtensionRequiredError instead of a plan.
	Blocked map[string][]string
}

// InstalledDependents returns the installed extensions whose recorded dependencies include
// the given id, sorted by id. Records that predate dependency tracking never appear.
func (m *Manager) InstalledDependents(id string) ([]*Extension, error) {
	installed, err := m.ListInstalled()
	if err != nil {
		return nil, err
	}

	var dependents []*Extension
	for _, extension := range installed {
		if dependsOn(extension, id) {
			dependents = append(dependents, extension)
		}
	}
	slices.SortFunc(dependents, func(a, b *Extension) int {
		return strings.Compare(a.Id, b.Id)
	})
	return dependents, nil
}

// PlanUninstall computes what uninstalling the given extensions would remove and what stops
// it, using only the installed records. It performs no removal and no registry lookups. A
// request that other installed extensions block fails with ExtensionRequiredError unless
// UninstallPlanOptions.IgnoreDependents is set.
func (m *Manager) PlanUninstall(ids []string, opts UninstallPlanOptions) (*UninstallPlan, error) {
	installed, err := m.ListInstalled()
	if err != nil {
		return nil, fmt.Errorf("failed to list installed extensions: %w", err)
	}

	plan := &UninstallPlan{}
	removal := map[string]struct{}{}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return nil, ErrEmptyExtensionId
		}
		extension, err := m.GetInstalled(FilterOptions{Id: id})
		if err != nil {
			return nil, fmt.Errorf("failed to get installed extension: %w", err)
		}
		if _, seen := removal[extension.Id]; seen {
			continue
		}
		removal[extension.Id] = struct{}{}
		plan.Targets = append(plan.Targets, extension)
	}

	// dependentsOutsideRemoval lists the installed extensions that require id and are not
	// themselves being removed. The removal set grows as orphans are found, so it is
	// evaluated lazily.
	dependentsOutsideRemoval := func(id string) []string {
		var dependents []string
		for _, extension := range installed {
			if _, removing := removal[extension.Id]; removing {
				continue
			}
			if dependsOn(extension, id) {
				dependents = append(dependents, extension.Id)
			}
		}
		slices.Sort(dependents)
		return dependents
	}

	// Walk the dependencies of everything being removed. A dependency joins the removal set
	// when it was installed as a dependency and nothing outside the removal set requires it.
	// Removed extensions are appended to the queue so their own dependencies are visited,
	// which also re-examines a dependency that was first kept because of a sibling that is
	// removed later (A -> B, A -> C, C -> B). This runs before the dependents check so that a
	// dependency-installed extension in a cycle with a target (A -> B -> A) leaves with it
	// instead of blocking it.
	considered := map[string]*Extension{}
	if !opts.KeepDependencies {
		queue := slices.Clone(plan.Targets)
		for i := 0; i < len(queue); i++ {
			for _, dependency := range queue[i].Dependencies {
				if strings.TrimSpace(dependency.Id) == "" {
					continue
				}
				dependencyExtension, err := m.GetInstalled(FilterOptions{Id: dependency.Id})
				if err != nil || dependencyExtension == nil {
					continue
				}
				if _, removing := removal[dependencyExtension.Id]; removing {
					continue
				}
				considered[dependencyExtension.Id] = dependencyExtension
				if !dependencyExtension.InstalledAsDependency ||
					len(dependentsOutsideRemoval(dependencyExtension.Id)) > 0 {
					continue
				}

				removal[dependencyExtension.Id] = struct{}{}
				delete(considered, dependencyExtension.Id)
				plan.Orphaned = append(plan.Orphaned, dependencyExtension)
				queue = append(queue, dependencyExtension)
			}
		}
	}

	blocked := map[string][]string{}
	for _, target := range plan.Targets {
		if dependents := dependentsOutsideRemoval(target.Id); len(dependents) > 0 {
			blocked[target.Id] = dependents
		}
	}
	if len(blocked) > 0 {
		if !opts.IgnoreDependents {
			return nil, &ExtensionRequiredError{Blocked: blocked}
		}
		plan.Blocked = blocked
	}

	for _, id := range slices.Sorted(maps.Keys(considered)) {
		plan.Retained = append(plan.Retained, RetainedDependency{
			Extension:  considered[id],
			RequiredBy: dependentsOutsideRemoval(id),
		})
	}

	return plan, nil
}

// dependsOn reports whether the installed extension declares a dependency on id.
func dependsOn(extension *Extension, id string) bool {
	return slices.ContainsFunc(extension.Dependencies, func(dependency ExtensionDependency) bool {
		return strings.EqualFold(dependency.Id, id)
	})
}
