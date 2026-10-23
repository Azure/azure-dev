// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedArtifactRecovery(t *testing.T) (TaskArtifactOptions, TaskArtifactManifest) {
	t.Helper()
	opts := artifactOptions(t)
	for _, name := range []string{"ready", "failed", "pending", "uncertain"} {
		writeArtifactArchive(t, filepath.Join(opts.TasksetDir, name+".tar.gz"),
			map[string]string{"environment/marker.txt": name})
	}
	j := &artifactJournal{path: opts.ManifestPath, data: TaskArtifactManifest{SchemaVersion: 1}}
	if err := preflightTaskArtifacts(j, opts); err != nil {
		t.Fatal(err)
	}
	for index := range j.data.Tasks {
		task := &j.data.Tasks[index]
		task.CLI.Build.RegistryID = testArtifactRegistry
		switch task.TaskName {
		case "ready":
			task.CLI.Build.Status, task.CLI.Build.RunID = "Succeeded", "ready-run"
			task.AcrImagePath = task.CLI.Build.ImagePath
			task.CLI.ImagePath, task.CLI.ImageDigest = task.AcrImagePath, testArtifactDigest
		case "failed":
			task.CLI.Build.Status, task.CLI.Build.RunID = "Failed", "failed-run"
			task.CLI.Build.LastError = "base image pull denied"
		case "uncertain":
			task.CLI.Build.Status = "submission_unknown"
			task.CLI.Build.LastError = "connection lost https://storage.example/source?sig=secret"
		}
	}
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	opts.TasksetDir = ""
	return opts, j.data
}

func TestTaskArtifactsFailureReportIncludesRecordedStates(t *testing.T) {
	opts, _ := seedArtifactRecovery(t)
	var output bytes.Buffer
	opts.Output = &output
	client := &fakeArtifactClient{runStatuses: map[string]string{"failed-run": "Failed"}}
	if _, err := PrepareTaskArtifacts(t.Context(), opts, client); err == nil {
		t.Fatal("expected the recorded failed build to prevent preparation")
	}
	raw, err := os.ReadFile(opts.ManifestPath + ".report.json")
	if err != nil {
		t.Fatal(err)
	}
	var report taskArtifactReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"ready", "failed", "pending", "unresolved"} {
		if report.Counts[status] != 1 {
			t.Fatalf("incorrect %s count: %+v", status, report)
		}
	}
	if bytes.Contains(raw, []byte("sig=secret")) || !strings.Contains(output.String(), "recorded state") {
		t.Fatalf("report must redact URLs and distinguish recorded state: %s", raw)
	}
}

func TestTaskArtifactsRetryOnlyFailedAndRetainHistory(t *testing.T) {
	opts, before := seedArtifactRecovery(t)
	opts.RetryFailed = true
	client := &fakeArtifactClient{runStatuses: map[string]string{"failed-run": "Failed"}}
	manifest, err := PrepareTaskArtifacts(t.Context(), opts, client)
	if err != nil {
		t.Fatal(err)
	}
	if client.submissions != 1 || client.uploads != 1 {
		t.Fatalf("must only resubmit failed builds: %+v", client)
	}
	for index, task := range manifest.Tasks {
		if task.TaskName == "failed" {
			if taskArtifactStatus(task) != "ready" || len(task.CLI.BuildHistory) != 1 ||
				task.CLI.BuildHistory[0].RunID != "failed-run" ||
				task.CLI.BuildHistory[0].LastError != "base image pull denied" {
				t.Fatalf("retry must preserve old run identity and error: %+v", task.CLI)
			}
		} else if task.CLI.Build.RunID != before.Tasks[index].CLI.Build.RunID ||
			task.CLI.Build.Status != before.Tasks[index].CLI.Build.Status {
			t.Fatalf("retry changed an unrelated task: %+v", task)
		}
	}
	if _, err := PrepareTaskArtifacts(t.Context(), opts, client); err != nil || client.submissions != 1 {
		t.Fatalf("repeating failed-only retries must not rebuild successes: %v", err)
	}
}

