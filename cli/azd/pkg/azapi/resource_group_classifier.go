// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// ClassifyResult holds the outcome of resource group classification.
type ClassifyResult struct {
	Owned   []string         // RGs classified as created by azd — safe to delete
	Skipped []ClassifiedSkip // RGs classified as external/unknown/vetoed — not deleted
}

// ClassifiedSkip represents a resource group that will NOT be deleted, with the reason.
type ClassifiedSkip struct {
	Name   string
	Reason string // Human-readable, e.g. "external (snapshot: not in predictedResources)"
}

// ResourceWithTags is a resource with its ARM tags, used for extra-resource checks.
type ResourceWithTags struct {
	Name string
	Type string             // ARM resource type, e.g. "Microsoft.Compute/virtualMachines"
	Tags map[string]*string // ARM tags on the resource; nil if none are set
}

// ManagementLock represents an ARM management lock on a resource.
type ManagementLock struct {
	Name     string
	LockType string // "CanNotDelete" or "ReadOnly"
}

// ClassifyOptions configures the classification pipeline.
type ClassifyOptions struct {
	// SnapshotPredictedRGs is the set of resource group names (lowercased) that the
	// Bicep template declares as created resources (not 'existing' references).
	// Populated from `bicep snapshot` → predictedResources filtered by RG type.
	//
	// When non-nil, snapshot-based classification is used:
	//   - RG in set → owned (template creates it)
	//   - RG not in set → external (template references it as existing)
	//   - Tier 4 still runs on all owned candidates (defense-in-depth)
	//
	// When nil, a simplified guard applies:
	//   - ForceMode: all RGs treated as owned (backward compat, zero API calls)
	//   - Interactive + Prompter: user prompted for each RG
	//   - Otherwise: all RGs skipped (cannot classify without snapshot)
	SnapshotPredictedRGs map[string]bool

	// ForceMode skips interactive prompts and API-calling safety checks.
	//
	// With snapshot available: snapshot classifies RGs (deterministic, offline),
	// Tier 4 vetoes are skipped (zero API calls, consistent with --force contract).
	//
	// Without snapshot: all RGs are treated as owned (backward compat, zero API
	// calls). This is the only path where an external RG could be deleted — it
	// requires both snapshot failure AND explicit --force.
	ForceMode bool
	// Interactive enables per-RG prompts for unknown and foreign-resource RGs.
	// When false, unknown/unverified RGs are always skipped without deletion.
	Interactive bool
	EnvName     string // Current azd environment name for tag matching

	// ListResourceGroupResources returns all resources in a resource group.
	ListResourceGroupResources func(ctx context.Context, rgName string) ([]*ResourceWithTags, error)
	// ListResourceGroupLocks returns management locks on a resource group.
	ListResourceGroupLocks func(ctx context.Context, rgName string) ([]*ManagementLock, error)
	// Prompter asks the user whether to delete an unknown RG. Returns true to delete.
	Prompter func(rgName, reason string) (bool, error)
}

const (
	cAzdEnvNameTag       = "azd-env-name"
	cAzdProvisionHashTag = "azd-provision-param-hash"
	cLockCanNotDelete    = "CanNotDelete"
	cLockReadOnly        = "ReadOnly"
	cTier4Parallelism    = 5
)

// TagKeyProvisionParamHash is the exported constant for the provision parameter hash tag key.
// Used by callers (e.g. bicep_destroy.go) to extract the expected hash from deployment tags.
const TagKeyProvisionParamHash = cAzdProvisionHashTag

// LockLevelCanNotDelete and LockLevelReadOnly are the ARM lock levels that block deletion.
const (
	LockLevelCanNotDelete = cLockCanNotDelete
	LockLevelReadOnly     = cLockReadOnly
)

