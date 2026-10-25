// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"strings"
)

func selectedProgress(plan Plan) map[string]ResourceProgress {
	return map[string]ResourceProgress{
		"storageAccount":    {ResourceID: plan.Storage.ID, Supplied: plan.Storage.Supplied},
		"containerRegistry": {ResourceID: plan.Registry.ID, Supplied: plan.RegistryCustomerSupplied},
		"hostingIdentity":   {ResourceID: plan.Hosting.ID, Supplied: plan.Hosting.Supplied},
		"workloadIdentity":  {ResourceID: plan.Workload.ID, Supplied: plan.Workload.Supplied},
	}
}

func validateProgressSelection(plan Plan, state PreparationState) error {
	if state.CleanupComplete {
		return nil
	}
	selected := selectedProgress(plan)
	for key, previous := range state.ResourceProgress {
		current, known := selected[key]
		if !known || !strings.EqualFold(previous.ResourceID, current.ResourceID) || previous.Supplied != current.Supplied {
			return fmt.Errorf("resourceProgress %s differs from the active resource selection/provenance", key)
		}
	}
	return nil
}

// RecordPreflight stores only current-round evidence. Preview callers must not invoke it.
func RecordPreflight(ctx context.Context, plan Plan, report Report, journal Journal) error {
	lifecycle := &ownedLifecycle{journal: journal}
	state, err := lifecycle.load(ctx, plan)
	if err != nil {
		return err
	}
	if err := validateProgressSelection(plan, state); err != nil {
		return err
	}
	progress := selectedProgress(plan)
	for key, current := range progress {
		if old, exists := state.ResourceProgress[key]; exists && !state.CleanupComplete {
			current.PreparationComplete = old.PreparationComplete
		}
		applicable, passed := false, true
		for _, check := range report {
			if !check.Mandatory || check.Status == NotApplicable {
				continue
			}
			relevant := scopeContains(check.Scope, current.ResourceID)
			if key == "hostingIdentity" && (check.ID == "G01" || check.ID == "G02") ||
				key == "workloadIdentity" && (check.ID == "G02" || check.ID == "G03") ||
				key == "storageAccount" && (strings.HasPrefix(check.ID, "S") || check.ID == "G03") ||
				key == "containerRegistry" && (check.ID == "C01" || check.ID == "P07" || check.ID == "G04") {
				relevant = true
			}
			if relevant {
				applicable = true
				passed = passed && check.Status == Pass
			}
		}
		current.PreflightPassed = applicable && passed
		progress[key] = current
	}
	state.Version, state.Owner = preparationJournalVersion, plan.Owner
	state.SubscriptionID, state.ResourceGroup, state.Location = plan.SubscriptionID, plan.ResourceGroup, plan.Location
	state.ResourceProgress = progress
	return journal.Save(ctx, state)
}
