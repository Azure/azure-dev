// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A lookback beside an end_time used to be measured from now, which made the
// same file valid today and invalid tomorrow with nothing edited: once now
// minus the lookback drifted past the end, the window was empty for good.
// Measuring back from where the window closes takes the clock out of it.
func TestValidateSource_LookbackMeasuresBackFromTheEnd(t *testing.T) {
	start, end, err := ValidateSource(&SourceDecl{
		Type:          SourceTypeTraces,
		AgentName:     "a",
		LookbackHours: 24,
		EndTime:       "2020-01-01T00:00:00Z",
	})

	require.NoError(t, err)
	assert.Equal(t, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), end.UTC())
	assert.Equal(t, time.Date(2019, 12, 31, 0, 0, 0, 0, time.UTC), start.UTC())
	assert.True(t, end.After(start))
}

// With nothing closing the window, the lookback measures back from now.
func TestValidateSource_LookbackWithNoEndMeasuresBackFromNow(t *testing.T) {
	start, end, err := ValidateSource(&SourceDecl{
		Type: SourceTypeTraces, AgentName: "a", LookbackHours: 24,
	})

	require.NoError(t, err)
	assert.InDelta(t, time.Now().Add(-24*time.Hour).Unix(), start.Unix(), 60)
	assert.True(t, end.IsZero(), "an open end means up to now")
}

// A source with no window at all is not an error: both ends open is what an
// eval that never mentioned a window means.
func TestValidateSource_OpenWindowIsFine(t *testing.T) {
	start, end, err := ValidateSource(&SourceDecl{Type: SourceTypeTraces, AgentName: "a"})

	require.NoError(t, err)
	assert.True(t, start.IsZero())
	assert.True(t, end.IsZero())

	start, end, err = ValidateSource(nil)
	require.NoError(t, err)
	assert.True(t, start.IsZero())
	assert.True(t, end.IsZero())
}

// One end bounded and the other open is a window, not an error: "everything
// since" and "everything up to" are both things an eval can mean.
func TestValidateSource_OneEndOpenIsAWindow(t *testing.T) {
	start, end, err := ValidateSource(&SourceDecl{StartTime: "2026-08-01T00:00:00Z"})
	require.NoError(t, err)
	assert.Equal(t, int64(1785542400), start.Unix())
	assert.True(t, end.IsZero())

	start, end, err = ValidateSource(&SourceDecl{EndTime: "2026-08-02T00:00:00Z"})
	require.NoError(t, err)
	assert.True(t, start.IsZero())
	assert.Equal(t, int64(1785628800), end.Unix())
}

// A lookback long enough to reach past the epoch lands on a start the wire
// drops, which is the same silence a written bound at the epoch is refused for.
// The bound is arrived at differently and has to be held to the same rule.
func TestValidateSource_RefusesALookbackPastTheEpoch(t *testing.T) {
	_, _, err := ValidateSource(&SourceDecl{
		EndTime: "1970-01-01T01:00:00Z", LookbackHours: 1,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "before any trace was recorded")
}

// A file wrong in two ways names the value that cannot be read at all, rather
// than a pair it also got wrong: fixing the pair would leave the unreadable
// value in place and send the reader round again.
func TestValidateSource_ReportsTheUnreadableValueFirst(t *testing.T) {
	_, _, err := ValidateSource(&SourceDecl{StartTime: "yesterday", LookbackHours: -1})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "which is not a time")
	assert.NotContains(t, err.Error(), "lookback_hours")
}

// Fields the declared type never reads are refused rather than ignored: a
// lookback under a responses source looks like it bounds the run and never has.
func TestValidateSource_RefusesFieldsTheTypeDoesNotRead(t *testing.T) {
	_, _, err := ValidateSource(&SourceDecl{
		Type: SourceTypeResponses, ResponseIDs: []string{"resp_1"},
		LookbackHours: 24, AgentName: "a",
	})
	require.Error(t, err)
	// Named, because a reader with several set should not have to bisect.
	assert.Contains(t, err.Error(), "lookback_hours, agent_name")

	_, _, err = ValidateSource(&SourceDecl{
		Type: SourceTypeTraces, AgentName: "a", MaxTurns: 3,
	})
	require.Error(t, err)
	// One field reads "remove it", not "remove them".
	assert.Contains(t, err.Error(), "source declares max_turns")
	assert.Contains(t, err.Error(), "remove it")

	// max_traces is refused for its sign wherever it appears; max_turns is the
	// same kind of value and was going out unchecked.
	_, _, err = ValidateSource(&SourceDecl{
		Type: SourceTypeResponses, ResponseIDs: []string{"resp_1"}, MaxTurns: -3,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source.max_turns is -3")
}

// Every rule, at the boundary rather than well past it, so a bound that is
// moved by one still fails.
func TestValidateSource_Refuses(t *testing.T) {
	cases := []struct {
		name    string
		source  SourceDecl
		wantErr string
	}{
		{
			name:    "start that is not a time",
			source:  SourceDecl{StartTime: "yesterday"},
			wantErr: "source.start_time is \"yesterday\", which is not a time",
		},
		{
			name:    "end that is not a time",
			source:  SourceDecl{EndTime: "tomorrow"},
			wantErr: "source.end_time is \"tomorrow\", which is not a time",
		},
		{
			name:    "start at year one",
			source:  SourceDecl{StartTime: "0001-01-01T00:00:00Z"},
			wantErr: "not a time any traces were recorded at",
		},
		{
			// Parses, is not Go's zero time, and still serializes to a unix
			// zero that omitempty drops from the request.
			name:    "start at the unix epoch",
			source:  SourceDecl{StartTime: "1970-01-01T00:00:00Z"},
			wantErr: "not a time any traces were recorded at",
		},
		{
			name:    "negative lookback",
			source:  SourceDecl{LookbackHours: -1},
			wantErr: "how far back to look cannot be negative",
		},
		{
			name:    "lookback one past the bound",
			source:  SourceDecl{LookbackHours: MaxLookbackHours + 1},
			wantErr: "beyond the 87600 hours",
		},
		{
			name:    "negative cap",
			source:  SourceDecl{MaxTraces: -1},
			wantErr: "source.max_traces is -1",
		},
		{
			name:    "window declared twice over",
			source:  SourceDecl{StartTime: "2026-08-01T00:00:00Z", LookbackHours: 1},
			wantErr: "keep one",
		},
		{
			name:    "end before start",
			source:  SourceDecl{StartTime: "2026-08-02T00:00:00Z", EndTime: "2026-08-01T00:00:00Z"},
			wantErr: "holds no traces",
		},
		{
			// An instant is not a window, and a run over it reads nothing.
			name:    "end equal to start",
			source:  SourceDecl{StartTime: "2026-08-01T00:00:00Z", EndTime: "2026-08-01T00:00:00Z"},
			wantErr: "holds no traces",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ValidateSource(&tc.source)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// The bound is exactly on the line, so the check is `>` and not `>=`. Asserted
// through the resolver rather than against the constant: a comparison of the
// constant with its own definition cannot fail, and the multiplication that
// would overflow is constant-folded, so an overflowing value would stop the
// package compiling rather than fail a test.
func TestValidateSource_AcceptsTheLargestLookbackAllowed(t *testing.T) {
	start, _, err := ValidateSource(&SourceDecl{LookbackHours: MaxLookbackHours})

	require.NoError(t, err)
	assert.True(t, start.Before(time.Now()), "the window has to open in the past")
	assert.True(t, start.After(time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)),
		"an overflowed duration lands centuries away, not ten years")
}
