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
	// only local client that speaks the Activity protocol.
	agentsPlaygroundCommand = "agentsplayground"
	// defaultPlaygroundChannel is the channel the Playground uses to round-trip
	// with a locally hosted activity agent without an Azure Bot registration.
	defaultPlaygroundChannel = "emulator"
	// activityMessagesPath is the container route for Activity protocol 2.0.0
	// and later. It is also the default when a definition advertises only an
	// Activity endpoint without a container protocol version.
	activityMessagesPath = "/activity/messages"
	// legacyActivityMessagesPath is the deprecated container route retained for
	// Activity protocol v1/1.0/1.0.0 definitions.
	legacyActivityMessagesPath = "/api/messages"
	// agentDigitalWorkerEnvVar toggles the agent's anonymous (digital-worker)
	// auth model. `run` sets it for the local process of an activity agent so the
	// Playground's emulator channel can round-trip off-box. It is never set for
	// deploy, so production keeps the default "simple" model.
	agentDigitalWorkerEnvVar = "AGENT_DIGITAL_WORKER"
	// playgroundReadyPollPeriod matches the inspector poll cadence.
	playgroundReadyPollPeriod = agentInspectorReadyPollPeriod
)

// activityRunProfile captures the activity-protocol facts `azd ai agent run`
// needs: whether the target wants the Microsoft 365 Agents Playground instead
// of Agent Inspector, and which versioned container route the Playground should
// call. It is deliberately self-contained so local run support does not depend
// on the deploy-side activity provisioning work.
type activityRunProfile struct {
	IsActivity   bool
	MessagesPath string
}

// resolveActivityRunProfile returns the activity profile for the run target, or a
// zero profile (IsActivity=false) when the definition is missing or non-activity.
func resolveActivityRunProfile(def *agent_yaml.ContainerAgent) activityRunProfile {
	if def == nil {
		return activityRunProfile{}
	}

	for _, p := range def.Protocols {
		if agent_api.IsActivityProtocolName(agent_api.AgentProtocol(strings.TrimSpace(p.Protocol))) {
			return activityRunProfile{
				IsActivity:   true,
				MessagesPath: activityMessagesPathForVersion(p.Version),
			}
		}
	}
	if def.AgentEndpoint != nil {
		for _, p := range def.AgentEndpoint.Protocols {
			if agent_api.AgentEndpointProtocol(strings.TrimSpace(p)) == agent_api.AgentEndpointProtocolActivity {
				return activityRunProfile{IsActivity: true, MessagesPath: activityMessagesPath}
			}
		}
	}
	return activityRunProfile{}
}

// activityMessagesPathForVersion returns the local container route selected by
// the Activity protocol version. Only the deprecated v1 versions use the
// legacy route; missing, current, and future versions use the 2.0 route.
func activityMessagesPathForVersion(version string) string {
	switch strings.ToLower(strings.TrimSpace(version)) {
	case "v1", "1.0", "1.0.0":
		return legacyActivityMessagesPath
	default:
		return activityMessagesPath
	}
}

// playgroundMessagesURL builds the Activity endpoint the Playground connects to.
// It always uses 127.0.0.1 (not localhost) because the agent binds IPv4;
// localhost resolves to IPv6 ::1 first and fails with ECONNREFUSED ::1:<port>.
func playgroundMessagesURL(port int, messagesPath string) string {
	if messagesPath == "" {
		messagesPath = activityMessagesPath
	}
	return fmt.Sprintf("http://127.0.0.1:%d%s", port, messagesPath)
}

// playgroundCommandArgs assembles the exact argv used to launch the Playground.
func playgroundCommandArgs(port int, channel, messagesPath string) []string {
	if channel == "" {
		channel = defaultPlaygroundChannel
	}
	return []string{agentsPlaygroundCommand, "-e", playgroundMessagesURL(port, messagesPath), "-c", channel}
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
func missingPlaygroundWarning(port int, channel, messagesPath string) string {
	return fmt.Sprintf(
		"Warning: the Microsoft 365 Agents Playground was not launched because the %q CLI is not installed.\n"+
			"Install it with: winget install agentsplayground\n"+
			"Then run: %s",
		agentsPlaygroundCommand,
		strings.Join(playgroundCommandArgs(port, channel, messagesPath), " "),
	)
}

// handlePlaygroundAutoLaunch is the activity-agent analogue of
// handleInspectorAutoLaunch. When not suppressed, it waits for the agent's port
// to bind and then launches the Playground. A missing CLI only warns (with an
// install hint) and never fails the run.
func handlePlaygroundAutoLaunch(
	ctx context.Context,
	port int,
	channel, messagesPath string,
	suppress bool,
	stderr io.Writer,
) {
	if suppress {
		return
	}
	if !isPlaygroundInstalled() {
		fmt.Fprintln(stderr, missingPlaygroundWarning(port, channel, messagesPath))
		return
	}
	startPlaygroundAfterAgentReady(ctx, port, channel, messagesPath, playgroundReadyPollPeriod, stderr)
}

// startPlaygroundAfterAgentReady launches the Playground once localhost:<port>
// accepts connections. It mirrors startInspectorAfterAgentReadyWithOptions:
// launch is deferred until the agent binds so the client doesn't connect before
// the server is ready.
func startPlaygroundAfterAgentReady(
	ctx context.Context,
	port int,
	channel, messagesPath string,
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
			strings.Join(playgroundCommandArgs(port, channel, messagesPath), " "),
		)
		if err := launchPlayground(ctx, port, channel, messagesPath); err != nil && !isContextCancellation(err) {
			fmt.Fprintf(stderr, "Warning: the Microsoft 365 Agents Playground was not launched: %v\n", err)
		}
	}()
}

// launchPlayground starts the Playground CLI as a child process bound to ctx, so
// it is torn down together with the agent on Ctrl+C. The Playground runs its own
// server and opens a browser tab; its stdio is discarded so it doesn't fight the
// agent for the foreground TTY.
func launchPlayground(ctx context.Context, port int, channel, messagesPath string) error {
	args := playgroundCommandArgs(port, channel, messagesPath)
	//nolint:gosec // G204: args use fixed route literals plus a validated int port and channel string.
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
