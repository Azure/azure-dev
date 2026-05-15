// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type invokeFlags struct {
	message         string
	inputFile       string
	local           bool
	name            string
	port            int
	timeout         int
	session         string
	newSession      bool
	conversation    string
	newConversation bool
	protocol        string
	agentEndpoint   string
}

const defaultInvokeTimeoutSeconds = 30 * 60

type InvokeAction struct {
	flags    *invokeFlags
	noPrompt bool
	endpoint *parsedAgentEndpoint
}

func newInvokeCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &invokeFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "invoke [name] [message]",
		Short: "Send a message to your agent.",
		Long: `Send a message to your agent.

By default the agent is invoked remotely on Foundry. When a single
argument is provided it is treated as the message and the agent name
is auto-detected from azure.yaml. With two arguments the first is the
agent name and the second is the message.

Use --input-file/-f to send the contents of a file as the request body
instead of a positional message argument. This is useful for structured
or large payloads with the invocations protocol.

Use --local to target a locally running agent (started via 'azd ai agent run')
instead of Foundry.

Sessions are persisted per-agent — consecutive invokes reuse the same
session automatically. Pass --new-session to force a reset.`,
		Example: `  # Invoke the remote agent on Foundry (auto-detects agent from azure.yaml)
  azd ai agent invoke "Hello!"

  # Invoke a specific remote agent by name
  azd ai agent invoke my-agent "Hello!"

  # Invoke using a specific protocol
  azd ai agent invoke --protocol invocations "Hello!"

  # Invoke with a file as the request body
  azd ai agent invoke -f request.json

  # Invoke a named agent with a file body
  azd ai agent invoke my-agent -f request.json

  # Invoke locally (agent must be running via 'azd ai agent run')
  azd ai agent invoke --local "Hello!"

  # Start a new session (discard conversation history)
  azd ai agent invoke --new-session "Hello!"

  # Invoke a deployed agent from any directory using the endpoint URL shown by 'azd ai agent show'
  azd ai agent invoke \
      --agent-endpoint https://<acct>.services.ai.azure.com/api/projects/<proj>/agents/<name>/endpoint/protocols/openai/responses?api-version=2025-11-15-preview \
      "Hello!"`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			switch len(args) {
			case 2:
				flags.name = args[0]
				flags.message = args[1]
			case 1:
				// Single arg could be a name (when -f is used) or a message
				if flags.inputFile != "" {
					flags.name = args[0]
				} else {
					flags.message = args[0]
				}
			case 0:
				// Only valid when -f is provided
			}

			action := &InvokeAction{flags: flags, noPrompt: extCtx.NoPrompt}

			// Agent-endpoint structural conflicts are surfaced first so the user sees
			// the precise reason their invocation cannot proceed.
			if flags.agentEndpoint != "" {
				if err := validateAgentEndpointFlags(cmd, flags); err != nil {
					return err
				}
				parsed, err := parseAgentEndpoint(flags.agentEndpoint)
				if err != nil {
					return err
				}
				flags.protocol = string(parsed.Protocol)
				flags.name = parsed.AgentName
				action.endpoint = parsed
			}

			if flags.inputFile != "" && flags.message != "" {
				return exterrors.Validation(
					exterrors.CodeInvalidParameter,
					"cannot use --input-file and a message argument together",
					"provide either a message argument or --input-file, not both",
				)
			}
			if flags.inputFile == "" && flags.message == "" {
				return exterrors.Validation(
					exterrors.CodeInvalidParameter,
					"a message argument or --input-file is required",
					"provide a message as a positional argument, or use --input-file/-f to send a file",
				)
			}

			if flags.name != "" && flags.local {
				return exterrors.Validation(
					exterrors.CodeInvalidParameter,
					"cannot use --local with a named agent; named agents are always invoked remotely on Foundry",
					"omit the agent name for local invocation, or remove --local for remote",
				)
			}

			if flags.protocol != "" {
				p := agent_api.AgentProtocol(flags.protocol)
				if !p.IsInvocable() {
					return exterrors.Validation(
						exterrors.CodeInvalidParameter,
						fmt.Sprintf("unsupported protocol %q for invocation", flags.protocol),
						"supported protocols are: responses, invocations",
					)
				}
			}

			return action.Run(ctx)
		},
	}

	cmd.Flags().BoolVarP(&flags.local, "local", "l", false, "Invoke on localhost instead of Foundry")
	cmd.Flags().StringVarP(&flags.inputFile, "input-file", "f", "", "Path to a file whose contents are sent as the request body")
	cmd.Flags().StringVarP(&flags.protocol, "protocol", "p", "", "Protocol to use: responses (default) or invocations")
	cmd.Flags().IntVar(&flags.port, "port", DefaultPort, "Local server port")
	cmd.Flags().IntVarP(
		&flags.timeout,
		"timeout",
		"t",
		defaultInvokeTimeoutSeconds,
		"Request timeout in seconds (0 for no timeout)",
	)
	cmd.Flags().StringVarP(&flags.session, "session-id", "s", "", "Explicit session ID override")
	cmd.Flags().BoolVar(&flags.newSession, "new-session", false, "Force a new session (discard saved one)")
	cmd.Flags().StringVar(&flags.conversation, "conversation-id", "", "Explicit conversation ID override")
	cmd.Flags().BoolVar(&flags.newConversation, "new-conversation", false, "Force a new conversation (discard saved one)")
	cmd.Flags().StringVar(
		&flags.agentEndpoint,
		"agent-endpoint",
		"",
		"Full endpoint URL of a deployed agent (run 'azd ai agent show' to see it). "+
			"Invokes without requiring an azd project; protocol is derived from the URL.",
	)

	return cmd
}

