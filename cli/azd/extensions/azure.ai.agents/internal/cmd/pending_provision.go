// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// pendingProvisionEnvVar names the extension-owned env var that lists
// the resource-class tags `azd ai agent init` configured but Azure has
// not yet materialized. The variable is read by nextstep.AssembleState
// to populate State.PendingProvisionReasons; nextstep.ResolveAfterInit
// fires `azd provision` whenever the list is non-empty.
//
// Format: comma-separated, sorted, deduplicated tags. An empty or
// unset value means "no pending provision work". Unknown tags are
// tolerated by readers for forward-compatibility — new init code can
// introduce new tags without coordinating with the resolver.
//
// Lifecycle:
//   - Init sites call addPendingProvisionReason as they configure
//     each non-existent resource (model deployment, project, ACR,
//     App Insights, …).
//   - Init sites call removePendingProvisionReason when re-running
//     init flips a previously-new resource back to "existing".
//   - postprovisionHandler calls clearPendingProvisionReasons after a
//     successful `azd provision` so subsequent invocations of doctor
//     or the init trailer do not falsely suggest provision again.
const pendingProvisionEnvVar = "AI_AGENT_PENDING_PROVISION"

// Known pending-provision reason tags. Adding a tag at an init site
// does not require a resolver change — the resolver treats the list
// as opaque and only checks for non-emptiness. Doctor and other
// readers can interpret tags for richer per-resource diagnostics.
const (
	pendingReasonProject         = "project"
	pendingReasonModelDeployment = "model_deployment"
	pendingReasonACR             = "acr"
	pendingReasonAppInsights     = "app_insights"
)

// parsePendingProvisionReasons splits the comma-separated env-var
// value into a sorted, deduplicated, whitespace-trimmed slice. An
// empty input — or any input that contains only separators and
// whitespace — returns nil. Malformed inputs round-trip to a
// best-effort normalized form rather than failing; the env var is a
// hint signal, not a critical config value, and the caller's path
// should never abort on parse trouble.
func parsePendingProvisionReasons(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	seen := make(map[string]struct{})
	for _, raw := range strings.Split(value, ",") {
		tag := strings.TrimSpace(raw)
		if tag == "" {
			continue
		}
		seen[tag] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	reasons := make([]string, 0, len(seen))
	for tag := range seen {
		reasons = append(reasons, tag)
	}
	slices.Sort(reasons)
	return reasons
}

// formatPendingProvisionReasons joins a list of tags into the on-disk
// env-var format. The input may be unsorted or contain duplicates;
// the output is always sorted and deduplicated. An empty or
// all-empty input produces an empty string (which writers interpret
// as "clear the signal").
func formatPendingProvisionReasons(reasons []string) string {
	return strings.Join(parsePendingProvisionReasons(strings.Join(reasons, ",")), ",")
}

// addPendingProvisionReason appends a reason tag to the
// AI_AGENT_PENDING_PROVISION env var if not already present. The
// read is best-effort: a missing variable (gRPC NotFound) is treated
// as an empty list. The write happens only when the resulting
// formatted value differs from what was on disk, so repeated calls
// for the same tag are cheap and idempotent.
//
// Returns the resulting (sorted, deduplicated) list for caller
// convenience; callers that only need the write effect can discard
// the slice.
func addPendingProvisionReason(
	ctx context.Context, azdClient *azdext.AzdClient, envName, reason string,
) ([]string, error) {
	return mutatePendingProvisionReasons(ctx, azdClient, envName, func(curr []string) []string {
		if slices.Contains(curr, reason) {
			return curr
		}
		return append(slices.Clone(curr), reason)
	})
}

// removePendingProvisionReason drops a reason tag from the
// AI_AGENT_PENDING_PROVISION env var. Idempotent: removing a tag
// that was not present is a no-op (no write performed). Used when
// re-running init swaps an "existing resource" pick into a slot
// that previously held a "new resource" pick, so the trailer does
// not keep showing a stale "needs provision" for that class.
func removePendingProvisionReason(
	ctx context.Context, azdClient *azdext.AzdClient, envName, reason string,
) ([]string, error) {
	return mutatePendingProvisionReasons(ctx, azdClient, envName, func(curr []string) []string {
		out := make([]string, 0, len(curr))
		for _, tag := range curr {
			if tag != reason {
				out = append(out, tag)
			}
		}
		return out
	})
}

// clearPendingProvisionReasons wipes the AI_AGENT_PENDING_PROVISION
// env var. Called by postprovisionHandler after a successful
// provision so the resolver no longer suggests `azd provision`
// against a now-stale signal. Writing the empty string (rather than
// deleting the key) is consistent with the rest of the extension
// and round-trips through the gRPC SetValue API.
func clearPendingProvisionReasons(
	ctx context.Context, azdClient *azdext.AzdClient, envName string,
) error {
	return setEnvValue(ctx, azdClient, envName, pendingProvisionEnvVar, "")
}

// readPendingProvisionEnv reads the AI_AGENT_PENDING_PROVISION env
// var. Production `environmentService.GetValue`
// (cli/azd/internal/grpcserver/environment_service.go) returns
// `{Value: ""}` with a nil error for unset keys — never NotFound —
// so the empty-string fast path is what actually runs in practice.
// The `codes.NotFound` branch below exists for two reasons:
// (1) the test fixture `testEnvironmentServiceServer.GetValue`
// returns NotFound for absent keys, so the branch is exercised by
// unit tests; (2) defensive parity with potential future env-service
// semantics. Any other transport error is surfaced with a wrapped
// context so callers can decide whether to fail or fall back to an
// empty list.
func readPendingProvisionEnv(
	ctx context.Context, azdClient *azdext.AzdClient, envName string,
) (string, error) {
	resp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     pendingProvisionEnvVar,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return "", nil
		}
		return "", fmt.Errorf("failed to read %s: %w", pendingProvisionEnvVar, err)
	}
	if resp == nil {
		return "", nil
	}
	return resp.Value, nil
}

