// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// parseAgentEndpoint parses a deployed agent endpoint URL of the form
//
//	<projectEndpoint>/agents/<agentName>/versions/<agentVersion>
//
// and returns the project endpoint and agent name. The version segment is
// accepted but not used by invocation requests.
//
// Both `/agents/<name>/versions/<v>` and `/agents/<name>` are accepted; a
// trailing slash is tolerated. Anything else after the agent name is rejected
// to surface typos early.
//
// Agent names are validated to contain only characters that are safe to use
// as a path segment without further encoding, since the parsed name is
// substituted into invocation URLs as-is.
func parseAgentEndpoint(raw string) (projectEndpoint, agentName string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("agent endpoint is empty")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("invalid agent endpoint URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", "", fmt.Errorf(
			"invalid agent endpoint URL %q: must include scheme and host", raw,
		)
	}
	if parsed.Scheme != "https" {
		return "", "", fmt.Errorf(
			"invalid agent endpoint URL %q: scheme must be https", raw,
		)
	}

	// Use the un-decoded path so percent-encoded segments are preserved when
	// re-substituting the agent name into invocation URLs downstream.
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if strings.HasSuffix(path, "/agents") {
		return "", "", fmt.Errorf(
			"invalid agent endpoint %q: agent name is empty", raw,
		)
	}
	idx := strings.LastIndex(path, "/agents/")
	if idx < 0 {
		return "", "", fmt.Errorf(
			"invalid agent endpoint %q: expected '/agents/<name>' in path", raw,
		)
	}

	projectPath := path[:idx]
	rest := strings.TrimPrefix(path[idx:], "/agents/")
	if rest == "" {
		return "", "", fmt.Errorf(
			"invalid agent endpoint %q: missing agent name after '/agents/'", raw,
		)
	}

	segments := strings.Split(rest, "/")
	agentName = segments[0]
	if agentName == "" {
		return "", "", fmt.Errorf(
			"invalid agent endpoint %q: agent name is empty", raw,
		)
	}
	if !isValidAgentNameSegment(agentName) {
		return "", "", fmt.Errorf(
			"invalid agent endpoint %q: agent name %q contains unsupported characters",
			raw, agentName,
		)
	}

	// Accept exactly `/agents/<name>` or `/agents/<name>/versions/<version>`;
	// reject anything longer to surface pasted-but-truncated URLs.
	switch len(segments) {
	case 1:
		// /agents/<name>
	case 3:
		if segments[1] != "versions" || segments[2] == "" {
			return "", "", fmt.Errorf(
				"invalid agent endpoint %q: expected '/agents/<name>' "+
					"or '/agents/<name>/versions/<version>'", raw,
			)
		}
	default:
		return "", "", fmt.Errorf(
			"invalid agent endpoint %q: expected '/agents/<name>' "+
				"or '/agents/<name>/versions/<version>'", raw,
		)
	}

	projectURL := *parsed
	if err := setURLEscapedPath(&projectURL, projectPath); err != nil {
		return "", "", fmt.Errorf("invalid agent endpoint %q: %w", raw, err)
	}
	projectURL.RawQuery = ""
	projectURL.Fragment = ""
	projectEndpoint = strings.TrimRight(projectURL.String(), "/")

	return projectEndpoint, agentName, nil
}

// isValidAgentNameSegment reports whether s is safe to substitute into a URL
// path segment without further escaping. Agent names produced by azd are
// alphanumeric plus '-' and '_'.
func isValidAgentNameSegment(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			// ok
		default:
			return false
		}
	}
	return true
}

// setURLEscapedPath assigns an already-escaped path to u, decoding it for
// u.Path so url.URL stays internally consistent.
func setURLEscapedPath(u *url.URL, escapedPath string) error {
	decoded, err := url.PathUnescape(escapedPath)
	if err != nil {
		return err
	}
	u.Path = decoded
	if escapedPath != decoded {
		u.RawPath = escapedPath
	} else {
		u.RawPath = ""
	}
	return nil
}

// printEphemeralSessionHint prints the server-assigned session ID (if any)
// when running in --agent-endpoint mode where azd has nowhere to persist it.
// This lets the user copy the ID into --session-id for subsequent invokes.
func printEphemeralSessionHint(existingSID string, resp *http.Response) {
	if existingSID != "" || resp == nil {
		return
	}
	newSID := resp.Header.Get("x-agent-session-id")
	if newSID == "" {
		return
	}
	fmt.Printf("Server session ID: %s\n", newSID)
	fmt.Printf("(Pass --session-id %s to reuse this session on the next invoke.)\n", newSID)
}