// validateAgentEndpointFlags rejects flags that have no effect (or conflict) when --agent-endpoint
// is used. Ephemeral mode has no project, no local persistence, and no localhost target.
func validateAgentEndpointFlags(cmd *cobra.Command, flags *invokeFlags) error {
	// Disallowed companion flags for --agent-endpoint, in the order checked.
	// `set` is true when the flag is meaningfully present on the command line.
	checks := []struct {
		name       string
		set        bool
		suggestion string
	}{
		{"--local", flags.local, "omit --local to invoke the deployed agent at the given URL"},
		{
			"a positional agent name",
			flags.name != "",
			"the agent name is read from the --agent-endpoint URL; remove the positional argument",
		},
		{"--port", cmd.Flags().Changed("port"), "--port targets a local agent; omit it when using --agent-endpoint"},
		{"--protocol", cmd.Flags().Changed("protocol"), "the protocol is read from the --agent-endpoint URL; omit --protocol"},
	}

	for _, c := range checks {
		if c.set {
			return exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("--agent-endpoint cannot be combined with %s", c.name),
				c.suggestion,
			)
		}
	}
	return nil
}

func (a *InvokeAction) Run(ctx context.Context) error {
	protocol, err := a.resolveProtocol(ctx)
	if err != nil {
		return err
	}

	if a.flags.local {
		switch protocol {
		case agent_api.AgentProtocolInvocations:
			return a.invocationsLocal(ctx)
		default:
			return a.responsesLocal(ctx)
		}
	}

	// Remote: route by protocol.
	if protocol == agent_api.AgentProtocolInvocations {
		return a.invocationsRemote(ctx)
	}
	return a.responsesRemote(ctx)
}

// resolveProtocol returns the protocol to use for this invocation.
// The explicit --protocol flag takes priority; otherwise the protocol
// is auto-detected from agent.yaml (local or remote).
func (a *InvokeAction) resolveProtocol(
	ctx context.Context,
) (agent_api.AgentProtocol, error) {
	if a.flags.protocol != "" {
		return agent_api.AgentProtocol(a.flags.protocol), nil
	}

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	if a.flags.local {
		return resolveAgentProtocol(
			ctx, azdClient, "", a.noPrompt,
		)
	}
	return resolveAgentProtocol(
		ctx, azdClient, a.flags.name, a.noPrompt,
	)
}

func (a *InvokeAction) httpTimeout() time.Duration {
	if a.flags.timeout <= 0 {
		return 0 // no timeout
	}
	return time.Duration(a.flags.timeout) * time.Second
}

// resolveBody returns the request body for invoke calls.
// When --input-file is set, the file contents are returned; otherwise the message string is used.
func (a *InvokeAction) resolveBody() ([]byte, string, error) {
	if a.flags.inputFile != "" {
		//nolint:gosec // G304: inputFile is a user-provided CLI flag
		data, err := os.ReadFile(a.flags.inputFile)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read input file %q: %w", a.flags.inputFile, err)
		}
		return data, fmt.Sprintf("(from file %s)", a.flags.inputFile), nil
	}
	return []byte(a.flags.message), fmt.Sprintf("%q", a.flags.message), nil
}

// contentTypeForBody returns "application/json" if data is valid JSON,
// otherwise "text/plain".
func contentTypeForBody(data []byte) string {
	if json.Valid(data) {
		return "application/json"
	}
	return "text/plain"
}

