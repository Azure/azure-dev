// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"time"
)

type taskArtifactReport struct {
	SchemaVersion int                      `json:"schema_version"`
	ManifestPath  string                   `json:"manifest_path"`
	RecordedAt    time.Time                `json:"recorded_at"`
	Counts        map[string]int           `json:"counts"`
	Tasks         []taskArtifactReportTask `json:"tasks"`
}

type taskArtifactReportTask struct {
	TaskName    string `json:"task_name"`
	Status      string `json:"status"`
	BuildStatus string `json:"build_status,omitempty"`
	RunID       string `json:"run_id,omitempty"`
	Image       string `json:"image,omitempty"`
	LastError   string `json:"last_error,omitempty"`
}

func taskArtifactStatus(task TaskArtifact) string {
	if _, err := PinnedTaskArtifactImage(task); err == nil {
		return "ready"
	}
	if task.CLI == nil || task.CLI.Build == nil {
		return "unverified"
	}
	build := task.CLI.Build
	if terminalArtifactRun(build.Status) {
		if build.Status == "Succeeded" {
			return "unverified"
		}
		return "failed"
	}
	if build.Status == "pending" {
		return "pending"
	}
	if build.RunID == "" {
		return "unresolved"
	}
	return "in_progress"
}

func (j *artifactJournal) report() error {
	report := taskArtifactReport{
		SchemaVersion: 1, ManifestPath: j.path, RecordedAt: time.Now().UTC(),
		Counts: map[string]int{
			"ready": 0, "failed": 0, "pending": 0, "unresolved": 0,
			"in_progress": 0, "unverified": 0,
		},
		Tasks: make([]taskArtifactReportTask, 0, len(j.data.Tasks)),
	}
	for _, task := range j.data.Tasks {
		status := taskArtifactStatus(task)
		report.Counts[status]++
		row := taskArtifactReportTask{TaskName: task.TaskName, Status: status, Image: task.AcrImagePath}
		if task.CLI != nil && task.CLI.Build != nil {
			build := task.CLI.Build
			row.BuildStatus, row.RunID, row.LastError = build.Status, build.RunID, redactArtifactURLs(build.LastError)
			if row.Image == "" {
				row.Image = build.ImagePath
			}
		}
		report.Tasks = append(report.Tasks, row)
	}
	filename := j.path + ".report.json"
	if err := writeTaskArtifactJSON(filename, report); err != nil {
		return fmt.Errorf("write task artifact report: %w", err)
	}
	return j.log("Task artifacts (recorded state): %d ready, %d failed, %d pending, %d unresolved, "+
		"%d in progress, %d unverified; report: %s\n",
		report.Counts["ready"], report.Counts["failed"], report.Counts["pending"], report.Counts["unresolved"],
		report.Counts["in_progress"], report.Counts["unverified"], filename)
}

func selectedTaskRetry(task TaskArtifact) bool {
	if task.CLI == nil || task.CLI.Build == nil || task.AcrImagePath != "" {
		return false
	}
	if terminalArtifactRun(task.CLI.Build.Status) {
		return task.CLI.Build.Status != "Succeeded"
	}
	history := task.CLI.BuildHistory
	return task.CLI.Build.Status == "pending" && len(history) > 0 &&
		terminalArtifactRun(history[len(history)-1].Status) && history[len(history)-1].Status != "Succeeded"
}

func retryFailedTaskArtifacts(
	ctx context.Context, j *artifactJournal, retry bool, client TaskArtifactClient,
) (map[int]bool, error) {
	selected := map[int]bool{}
	if !retry {
		return selected, nil
	}
	states := map[string]ArtifactRun{}
	for index, task := range j.data.Tasks {
		if !selectedTaskRetry(task) {
			continue
		}
		selected[index] = true
		build := task.CLI.Build
		if build.Status == "pending" {
			continue
		}
		if build.RunID == "" || build.RegistryID == "" {
			return nil, fmt.Errorf("task %q has no recorded failed run identity; reconcile before retrying", task.TaskName)
		}
		key := build.RegistryID + "/" + build.RunID
		if _, exists := states[key]; !exists {
			run, err := client.Run(ctx, build.RegistryID, build.RunID)
			if err != nil {
				return nil, fmt.Errorf("reconcile failed task %q before retrying: %w", task.TaskName, err)
			}
			if !terminalArtifactRun(run.Status) {
				return nil, fmt.Errorf("task %q run %s is %s; no replacement build submitted",
					task.TaskName, build.RunID, run.Status)
			}
			states[key] = run
		}
	}
	for index := range selected {
		task := &j.data.Tasks[index]
		build := task.CLI.Build
		if build.Status == "pending" {
			continue
		}
		if states[build.RegistryID+"/"+build.RunID].Status == "Succeeded" {
			build.Status = "Succeeded"
			continue
		}
		task.CLI.BuildHistory = append(task.CLI.BuildHistory, *build)
		task.CLI.Build = &TaskArtifactBuild{
			RegistryID: build.RegistryID, ImagePath: build.ImagePath, Status: "pending",
		}
	}
	if len(selected) > 0 {
		return selected, j.save()
	}
	return selected, nil
}
