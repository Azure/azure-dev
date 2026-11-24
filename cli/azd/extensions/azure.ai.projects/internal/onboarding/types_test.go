// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportErrPreservesCauses(t *testing.T) {
	for _, cause := range []error{
		errors.New("authentication challenge failed"),
		errors.New("cannot decode resource response"),
		fmt.Errorf("request canceled: %w", context.Canceled),
		fmt.Errorf("request timed out: %w", context.DeadlineExceeded),
		errors.New("connection reset at https://cause-user:cause-password@example.test/resource" +
			"?sig=cause-secret#cause-fragment"),
	} {
		t.Run(cause.Error(), func(t *testing.T) {
			report := Report{{
				ID: "resource-refs", ResourceType: aksResourceType, Status: Unverified, Mandatory: true,
				FailureReasons: []string{"AKS cluster could not be read"}, Cause: cause,
			}}
			err := report.Err()
			require.ErrorIs(t, err, cause)
			blocked, ok := errors.AsType[*BlockedError](err)
			require.True(t, ok)
			require.Len(t, blocked.Checks, 1)
			assert.Same(t, cause, blocked.Checks[0].Cause)
			for _, secret := range []string{"cause-user", "cause-password", "sig=", "cause-secret", "cause-fragment"} {
				assert.NotContains(t, err.Error(), secret)
			}
			assert.Same(t, cause, report[0].Cause)
			assert.Equal(t, Unverified, report[0].Status)
			for _, cancellation := range []error{context.Canceled, context.DeadlineExceeded} {
				assert.Equal(t, errors.Is(cause, cancellation), errors.Is(err, cancellation))
			}
		})
	}
}

func TestBlockedErrorGroupsAndDeduplicatesOnlyBlockingReasons(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	report := Report{
		{ID: "agent-pool", ResourceType: aksResourceType, Status: Fail, Mandatory: true,
			FailureReasons: []string{"The selected User node pool is not Kata-enabled"}},
		{ID: "storage-type", ResourceType: storageResourceType, Status: Fail, Mandatory: true,
			FailureReasons: []string{"Hot storage access tier is required"}},
		{ID: "aks-federation", ResourceType: aksResourceType, Status: Fail, Mandatory: true,
			FailureReasons: []string{"Workload identity is not enabled", "The selected User node pool is not Kata-enabled"}},
		{ID: "agent-label", ResourceType: aksResourceType, Status: Unverified, Mandatory: true,
			FailureReasons: []string{"The selected User node pool is not Kata-enabled"}},
		{ResourceType: aksResourceType, Status: Pass, Mandatory: true, FailureReasons: []string{"passed check"}},
		{ResourceType: networkResourceType, Status: NotApplicable, Mandatory: true,
			FailureReasons: []string{"not applicable"}},
		{ResourceType: hostingIdentityResourceType, Status: Fail, FailureReasons: []string{"optional check"}},
		{ResourceType: subscriptionResourceType, Status: Skipped, FailureReasons: []string{"unresolved template"}},
	}
	err := report.Err()
	require.Error(t, err)
	assert.EqualError(t, err, "AKS does not meet requirements. Review the requirements ("+prerequisitesURL+
		") and edit your configuration before proceeding.\n"+
		"- The selected User node pool is not Kata-enabled\n- Workload identity is not enabled\n\n"+
		"Storage account does not meet requirements. Review the requirements ("+prerequisitesURL+
		") and edit your configuration before proceeding.\n- Hot storage access tier is required")
}

func TestReportMissingFailureReasonsStillBlocks(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, status := range []Status{Fail, Unverified, Skipped} {
		for _, reasons := range [][]string{nil, {"", " "}} {
			report := Report{{Status: status, Mandatory: true, FailureReasons: reasons}}
			err := report.Err()
			require.Error(t, err)
			assert.EqualError(t, err, "Resource configuration does not meet requirements. Review the requirements ("+
				prerequisitesURL+") and edit your configuration before proceeding.\n"+
				"- A mandatory prerequisite could not be verified")
		}
	}
}

func TestPrerequisitesLinkIsPlainText(t *testing.T) {
	for _, test := range []struct {
		name, forceColor, noColor, ci, term, windowsTerminal, program string
	}{
		{"Windows Terminal", "1", "", "", "xterm-256color", "session", ""},
		{"VS Code", "1", "", "", "xterm-256color", "", "vscode"},
		{"iTerm", "1", "", "", "xterm-256color", "", "iTerm.app"},
		{"piped host", "", "", "", "xterm-256color", "session", "vscode"},
		{"color alone", "1", "", "", "xterm-256color", "", ""},
		{"no color", "1", "1", "", "xterm-256color", "session", ""},
		{"CI log", "1", "", "true", "xterm-256color", "session", ""},
		{"dumb", "1", "", "", "dumb", "session", ""},
		{"screen", "1", "", "", "screen-256color", "", "iTerm.app"},
		{"tmux", "1", "", "", "tmux-256color", "", "WezTerm"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for key, value := range map[string]string{
				"FORCE_COLOR": test.forceColor, "NO_COLOR": test.noColor, "CI": test.ci,
				"TERM": test.term, "WT_SESSION": test.windowsTerminal, "TERM_PROGRAM": test.program,
			} {
				t.Setenv(key, value)
			}
			link := prerequisitesLink()
			assert.Equal(t, "requirements ("+prerequisitesURL+")", link)
			assert.NotContains(t, link, "\x1b")
		})
	}
}