func (a *InvokeAction) responsesLocal(ctx context.Context) error {
	port := a.flags.port

	body, bodyLabel, err := a.resolveBody()
	if err != nil {
		return err
	}

	msg := string(body)

	// Open azd client for session/conversation persistence.
	var azdClient *azdext.AzdClient
	if c, err := azdext.NewAzdClient(); err == nil {
		azdClient = c
		defer azdClient.Close()
	}

	agentKey := resolveLocalAgentKey(ctx, azdClient, a.flags.name, a.noPrompt)

	// Resolve local session and conversation IDs (always generated locally).
	var sid, convID string
	if azdClient != nil {
		sid, err = resolveStoredID(
			ctx, azdClient, agentKey, a.flags.session, a.flags.newSession, "sessions", true,
		)
		if err != nil {
			log.Printf("invoke local: failed to resolve session ID: %v", err)
		}
		convID, err = resolveStoredID(
			ctx, azdClient, agentKey, a.flags.conversation, a.flags.newConversation, "conversations", true,
		)
		if err != nil {
			log.Printf("invoke local: failed to resolve conversation ID: %v", err)
		}
	}

	fmt.Printf("Target:       localhost:%d (local)\n", port)
	fmt.Printf("Message:      %s\n", bodyLabel)
	printSessionStatus("Session:      ", sid)
	fmt.Printf("Conversation: %s\n\n", convID)

	reqBody := map[string]any{
		"input": msg,
	}
	if sid != "" {
		reqBody["session_id"] = sid
	}
	if convID != "" {
		reqBody["conversation"] = map[string]string{"id": convID}
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	reqURL := fmt.Sprintf("http://localhost:%d/responses", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: a.httpTimeout()}
	resp, err := client.Do(req) //nolint:gosec // G704: URL targets localhost with user-configured port
	if err != nil {
		return fmt.Errorf(
			"could not connect to localhost:%d — is the agent running? Start it with: azd ai agent run",
			port,
		)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		requestID := resp.Header.Get("apim-request-id")
		if requestID != "" {
			fmt.Printf("Trace ID: %s\n", requestID)
		}
		return fmt.Errorf(
			"POST %s failed with HTTP %d: %s\n%s",
			reqURL, resp.StatusCode, resp.Status, string(respBody),
		)
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		// Not JSON — just print raw response
		fmt.Println(string(respBody))
		return nil
	}

	return printAgentResponse(result, "local")
}

// remoteContext holds the resolved inputs for a remote (Foundry) invoke.
// In ephemeral mode (--agent-endpoint) the project endpoint / agent name /
// api-version come from the parsed URL.
//
// agentKey is the persistence key used by the global UserConfig store. It is
// non-empty whenever session/conversation IDs should be saved or resumed:
//   - project mode: derived from AGENT_{SVC}_ENDPOINT
//   - ephemeral mode: derived from the parsed --agent-endpoint URL
//     (independent of api-version / trailing slash / fragment)
//
// In standalone mode (no parent azd daemon, e.g. running the extension binary
// directly outside an azd command) azdClient is nil and persistence helpers
// no-op. agentKey may still be non-empty in that case.
type remoteContext struct {
	name            string
	agentKey        string
	projectEndpoint string
	apiVersion      string
	azdClient       *azdext.AzdClient
	bearerToken     string
}

// resolveRemoteContext returns the inputs required to invoke a remote agent.
// In project mode it opens an azd client and reads the environment; in ephemeral
// mode (--agent-endpoint) it skips both. Auth token acquisition is intentionally
// deferred to acquireBearerToken so callers can validate the request body first
// and avoid unnecessary token round-trips on invalid input. Callers must close
// rc.azdClient when non-nil.
func (a *InvokeAction) resolveRemoteContext(ctx context.Context) (*remoteContext, error) {
	rc := &remoteContext{apiVersion: DefaultAgentAPIVersion}

	if a.endpoint != nil {
		rc.name = a.endpoint.AgentName
		rc.projectEndpoint = a.endpoint.ProjectEndpoint
		if a.endpoint.APIVersion != "" {
			rc.apiVersion = a.endpoint.APIVersion
		}
		rc.agentKey = buildAgentKey(a.endpoint.ProjectEndpoint, a.endpoint.AgentName, "", false)
		// Best-effort attach to the parent azd daemon so session/conversation IDs
		// persist across invokes via global UserConfig. When running the extension
		// binary directly (standalone), this fails and we proceed without persistence.
		if azdClient, err := azdext.NewAzdClient(); err == nil {
			rc.azdClient = azdClient
		}
		return rc, nil
	}

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create azd client: %w", err)
	}
	rc.azdClient = azdClient

	rc.name = a.flags.name
	if info, err := resolveAgentServiceFromProject(ctx, azdClient, rc.name, a.noPrompt); err == nil {
		if rc.name == "" && info.AgentName != "" {
			rc.name = info.AgentName
		}
		if info.AgentEndpoint != "" {
			rc.agentKey = buildRemoteAgentKeyFromEndpoint(info.AgentEndpoint)
		}
	}
	if rc.name == "" {
		azdClient.Close()
		return nil, fmt.Errorf(
			"agent name is required; provide as the first argument or " +
				"define an azure.ai.agent service in azure.yaml",
		)
	}

	ep, err := resolveAgentEndpoint(ctx, "", "")
	if err != nil {
		azdClient.Close()
		return nil, err
	}
	rc.projectEndpoint = ep
	return rc, nil
}

