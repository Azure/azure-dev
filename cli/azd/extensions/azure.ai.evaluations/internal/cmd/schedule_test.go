// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildTrigger_Cron(t *testing.T) {
	got, err := buildTrigger(triggerFlags{cron: "0 9 * * *"})
	require.NoError(t, err)
	assert.Equal(t, eval_api.TriggerCron, got.Type)
	assert.Equal(t, "0 9 * * *", got.Expression)
	assert.Equal(t, "UTC", got.Timezone, "UTC unless the caller says otherwise")
}

// A schedule repeats the group's most recent run. Scheduling a group whose last
// run came from --from-traces therefore creates a trace evaluation, and the
// service allows only an hourly trigger for those. Confirmed live: a daily
// trigger was accepted after an agent run, refused after a traces run on the
// same group, and hourly was accepted for that same traces run.
func TestIsTracesHourlyOnly(t *testing.T) {
	assert.True(t, isTracesHourlyOnly(
		errors.New(`{"message": "Scheduled trace evaluations only support hourly recurrence triggers. is invalid"}`)))
	assert.False(t, isTracesHourlyOnly(errors.New("some other 400")))
	assert.False(t, isTracesHourlyOnly(nil))
}

func TestBuildTrigger_OneTime(t *testing.T) {
	got, err := buildTrigger(triggerFlags{atTime: "2026-08-01T09:00:00Z", timezone: "Europe/Dublin"})
	require.NoError(t, err)
	assert.Equal(t, eval_api.TriggerOneTime, got.Type)
	assert.Equal(t, "2026-08-01T09:00:00Z", got.ScheduledTime)
	assert.Equal(t, "Europe/Dublin", got.Timezone)

	_, err = buildTrigger(triggerFlags{atTime: "next tuesday"})
	require.ErrorContains(t, err, "RFC3339")
}

func TestBuildTrigger_Recurrence(t *testing.T) {
	cases := []struct {
		name     string
		flags    triggerFlags
		wantType string
		assert   func(*testing.T, *eval_api.RecurrencePattern)
	}{
		{
			name:     "hourly",
			flags:    triggerFlags{every: "hourly", interval: 6},
			wantType: eval_api.RecurrenceHourly,
		},
		{
			name:     "daily with hours",
			flags:    triggerFlags{every: "Daily", atHours: []int{9, 17}},
			wantType: eval_api.RecurrenceDaily,
			assert: func(t *testing.T, p *eval_api.RecurrencePattern) {
				assert.Equal(t, []int{9, 17}, p.Hours)
			},
		},
		{
			name:     "weekly normalizes day casing",
			flags:    triggerFlags{every: "weekly", onDays: []string{"monday", "THURSDAY"}},
			wantType: eval_api.RecurrenceWeekly,
			assert: func(t *testing.T, p *eval_api.RecurrencePattern) {
				assert.Equal(t, []string{"Monday", "Thursday"}, p.DaysOfWeek)
			},
		},
		{
			name:     "monthly",
			flags:    triggerFlags{every: "monthly", onDaysOfMon: []int{1, 15}},
			wantType: eval_api.RecurrenceMonthly,
			assert: func(t *testing.T, p *eval_api.RecurrencePattern) {
				assert.Equal(t, []int{1, 15}, p.DaysOfMonth)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildTrigger(tc.flags)
			require.NoError(t, err)
			assert.Equal(t, eval_api.TriggerRecurrence, got.Type)
			require.NotNil(t, got.Schedule)
			assert.Equal(t, tc.wantType, got.Schedule.Type)
			if tc.assert != nil {
				tc.assert(t, got.Schedule)
			}
		})
	}
}

// An interval is always sent, so the service never has to infer one.
func TestBuildTrigger_IntervalDefaultsToOne(t *testing.T) {
	got, err := buildTrigger(triggerFlags{every: "daily"})
	require.NoError(t, err)
	assert.Equal(t, 1, got.Interval)

	got, err = buildTrigger(triggerFlags{every: "daily", interval: 3})
	require.NoError(t, err)
	assert.Equal(t, 3, got.Interval)
}

// Each period reads only its own qualifier. Accepting one that does not apply
// would drop it silently, which is the failure mode the trace fields already
// taught us to avoid.
func TestBuildTrigger_RejectsQualifiersFromAnotherPeriod(t *testing.T) {
	cases := []struct {
		name  string
		flags triggerFlags
		want  string
	}{
		{"hours on weekly", triggerFlags{every: "weekly", atHours: []int{9}}, "--at applies to --every daily"},
		{"days on daily", triggerFlags{every: "daily", onDays: []string{"Monday"}}, "--on applies to --every weekly"},
		{"month days on hourly", triggerFlags{every: "hourly", onDaysOfMon: []int{1}}, "--on-day applies to --every monthly"},
		{"hours on monthly", triggerFlags{every: "monthly", atHours: []int{9}}, "--at applies to --every daily"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildTrigger(tc.flags)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestBuildTrigger_RejectsOutOfRangeValues(t *testing.T) {
	_, err := buildTrigger(triggerFlags{every: "daily", atHours: []int{24}})
	require.ErrorContains(t, err, "hour of the day")

	_, err = buildTrigger(triggerFlags{every: "monthly", onDaysOfMon: []int{0}})
	require.ErrorContains(t, err, "day of the month")

	_, err = buildTrigger(triggerFlags{every: "weekly", onDays: []string{"Caturday"}})
	require.ErrorContains(t, err, "not a day of the week")

	_, err = buildTrigger(triggerFlags{every: "fortnightly"})
	require.ErrorContains(t, err, "hourly, daily, weekly or monthly")
}

func TestBuildTrigger_NeedsATrigger(t *testing.T) {
	_, err := buildTrigger(triggerFlags{})
	require.ErrorContains(t, err, "--cron, --every or --at-time")
}

func TestScheduleSummary(t *testing.T) {
	cron := &eval_api.Schedule{Trigger: &eval_api.ScheduleTrigger{
		Type: eval_api.TriggerCron, Expression: "0 9 * * *"}}
	assert.Equal(t, "cron 0 9 * * *", cron.Summary())

	weekly := &eval_api.Schedule{Trigger: &eval_api.ScheduleTrigger{
		Type:     eval_api.TriggerRecurrence,
		Schedule: &eval_api.RecurrencePattern{Type: eval_api.RecurrenceWeekly}}}
	assert.Equal(t, "every Weekly", weekly.Summary())

	once := &eval_api.Schedule{Trigger: &eval_api.ScheduleTrigger{
		Type: eval_api.TriggerOneTime, ScheduledTime: "2026-08-01T09:00:00Z"}}
	assert.Equal(t, "once at 2026-08-01T09:00:00Z", once.Summary())

	var nilSchedule *eval_api.Schedule
	assert.Empty(t, nilSchedule.Summary())
	assert.Empty(t, (&eval_api.Schedule{}).Summary())
}