// mutatePendingProvisionReasons is the shared read-modify-write
// helper for addPendingProvisionReason and
// removePendingProvisionReason. The caller supplies a pure function
// that transforms the current parsed list into the desired list.
// The helper handles parse normalization, equality detection (to
// avoid redundant writes), and error wrapping.
func mutatePendingProvisionReasons(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	mutate func(curr []string) []string,
) ([]string, error) {
	priorRaw, err := readPendingProvisionEnv(ctx, azdClient, envName)
	if err != nil {
		return nil, err
	}
	curr := parsePendingProvisionReasons(priorRaw)
	next := parsePendingProvisionReasons(formatPendingProvisionReasons(mutate(curr)))
	formatted := strings.Join(next, ",")
	if formatted == priorRaw {
		return next, nil
	}
	if err := setEnvValue(ctx, azdClient, envName, pendingProvisionEnvVar, formatted); err != nil {
		return nil, err
	}
	return next, nil
}

// updatePendingModelDeploymentSignal centralizes the decision rule
// for the "model_deployment" tag in AI_AGENT_PENDING_PROVISION.
// It is called from both ProcessModels (manifest-driven init path)
// and init_from_code (code-discovery init path) so the signal
// semantics stay in one place.
//
// Rules:
//   - anyModelProcessed=false → no-op. A flow that did not configure
//     any model resources should not touch the signal (other tags,
//     other init runs, or doctor's manual env edits must be
//     preserved).
//   - anyModelProcessed=true, anyNew=true → add "model_deployment".
//     At least one configured model needs Azure to provision a new
//     deployment.
//   - anyModelProcessed=true, anyNew=false → remove "model_deployment".
//     Every configured model points at an existing Azure deployment,
//     so any prior "needs provision" hint from a previous init is
//     stale.
//
// Errors are surfaced for callers to log; this function does not log
// directly so callers can adapt the message to their context (the
// interactive init flows currently use `log.Printf` with a "warning:"
// prefix). The signal is best-effort by design.
func updatePendingModelDeploymentSignal(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	anyModelProcessed bool,
	anyNew bool,
) error {
	if !anyModelProcessed {
		return nil
	}
	if anyNew {
		_, err := addPendingProvisionReason(ctx, azdClient, envName, pendingReasonModelDeployment)
		return err
	}
	_, err := removePendingProvisionReason(ctx, azdClient, envName, pendingReasonModelDeployment)
	return err
}

// updatePendingProjectSignal centralizes the decision rule for the
// "project" tag in AI_AGENT_PENDING_PROVISION. It is called from every
// init.go branch that writes the USE_EXISTING_AI_PROJECT env var so
// the producer of the Bicep "skip project creation" signal and the
// producer of the trailer "needs provision" signal stay in sync.
//
// Rules:
//   - useExisting=true → remove "project". The user picked an
//     existing Foundry project; its endpoint and related vars were
//     populated immediately at init time, so a prior init run's
//     "project" hint (if any) is now stale.
//   - useExisting=false → add "project". The user opted to create a
//     new Foundry project, which requires `azd provision` to run
//     before AZURE_AI_PROJECT_ENDPOINT reflects a real resource.
//
// Errors are surfaced for callers to log; this helper does not log
// directly so each call site can attach its own context. The signal
// is best-effort by design.
func updatePendingProjectSignal(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	useExisting bool,
) error {
	if useExisting {
		_, err := removePendingProvisionReason(ctx, azdClient, envName, pendingReasonProject)
		return err
	}
	_, err := addPendingProvisionReason(ctx, azdClient, envName, pendingReasonProject)
	return err
}