// ClassifyResourceGroups determines which resource groups from a deployment are
// safe to delete (owned by azd) vs which should be skipped (external/unknown/vetoed).
//
// When SnapshotPredictedRGs is set, snapshot-based classification is used as the
// primary signal, with Tier 4 (locks + foreign resources) as defense-in-depth.
//
// When SnapshotPredictedRGs is nil (snapshot unavailable):
//   - ForceMode: all RGs returned as owned (backward compat, zero API calls)
//   - Interactive + Prompter: user prompted for each RG
//   - Otherwise: all RGs skipped with reason "snapshot unavailable"
func ClassifyResourceGroups(
	ctx context.Context,
	rgNames []string,
	opts ClassifyOptions,
) (*ClassifyResult, error) {
	if len(rgNames) == 0 {
		return &ClassifyResult{}, nil
	}

	result := &ClassifyResult{}

	// --- Snapshot path: deterministic classification from bicep snapshot ---
	if opts.SnapshotPredictedRGs != nil {
		return classifyFromSnapshot(ctx, rgNames, opts, result)
	}

	// --- Snapshot unavailable: simplified guard ---

	// ForceMode without snapshot: return all RGs as owned (backward compat).
	if opts.ForceMode {
		result.Owned = slices.Clone(rgNames)
		return result, nil
	}

	// Interactive without snapshot: prompt user for each RG.
	if opts.Interactive && opts.Prompter != nil {
		var owned []string
		for _, rg := range rgNames {
			accept, err := opts.Prompter(
				rg,
				"snapshot unavailable — cannot verify ownership",
			)
			if err != nil {
				return nil, fmt.Errorf(
					"classify rg=%s prompt: %w", rg, err)
			}
			if accept {
				owned = append(owned, rg)
			} else {
				result.Skipped = append(result.Skipped,
					ClassifiedSkip{
						Name: rg,
						Reason: "skipped (snapshot unavailable" +
							" — user declined)",
					})
			}
		}
		return runTier4Vetoes(ctx, owned, opts, result)
	}

	// Non-interactive without snapshot: skip all RGs.
	for _, rg := range rgNames {
		result.Skipped = append(result.Skipped, ClassifiedSkip{
			Name: rg,
			Reason: "skipped (snapshot unavailable" +
				" — cannot classify without snapshot)",
		})
	}
	return result, nil
}

// classifyFromSnapshot uses the Bicep snapshot predictedResources to classify RGs.
// RGs whose names appear in the predicted set are owned (the template creates them).
// RGs not in the predicted set are external (referenced via the `existing` keyword).
//
// Tier 4 (lock + foreign-resource veto) still runs on owned candidates unless ForceMode
// is active, providing defense-in-depth even when snapshot says "owned."
func classifyFromSnapshot(
	ctx context.Context,
	rgNames []string,
	opts ClassifyOptions,
	result *ClassifyResult,
) (*ClassifyResult, error) {
	var owned []string
	for _, rg := range rgNames {
		if opts.SnapshotPredictedRGs[strings.ToLower(rg)] {
			owned = append(owned, rg)
		} else {
			result.Skipped = append(result.Skipped, ClassifiedSkip{
				Name:   rg,
				Reason: "external (snapshot: not in predictedResources)",
			})
		}
	}

	// ForceMode + snapshot: deterministic classification, zero API calls, no Tier 4.
	if opts.ForceMode {
		result.Owned = owned
		return result, nil
	}

	// --- Tier 4: veto checks on all snapshot-owned candidates (defense-in-depth) ---
	// Even if the snapshot says "owned," a management lock or foreign resources
	// should still prevent deletion.
	return runTier4Vetoes(ctx, owned, opts, result)
}

// tier4Veto represents a resource group vetoed by a Tier 4 safety check.
type tier4Veto struct {
	rg     string
	reason string
}

// tier4PendingPrompt represents a Tier 4 foreign-resource finding that needs
// interactive confirmation (or becomes a hard veto in non-interactive mode).
type tier4PendingPrompt struct {
	rg     string
	reason string
}

