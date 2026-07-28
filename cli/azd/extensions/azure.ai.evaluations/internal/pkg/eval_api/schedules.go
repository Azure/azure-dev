// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

const pathSchedules = "/schedules"

// Trigger types accepted by the schedules API.
const (
	TriggerCron       = "Cron"
	TriggerRecurrence = "Recurrence"
	TriggerOneTime    = "OneTime"
)

// Recurrence patterns accepted under a Recurrence trigger.
const (
	RecurrenceHourly  = "Hourly"
	RecurrenceDaily   = "Daily"
	RecurrenceWeekly  = "Weekly"
	RecurrenceMonthly = "Monthly"
)

// ScheduleTaskEvaluation is the only task type the service accepts today; an
// Insight task is rejected on validation.
const ScheduleTaskEvaluation = "Evaluation"

// RecurrencePattern is the shape of a Recurrence trigger's repeat rule. Which
// fields apply depends on Type: Daily reads Hours, Weekly reads DaysOfWeek,
// Monthly reads DaysOfMonth, and Hourly reads neither.
type RecurrencePattern struct {
	Type        string   `json:"type"`
	Hours       []int    `json:"hours,omitempty"`
	DaysOfWeek  []string `json:"daysOfWeek,omitempty"`
	DaysOfMonth []int    `json:"daysOfMonth,omitempty"`
}

// ScheduleTrigger says when the task runs. The discriminator is Type; the
// other fields are per-type and only one set is ever populated.
type ScheduleTrigger struct {
	Type string `json:"type"`

	// Cron
	Expression string `json:"expression,omitempty"`

	// Recurrence
	Schedule *RecurrencePattern `json:"schedule,omitempty"`
	Interval int                `json:"interval,omitempty"`

	// OneTime
	ScheduledTime string `json:"scheduledTime,omitempty"`

	// Cron and Recurrence
	StartTime string `json:"startTime,omitempty"`
	EndTime   string `json:"endTime,omitempty"`

	Timezone string `json:"timezone,omitempty"`
}

// ScheduleTask is what the trigger fires. An evaluation task needs both the
// group and the run to repeat: the group holds only its testing criteria, so
// the target and dataset travel with the run.
type ScheduleTask struct {
	Type    string                      `json:"type"`
	EvalID  string                      `json:"evalId,omitempty"`
	EvalRun *CreateOpenAIEvalRunRequest `json:"evalRun,omitempty"`
}

// Schedule is a named, project-scoped recurring evaluation.
type Schedule struct {
	ID                 string            `json:"id,omitempty"`
	DisplayName        string            `json:"displayName,omitempty"`
	Description        string            `json:"description,omitempty"`
	Enabled            bool              `json:"enabled"`
	ProvisioningStatus string            `json:"provisioningStatus,omitempty"`
	Trigger            *ScheduleTrigger  `json:"trigger,omitempty"`
	Task               *ScheduleTask     `json:"task,omitempty"`
	Tags               map[string]string `json:"tags,omitempty"`
	Properties         map[string]string `json:"properties,omitempty"`
	Error              *JobError         `json:"error,omitempty"`
}

// Summary renders the trigger as a single line for listings.
func (s *Schedule) Summary() string {
	if s == nil || s.Trigger == nil {
		return ""
	}
	switch s.Trigger.Type {
	case TriggerCron:
		return "cron " + s.Trigger.Expression
	case TriggerOneTime:
		return "once at " + s.Trigger.ScheduledTime
	case TriggerRecurrence:
		if s.Trigger.Schedule == nil {
			return "recurrence"
		}
		return "every " + s.Trigger.Schedule.Type
	}
	return s.Trigger.Type
}

// ScheduleList is the response for ListSchedules.
type ScheduleList struct {
	Value []Schedule `json:"value"`
}

// Settled reports whether the schedule has finished provisioning.
//
// A schedule that is still being created refuses a delete, and does it two
// different ways: 409 while it is busy, or 404 because the trigger behind it
// does not exist yet. Waiting for it to settle avoids both.
func (s *Schedule) Settled() bool {
	if s == nil {
		return true
	}
	switch s.ProvisioningStatus {
	case "Creating", "Updating", "Deleting":
		return false
	}
	return true
}

// PutSchedule creates or replaces a schedule. The route is keyed by name, and
// the same call updates an existing schedule in place.
func (c *EvalClient) PutSchedule(
	ctx context.Context,
	name string,
	schedule *Schedule,
	apiVersion string,
) (*Schedule, error) {
	path := pathSchedules + "/" + url.PathEscape(name)
	return doRequestTyped[Schedule](c, ctx, http.MethodPut, path, nil, schedule, apiVersion)
}

// GetSchedule reads one schedule by name.
func (c *EvalClient) GetSchedule(
	ctx context.Context,
	name string,
	apiVersion string,
) (*Schedule, error) {
	path := pathSchedules + "/" + url.PathEscape(name)
	return doRequestTyped[Schedule](c, ctx, http.MethodGet, path, nil, nil, apiVersion)
}

// ListSchedules returns the project's schedules.
func (c *EvalClient) ListSchedules(ctx context.Context, apiVersion string) (*ScheduleList, error) {
	return doRequestTyped[ScheduleList](c, ctx, http.MethodGet, pathSchedules, nil, nil, apiVersion)
}

// DeleteSchedule removes a schedule by name.
func (c *EvalClient) DeleteSchedule(ctx context.Context, name string, apiVersion string) error {
	path := pathSchedules + "/" + url.PathEscape(name)
	_, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil, apiVersion)
	return err
}

// IsConflict reports whether the service refused because the resource is busy.
//
// A schedule that is still provisioning answers 409 to a delete, which is worth
// waiting out rather than reporting.
func IsConflict(err error) bool {
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		return false
	}
	return respErr.StatusCode == http.StatusConflict
}
