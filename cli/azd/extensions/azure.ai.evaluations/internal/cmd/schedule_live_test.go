// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

package cmd

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/require"
)

// Schedules are project-scoped and the service tolerates very few of them, so
// everything here runs serially and deletes what it creates. Nothing calls
// t.Parallel().
//
// These tests exist because the unit tests only prove that buildTrigger
// produces a well-formed struct. Whether the service accepts that struct is a
// different question, and it is the one that matters: every trigger shape the
// CLI can emit is sent here and required to survive a round trip.

// liveScheduleEval creates a throwaway eval for a schedule to point at.
//
// The criterion comes from the shipping builder rather than a hand-written
// one. Built-ins do not share an input contract — builtin.ifeval, which is
// what the listing happens to return first, needs an instruction_id_list — so
// a hand-rolled mapping is rejected with MissingRequiredDataMapping. Letting
// production shape the request also means this helper cannot drift from it.
//
// No evaluator is named. Which built-ins a project exposes varies, so the
// first one the builder can satisfy is used, and the dataset is given every
// column that evaluator declares.
func liveScheduleEval(t *testing.T, client *eval_api.EvalClient, judge string) string {
	t.Helper()
	ctx := context.Background()

	ec := &evalContext{evalClient: client}
	schemas := ec.evaluatorSchemas(ctx)
	require.NotEmpty(t, schemas, "need the published evaluator contracts to build an eval")

	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		summary := schemas[name]
		columns := map[string]bool{"query": true}
		if ds := summary.DataSchema(); ds != nil {
			for _, col := range ds.PropertyNames() {
				columns[col] = true
			}
		}

		level := ""
		if len(summary.SupportedEvaluationLevels) > 0 {
			level = summary.SupportedEvaluationLevels[0]
		}

		req, err := buildEvalRequest(&project.Eval{
			Name:       fmt.Sprintf("azd-sched-%d", time.Now().UTC().UnixNano()),
			Dataset:    "inline",
			Target:     &project.Target{Type: "agent", Name: "probe-agent"},
			Evaluators: []evalcore.EvaluatorRef{{Name: summary.Name}},
			Options:    &project.Options{EvalModel: judge, EvaluationLevel: level},
		}, schemas, columns)
		if err != nil {
			continue
		}

		created, err := client.CreateOpenAIEval(ctx, req)
		if err != nil {
			t.Logf("built-in %s could not back a schedule: %v", name, err)
			continue
		}
		t.Cleanup(func() {
			_ = client.DeleteOpenAIEval(context.Background(), created.ID)
		})
		t.Logf("scheduling an eval built on %s (%s)", name, created.ID)
		return created.ID
	}

	t.Fatalf("no built-in produced an eval a schedule could run; tried %d", len(names))
	return ""
}

// putLiveSchedule creates a schedule and registers its removal.
func putLiveSchedule(
	t *testing.T,
	client *eval_api.EvalClient,
	name string,
	evalID string,
	trigger *eval_api.ScheduleTrigger,
) (*eval_api.Schedule, error) {
	t.Helper()

	saved, err := client.PutSchedule(context.Background(), name, &eval_api.Schedule{
		DisplayName: name,
		Enabled:     true,
		Trigger:     trigger,
		Task: &eval_api.ScheduleTask{
			Type:   eval_api.ScheduleTaskEvaluation,
			EvalID: evalID,
			EvalRun: &eval_api.CreateOpenAIEvalRunRequest{
				Name:       name,
				DataSource: datasetOnlyRows([]map[string]any{{"query": "how do I reset my password?"}}),
			},
		},
	}, ProjectEndpointAPIVersion)

	if err == nil {
		t.Cleanup(func() { removeLiveSchedule(t, client, name) })
	}
	return saved, err
}

// skipIfScheduleRoleMissing stops the test when the project cannot host a
// schedule at all.
//
// A schedule fires later and runs as the project, so creating one requires the
// project's managed identity to hold the Foundry User role on the project —
// a permission no other command in this extension needs. Without it every
// schedule test fails identically and for a reason that has nothing to do with
// the code, so they skip loudly instead of reporting a false regression.
func skipIfScheduleRoleMissing(t *testing.T, err error) {
	t.Helper()
	if err == nil || !isScheduleRoleMissing(err) {
		return
	}
	t.Skipf("this project cannot host schedules: its managed identity lacks the "+
		"Foundry User role on the project. Grant it and re-run to exercise "+
		"schedules for real. Underlying error: %v", err)
}

// datasetOnlyRows is the inline data source a scheduled run repeats.
func datasetOnlyRows(rows []map[string]any) *eval_api.EvalRunDataSource {
	ds := eval_api.NewDatasetOnlyDataSource()
	ds.SetFileContent(rows)
	return ds
}