// runTier4Vetoes runs lock + foreign-resource veto checks on all owned candidates
// in parallel (capped by cTier4Parallelism). Foreign-resource prompts are collected
// and executed sequentially on the caller's goroutine to avoid concurrent terminal
// output. Returns the final ClassifyResult with vetoed RGs moved to Skipped.
func runTier4Vetoes(
	ctx context.Context,
	owned []string,
	opts ClassifyOptions,
	result *ClassifyResult,
) (*ClassifyResult, error) {
	// Goroutine invariant: every RG either (a) enters wg.Go — which sends at
	// most once to vetoCh or promptCh (clean RGs send to neither) — or (b) sends
	// to vetoCh directly (cancelled context). Both channels are buffered to
	// len(owned) so sends never block and goroutines never leak.
	vetoCh := make(chan tier4Veto, len(owned))
	promptCh := make(chan tier4PendingPrompt, len(owned))
	sem := make(chan struct{}, cTier4Parallelism)
	var wg sync.WaitGroup
	for _, rg := range owned {
		// Context-aware semaphore: bail out if context is cancelled while waiting.
		select {
		case sem <- struct{}{}:
			// Re-check cancellation after acquiring the semaphore.
			// Go's select is non-deterministic when both cases are ready,
			// so ctx.Done may have fired but the semaphore case was chosen.
			if ctx.Err() != nil {
				<-sem
				vetoCh <- tier4Veto{
					rg:     rg,
					reason: "error during safety check: " + ctx.Err().Error(),
				}
				continue
			}
		case <-ctx.Done():
			vetoCh <- tier4Veto{
				rg:     rg,
				reason: "error during safety check: " + ctx.Err().Error(),
			}
			continue
		}
		wg.Go(func() {
			defer func() { <-sem }()
			reason, vetoed, needsPrompt, err := classifyTier4(ctx, rg, opts)
			if err != nil {
				// Fail safe: treat errors as vetoes to avoid accidental deletion.
				log.Printf(
					"ERROR: classify rg=%s tier=4: safety check failed: %v "+
						"(treating as veto)", rg, err,
				)
				vetoCh <- tier4Veto{
					rg: rg,
					reason: fmt.Sprintf(
						"error during safety check: %s", err.Error()),
				}
				return
			}
			if needsPrompt {
				promptCh <- tier4PendingPrompt{rg: rg, reason: reason}
				return
			}
			if vetoed {
				vetoCh <- tier4Veto{rg: rg, reason: reason}
			}
		})
	}
	wg.Wait()
	close(vetoCh)
	close(promptCh)

	vetoedSet := make(map[string]string, len(owned))
	for v := range vetoCh {
		vetoedSet[v.rg] = v.reason
	}

	// Process foreign-resource prompts sequentially on the main goroutine
	// to avoid concurrent terminal output.
	for p := range promptCh {
		if opts.Interactive && opts.Prompter != nil {
			accept, err := opts.Prompter(p.rg, p.reason)
			if err != nil {
				return nil, fmt.Errorf(
					"classify rg=%s tier=4 prompt: %w", p.rg, err)
			}
			if !accept {
				vetoedSet[p.rg] = p.reason
			}
		} else {
			// Non-interactive: foreign resources are a hard veto.
			log.Printf(
				"classify rg=%s tier=4: non-interactive veto: %s",
				p.rg, p.reason,
			)
			vetoedSet[p.rg] = p.reason
		}
	}

	for _, rg := range owned {
		if reason, vetoed := vetoedSet[rg]; vetoed {
			result.Skipped = append(result.Skipped, ClassifiedSkip{
				Name: rg, Reason: reason,
			})
		} else {
			result.Owned = append(result.Owned, rg)
		}
	}

	return result, nil
}