// acquireBearerToken obtains a Foundry bearer token. Called after request body
// validation so that local errors (e.g., a missing --input-file) are surfaced
// before any auth round-trip is attempted.
func (a *InvokeAction) acquireBearerToken(ctx context.Context) (string, error) {
	credential, err := newAgentCredential()
	if err != nil {
		return "", err
	}
	token, err := credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://ai.azure.com/.default"},
	})
	if err != nil {
		return "", ephemeralAuthError(a.endpoint != nil, err)
	}
	return token.Token, nil
}

// ephemeralAuthError wraps a token-acquisition failure with a login suggestion when
// the user is invoking outside an azd project (where mis-configured credentials are common).
func ephemeralAuthError(ephemeral bool, err error) error {
	if !ephemeral {
		return fmt.Errorf("failed to get auth token: %w", err)
	}
	return exterrors.Auth(
		exterrors.CodeAuthFailed,
		fmt.Sprintf("failed to get auth token: %v", err),
		"run `azd auth login` and try again",
	)
}

func (a *InvokeAction) responsesRemote(ctx context.Context) error {
	rc, err := a.resolveRemoteContext(ctx)
	if err != nil {
		return err
	}
	if rc.azdClient != nil {
		defer rc.azdClient.Close()
	}

	body, bodyLabel, err := a.resolveBody()
	if err != nil {
		return err
	}

	agentKey := rc.agentKey
	if agentKey == "" && rc.azdClient != nil {
		log.Printf("warning: agent endpoint not available, session state will not be persisted")
	}

	// Acquire the bearer token after body validation so a local input error
	// (e.g., unreadable --input-file) does not pay an unnecessary auth round-trip
	// and is surfaced before any auth failure.
	rc.bearerToken, err = a.acquireBearerToken(ctx)
	if err != nil {
		return err
	}

	msg := string(body)

	// Build request body — uses streaming to receive the full agent response.
	reqBody := map[string]any{
		"input":  msg,
		"stream": true,
	}

	// Session ID — routes to the same microVM container instance.
	// When empty, let the server assign one.
	var sid string
	if agentKey != "" && rc.azdClient != nil {
		sid, err = resolveStoredID(
			ctx, rc.azdClient, agentKey, a.flags.session, a.flags.newSession, "sessions", false,
			legacyKeysForRemote(rc.name)...,
		)
		if err != nil {
			return err
		}
	} else {
		sid = a.flags.session
	}
	if sid != "" {
		reqBody["session_id"] = sid
	}

	// Conversation ID — enables multi-turn memory via Foundry Conversations API.
	var convID string
	if agentKey != "" && rc.azdClient != nil {
		convID, err = resolveConversationID(
			ctx,
			rc.azdClient,
			agentKey,
			a.flags.conversation,
			a.flags.newConversation,
			rc.projectEndpoint,
			rc.bearerToken,
			rc.name,
			legacyKeysForRemote(rc.name)...,
		)
		if err != nil {
			return err
		}
	} else if a.flags.conversation != "" {
		convID = a.flags.conversation
	} else {
		convID, err = createConversation(ctx, rc.projectEndpoint, rc.name, rc.bearerToken)
		if err != nil {
			return err
		}
	}
	reqBody["conversation"] = map[string]string{"id": convID}

	fmt.Printf("Agent:        %s (remote)\n", rc.name)
	fmt.Printf("Message:      %s\n", bodyLabel)
	printSessionStatus("Session:      ", sid)
	fmt.Printf("Conversation: %s\n", convID)
	fmt.Println()

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	respURL := buildResponsesURL(rc.projectEndpoint, rc.name, rc.apiVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, respURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+rc.bearerToken)

	client := &http.Client{Timeout: a.httpTimeout()}
	//nolint:gosec // G704: URL is built from a validated Foundry endpoint (env or --agent-endpoint)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s failed: %w", respURL, err)
	}
	defer resp.Body.Close()

	requestID := resp.Header.Get("apim-request-id")
	if requestID != "" {
		fmt.Printf("Trace ID: %s\n", requestID)
	}

	captureResponseSession(ctx, rc.azdClient, agentKey, sid, resp, "Session:      ")

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST %s failed with HTTP %d: %s\n%s", respURL, resp.StatusCode, resp.Status, string(respBody))
	}

	// Parse SSE stream for agent output
	if err := readSSEStream(resp.Body, rc.name); err != nil {
		return err
	}

	if agentKey != "" && rc.azdClient != nil {
		fmt.Println("\n(tip: pass --new-session or --new-conversation to reset; see `azd ai agent invoke --help`)")
	}
	return nil
}