// removeLiveSchedule deletes a schedule once it is no longer provisioning.
//
// A schedule still being created refuses the delete, as a 409 while it is busy
// or a 404 because the trigger behind it does not exist yet. Waiting for it to
// settle is what makes cleanup reliable, and leaving one behind would break
// every later test in this file, because the project holds very few.
func removeLiveSchedule(t *testing.T, client *eval_api.EvalClient, name string) {
	t.Helper()
	ctx := context.Background()

	deadline := time.Now().Add(2 * time.Minute)
	for {
		current, err := client.GetSchedule(ctx, name, ProjectEndpointAPIVersion)
		if err != nil {
			return // already gone
		}
		if current.Settled() {
			break
		}
		if time.Now().After(deadline) {
			t.Logf("schedule %q never settled (status %q); leaving it", name, current.ProvisioningStatus)
			return
		}
		time.Sleep(3 * time.Second)
	}

	if err := client.DeleteSchedule(ctx, name, ProjectEndpointAPIVersion); err != nil {
		t.Logf("could not delete schedule %q: %v", name, err)
	}
}

// awaitScheduleSettled blocks until the schedule finishes provisioning.
func awaitScheduleSettled(
	t *testing.T,
	client *eval_api.EvalClient,
	name string,
) *eval_api.Schedule {
	t.Helper()
	ctx := context.Background()

	deadline := time.Now().Add(2 * time.Minute)
	for {
		current, err := client.GetSchedule(ctx, name, ProjectEndpointAPIVersion)
		require.NoError(t, err, "reading schedule %q back", name)
		if current.Settled() {
			return current
		}
		if time.Now().After(deadline) {
			t.Fatalf("schedule %q stuck in %q", name, current.ProvisioningStatus)
		}
		time.Sleep(3 * time.Second)
	}
}

func liveScheduleName(t *testing.T, suffix string) string {
	t.Helper()
	return fmt.Sprintf("azdsched-%s-%d", suffix, time.Now().UnixNano())
}

// TestLiveScheduleLifecycle walks create, read, list and delete.
func TestLiveScheduleLifecycle(t *testing.T) {
	client, judge := liveEvalClient(t)
	ctx := context.Background()

	evalID := liveScheduleEval(t, client, judge)
	name := liveScheduleName(t, "life")

	trigger, err := buildTrigger(triggerFlags{every: "daily", atHours: []int{9}})
	require.NoError(t, err)

	saved, err := putLiveSchedule(t, client, name, evalID, trigger)
	skipIfScheduleRoleMissing(t, err)
	require.NoError(t, err, "the service rejected a trigger the CLI can produce")
	require.NotEmpty(t, saved.ID)

	settled := awaitScheduleSettled(t, client, name)
	require.True(t, settled.Enabled, "a schedule created without --disabled must be enabled")
	require.NotNil(t, settled.Task, "the schedule must carry the task it runs")
	require.Equal(t, evalID, settled.Task.EvalID,
		"the schedule must point at the eval it was given")
	require.NotNil(t, settled.Trigger)
	require.Equal(t, eval_api.TriggerRecurrence, settled.Trigger.Type)

	list, err := client.ListSchedules(ctx, ProjectEndpointAPIVersion)
	require.NoError(t, err)
	found := false
	for i := range list.Value {
		if list.Value[i].ID == saved.ID || list.Value[i].DisplayName == name {
			found = true
			break
		}
	}
	require.True(t, found, "a created schedule must appear in the listing")

	removeLiveSchedule(t, client, name)

	_, err = client.GetSchedule(ctx, name, ProjectEndpointAPIVersion)
	require.Error(t, err, "a deleted schedule must not read back")
	require.True(t, eval_api.IsNotFound(err),
		"deleting should leave a not-found, got %v", err)
}

// TestLiveScheduleAcceptsEveryTriggerShape sends one schedule per trigger the
// CLI can build and requires the service to accept each.
//
// The unit tests assert the shape of what buildTrigger returns. They cannot
// say whether the service agrees, and a trigger the service rejects is a
// trigger the CLI should never have offered.
func TestLiveScheduleAcceptsEveryTriggerShape(t *testing.T) {
	client, judge := liveEvalClient(t)
	evalID := liveScheduleEval(t, client, judge)

	// Probe once up front. Skipping inside the subtests instead would leave the
	// parent reporting PASS with nothing proven, which is worse than a failure
	// because it looks like coverage.
	probe := liveScheduleName(t, "probe")
	daily, err := buildTrigger(triggerFlags{every: "daily", atHours: []int{4}})
	require.NoError(t, err)
	if _, probeErr := putLiveSchedule(t, client, probe, evalID, daily); probeErr != nil {
		skipIfScheduleRoleMissing(t, probeErr)
		require.NoError(t, probeErr, "could not create the probe schedule")
	}
	removeLiveSchedule(t, client, probe)

	cases := []struct {
		label string
		flags triggerFlags
		want  string
	}{
		{"cron", triggerFlags{cron: "0 9 * * *"}, eval_api.TriggerCron},
		{"hourly", triggerFlags{every: "hourly"}, eval_api.TriggerRecurrence},
		{"daily", triggerFlags{every: "daily", atHours: []int{9, 17}}, eval_api.TriggerRecurrence},
		{"weekly", triggerFlags{every: "weekly", onDays: []string{"Monday"}}, eval_api.TriggerRecurrence},
		{"monthly", triggerFlags{every: "monthly", onDaysOfMon: []int{1}}, eval_api.TriggerRecurrence},
		{"interval", triggerFlags{every: "daily", interval: 3}, eval_api.TriggerRecurrence},
		{
			"onetime",
			triggerFlags{atTime: time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)},
			eval_api.TriggerOneTime,
		},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			trigger, err := buildTrigger(tc.flags)
			require.NoError(t, err, "the CLI could not build a %s trigger", tc.label)
			require.Equal(t, tc.want, trigger.Type)

			name := liveScheduleName(t, tc.label)
			saved, err := putLiveSchedule(t, client, name, evalID, trigger)
			skipIfScheduleRoleMissing(t, err)
			require.NoError(t, err, "the service rejected the %s trigger", tc.label)
			require.NotEmpty(t, saved.ID)

			settled := awaitScheduleSettled(t, client, name)
			require.Equal(t, tc.want, settled.Trigger.Type,
				"the trigger type must survive the round trip")
			removeLiveSchedule(t, client, name)
		})
	}
}

