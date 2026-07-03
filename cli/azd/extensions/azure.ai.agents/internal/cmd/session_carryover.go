// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// Session carry-over across deploys
// ---------------------------------
//
// When a hosted agent is redeployed, Foundry assigns the agent a NEW version.
// A session is bound to the version it was created on, and azd persists the
// "current" session pointer under a version-scoped config key
// (<endpoint>/agents/<name>/versions/<version>/remote). As a result, the first
// `azd ai agent invoke` after a deploy would normally mint a BRAND-NEW session
// on the new version, silently dropping the previous session (and any state on
// its persistent volume).
//
// Empirically (validated against a live Foundry agent):
//   - Invoking an existing session id against the newly deployed endpoint
//     RE-BINDS that session to the new version and runs the new code
//     (sessions show: version 1 -> 2).
//   - A session's persistent data volume (mounted at /home/session) SURVIVES
//     both the stop and the version re-bind; only the container's ephemeral
//     root filesystem is reset.
//   - The re-bind only happens from a STOPPED/idle session. Resuming a session
//     that is still active on the old version keeps serving the OLD code, so we
//     must stop it first.
//
// This module bridges that gap: on deploy it captures the previous version's
// session (predeploy), stops it, and re-points the new version's session
// pointer at it (postdeploy). The next `azd ai agent invoke` then resumes the
// same session on the new code, preserving the /home/session volume.
//
// The behavior is OPT-IN per agent service via `resumeSessionOnDeploy: true` in
// azure.yaml. It is always best-effort and never fails a deploy.

// sessionCarryoverEnabledForService reports whether the agent service opted into
// carrying its session across deploys via `resumeSessionOnDeploy: true` in
// azure.yaml. Defaults to false (a redeploy starts a fresh session). Any error
// reading the service config is treated as "disabled" so carry-over never
// interferes with a deploy.
func sessionCarryoverEnabledForService(svc *azdext.ServiceConfig) bool {
	if svc == nil {
		return false
	}
	cfg, err := project.LoadServiceTargetAgentConfig(svc)
	if err != nil || cfg == nil {
		return false
	}
	return cfg.ResumeSessionOnDeploy
}

// pendingSessionCarryover holds the pre-deploy session id for each hosted agent
// service, captured in the predeploy handler and consumed in the postdeploy
// handler within the same azd process. Keyed by azd service name. Deploy of
// multiple services may run handlers concurrently, so access is mutex-guarded.
var pendingSessionCarryover = struct {
	sync.Mutex
	byService map[string]string
}{byService: map[string]string{}}

// sessionStoreWriteMu serializes carry-over writes to the shared "sessions" map
// in UserConfig. setAgentSpecificContextValue does a read-modify-write on that
// single map, which is last-writer-wins across keys (see its doc comment in
// config_store.go). Because postdeploy handlers for multiple agent services can
// run concurrently, unsynchronized writes could drop one service's carried
// session. This mutex makes the carry-over path's read-modify-write atomic.
var sessionStoreWriteMu sync.Mutex

// stopSessionOutcome classifies how carry-over should react to the result of
// StopSession.
type stopSessionOutcome int

const (
	// stopOutcomeProceed: the previous session is stopped (or was already
	// stopped) — carry it forward to the new version.
	stopOutcomeProceed stopSessionOutcome = iota
	// stopOutcomeSkip: the session is gone or the stop failed — do not carry it
	// forward; the next invoke starts a fresh session.
	stopOutcomeSkip
)

// classifyStopSessionErr maps a StopSession error to a carry-over outcome:
//   - nil error -> proceed (stopped successfully).
//   - 409 session_already_stopped -> proceed (resuming still re-binds it).
//   - 404 -> skip (session deleted/expired; nothing to resume).
//   - any other error -> skip (leave the next invoke on a safe fresh session).
func classifyStopSessionErr(err error) stopSessionOutcome {
	if err == nil {
		return stopOutcomeProceed
	}
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		if respErr.StatusCode == http.StatusConflict &&
			respErr.ErrorCode == "session_already_stopped" {
			return stopOutcomeProceed
		}
	}
	return stopOutcomeSkip
}