func TestTaskArtifactsRetryRechecksRemoteState(t *testing.T) {
	for _, status := range []string{"Running", "Succeeded"} {
		t.Run(status, func(t *testing.T) {
			opts, _ := seedArtifactRecovery(t)
			opts.RetryFailed = true
			client := &fakeArtifactClient{runStatuses: map[string]string{"failed-run": status}}
			manifest, err := PrepareTaskArtifacts(t.Context(), opts, client)
			if status == "Running" && (err == nil || !strings.Contains(err.Error(), "no replacement")) {
				t.Fatalf("active remote build must block retry: %v", err)
			}
			if status == "Succeeded" {
				if err != nil {
					t.Fatal(err)
				}
				for _, task := range manifest.Tasks {
					if task.TaskName == "failed" && taskArtifactStatus(task) != "ready" {
						t.Fatalf("remotely successful run must be verified, not rebuilt: %+v", task)
					}
				}
			}
			if client.submissions != 0 || client.uploads != 0 {
				t.Fatalf("must reconcile existing run without resubmission: %+v", client)
			}
		})
	}
}

func TestTaskArtifactsManualRemovalUsesOnlyRemainingManifestTasks(t *testing.T) {
	opts, manifest := seedArtifactRecovery(t)
	var remaining []TaskArtifact
	for _, task := range manifest.Tasks {
		if task.TaskName == "ready" {
			remaining = append(remaining, task)
		}
	}
	manifest.Tasks = remaining
	writeArtifactManifest(t, opts.ManifestPath, manifest)
	client := &fakeArtifactClient{}
	prepared, err := PrepareTaskArtifacts(t.Context(), opts, client)
	if err != nil || len(prepared.Tasks) != 1 || prepared.Tasks[0].TaskName != "ready" ||
		client.submissions != 0 || client.uploads != 0 || client.runs != 0 {
		t.Fatalf("manifest-only resume must not rediscover or process removed tasks: %+v, %+v, %v",
			prepared, client, err)
	}
	stored, err := loadTaskArtifactManifest(opts.ManifestPath)
	if err != nil || len(stored.Tasks) != 1 || stored.Tasks[0].TaskName != "ready" {
		t.Fatalf("removed task rows must stay removed: %+v, %v", stored, err)
	}
	raw, err := os.ReadFile(opts.ManifestPath + ".report.json")
	if err != nil {
		t.Fatal(err)
	}
	var report taskArtifactReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Tasks) != 1 || report.Counts["ready"] != 1 || report.Counts["failed"] != 0 {
		t.Fatalf("report must reflect the remaining tasks: %+v", report)
	}
}

func TestTaskArtifactsRetrySharedFailedContextSubmitsOnce(t *testing.T) {
	opts, manifest := seedArtifactRecovery(t)
	for _, task := range manifest.Tasks {
		if task.TaskName == "failed" {
			peer := task
			peer.TaskName = "failed-peer"
			peer.CLI = new(*task.CLI)
			peer.CLI.Build = new(*task.CLI.Build)
			manifest.Tasks = append(manifest.Tasks, peer)
			break
		}
	}
	writeArtifactManifest(t, opts.ManifestPath, manifest)
	opts.RetryFailed = true
	client := &fakeArtifactClient{runStatuses: map[string]string{"failed-run": "Failed"}}
	if _, err := PrepareTaskArtifacts(t.Context(), opts, client); err != nil || client.submissions != 1 {
		t.Fatalf("shared failed context must have only one replacement run: %+v, %v", client, err)
	}
}

func TestTaskArtifactsRetryChangedFailedContextPreservesRunHistory(t *testing.T) {
	opts, _ := seedArtifactRecovery(t)
	writeArtifactArchive(t, filepath.Join(filepath.Dir(opts.ManifestPath), "failed.tar.gz"),
		map[string]string{"environment/marker.txt": "corrected context"})
	opts.RetryFailed = true
	client := &fakeArtifactClient{}
	manifest, err := PrepareTaskArtifacts(t.Context(), opts, client)
	if err != nil || client.submissions != 1 {
		t.Fatalf("changed failed context must remain eligible for retry: %+v, %v", client, err)
	}
	for _, task := range manifest.Tasks {
		if task.TaskName == "failed" && (len(task.CLI.BuildHistory) != 1 ||
			task.CLI.BuildHistory[0].RunID != "failed-run" || taskArtifactStatus(task) != "ready") {
			t.Fatalf("changed context must preserve the failed attempt: %+v", task.CLI)
		}
	}
}
