// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
)

const (
	// agentsPlaygroundCommand is the Microsoft 365 Agents Playground CLI — the
	// only local client that speaks the Activity protocol (/api/messages).
	agentsPlaygroundCommand = "agentsplayground"
	// defaultPlaygroundChannel is the channel the Playground uses to round-trip
	// with a locally hosted activity agent without an Azure Bot registration.
	defaultPlaygroundChannel = "emulator"
	// agentDigitalWorkerEnvVar toggles the agent's anonymous (digital-worker)
	// auth model. `run` sets it for the local process of an activity agent so the
	// Playground's emulator channel can round-trip off-box. It is never set for
	// deploy, so production keeps the default "simple" model.
	agentDigitalWorkerEnvVar = "AGENT_DIGITAL_WORKER"
	// playgroundReadyPollPeriod matches the inspector poll cadence.
	playgroundReadyPollPeriod = agentInspectorReadyPollPeriod
	// activityProtocolCanonicalName and activityProtocolLegacyName are the two
	// container-level Activity protocol names accepted in an agent definition.
	// These are matched as literals rather than via agent_api.AgentProtocol*
	// constants on purpose: the canonical wire value was renamed upstream from
	// "activity_protocol" to "activity", so binding to the constant would (a)
	// collapse both switch arms onto the same value and (b) silently stop
	// matching whichever name the constant no longer holds. Matching both
	// literals keeps detection stable regardless of that rename.
	activityProtocolCanonicalName = "activity"
	activityProtocolLegacyName    = "activity_protocol"
)

// activityRunProfile captures the only activity-protocol fact `azd ai agent run`
// needs: whether the target agent speaks the Activity protocol (and therefore
// wants the Microsoft 365 Agents Playground as its local client instead of Agent
// Inspector). It is deliberately self-contained so local run support does not
// depend on the deploy-side activity provisioning work.
type activityRunProfile struct {
	IsActivity bool
}

// resolveActivityRunProfile returns the activity profile for the run target, or a
// zero profile (IsActivity=false) when the definition is missing or non-activity.
func resolveActivityRunProfile(def *agent_yaml.ContainerAgent) activityRunProfile {
	if def == nil {
		return activityRunProfile{}
	}
	return activityRunProfile{IsActivity: isActivityProtocolDefinition(*def)}
}

// isActivityProtocolDefinition reports whether a hosted agent definition opts
// into the Activity protocol, either through a container-level protocol entry or
// an agent_endpoint advertising the "activity" protocol. Both the canonical
// "activity" and the legacy "activity_protocol" definition names are accepted.
func isActivityProtocolDefinition(ca agent_yaml.ContainerAgent) bool {
	for _, p := range ca.Protocols {
		switch strings.TrimSpace(p.Protocol) {
		case activityProtocolCanonicalName, activityProtocolLegacyName:
			return true
		}
	}
	if ca.AgentEndpoint != nil {
		for _, p := range ca.AgentEndpoint.Protocols {
			if agent_api.AgentEndpointProtocol(strings.TrimSpace(p)) == agent_api.AgentEndpointProtocolActivity {
				return true
			}
		}
	}
	return false
}

// playgroundMessagesURL builds the /api/messages endpoint the Playground connects
// to. It always uses 127.0.0.1 (not localhost) because the agent binds IPv4;
// localhost resolves to IPv6 ::1 first and fails with ECONNREFUSED ::1:<port>.
func playgroundMessagesURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/api/messages", port)
}

// playgroundCommandArgs assembles the exact argv used to launch the Playground.
func playgroundCommandArgs(port int, channel string) []string {
	if channel == "" {
		channel = defaultPlaygroundChannel
	}
	return []string{agentsPlaygroundCommand, "-e", playgroundMessagesURL(port), "-c", channel}
}

// isPlaygroundInstalled reports whether the agentsplayground CLI is on PATH.
func isPlaygroundInstalled() bool {
	_, err := exec.LookPath(agentsPlaygroundCommand)
	return err == nil
}

// missingPlaygroundWarning mirrors missingInspectorExtensionWarning: it tells the
// user how to install the Playground and the exact command to run manually. The
// agent keeps running so the local endpoint is still usable (e.g. for a smoke
// test) even without the Playground.
func missingPlaygroundWarning(port int, channel string) string {
	return fmt.Sprintf(
		"Warning: the Microsoft 365 Agents Playground was not launched because the %q CLI is not installed.\n"+
			"Install it with: winget install agentsplayground\n"+
			"Then run: %s",
		agentsPlaygroundCommand,
		strings.Join(playgroundCommandArgs(port, channel), " "),
	)
}

// handlePlaygroundAutoLaunch is the activity-agent analogue of
// handleInspectorAutoLaunch. When not suppressed, it waits for the agent's port
// to bind and then launches the Playground. A missing CLI only warns (with an
// install hint) and never fails the run.
func handlePlaygroundAutoLaunch(ctx context.Context, port int, channel string, suppress bool, stderr io.Writer) {
	if suppress {
		return
	}
	if !isPlaygroundInstalled() {
		fmt.Fprintln(stderr, missingPlaygroundWarning(port, channel))
		return
	}
	startPlaygroundAfterAgentReady(ctx, port, channel, playgroundReadyPollPeriod, stderr)
}

// startPlaygroundAfterAgentReady launches the Playground once localhost:<port>
// accepts connections. It mirrors startInspectorAfterAgentReadyWithOptions:
// launch is deferred until the agent binds so the client doesn't connect before
// the server is ready.
func startPlaygroundAfterAgentReady(
	ctx context.Context,
	port int,
	channel string,
	pollPeriod time.Duration,
	stderr io.Writer,
) {
	go func() {
		if err := waitForLocalPort(ctx, port, pollPeriod); err != nil {
			if ctx.Err() == nil {
				fmt.Fprintf(
					stderr,
					"Warning: the Microsoft 365 Agents Playground was not launched because localhost:%d was not ready: %v\n",
					port,
					err,
				)
			}
			return
		}

		fmt.Fprintf(
			stderr,
			"Launching Microsoft 365 Agents Playground: %s\n",
			strings.Join(playgroundCommandArgs(port, channel), " "),
		)
		if err := launchPlayground(ctx, port, channel); err != nil && !isContextCancellation(err) {
			fmt.Fprintf(stderr, "Warning: the Microsoft 365 Agents Playground was not launched: %v\n", err)
		}
	}()
}

// launchPlayground starts the Playground CLI as a child process bound to ctx, so
// it is torn down together with the agent on Ctrl+C. The Playground runs its own
// server and opens a browser tab; its stdio is discarded so it doesn't fight the
// agent for the foreground TTY.
func launchPlayground(ctx context.Context, port int, channel string) error {
	args := playgroundCommandArgs(port, channel)
	//nolint:gosec // G204: args are fixed literals plus a validated int port and channel string.
	proc := exec.CommandContext(ctx, args[0], args[1:]...)
	proc.Stdout = io.Discard
	proc.Stderr = io.Discard
	if err := proc.Start(); err != nil {
		return err
	}
	// exec.Cmd requires Wait to be called after a successful Start to release
	// the associated OS resources and reap the child once it exits. The process
	// is bound to ctx (killed on Ctrl+C together with the agent), so this Wait
	// just reaps it — we don't care about the exit code.
	go func() {
		_ = proc.Wait()
	}()
	return nil
}
