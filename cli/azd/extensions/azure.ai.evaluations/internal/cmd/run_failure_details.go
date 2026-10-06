// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
)

const (
	// maxFailureDetails bounds how many detail messages a multi-line view prints.
	maxFailureDetails = 5
	// maxFailureDetailsInline is the smaller bound for the single line a pipeline
	// logs, which is read in full where the multi-line view is scrolled.
	maxFailureDetailsInline = 3
)

// failureDetails are the specific things the service found wrong, for an error
// whose own message only summarizes them.
//
// A top-level message such as "Evaluation validation failed" says that
// something was rejected; the details array says what, and is the only place
// that names the missing model, field or value. They are read when the error
// has a message of its own. An error with no message is already explained by
// its first nested reason, which is what the headline prints, so repeating the
// details would only echo it.
//
// Every entry is redacted and bounded like the headline, one that restates the
// headline or repeats an earlier entry is dropped, and what does not fit under
// limit is counted rather than silently lost.
func failureDetails(failure *eval_api.JobError, limit int) (lines []string, more int) {
	if failure == nil || strings.TrimSpace(failure.Message) == "" {
		return nil, 0
	}
	headline := failureKey(failureText(failure.Message))
	var seen []string
	for _, detail := range failure.Details() {
		text := failureText(detail.Message)
		if text == "" {
			text = failureText(detail.Code)
		}
		if text == "" {
			continue
		}
		target := failureText(detail.Target)
		if target != "" {
			text = messages.FailureDetailWithTarget(text, target)
		}
		// A detail that sits inside the headline adds nothing, unless it names a
		// target the headline does not. An earlier entry is a repeat only when
		// it reads exactly the same: containment would lose "gpt-4o" after
		// "gpt-4o-mini".
		key := failureKey(text)
		if slices.Contains(seen, key) || target == "" && strings.Contains(headline, key) {
			continue
		}
		seen = append(seen, key)
		if len(lines) == limit {
			more++
			continue
		}
		lines = append(lines, text)
	}
	return lines, more
}

// failureKey is the text two reasons are compared on: the same words in any
// case, spacing or trailing punctuation are one reason.
func failureKey(text string) string {
	return strings.TrimRight(strings.ToLower(strings.Join(strings.Fields(text), " ")), ".")
}

// renderFailureDetails prints the failure's detail messages under its headline.
func renderFailureDetails(out io.Writer, failure *eval_api.JobError) {
	lines, more := failureDetails(failure, maxFailureDetails)
	for _, line := range lines {
		fmt.Fprint(out, messages.FailureDetail(line))
	}
	if more > 0 {
		fmt.Fprint(out, messages.FailureDetailsMore(more))
	}
}

// failureReasonLine is the headline with its detail messages appended on the
// same line, for the error a pipeline logs.
func failureReasonLine(headline string, failure *eval_api.JobError) string {
	lines, more := failureDetails(failure, maxFailureDetailsInline)
	return messages.FailureReasonWithDetails(headline, lines, more)
}