// classifyTier4 runs lock and extra-resource veto checks on an owned RG.
// Returns (reason, vetoed, needsPrompt, error).
// When needsPrompt is true, the caller should prompt the user sequentially (not from a goroutine)
// and veto if the user declines.
func classifyTier4(ctx context.Context, rgName string, opts ClassifyOptions) (string, bool, bool, error) {
	// Lock check — best-effort: 403 = no veto.
	// Rationale: locks are an additive protection layer; inability to read
	// them does not imply the RG is unsafe to delete. A user who can delete
	// the RG but cannot read its locks should not be blocked by a permission
	// gap in a defense-in-depth check. Contrast with resource 403 below.
	if opts.ListResourceGroupLocks != nil {
		lockVetoed, lockReason, lockErr := checkTier4Locks(ctx, rgName, opts)
		if lockErr != nil {
			return "", false, false, lockErr
		}
		if lockVetoed {
			return lockReason, true, false, nil
		}
	}

	// Extra-resource check — strict: 403 = hard veto.
	// Rationale: if we cannot enumerate resources in a resource group, we
	// cannot verify that all resources belong to this azd environment.
	// Deleting a resource group with unknown contents risks destroying
	// foreign resources. Unlike lock 403 (where inability to read is
	// benign), resource 403 means we lack visibility into what we'd delete.
	if opts.ListResourceGroupResources != nil {
		// When EnvName is empty, foreign-resource detection cannot distinguish owned from
		// untagged resources. Veto to be safe rather than silently allowing deletion.
		if opts.EnvName == "" {
			return "vetoed (Tier 4: cannot verify resource ownership" +
				" without environment name)", true, false, nil
		}

		resources, err := opts.ListResourceGroupResources(ctx, rgName)
		if err != nil {
			if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
				switch respErr.StatusCode {
				case 404:
					// RG already deleted — no veto needed.
					return "", false, false, nil
				case 403:
					// Cannot enumerate resources due to auth failure — veto to be safe.
					reason := "vetoed (Tier 4: unable to enumerate resource group" +
						" resources due to authorization failure)"
					return reason, true, false, nil
				}
			}
			return "", false, false, fmt.Errorf("classify rg=%s tier=4 resources: %w", rgName, err)
		}
		var foreign []string
		for _, res := range resources {
			// Skip known extension resource types that don't support tags
			// (e.g. roleAssignments, diagnosticSettings). These are commonly
			// created by azd scaffold templates and never carry azd-env-name.
			if isExtensionResourceType(res.Type) {
				continue
			}
			tv := tagValue(res.Tags, cAzdEnvNameTag)
			if !strings.EqualFold(tv, opts.EnvName) {
				foreign = append(foreign, res.Name)
			}
		}
		if len(foreign) > 0 {
			reason := fmt.Sprintf(
				"vetoed (Tier 4: %d foreign resource(s) without azd-env-name=%q)", len(foreign), opts.EnvName,
			)
			log.Printf("classify rg=%s tier=4: foreign resources: %v", rgName, foreign)
			return reason, true, true, nil
		}
	}

	return "", false, false, nil
}

// checkTier4Locks checks management locks on an RG.
// Returns (vetoed, reason, error). On 403/404, logs and returns no veto (best-effort).
func checkTier4Locks(
	ctx context.Context, rgName string, opts ClassifyOptions,
) (bool, string, error) {
	locks, err := opts.ListResourceGroupLocks(ctx, rgName)
	if err != nil {
		if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
			if respErr.StatusCode == 403 || respErr.StatusCode == 404 {
				log.Printf("classify rg=%s tier=4: lock check skipped (HTTP %d)", rgName, respErr.StatusCode)
				return false, "", nil
			}
		}
		return false, "", fmt.Errorf("classify rg=%s tier=4 locks: %w", rgName, err)
	}
	for _, lock := range locks {
		if strings.EqualFold(lock.LockType, cLockCanNotDelete) ||
			strings.EqualFold(lock.LockType, cLockReadOnly) {
			reason := fmt.Sprintf(
				"vetoed (Tier 4: management lock %q of type %q)", lock.Name, lock.LockType,
			)
			return true, reason, nil
		}
	}
	return false, "", nil
}

// tagValue returns the dereferenced value of a tag, or "" if the key is absent or nil.
func tagValue(tags map[string]*string, key string) string {
	if tags == nil {
		return ""
	}
	for k, v := range tags {
		if strings.EqualFold(k, key) {
			if v != nil {
				return *v
			}
			return ""
		}
	}
	return ""
}

// extensionResourceTypePrefixes lists ARM resource type prefixes for extension
// resources that don't support tags. These are skipped during Tier 4
// foreign-resource detection to avoid false-positive vetoes on resources
// commonly created by azd scaffold templates.
// All values are pre-lowercased for efficient case-insensitive comparison.
var extensionResourceTypePrefixes = []string{
	"microsoft.authorization/",
	"microsoft.insights/diagnosticsettings",
	"microsoft.resources/links",
}

// isExtensionResourceType returns true if the given ARM resource type is a
// known extension resource that does not support tags.
func isExtensionResourceType(resourceType string) bool {
	lower := strings.ToLower(resourceType)
	return slices.ContainsFunc(extensionResourceTypePrefixes, func(prefix string) bool {
		return strings.HasPrefix(lower, prefix)
	})
}