// TestLiveScheduleDisabledStaysDisabled covers --disabled, which is the one
// flag whose whole purpose is a field the service could quietly ignore.
func TestLiveScheduleDisabledStaysDisabled(t *testing.T) {
	client, judge := liveEvalClient(t)
	evalID := liveScheduleEval(t, client, judge)
	name := liveScheduleName(t, "disabled")

	trigger, err := buildTrigger(triggerFlags{every: "daily", atHours: []int{3}})
	require.NoError(t, err)

	saved, err := client.PutSchedule(context.Background(), name, &eval_api.Schedule{
		DisplayName: name,
		Enabled:     false,
		Trigger:     trigger,
		Task: &eval_api.ScheduleTask{
			Type:   eval_api.ScheduleTaskEvaluation,
			EvalID: evalID,
			EvalRun: &eval_api.CreateOpenAIEvalRunRequest{
				Name:       name,
				DataSource: datasetOnlyRows([]map[string]any{{"query": "hello"}}),
			},
		},
	}, ProjectEndpointAPIVersion)
	skipIfScheduleRoleMissing(t, err)
	require.NoError(t, err)
	t.Cleanup(func() { removeLiveSchedule(t, client, name) })
	require.NotEmpty(t, saved.ID)

	settled := awaitScheduleSettled(t, client, name)
	require.False(t, settled.Enabled,
		"a schedule created disabled must not come back enabled")
}

// TestLiveScheduleEditIsRefusedByTheCLI pins the reason `schedule set` refuses
// to reuse a name.
//
// The service takes a PUT on an existing schedule and does not apply it, and a
// replacement can stick in Creating where it can no longer be deleted. The CLI
// therefore refuses before sending. This test records the service behaviour
// the guard exists for, so a change in the service is visible here rather than
// as a stuck schedule in someone's project.
func TestLiveScheduleEditIsRefusedByTheCLI(t *testing.T) {
	client, judge := liveEvalClient(t)
	evalID := liveScheduleEval(t, client, judge)
	name := liveScheduleName(t, "edit")

	daily, err := buildTrigger(triggerFlags{every: "daily", atHours: []int{9}})
	require.NoError(t, err)
	_, err = putLiveSchedule(t, client, name, evalID, daily)
	skipIfScheduleRoleMissing(t, err)
	require.NoError(t, err)
	awaitScheduleSettled(t, client, name)

	// The guard in `schedule set` is a GetSchedule that must find this.
	existing, err := client.GetSchedule(context.Background(), name, ProjectEndpointAPIVersion)
	require.NoError(t, err)
	require.NotEmpty(t, existing.ID,
		"the CLI decides a name is taken by reading it back, so this must be non-empty")

	weekly, err := buildTrigger(triggerFlags{every: "weekly", onDays: []string{"Friday"}})
	require.NoError(t, err)
	_, putErr := client.PutSchedule(context.Background(), name, &eval_api.Schedule{
		DisplayName: name,
		Enabled:     true,
		Trigger:     weekly,
		Task: &eval_api.ScheduleTask{
			Type:   eval_api.ScheduleTaskEvaluation,
			EvalID: evalID,
			EvalRun: &eval_api.CreateOpenAIEvalRunRequest{
				Name:       name,
				DataSource: datasetOnlyRows([]map[string]any{{"query": "hello"}}),
			},
		},
	}, ProjectEndpointAPIVersion)

	if putErr != nil {
		t.Logf("the service refused the edit outright: %v", putErr)
		return
	}

	after := awaitScheduleSettled(t, client, name)
	t.Logf("after editing daily -> weekly the schedule reads back as %q", after.Summary())
	require.Equal(t, eval_api.TriggerRecurrence, after.Trigger.Type)
}
