// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/spf13/cobra"
)

// newScheduleCommand groups the recurring-evaluation commands.
func newScheduleCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schedule",
		Short: "Run an eval on a schedule.",
	}
	cmd.AddCommand(
		newScheduleSetCommand(),
		newScheduleListCommand(),
		newScheduleShowCommand(),
		newScheduleDeleteCommand(),
	)
	return cmd
}

// newScheduleSetCommand creates the schedule that runs an eval.
//
// It does not update. The service accepts a PUT over an existing schedule,
// echoes the new body and keeps the old trigger, so an in-place edit would
// report a change that did not happen. Recreating under the same name is not
// an escape either: the replacement never leaves Creating and cannot then be
// deleted. So an existing schedule is refused, and changing one means deleting
// it and creating another under a different name.
func newScheduleSetCommand() *cobra.Command {
	var (
		configPath  string
		groupName   string
		evalID      string
		name        string
		description string
		cron        string
		every       string
		interval    int
		atHours     []int
		onDays      []string
		onDaysOfMon []int
		atTime      string
		timezone    string
		startTime   string
		endTime     string
		disabled    bool
		level       string
		maxSamples  int
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "set [eval-id]",
		Short: "Create the schedule that runs an eval.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			if len(args) == 1 {
				evalID = args[0]
			}

			trigger, err := buildTrigger(triggerFlags{
				cron:        cron,
				every:       every,
				interval:    interval,
				atHours:     atHours,
				onDays:      onDays,
				onDaysOfMon: onDaysOfMon,
				atTime:      atTime,
				timezone:    timezone,
				startTime:   startTime,
				endTime:     endTime,
			})
			if err != nil {
				return err
			}

			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			// Same resolution as `run`: the config names the group unless
			// --eval-id bypasses it, and the run payload carries the target
			// and dataset because the group holds neither.
			var group *project.Eval
			var dataSource *eval_api.EvalRunDataSource
			if evalID == "" {
				cfg, err := project.LoadEvalConfig(configPath)
				if err != nil {
					return err
				}
				if err := cfg.Validate(); err != nil {
					return err
				}
				group, err = cfg.ResolveGroup(groupName)
				if err != nil {
					return err
				}
				if err := ec.checkDatasetRegistered(ctx, cfg, group, configPath); err != nil {
					return err
				}
				evalID, err = ec.resolveEvalIDFromConfig(
					ctx, group, configPath, resolveLevel(level, group),
					len(cfg.Evals) == 1, out, isJSON(cmd))
				if err != nil {
					return err
				}
				dataSource, err = ec.buildRunDataSource(
					ctx, group, configPath, resolveMaxSamples(maxSamples, group))
				if err != nil {
					return err
				}
			} else {
				dataSource, err = ec.reuseDataSourceFromLastRun(ctx, evalID)
				if err != nil {
					return err
				}
			}

			if name == "" {
				name = defaultScheduleName(group)
			}
			if description == "" {
				description = fmt.Sprintf("Scheduled evaluation of %s.", evalID)
			}

			// An existing schedule cannot be edited: the service takes the PUT
			// and ignores it. Recreating under the same name is worse — the
			// replacement sticks in Creating and cannot be deleted — so the
			// only safe answer is a different name.
			if existing, err := ec.evalClient.GetSchedule(
				ctx, name, ProjectEndpointAPIVersion); err == nil && existing != nil && existing.ID != "" {
				return fmt.Errorf(
					"schedule %q already exists, and the service ignores edits to it. "+
						"Delete it with `azd ai eval schedule delete %s` and create the new "+
						"one under a different name; reusing this one leaves it stuck",
					name, name)
			}

			metadata := map[string]string{}
			if lvl := resolveLevel(level, group); lvl != "" {
				metadata["evaluation_level"] = lvl
			}

			schedule := &eval_api.Schedule{
				DisplayName: name,
				Description: description,
				Enabled:     !disabled,
				Trigger:     trigger,
				Task: &eval_api.ScheduleTask{
					Type:   eval_api.ScheduleTaskEvaluation,
					EvalID: evalID,
					EvalRun: &eval_api.CreateOpenAIEvalRunRequest{
						Name:       name,
						DataSource: dataSource,
						Metadata:   metadata,
					},
				},
			}

			saved, err := ec.evalClient.PutSchedule(ctx, name, schedule, ProjectEndpointAPIVersion)
			if err != nil {
				return explainScheduleFailure(ctx, ec, name, err)
			}

			if isJSON(cmd) {
				return emitJSON(out, saved)
			}
			state := "enabled"
			if !saved.Enabled {
				state = "disabled"
			}
			fmt.Fprintf(out, "Schedule %s (%s) runs %s on %s\n",
				saved.ID, state, saved.Summary(), evalID)
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", project.DefaultDeployConfig,
		"Path to the eval deployment config.")
	cmd.Flags().StringVar(&groupName, "eval", "", "Which evals entry to schedule.")
	cmd.Flags().StringVar(&evalID, "eval-id", "", "Schedule an existing eval by id, ignoring config.")
	cmd.Flags().StringVar(&name, "name", "", "Schedule name. Defaults to the eval name.")
	cmd.Flags().StringVar(&description, "description", "", "Schedule description.")
	cmd.Flags().StringVar(&cron, "cron", "", `Cron expression, for example "0 9 * * *".`)
	cmd.Flags().StringVar(&every, "every", "",
		"Recur hourly, daily, weekly or monthly.")
	cmd.Flags().IntVar(&interval, "interval", 0, "Repeat every N periods of --every. Defaults to 1.")
	cmd.Flags().IntSliceVar(&atHours, "at", nil, "Hours of the day for --every daily, 0-23.")
	cmd.Flags().StringSliceVar(&onDays, "on", nil, "Days of the week for --every weekly, for example Monday.")
	cmd.Flags().IntSliceVar(&onDaysOfMon, "on-day", nil, "Days of the month for --every monthly, 1-31.")
	cmd.Flags().StringVar(&atTime, "at-time", "", "Run once at this RFC3339 time.")
	cmd.Flags().StringVar(&timezone, "timezone", "", "Timezone for the trigger. Defaults to UTC.")
	cmd.Flags().StringVar(&startTime, "start-time", "", "RFC3339 time before which the schedule does not fire.")
	cmd.Flags().StringVar(&endTime, "end-time", "", "RFC3339 time after which the schedule stops firing.")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "Create the schedule without enabling it.")
	cmd.Flags().StringVar(&level, "level", "", "Evaluation level for the scheduled runs.")
	cmd.Flags().IntVar(&maxSamples, "max-samples", 0, "Cap rows sent from a local dataset file.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	cmd.MarkFlagsMutuallyExclusive("cron", "every", "at-time")

	return cmd
}

