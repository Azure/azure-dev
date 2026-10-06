// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package failuretext turns what a service said about a failure into text that
// is safe to print on one line: URL credentials redacted, whitespace collapsed,
// length bounded. It is the one place a failure's reason and its detail messages
// are shaped, so a run, a job and a refused request read the same way.
package failuretext

import (
	"slices"
	"strings"

	"azureaieval/internal/urlsafe"
)

// MaxRunes bounds one reason so a service body pasted into an error cannot fill
// a terminal or a pipeline log.
const MaxRunes = 300

// Detail is one finding behind a failure's headline: what the service found
// wrong, and the part of the request it was about.
type Detail struct {
	Code    string
	Message string
	Target  string
}

// Text makes a service-supplied reason safe to print on one line.
func Text(text string) string {
	collapsed := strings.Join(strings.Fields(urlsafe.Text(text)), " ")
	runes := []rune(collapsed)
	if len(runes) <= MaxRunes {
		return collapsed
	}
	return string(runes[:MaxRunes]) + "..."
}

// Lines are the detail messages worth printing under a headline.
//
// The headline says that something was rejected; the details say what, and are
// the only place that names a missing model, field or value. Each is redacted
// and bounded like the headline. One that sits inside the headline adds nothing,
// unless it names a target the headline does not, and one that reads exactly
// like an earlier detail is a repeat; containment between details would lose
// "gpt-4o" after "gpt-4o-mini". What does not fit under limit is counted rather
// than silently lost.
func Lines(headline string, details []Detail, limit int) (lines []string, more int) {
	head := key(Text(headline))
	var seen []string
	for _, detail := range details {
		text, target := render(detail)
		if text == "" {
			continue
		}
		k := key(text)
		if slices.Contains(seen, k) || target == "" && strings.Contains(head, k) {
			continue
		}
		seen = append(seen, k)
		if len(lines) == limit {
			more++
			continue
		}
		lines = append(lines, text)
	}
	return lines, more
}

// render is what a detail prints: its message, else its code, followed by the part
// of the request it was about. It also returns the target on its own.
func render(detail Detail) (text, target string) {
	text = Text(detail.Message)
	if text == "" {
		text = Text(detail.Code)
	}
	if text == "" {
		return "", ""
	}
	target = Text(detail.Target)
	if target != "" {
		text += " (target: " + target + ")"
	}
	return text, target
}

// Key identifies a detail by what it would print, so entries that read the same
// are one finding whatever their case, spacing or trailing punctuation. Callers
// that bound how many details they keep use it to drop repeats first, so a run of
// repeats cannot crowd out the entry that names the cause.
func Key(detail Detail) string {
	text, _ := render(detail)
	return key(text)
}

// key is the text two reasons are compared on: the same words in any case,
// spacing or trailing punctuation are one reason.
func key(text string) string {
	return strings.TrimRight(strings.ToLower(strings.Join(strings.Fields(text), " ")), ".")
}
