// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package e2elive contains the Tier 2 live golden-path end-to-end test for the
// azure.ai.agents extension: init -> provision -> deploy -> invoke -> down,
// driven against real Azure resources. See README.md for setup and how to run.
package e2elive

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"unicode"

	"gopkg.in/yaml.v3"
)

// spelledFourRe matches the spelled-out word "four" as a standalone word
// (case-insensitive), e.g. "the answer is four".
var spelledFourRe = regexp.MustCompile(`(?i)\bfour\b`)

// agentLineRe matches the start of an agent reply line, which invoke prints as
// "[<agentName>] <text>" (invoke.go printf "[%s] %s"). responseEndRe matches the
// timing footer. Accept the previous footer too when testing older releases.
var (
	agentLineRe   = regexp.MustCompile(`(?m)^\[[^\]]+\] `)
	responseEndRe = regexp.MustCompile(`(?m)^(?:Client elapsed:|Server responded in)`)
)

// agentResponseRegion returns just the agent's printed answer, sliced from the
// first "[<agent>] " line to the client timing footer. Scoping the
// 2+2 check to this region keeps stray "4"s from the rest of the CLI output
// (model names, versions, status codes) from passing the test. If either marker
// is missing the format changed, so it returns the full text and lets the
// standalone-digit rules below guard against false positives.
func agentResponseRegion(out string) string {
	start := agentLineRe.FindStringIndex(out)
	if start == nil {
		return out
	}
	rest := out[start[0]:]
	if end := responseEndRe.FindStringIndex(rest); end != nil {
		return rest[:end[0]]
	}
	return out
}

// responseHasExpectedAnswer reports whether text answers "what is 2+2?" with a
// standalone "4" or the spelled-out word "four".
//
// A live model may answer either, and the captured CLI output also contains
// unrelated digits — model names ("gpt-4o-mini"), versions ("4.1"), or status
// codes ("404") — so a bare substring search would produce false positives.
// The "4" must therefore stand alone: not part of a larger word or number.
// The standalone-"4" rule is the lookaround (?<![\w.])4(?!\.\d)(?!\w); the
// spelled-out "four" is matched case-insensitively as a whole word.
//
// A decimal such as "4.0" is deliberately rejected too: although 4.0 == 4
// mathematically, the "4.<digit>" form is treated as a version/decimal token to
// keep "4.1"-style strings out, and a live model answering "2+2" replies "4" or
// "four", never "4.0".
//
// Go's regexp engine (RE2) has no lookahead/lookbehind, so the standalone-"4"
// rule is implemented by scanning runes instead of with a single expression.
func responseHasExpectedAnswer(text string) bool {
	if spelledFourRe.MatchString(text) {
		return true
	}
	return hasStandaloneFour(text)
}

// hasStandaloneFour reports whether text contains a "4" digit that stands alone,
// reproducing the lookaround in the Python regex (?<![\w.])4(?!\.\d)(?!\w):
//   - not preceded by a word rune or '.'  (rejects "x4", "_4", ".4")
//   - not followed by '.' then a digit    (rejects "4.1", "4.0")
//   - not followed by a word rune         (rejects "40", "4o")
func hasStandaloneFour(text string) bool {
	runes := []rune(text)
	for i, r := range runes {
		if r != '4' {
			continue
		}
		if i > 0 {
			if prev := runes[i-1]; prev == '.' || isWordRune(prev) {
				continue
			}
		}
		if i+2 < len(runes) && runes[i+1] == '.' && unicode.IsDigit(runes[i+2]) {
			continue
		}
		if i+1 < len(runes) && isWordRune(runes[i+1]) {
			continue
		}
		return true
	}
	return false
}

// isWordRune reports whether r is a word character, matching the Python regex
// \w class (Unicode letters, digits, and underscore).
func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func enableSampleMemorySnapshot(manifest []byte) ([]byte, error) {
	var config map[string]any
	if err := yaml.Unmarshal(manifest, &config); err != nil {
		return nil, fmt.Errorf("parse sample manifest: %w", err)
	}
	services, ok := config["services"].(map[string]any)
	if !ok {
		return nil, errors.New("sample manifest must contain services")
	}
	var agentService map[string]any
	for _, value := range services {
		service, ok := value.(map[string]any)
		if !ok || service["host"] != "azure.ai.agent" {
			continue
		}
		if agentService != nil {
			return nil, errors.New("snapshot sample validation requires exactly one agent service")
		}
		if kind, exists := service["kind"]; exists && kind != "hosted" {
			return nil, errors.New("snapshot sample validation requires a hosted agent")
		}
		agentService = service
	}
	if agentService == nil {
		return nil, errors.New("sample manifest has no azure.ai.agent service")
	}
	agentService["enableMemorySnapshot"] = true
	return yaml.Marshal(config)
}

func validateSnapshotAgentVersion(data []byte) error {
	var version struct {
		Status     string `json:"status"`
		Definition struct {
			Kind                 string `json:"kind"`
			EnableMemorySnapshot *bool  `json:"enable_memory_snapshot"`
		} `json:"definition"`
	}
	if err := json.Unmarshal(data, &version); err != nil {
		return fmt.Errorf("parse deployed agent version: %w", err)
	}
	if version.Status != "active" {
		return fmt.Errorf("snapshot agent version is not active: %q", version.Status)
	}
	if version.Definition.Kind != "hosted" || version.Definition.EnableMemorySnapshot == nil ||
		!(*version.Definition.EnableMemorySnapshot) {
		return errors.New("service did not confirm definition.enable_memory_snapshot=true for the deployed hosted agent")
	}
	return nil
}