// captureSessionForCarryover records the current (pre-deploy) session pointer
// for a hosted agent service so it can be resumed after the deploy assigns a new
// version. It is a no-op — leaving nothing to carry — when the service has not
// opted in, on the first deploy (no prior endpoint), or when the agent was never
// invoked on the previous version (no stored session). Best-effort: all errors
// are swallowed so predeploy is never blocked.
func captureSessionForCarryover(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	svc *azdext.ServiceConfig,
) {
	if !sessionCarryoverEnabledForService(svc) {
		return
	}

	// Clear any stale capture from a previous deploy of the same service in this
	// process so a failed lookup below can't resurrect an old session id.
	pendingSessionCarryover.Lock()
	delete(pendingSessionCarryover.byService, svc.Name)
	pendingSessionCarryover.Unlock()

	envName, err := currentEnvName(ctx, azdClient)
	if err != nil || envName == "" {
		return
	}

	serviceKey := toServiceKey(svc.Name)

	// The agent endpoint still points at the previously deployed version here
	// (it is rewritten to the new version during deploy). Empty on first deploy.
	endpointResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     fmt.Sprintf("AGENT_%s_ENDPOINT", serviceKey),
	})
	if err != nil || endpointResp.Value == "" {
		return
	}

	oldKey := buildRemoteAgentKeyFromEndpoint(endpointResp.Value)
	sessionID, err := getAgentSpecificContextValue(ctx, azdClient, "sessions", oldKey)
	if err != nil || sessionID == "" {
		return
	}

	pendingSessionCarryover.Lock()
	pendingSessionCarryover.byService[svc.Name] = sessionID
	pendingSessionCarryover.Unlock()
}

// carryOverSessionAfterDeploy stops the session captured before the deploy and
// re-points the new version's session pointer at it, so the next invoke resumes
// the same session on the freshly deployed code. It is best-effort and never
// returns an error: any failure simply falls back to azd's default behavior
// (the next invoke starts a fresh session on the new version).
//
// agentClient must target the project endpoint (FOUNDRY_PROJECT_ENDPOINT); the
// :stop route is endpoint-scoped, not version-scoped, so a single client works
// across versions.
func carryOverSessionAfterDeploy(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentClient *agent_api.AgentClient,
	svc *azdext.ServiceConfig,
	envName string,
) {
	if !sessionCarryoverEnabledForService(svc) || agentClient == nil {
		return
	}

	// Consume the captured session up-front (read + delete). Consumption is
	// intentionally single-shot and non-retriable: if the stop succeeds but a
	// later step here fails, the id is already gone from the stash so a
	// subsequent redeploy won't re-attempt carry-over for it. That is the
	// desired best-effort behavior — the fallback (a fresh session on the new
	// version at the next invoke) is always safe, and retrying a stop against a
	// half-carried session would add complexity for no benefit.
	pendingSessionCarryover.Lock()
	sessionID := pendingSessionCarryover.byService[svc.Name]
	delete(pendingSessionCarryover.byService, svc.Name)
	pendingSessionCarryover.Unlock()

	// Nothing captured: first deploy, carry-over disabled at capture time, or the
	// agent was never invoked on the previous version.
	if sessionID == "" {
		return
	}

	// Stop the previous session so that resuming it re-binds it to the new
	// version. A still-running session keeps serving the old code.
	err := agentClient.StopSession(ctx, svc.Name, sessionID, DefaultAgentAPIVersion, nil)
	if classifyStopSessionErr(err) == stopOutcomeSkip {
		// Session gone (404), or the stop failed for another reason. Do not carry
		// it forward — the next invoke will start a fresh session on the new
		// version, which is always safe.
		log.Printf(
			"session carry-over: not carrying session %q for agent %q "+
				"(stop result: %v); the next invoke will start a new session",
			sessionID, svc.Name, err,
		)
		return
	}

	// Re-point the NEW version's session pointer at the carried session. The
	// agent endpoint env var now reflects the newly deployed version.
	newEndpointResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     fmt.Sprintf("AGENT_%s_ENDPOINT", toServiceKey(svc.Name)),
	})
	if err != nil || newEndpointResp.Value == "" {
		log.Printf(
			"session carry-over: new endpoint for agent %q unavailable; "+
				"skipping carry-over of session %q",
			svc.Name, sessionID,
		)
		return
	}

	newKey := buildRemoteAgentKeyFromEndpoint(newEndpointResp.Value)
	// Serialize the read-modify-write of the shared sessions map so concurrent
	// postdeploy handlers can't clobber each other's carried session.
	sessionStoreWriteMu.Lock()
	err = setAgentSpecificContextValue(ctx, azdClient, "sessions", newKey, sessionID)
	sessionStoreWriteMu.Unlock()
	if err != nil {
		log.Printf(
			"session carry-over: failed to persist carried session %q for agent %q: %v",
			sessionID, svc.Name, err,
		)
		return
	}

	fmt.Printf(
		"Session %q will resume on the newly deployed version of agent %q. "+
			"Run 'azd ai agent invoke %s' to continue on the new code with the "+
			"session's persisted volume intact.\n",
		sessionID, svc.Name, svc.Name,
	)
}