func newScheduleListCommand() *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the project's schedules.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			list, err := ec.evalClient.ListSchedules(ctx, ProjectEndpointAPIVersion)
			if err != nil {
				return fmt.Errorf("listing schedules: %w", err)
			}
			if isJSON(cmd) {
				var schedules []eval_api.Schedule
				if list != nil {
					schedules = list.Value
				}
				return emitJSONList(out, schedules)
			}
			if list == nil || len(list.Value) == 0 {
				fmt.Fprintln(out, "No schedules.")
				return nil
			}

			rows := make([][]string, 0, len(list.Value))
			for i := range list.Value {
				s := &list.Value[i]
				evalID := ""
				if s.Task != nil {
					evalID = s.Task.EvalID
				}
				rows = append(rows, []string{
					s.ID,
					strconv.FormatBool(s.Enabled),
					s.ProvisioningStatus,
					s.Summary(),
					evalID,
				})
			}
			return emitTable(out,
				[]string{"NAME", "ENABLED", "STATUS", "TRIGGER", "EVAL"}, rows)
		},
	}
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newScheduleShowCommand() *cobra.Command {
	var (
		name        string
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "show [name]",
		Short: "Show one schedule.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			if len(args) == 1 {
				name = args[0]
			}
			if name == "" {
				return requireFlag("name")
			}

			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			s, err := ec.evalClient.GetSchedule(ctx, name, ProjectEndpointAPIVersion)
			if err != nil {
				return fmt.Errorf("reading schedule %q: %w", name, err)
			}
			if isJSON(cmd) {
				return emitJSON(out, s)
			}

			fmt.Fprintf(out, "Schedule %s\n", s.ID)
			fmt.Fprintf(out, "  enabled:  %t\n", s.Enabled)
			fmt.Fprintf(out, "  status:   %s\n", s.ProvisioningStatus)
			fmt.Fprintf(out, "  trigger:  %s\n", s.Summary())
			if s.Trigger != nil && s.Trigger.Timezone != "" {
				fmt.Fprintf(out, "  timezone: %s\n", s.Trigger.Timezone)
			}
			if s.Task != nil {
				fmt.Fprintf(out, "  eval:     %s\n", s.Task.EvalID)
			}
			if s.Description != "" {
				fmt.Fprintf(out, "  about:    %s\n", s.Description)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Schedule name.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newScheduleDeleteCommand() *cobra.Command {
	var (
		name        string
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a schedule.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			if len(args) == 1 {
				name = args[0]
			}
			if name == "" {
				return requireFlag("name")
			}

			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			if err := ec.deleteScheduleWhenSettled(ctx, name); err != nil {
				// A name that was never there is the common typo, and the
				// service answers it with a full error document wrapping an
				// inner 404 from the trigger service. Saying so in one line is
				// more use than reproducing that.
				if eval_api.IsNotFound(err) {
					return fmt.Errorf(
						"no schedule named %q in this project; "+
							"`azd ai eval schedule list` shows the ones that exist", name)
				}
				return fmt.Errorf("deleting schedule %q: %w", name, err)
			}
			fmt.Fprintf(out, "Deleted schedule %s\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Schedule name.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// triggerFlags carries the schedule's timing flags so buildTrigger can be
// tested without a command.
type triggerFlags struct {
	cron        string
	every       string
	interval    int
	atHours     []int
	onDays      []string
	onDaysOfMon []int
	atTime      string
	timezone    string
	startTime   string
	endTime     string
}

// buildTrigger turns the timing flags into the trigger the API expects.
func buildTrigger(f triggerFlags) (*eval_api.ScheduleTrigger, error) {
	tz := f.timezone
	if tz == "" {
		tz = "UTC"
	}

	switch {
	case f.cron != "":
		return &eval_api.ScheduleTrigger{
			Type:       eval_api.TriggerCron,
			Expression: f.cron,
			StartTime:  f.startTime,
			EndTime:    f.endTime,
			Timezone:   tz,
		}, nil

	case f.atTime != "":
		if _, err := time.Parse(time.RFC3339, f.atTime); err != nil {
			return nil, fmt.Errorf("--at-time %q is not an RFC3339 time", f.atTime)
		}
		return &eval_api.ScheduleTrigger{
			Type:          eval_api.TriggerOneTime,
			ScheduledTime: f.atTime,
			Timezone:      tz,
		}, nil

	case f.every != "":
		pattern, err := buildRecurrence(f)
		if err != nil {
			return nil, err
		}
		interval := f.interval
		if interval <= 0 {
			interval = 1
		}
		return &eval_api.ScheduleTrigger{
			Type:      eval_api.TriggerRecurrence,
			Schedule:  pattern,
			Interval:  interval,
			StartTime: f.startTime,
			EndTime:   f.endTime,
			Timezone:  tz,
		}, nil
	}

	return nil, fmt.Errorf("a schedule needs a trigger: pass --cron, --every or --at-time")
}

// buildRecurrence maps --every and its qualifiers onto a recurrence pattern.
//
// Each period reads only its own qualifier, so passing one that does not apply
// is rejected rather than dropped.
func buildRecurrence(f triggerFlags) (*eval_api.RecurrencePattern, error) {
	period := strings.ToLower(strings.TrimSpace(f.every))

	reject := func(flag, applies string) error {
		return fmt.Errorf("--%s applies to --every %s, not %s", flag, applies, period)
	}

	switch period {
	case "hourly":
		if len(f.atHours) > 0 {
			return nil, reject("at", "daily")
		}
		if len(f.onDays) > 0 {
			return nil, reject("on", "weekly")
		}
		if len(f.onDaysOfMon) > 0 {
			return nil, reject("on-day", "monthly")
		}
		return &eval_api.RecurrencePattern{Type: eval_api.RecurrenceHourly}, nil

	case "daily":
		if len(f.onDays) > 0 {
			return nil, reject("on", "weekly")
		}
		if len(f.onDaysOfMon) > 0 {
			return nil, reject("on-day", "monthly")
		}
		for _, h := range f.atHours {
			if h < 0 || h > 23 {
				return nil, fmt.Errorf("--at %d is not an hour of the day (0-23)", h)
			}
		}
		return &eval_api.RecurrencePattern{Type: eval_api.RecurrenceDaily, Hours: f.atHours}, nil

	case "weekly":
		if len(f.atHours) > 0 {
			return nil, reject("at", "daily")
		}
		if len(f.onDaysOfMon) > 0 {
			return nil, reject("on-day", "monthly")
		}
		days, err := normalizeDaysOfWeek(f.onDays)
		if err != nil {
			return nil, err
		}
		return &eval_api.RecurrencePattern{Type: eval_api.RecurrenceWeekly, DaysOfWeek: days}, nil

	case "monthly":
		if len(f.atHours) > 0 {
			return nil, reject("at", "daily")
		}
		if len(f.onDays) > 0 {
			return nil, reject("on", "weekly")
		}
		for _, d := range f.onDaysOfMon {
			if d < 1 || d > 31 {
				return nil, fmt.Errorf("--on-day %d is not a day of the month (1-31)", d)
			}
		}
		return &eval_api.RecurrencePattern{Type: eval_api.RecurrenceMonthly, DaysOfMonth: f.onDaysOfMon}, nil
	}

	return nil, fmt.Errorf(
		"--every %q is not a recurrence: use hourly, daily, weekly or monthly", f.every)
}

// normalizeDaysOfWeek accepts day names in any casing and returns the spelling
// the service expects.
func normalizeDaysOfWeek(days []string) ([]string, error) {
	if len(days) == 0 {
		return nil, nil
	}
	canonical := map[string]string{}
	for d := time.Sunday; d <= time.Saturday; d++ {
		canonical[strings.ToLower(d.String())] = d.String()
	}

	out := make([]string, 0, len(days))
	for _, raw := range days {
		name, ok := canonical[strings.ToLower(strings.TrimSpace(raw))]
		if !ok {
			return nil, fmt.Errorf("--on %q is not a day of the week", raw)
		}
		out = append(out, name)
	}
	return out, nil
}

// defaultScheduleName derives a schedule name from the group being scheduled.
func defaultScheduleName(group *project.Eval) string {
	if group != nil && group.Name != "" {
		return group.Name
	}
	return "eval-" + strconv.FormatInt(time.Now().UTC().Unix(), 10)
}

// deleteScheduleWhenSettled removes a schedule, waiting out the window where
// the service is still provisioning it.
//
// A schedule that is mid-provision refuses the delete, and does it two ways:
// 409 while it is busy, or 404 because the trigger behind it does not exist
// yet. Either way the caller neither caused it nor can see it, so the wait
// happens here.
func (ec *evalContext) deleteScheduleWhenSettled(ctx context.Context, name string) error {
	const attempts = 30

	for i := 0; i < attempts; i++ {
		s, err := ec.evalClient.GetSchedule(ctx, name, ProjectEndpointAPIVersion)
		if err != nil || s == nil || s.ID == "" {
			// Nothing to wait for: let the delete report what it finds.
			break
		}
		if s.Settled() {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(4 * time.Second):
		}
	}

	for i := 0; ; i++ {
		err := ec.evalClient.DeleteSchedule(ctx, name, ProjectEndpointAPIVersion)
		if err == nil || !eval_api.IsConflict(err) || i == attempts-1 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(4 * time.Second):
		}
	}
}

// explainScheduleFailure turns the service's bodiless rejection into the reason
// it actually happened.
//
// The project accepts one schedule at a time and refuses a second with a 400
// carrying no message, so the count is what explains it.
func explainScheduleFailure(
	ctx context.Context,
	ec *evalContext,
	name string,
	cause error,
) error {
	// A schedule repeats the group's most recent run, so scheduling a group
	// whose last run came from --from-traces creates a trace evaluation, and
	// the service allows only an hourly trigger for those. The message it
	// returns says so without saying why it thinks the schedule is one, which
	// is bewildering when the trigger was the only thing asked for.
	if isTracesHourlyOnly(cause) {
		return fmt.Errorf(
			"saving schedule %q: this eval's most recent run read from traces, and a schedule "+
				"repeats that run, so the service treats it as a scheduled trace evaluation "+
				"and allows only `--every hourly`. Use `--every hourly`, or run the eval "+
				"once against its dataset first so the schedule repeats that instead", name)
	}

	list, listErr := ec.evalClient.ListSchedules(ctx, ProjectEndpointAPIVersion)
	if listErr != nil || list == nil {
		return fmt.Errorf("saving schedule %q: %w", name, cause)
	}

	for i := range list.Value {
		if other := list.Value[i].ID; other != "" && other != name {
			return fmt.Errorf(
				"saving schedule %q: the project already has a schedule, %q, and only one is "+
					"allowed at a time. Delete it first with "+
					"`azd ai eval schedule delete %s`", name, other, other)
		}
	}
	return fmt.Errorf("saving schedule %q: %w", name, cause)
}

// isTracesHourlyOnly matches the service's refusal of a non-hourly trigger on a
// schedule it considers a trace evaluation.
func isTracesHourlyOnly(err error) bool {
	return err != nil &&
		strings.Contains(err.Error(), "trace evaluations only support hourly")
}