func (a *InvokeAction) invocationsLocal(ctx context.Context) error {
	port := a.flags.port

	body, bodyLabel, err := a.resolveBody()
	if err != nil {
		return err
	}

	var azdClient *azdext.AzdClient
	if c, err := azdext.NewAzdClient(); err == nil {
		azdClient = c
		defer azdClient.Close()
	}

	agentKey := resolveLocalAgentKey(ctx, azdClient, a.flags.name, a.noPrompt)

	// Resolve local session ID (generated locally, not server-assigned).
	var sid string
	if azdClient != nil {
		sid, err = resolveStoredID(
			ctx, azdClient, agentKey, a.flags.session, a.flags.newSession, "sessions", true,
		)
		if err != nil {
			log.Printf("invoke local: failed to resolve session ID: %v", err)
		}
	}

	fmt.Printf("Target:   localhost:%d (local, invocations protocol)\n", port)
	fmt.Printf("Input:    %s\n", bodyLabel)
	printSessionStatus("Session:  ", sid)
	fmt.Println()

	localBaseURL := fmt.Sprintf("http://localhost:%d", port)

	// Fetch and cache the agent's OpenAPI spec (always refresh for local).
	if azdClient != nil {
		fetchOpenAPISpec(ctx, azdClient, localBaseURL, agentKey, "local", "", true)
	}

	invURL := localBaseURL + "/invocations"
	if sid != "" {
		invURL += "?agent_session_id=" + url.QueryEscape(sid)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, invURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", contentTypeForBody(body))

	client := &http.Client{Timeout: a.httpTimeout()}
	resp, err := client.Do(req) //nolint:gosec // G704: URL targets localhost with user-configured port
	if err != nil {
		return fmt.Errorf(
			"could not connect to localhost:%d — is the agent running? Start it with: azd ai agent run",
			port,
		)
	}
	defer resp.Body.Close()

	// Print the invocation ID if the agent returned one.
	if invID := resp.Header.Get("x-agent-invocation-id"); invID != "" {
		fmt.Printf("Invocation:   %s\n", invID)
	}

	return handleInvocationResponse(ctx, resp, "", "", agentKey, a.httpTimeout())
}

// invocationsRemote sends the user's message to Foundry using
// the invocations protocol (POST /agents/{name}/endpoint/protocols/invocations).
func (a *InvokeAction) invocationsRemote(ctx context.Context) error {
	rc, err := a.resolveRemoteContext(ctx)
	if err != nil {
		return err
	}
	if rc.azdClient != nil {
		defer rc.azdClient.Close()
	}

	agentKey := rc.agentKey
	if agentKey == "" && rc.azdClient != nil {
		log.Printf("warning: agent endpoint not available, session state will not be persisted")
	}

	if a.flags.newConversation {
		fmt.Fprintln(os.Stderr,
			"note: --new-conversation has no effect for the invocations protocol "+
				"(memory is bound to the session; use --new-session to reset).")
	}

	body, bodyLabel, err := a.resolveBody()
	if err != nil {
		return err
	}

	// Acquire the bearer token after body validation so a local input error
	// (e.g., unreadable --input-file) does not pay an unnecessary auth round-trip
	// and is surfaced before any auth failure.
	rc.bearerToken, err = a.acquireBearerToken(ctx)
	if err != nil {
		return err
	}

	// Session ID — routes to the same container instance.
	var sid string
	if agentKey != "" && rc.azdClient != nil {
		sid, err = resolveStoredID(
			ctx, rc.azdClient, agentKey, a.flags.session, a.flags.newSession, "sessions", false,
			legacyKeysForRemote(rc.name)...,
		)
		if err != nil {
			return err
		}
	} else {
		sid = a.flags.session
	}

	fmt.Printf("Agent:    %s (remote, invocations protocol)\n", rc.name)
	fmt.Printf("Input:    %s\n", bodyLabel)
	printSessionStatus("Session:  ", sid)
	fmt.Println()

	remoteBaseURL := fmt.Sprintf("%s/agents/%s/endpoint/protocols", rc.projectEndpoint, rc.name)

	// Fetch and cache the agent's OpenAPI spec only in project mode. In ephemeral
	// mode (--agent-endpoint) we deliberately avoid the on-disk side effect since
	// the user is one-off targeting a remote endpoint.
	if rc.azdClient != nil && a.endpoint == nil {
		fetchOpenAPISpec(ctx, rc.azdClient, remoteBaseURL, rc.name, "remote", rc.bearerToken, false)
	}

	invURL := buildInvocationsURL(rc.projectEndpoint, rc.name, rc.apiVersion, sid)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, invURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", contentTypeForBody(body))
	req.Header.Set("Authorization", "Bearer "+rc.bearerToken)

	client := &http.Client{Timeout: a.httpTimeout()}
	//nolint:gosec // G704: URL is built from a validated Foundry endpoint (env or --agent-endpoint)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s failed: %w", invURL, err)
	}
	defer resp.Body.Close()

	// Print the invocation ID if the agent returned one. We do not persist it
	// to the per-user config: the config store only supports the "sessions"
	// and "conversations" maps (see validateStoreField), and invocation IDs
	// are not used to drive any subsequent invoke — they are emitted purely
	// for trace correlation.
	if invID := resp.Header.Get("x-agent-invocation-id"); invID != "" {
		fmt.Printf("Invocation:   %s\n", invID)
	}

	captureResponseSession(ctx, rc.azdClient, agentKey, sid, resp, "Session:  ")

	if err := handleInvocationResponse(ctx, resp, rc.projectEndpoint, rc.bearerToken, rc.name, a.httpTimeout()); err != nil {
		return err
	}

	if agentKey != "" && rc.azdClient != nil {
		fmt.Println("\n(tip: pass --new-session to reset; see `azd ai agent invoke --help`)")
	}
	return nil
}

