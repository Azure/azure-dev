// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
)

const invokeLatencyHeaderPrefix = "x-ms-debug-latency-"

var invokeLatencyFields = [...]struct {
	suffix       string
	label        string
	platformOnly bool
}{
	{"platform-preprocessing-ms", "preprocess", true},
	{"infra-setup-ms", "infra", true},
	{"container-readiness-ms", "readiness", true},
	{"container-response-ms", "container", false},
	{"response-begin-ms", "response headers", false},
}

// A nil collector represents disabled diagnostics. It never reads the response body
// or waits for trailers, so enabling diagnostics does not change streaming behavior.
type invokeLatency struct {
	headers      http.Header
	platformOnly bool
}

func (a *InvokeAction) validateDebugLatencyRoute(protocol agent_api.AgentProtocol, isPrompt bool) error {
	if !a.debugLatencyExplicit || !a.flags.debugLatency {
		return nil
	}
	if !a.flags.local && !isPrompt &&
		(protocol == agent_api.AgentProtocolResponses || protocol == agent_api.AgentProtocolInvocations) {
		return nil
	}
	return exterrors.Validation(
		exterrors.CodeConflictingArguments,
		"--debug-latency=true requires a remote Hosted Agent using responses or invocations",
		"omit --debug-latency or use --debug-latency=false for local, A2A, or prompt agents",
	)
}

func newInvokeLatency(req *http.Request, enabled, platformOnly bool) *invokeLatency {
	if !enabled {
		return nil
	}
	req.Header.Set(invokeLatencyHeaderPrefix+"enabled", "true")
	return &invokeLatency{headers: make(http.Header), platformOnly: platformOnly}
}

func (l *invokeLatency) captureResponse(resp *http.Response) {
	if l == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return
	}
	l.platformOnly = l.platformOnly || resp.StatusCode == http.StatusAccepted
	l.captureHeader(resp.Header, "session-start-type")
	for _, field := range invokeLatencyFields {
		l.captureHeader(resp.Header, field.suffix)
	}
}

func (l *invokeLatency) captureHeader(headers http.Header, suffix string) {
	name := invokeLatencyHeaderPrefix + suffix
	if values := headers.Values(name); len(values) > 0 {
		// Existing LRO GETs can return the original POST's persisted metrics later.
		// An earlier GET with no stored metrics must not erase the POST snapshot.
		l.headers[http.CanonicalHeaderKey(name)] = slices.Clone(values)
	}
}

func (l *invokeLatency) writeTo(w io.Writer) error {
	if l == nil {
		return nil
	}

	startType := "unknown"
	var invalid []string
	if values := l.headers.Values(invokeLatencyHeaderPrefix + "session-start-type"); len(values) > 0 {
		if len(values) == 1 {
			switch values[0] {
			case "cold", "warm", "resume":
				startType = values[0]
			default:
				invalid = append(invalid, "session-start-type")
			}
		} else {
			invalid = append(invalid, "session-start-type")
		}
	}

	var stages []string
	var responseBegin *int64
	for _, field := range invokeLatencyFields {
		if l.platformOnly && !field.platformOnly {
			continue
		}
		values := l.headers.Values(invokeLatencyHeaderPrefix + field.suffix)
		if len(values) == 0 {
			continue
		}
		if len(values) != 1 {
			invalid = append(invalid, field.suffix)
			continue
		}
		ms, err := strconv.ParseInt(strings.TrimSpace(values[0]), 10, 64)
		if err != nil || ms < 0 {
			invalid = append(invalid, field.suffix)
			continue
		}
		if field.suffix == "response-begin-ms" {
			responseBegin = new(ms)
		} else {
			stages = append(stages, fmt.Sprintf("%s %d ms", field.label, ms))
		}
	}

	var summary strings.Builder
	if startType == "unknown" && len(stages) == 0 && responseBegin == nil {
		if len(invalid) == 0 {
			summary.WriteString("Platform latency: not returned by the service.\n")
		} else {
			summary.WriteString("Platform latency: unavailable.\n")
		}
	} else {
		if l.platformOnly {
			fmt.Fprintf(&summary, "Platform latency (%s, async; platform overhead only)\n", startType)
		} else if responseBegin != nil {
			fmt.Fprintf(&summary, "Platform latency (%s): response headers %d ms\n", startType, *responseBegin)
		} else {
			fmt.Fprintf(&summary, "Platform latency (%s)\n", startType)
		}
		if len(stages) > 0 {
			fmt.Fprintf(&summary, "  %s\n", strings.Join(stages, " | "))
		}
	}
	if len(invalid) > 0 {
		fmt.Fprintf(&summary, "WARNING: Ignored invalid platform latency fields: %s.\n", strings.Join(invalid, ", "))
	}
	_, err := io.WriteString(w, summary.String())
	return err
}