// handleInvocationResponse dispatches the response from a POST /invocations call
// to the correct handler based on the HTTP status code and content type.
func handleInvocationResponse(
	ctx context.Context,
	resp *http.Response,
	endpoint string,
	bearerToken string,
	agentName string,
	timeout time.Duration,
) error {
	requestID := resp.Header.Get("apim-request-id")
	if requestID != "" {
		fmt.Printf("Trace ID: %s\n", requestID)
	}

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		requestURL := "/invocations"
		if resp.Request != nil && resp.Request.URL != nil {
			requestURL = resp.Request.URL.String()
		}
		return fmt.Errorf(
			"POST %s failed with HTTP %d: %s\n%s",
			requestURL, resp.StatusCode, resp.Status, string(respBody),
		)
	}

	if resp.StatusCode == http.StatusAccepted {
		return handleInvocationLRO(ctx, resp, endpoint, bearerToken, agentName, timeout)
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "text/event-stream") {
		return handleInvocationSSE(os.Stdout, resp.Body, agentName)
	}

	return handleInvocationSync(resp.Body, agentName)
}

// handleInvocationSync handles a synchronous (200 OK, immediate result) invocations response.
func handleInvocationSync(body io.Reader, agentName string) error {
	respBody, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Try to detect the recommended error envelope
	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err == nil {
		if errObj, ok := result["error"].(map[string]any); ok {
			msg, _ := errObj["message"].(string)
			errType, _ := errObj["type"].(string)
			code, _ := errObj["code"].(string)
			label := code
			if label == "" {
				label = errType
			}
			if label != "" {
				return fmt.Errorf("agent error (%s): %s", label, msg)
			}
			return fmt.Errorf("agent error: %s", msg)
		}
	}

	// Print response — try pretty JSON, fall back to raw text
	if json.Valid(respBody) {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, respBody, "", "  "); err == nil {
			fmt.Printf("[%s] %s\n", agentName, pretty.String())
			return nil
		}
	}

	fmt.Printf("[%s] %s\n", agentName, string(respBody))
	return nil
}

// handleInvocationSSE handles a streaming (200 OK, text/event-stream) invocations response.
// The invocations protocol has a developer-defined SSE format, so we print data lines as they arrive.
func handleInvocationSSE(w io.Writer, body io.Reader, agentName string) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var printed bool

	for scanner.Scan() {
		line := scanner.Text()

		if data, ok := strings.CutPrefix(line, "data: "); ok {
			if data == "[DONE]" {
				break
			}

			// Try to detect error events
			var errEnvelope struct {
				Error struct {
					Message string `json:"message"`
					Type    string `json:"type"`
					Code    string `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(data), &errEnvelope) == nil && errEnvelope.Error.Message != "" {
				label := errEnvelope.Error.Code
				if label == "" {
					label = errEnvelope.Error.Type
				}
				if label != "" {
					return fmt.Errorf("agent error (%s): %s", label, errEnvelope.Error.Message)
				}
				return fmt.Errorf("agent error: %s", errEnvelope.Error.Message)
			}

			// Print data as-is, one line per SSE data object
			if !printed {
				fmt.Fprintf(w, "[%s] ", agentName)
				printed = true
			}
			fmt.Fprintln(w, data)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading response stream: %w", err)
	}

	return nil
}

var (
	defaultLROPollInterval = 2 * time.Second //nolint:revive // package-level var allows test overrides
	maxLROPollInterval     = 30 * time.Second
)

// handleInvocationLRO handles a long-running operation (202 Accepted) invocations response
// by polling GET on the invocation's status URL (derived from the original request URL)
// until a terminal state is reached.
func handleInvocationLRO(
	ctx context.Context,
	resp *http.Response,
	endpoint string,
	bearerToken string,
	agentName string,
	timeout time.Duration,
) error {
	// Read the 202 body once — used for both invocation ID extraction and status display.
	body202, _ := io.ReadAll(resp.Body)
	var bodyJSON map[string]any
	if len(body202) > 0 {
		_ = json.Unmarshal(body202, &bodyJSON) // best-effort; bodyJSON stays nil on failure
	}

	invocationID := resp.Header.Get("x-agent-invocation-id")
	if invocationID == "" && bodyJSON != nil {
		if id, ok := bodyJSON["invocation_id"].(string); ok {
			invocationID = id
		}
	}
	if invocationID == "" {
		return fmt.Errorf(
			"received 202 Accepted but no invocation ID found " +
				"(expected x-agent-invocation-id response header)",
		)
	}

	// Display initial 202 status if present
	if bodyJSON != nil {
		if status, _ := bodyJSON["status"].(string); status != "" {
			fmt.Printf("[%s] Invocation %s: %s\n", agentName, invocationID, status)
		}
	}

	// TODO: Async-with-callbacks (§5.4) is not yet supported. If the agent uses
	// a callback pattern, this polling loop will time out. Consider adding callback
	// support in a future iteration.

	fmt.Printf("[%s] Polling for result (invocation %s)...\n", agentName, invocationID)

	// Derive the poll URL from the original request URL so this works for both
	// local and remote agents. The original URL looks like .../invocations?...
	// and the poll URL inserts the invocation ID: .../invocations/{id}?...
	var pollURL string
	if resp.Request != nil && resp.Request.URL != nil {
		baseURL := resp.Request.URL.String()
		if i := strings.Index(baseURL, "/invocations?"); i >= 0 {
			pollURL = baseURL[:i] + "/invocations/" + invocationID + baseURL[i+len("/invocations"):]
		}
	}
	if pollURL == "" {
		pollURL = fmt.Sprintf(
			"%s/agents/%s/endpoint/protocols/invocations/%s?api-version=%s",
			endpoint, agentName, invocationID, DefaultAgentAPIVersion,
		)
	}

	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}

	pollInterval := defaultLROPollInterval

	for {
		if !deadline.IsZero() && time.Now().After(deadline) {
			return fmt.Errorf(
				"timed out waiting for invocation %s to complete (timeout: %s)",
				invocationID, timeout,
			)
		}

		time.Sleep(pollInterval)

		req, err := http.NewRequestWithContext(
			ctx, http.MethodGet, pollURL, nil,
		)
		if err != nil {
			return fmt.Errorf("failed to create poll request: %w", err)
		}
		if bearerToken != "" {
			req.Header.Set("Authorization", "Bearer "+bearerToken)
		}

		client := &http.Client{Timeout: 30 * time.Second}
		pollResp, err := client.Do(req) //nolint:gosec // G704: endpoint from azd environment
		if err != nil {
			return fmt.Errorf("GET %s failed: %w", pollURL, err)
		}

		pollBody, _ := io.ReadAll(pollResp.Body)
		_ = pollResp.Body.Close()

		if pollResp.StatusCode == http.StatusNotFound {
			continue // invocation not yet registered
		}

		if pollResp.StatusCode >= 400 {
			return fmt.Errorf(
				"GET %s failed with HTTP %d: %s\n%s",
				pollURL, pollResp.StatusCode, pollResp.Status, string(pollBody),
			)
		}

		var result map[string]any
		if err := json.Unmarshal(pollBody, &result); err == nil {
			status, _ := result["status"].(string)
			switch status {
			case "completed":
				fmt.Printf("[%s] Invocation completed.\n", agentName)
				// Pretty-print the result
				if json.Valid(pollBody) {
					var pretty bytes.Buffer
					if err := json.Indent(&pretty, pollBody, "", "  "); err == nil {
						fmt.Println(pretty.String())
						return nil
					}
				}
				fmt.Println(string(pollBody))
				return nil
			case "failed":
				if errObj, ok := result["error"].(map[string]any); ok {
					msg, _ := errObj["message"].(string)
					code, _ := errObj["code"].(string)
					return fmt.Errorf("invocation failed (%s): %s", code, msg)
				}
				return fmt.Errorf("invocation failed: %s", string(pollBody))
			case "cancelled":
				return fmt.Errorf("invocation was cancelled")
			}
		}

		// Respect Retry-After header from the poll response
		if ra := pollResp.Header.Get("Retry-After"); ra != "" {
			if seconds, err := strconv.Atoi(ra); err == nil && seconds > 0 {
				pollInterval = min(time.Duration(seconds)*time.Second, maxLROPollInterval)
			}
		}
	}
}

// createConversation creates a new Foundry conversation for multi-turn memory.
func createConversation(ctx context.Context, projectEndpoint, agentName, bearerToken string) (string, error) {
	convURL := fmt.Sprintf(
		"%s/agents/%s/endpoint/protocols/openai/conversations?api-version=%s",
		projectEndpoint, agentName, ConversationsAPIVersion,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, convURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearerToken)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req) //nolint:gosec // G704: endpoint is resolved from azd environment configuration
	if err != nil {
		return "", fmt.Errorf("POST %s failed: %w", convURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("POST %s failed with HTTP %d: %s\n%s", convURL, resp.StatusCode, resp.Status, string(respBody))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	if id, ok := result["id"].(string); ok {
		return id, nil
	}
	return "", fmt.Errorf("conversation response missing 'id' field")
}

// readSSEStream reads a Server-Sent Events stream from the Foundry Responses API,
// printing text deltas in real-time and returning the final response or any error.
func readSSEStream(body io.Reader, agentName string) error {
	scanner := bufio.NewScanner(body)
	// Allow large SSE data lines (up to 1 MB)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var currentEvent string
	var printed bool

	for scanner.Scan() {
		line := scanner.Text()

		if after, ok := strings.CutPrefix(line, "event: "); ok {
			currentEvent = after
			continue
		}

		if data, ok := strings.CutPrefix(line, "data: "); ok {
			switch currentEvent {
			case "response.output_text.delta":
				var delta struct {
					Delta string `json:"delta"`
				}
				if err := json.Unmarshal([]byte(data), &delta); err == nil && delta.Delta != "" {
					if !printed {
						fmt.Printf("[%s] ", agentName)
						printed = true
					}
					fmt.Print(delta.Delta)
				}

			case "response.completed":
				if printed {
					fmt.Println()
				}
				// Parse the completed response to check for errors
				var event struct {
					Response json.RawMessage `json:"response"`
				}
				if err := json.Unmarshal([]byte(data), &event); err == nil && event.Response != nil {
					var result map[string]any
					if err := json.Unmarshal(event.Response, &result); err == nil {
						if status, _ := result["status"].(string); status == "failed" {
							if errObj, ok := result["error"].(map[string]any); ok {
								msg, _ := errObj["message"].(string)
								code, _ := errObj["code"].(string)
								return fmt.Errorf("agent failed (%s): %s", code, msg)
							}
							return fmt.Errorf("agent returned failed status")
						}
						// If no text was streamed, extract output from the completed response
						if !printed {
							return printAgentResponse(result, agentName)
						}
					}
				}
				return nil

			case "error":
				if printed {
					fmt.Println()
				}
				var sseErr struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				}
				if err := json.Unmarshal([]byte(data), &sseErr); err == nil {
					return fmt.Errorf("agent error (%s): %s", sseErr.Code, sseErr.Message)
				}
				return fmt.Errorf("agent stream error: %s", data)
			}

			currentEvent = ""
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading response stream: %w", err)
	}

	if printed {
		fmt.Println()
	}
	return nil
}

// printAgentResponse pretty-prints the output_text items from an agent response.
func printAgentResponse(result map[string]any, title string) error {
	// Check for agent-level errors (e.g., agent runtime failures)
	if status, _ := result["status"].(string); status == "failed" {
		if errObj, ok := result["error"].(map[string]any); ok {
			msg, _ := errObj["message"].(string)
			code, _ := errObj["code"].(string)
			return fmt.Errorf("agent failed (%s): %s", code, msg)
		}
		return fmt.Errorf("agent returned failed status")
	}

	// Check for server-level errors (e.g., local agentserver: {"code": "server_error", "message": "..."})
	if code, ok := result["code"].(string); ok && code != "" {
		msg, _ := result["message"].(string)
		return fmt.Errorf("agent error (%s): %s", code, msg)
	}

	outputItems, ok := result["output"].([]any)
	if !ok {
		// Try printing the whole response as formatted JSON
		jsonBytes, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonBytes))
		return nil
	}

	printed := false
	for _, item := range outputItems {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		contentItems, ok := itemMap["content"].([]any)
		if !ok {
			continue
		}
		for _, content := range contentItems {
			contentMap, ok := content.(map[string]any)
			if !ok {
				continue
			}
			if contentMap["type"] == "output_text" {
				if text, ok := contentMap["text"].(string); ok {
					fmt.Printf("[%s] %s\n", title, text)
					printed = true
				}
			}
		}
	}

	if !printed {
		jsonBytes, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonBytes))
	}
	return nil
}
