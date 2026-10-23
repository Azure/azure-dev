// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/gofrs/flock"
)

const testArtifactRegistry = "/subscriptions/sub/resourceGroups/group/providers/Microsoft.ContainerRegistry/registries/example"
const testArtifactDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func writeArtifactArchive(t *testing.T, filename string, overrides map[string]string, extra ...*tar.Header) []byte {
	t.Helper()
	files := map[string]string{
		"task.toml": "[environment]\ncpus = 1\n", "instruction.md": "instruction",
		"environment/Dockerfile": "FROM scratch\nCOPY marker.txt /marker.txt\n",
		"environment/marker.txt": "context-only", "tests/test.sh": "#!/bin/sh\n", "solution/solve.sh": "solution",
	}
	maps.Copy(files, overrides)
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, content); err != nil {
			t.Fatal(err)
		}
	}
	for _, header := range extra {
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, raw.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

type fakeArtifactClient struct {
	mu                   sync.Mutex
	uploads, submissions int
	digests, runs        int
	scheduleErr          error
	failSubmission       int
	digestErr            error
	status               string
	runStatuses          map[string]string
	digest               string
	barrier              chan struct{}
}

func (*fakeArtifactClient) Registry(context.Context, string) (string, error) {
	return testArtifactRegistry, nil
}
func (f *fakeArtifactClient) Upload(_ context.Context, _ string, raw []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads++
	if _, err := inspectHarborArchiveBytes(raw); err != nil {
		return "", err
	}
	return "private/source.tar.gz", nil
}
func (f *fakeArtifactClient) Schedule(context.Context, string, string, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submissions++
	if f.barrier != nil && f.submissions > 1 && f.digests == 0 {
		return "", errors.New("remaining build submitted before canary digest verification")
	}
	if f.scheduleErr != nil && (f.failSubmission == 0 || f.submissions == f.failSubmission) {
		return "", f.scheduleErr
	}
	if f.submissions == 3 && f.barrier != nil {
		close(f.barrier)
	}
	return "run-" + strings.Repeat("x", f.submissions), nil
}
func (f *fakeArtifactClient) Run(ctx context.Context, _, runID string) (ArtifactRun, error) {
	if f.barrier != nil && runID != "run-x" {
		select {
		case <-f.barrier:
		case <-ctx.Done():
			return ArtifactRun{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs++
	status := f.status
	if specific := f.runStatuses[runID]; specific != "" {
		status = specific
	}
	if status == "" {
		status = "Succeeded"
	}
	return ArtifactRun{Status: status}, nil
}
func (f *fakeArtifactClient) Digest(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.digests++
	if f.digestErr != nil {
		return "", f.digestErr
	}
	if f.digest != "" {
		return f.digest, nil
	}
	return testArtifactDigest, nil
}

func artifactOptions(t *testing.T) TaskArtifactOptions {
	t.Helper()
	dir := t.TempDir()
	return TaskArtifactOptions{
		ManifestPath: filepath.Join(dir, "task-artifacts.json"), TasksetDir: dir,
		Registry: "example.azurecr.io", Concurrency: 2, PollInterval: time.Millisecond,
	}
}

func writeArtifactManifest(t *testing.T, filename string, manifest TaskArtifactManifest) {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTaskArtifactsConcurrencyBounds(t *testing.T) {
	for _, tt := range []struct {
		name        string
		concurrency int
		valid       bool
	}{
		{"below minimum", 0, false},
		{"minimum", 1, true},
		{"maximum", 32, true},
		{"above maximum", 33, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			opts := artifactOptions(t)
			opts.Concurrency = tt.concurrency
			writeArtifactArchive(t, filepath.Join(opts.TasksetDir, "task.tar.gz"), nil)
			client := &fakeArtifactClient{}
			manifest, err := PrepareTaskArtifacts(t.Context(), opts, client)
			if !tt.valid {
				if err == nil || client.uploads != 0 || client.submissions != 0 {
					t.Fatalf("invalid concurrency must fail before upload or submission: %v", err)
				}
				return
			}
			if err != nil || len(manifest.Tasks) != 1 || client.submissions != 1 || client.digests != 1 {
				t.Fatalf("valid concurrency must prepare the task: manifest=%+v client=%+v err=%v",
					manifest, client, err)
			}
		})
	}
}

func TestTaskArtifactsImportMinimalManifestAndInvalidateEditedImage(t *testing.T) {
	opts := artifactOptions(t)
	opts.TasksetDir, opts.Registry = "", ""
	writeArtifactArchive(t, filepath.Join(filepath.Dir(opts.ManifestPath), "task.tar.gz"), nil)
	writeArtifactManifest(t, opts.ManifestPath, TaskArtifactManifest{SchemaVersion: 1, Tasks: []TaskArtifact{{
		TaskName: "task", LocalBundlePath: filepath.Join(filepath.Dir(opts.ManifestPath), "task.tar.gz"),
		AcrImagePath: "EXAMPLE.azurecr.io/custom/user--image__name:v1",
	}}})
	client := &fakeArtifactClient{}
	manifest, err := PrepareTaskArtifacts(t.Context(), opts, client)
	if err != nil {
		t.Fatal(err)
	}
	if client.uploads != 0 || client.submissions != 0 || client.runs != 0 || client.digests != 1 {
		t.Fatalf("prebuilt import must only verify the image: %+v", client)
	}
	pinned, err := PinnedTaskArtifactImage(manifest.Tasks[0])
	if err != nil || pinned != "EXAMPLE.azurecr.io/custom/user--image__name@"+testArtifactDigest {
		t.Fatalf("incorrect immutable image: %q, %v", pinned, err)
	}
	manifest.Tasks[0].AcrImagePath = "example.azurecr.io/custom/different:v2"
	writeArtifactManifest(t, opts.ManifestPath, manifest)
	client.digest = "sha256:" + strings.Repeat("b", 64)
	changed, err := PrepareTaskArtifacts(t.Context(), opts, client)
	if err != nil || changed.Tasks[0].CLI.ImageDigest != client.digest || client.submissions != 0 {
		t.Fatalf("edited image must invalidate old verification, not rebuild: %+v, %v", changed, err)
	}
}

func TestTaskArtifactsCanaryThenQueuesAllBuildsAndReuse(t *testing.T) {
	opts := artifactOptions(t)
	opts.Concurrency = 1
	firstBytes := writeArtifactArchive(t, filepath.Join(opts.TasksetDir, "first.tar.gz"), nil)
	writeArtifactArchive(t, filepath.Join(opts.TasksetDir, "second.tar.gz"),
		map[string]string{"environment/marker.txt": "different-context"})
	writeArtifactArchive(t, filepath.Join(opts.TasksetDir, "third.tar.gz"),
		map[string]string{"environment/marker.txt": "third-context"})
	client := &fakeArtifactClient{barrier: make(chan struct{})}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	manifest, err := PrepareTaskArtifacts(ctx, opts, client)
	if err != nil {
		t.Fatal(err)
	}
	if client.submissions != 3 || len(manifest.Tasks) != 3 {
		t.Fatalf("one upload slot must not cap outstanding builds after the canary: %+v, %+v", client, manifest)
	}
	blob, err := ReadHarborArtifactBundle(opts.ManifestPath, manifest.Tasks[0])
	if err != nil || !bytes.Equal(blob, firstBytes) {
		t.Fatalf("must preserve full original archive bytes, including solution: %v", err)
	}
	opts.TasksetDir, opts.Registry = "", ""
	if _, err := PrepareTaskArtifacts(t.Context(), opts, client); err != nil {
		t.Fatal(err)
	}
	if client.submissions != 3 || client.uploads != 3 || client.digests != 6 {
		t.Fatalf("ready manifest must reuse images with independent digest checks: %+v", client)
	}
	raw, err := os.ReadFile(opts.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]json.RawMessage
	if err := json.Unmarshal(raw, &stored); err != nil || len(stored) != 2 {
		t.Fatalf("manifest must have only schema_version and tasks: %s, %v", raw, err)
	}
	var tasks []map[string]json.RawMessage
	if err := json.Unmarshal(stored["tasks"], &tasks); err != nil || len(tasks[0]) != 4 || tasks[0]["_cli"] == nil {
		t.Fatalf("CLI metadata must be nested: %s, %v", raw, err)
	}
}

func TestTaskArtifactsCanaryFailurePreventsRemainingSubmissions(t *testing.T) {
	for _, failure := range []string{"submission", "build", "digest"} {
		t.Run(failure, func(t *testing.T) {
			opts := artifactOptions(t)
			for _, name := range []string{"first", "second", "third"} {
				writeArtifactArchive(t, filepath.Join(opts.TasksetDir, name+".tar.gz"),
					map[string]string{"environment/marker.txt": name})
			}
			client := &fakeArtifactClient{}
			switch failure {
			case "submission":
				client.scheduleErr = errors.New("submission failed")
			case "build":
				client.status = "Failed"
			case "digest":
				client.digestErr = errors.New("verification failed")
			}
			_, err := PrepareTaskArtifacts(t.Context(), opts, client)
			if err == nil || client.uploads != 1 || client.submissions != 1 {
				t.Fatalf("canary failure must stop fan-out: uploads=%d submissions=%d err=%v",
					client.uploads, client.submissions, err)
			}
			stored, err := loadTaskArtifactManifest(opts.ManifestPath)
			if err != nil || stored.Tasks[0].CLI.Build.LastError == "" ||
				stored.Tasks[1].CLI.Build.RunID != "" || stored.Tasks[2].CLI.Build.RunID != "" {
				t.Fatalf("canary failure must preserve recovery state without later submissions: %+v, %v", stored, err)
			}
			raw, err := os.ReadFile(opts.ManifestPath + ".report.json")
			if err != nil {
				t.Fatal(err)
			}
			var report taskArtifactReport
			if err := json.Unmarshal(raw, &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Tasks) != 3 || report.Counts["pending"] != 2 || report.Tasks[0].LastError == "" {
				t.Fatalf("failure report must retain all tasks and the canary error: %+v", report)
			}
		})
	}
}

func TestTaskArtifactsQueuedSharedContextUsesOneRun(t *testing.T) {
	opts := artifactOptions(t)
	opts.Concurrency = 1
	writeArtifactArchive(t, filepath.Join(opts.TasksetDir, "canary.tar.gz"), nil)
	for _, name := range []string{"second", "third"} {
		writeArtifactArchive(t, filepath.Join(opts.TasksetDir, name+".tar.gz"),
			map[string]string{"environment/marker.txt": "shared non-canary context"})
	}
	client := &fakeArtifactClient{}
	manifest, err := PrepareTaskArtifacts(t.Context(), opts, client)
	if err != nil || client.submissions != 2 || len(manifest.Tasks) != 3 {
		t.Fatalf("queued shared context must not submit duplicate builds: %+v, %v", client, err)
	}
	if manifest.Tasks[1].CLI.Build.RunID != manifest.Tasks[2].CLI.Build.RunID ||
		manifest.Tasks[1].AcrImagePath != manifest.Tasks[2].AcrImagePath {
		t.Fatalf("shared tasks must retain the same run and verified image: %+v", manifest)
	}
}

func TestTaskArtifactsFailureStillRecordsOtherSubmittedBuilds(t *testing.T) {
	for _, failure := range []string{"submission", "build"} {
		t.Run(failure, func(t *testing.T) {
			opts := artifactOptions(t)
			opts.Concurrency = 1
			for _, name := range []string{"first", "second", "third", "fourth"} {
				writeArtifactArchive(t, filepath.Join(opts.TasksetDir, name+".tar.gz"),
					map[string]string{"environment/marker.txt": name})
			}
			client := &fakeArtifactClient{}
			if failure == "submission" {
				client.scheduleErr, client.failSubmission = errors.New("submission failed"), 3
			} else {
				client.runStatuses = map[string]string{"run-xx": "Failed"}
			}
			_, err := PrepareTaskArtifacts(t.Context(), opts, client)
			if err == nil {
				t.Fatal("expected fan-out failure")
			}
			stored, err := loadTaskArtifactManifest(opts.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "submission" {
				if client.submissions != 3 || stored.Tasks[1].AcrImagePath == "" ||
					stored.Tasks[2].CLI.Build.Status != "submission_unknown" || stored.Tasks[3].CLI.Build.RunID != "" {
					t.Fatalf("submission failure must retain and monitor earlier runs: %+v", stored)
				}
			} else if client.submissions != 4 || stored.Tasks[1].CLI.Build.LastError == "" ||
				stored.Tasks[2].AcrImagePath == "" || stored.Tasks[3].AcrImagePath == "" {
				t.Fatalf("one failed build must not prevent recording other build results: %+v", stored)
			}
		})
	}
}

func TestTaskArtifactsReadyPeerDoesNotReplaceUnresolvedRun(t *testing.T) {
	for _, status := range []string{"submission_unknown", "Failed"} {
		t.Run(status, func(t *testing.T) {
			opts := artifactOptions(t)
			for _, name := range []string{"first", "second"} {
				writeArtifactArchive(t, filepath.Join(opts.TasksetDir, name+".tar.gz"), nil)
			}
			client := &fakeArtifactClient{}
			manifest, err := PrepareTaskArtifacts(t.Context(), opts, client)
			if err != nil {
				t.Fatal(err)
			}
			manifest.Tasks[1].AcrImagePath = ""
			manifest.Tasks[1].CLI.ImagePath, manifest.Tasks[1].CLI.ImageDigest = "", ""
			manifest.Tasks[1].CLI.Build.Status = status
			manifest.Tasks[1].CLI.Build.RunID = ""
			if status == "Failed" {
				manifest.Tasks[1].CLI.Build.RunID = "different-failed-run"
				client.status = "Failed"
			}
			writeArtifactManifest(t, opts.ManifestPath, manifest)
			beforeUploads, beforeSubmissions := client.uploads, client.submissions
			_, err = PrepareTaskArtifacts(t.Context(), opts, client)
			if err == nil || client.uploads != beforeUploads || client.submissions != beforeSubmissions {
				t.Fatalf("a ready peer must not mask an unresolved submission or failed run: %v", err)
			}
			stored, err := loadTaskArtifactManifest(opts.ManifestPath)
			if err != nil || stored.Tasks[1].AcrImagePath != "" ||
				stored.Tasks[1].CLI.Build.RunID != manifest.Tasks[1].CLI.Build.RunID {
				t.Fatalf("must preserve the unresolved build identity: %+v, %v", stored, err)
			}
		})
	}
}

func TestTaskArtifactsResumeKnownRunAndBlockUnknownSubmission(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "known run", true: "unknown submission"}[unknown], func(t *testing.T) {
			opts := artifactOptions(t)
			opts.Concurrency = 1
			writeArtifactArchive(t, filepath.Join(opts.TasksetDir, "task.tar.gz"), nil)
			client := &fakeArtifactClient{}
			if unknown {
				client.scheduleErr = errors.New("connection lost")
			} else {
				client.status = "Running"
			}
			if unknown {
				if _, err := PrepareTaskArtifacts(t.Context(), opts, client); err == nil {
					t.Fatal("expected submission failure")
				}
			} else {
				j := &artifactJournal{path: opts.ManifestPath, data: TaskArtifactManifest{SchemaVersion: 1}}
				if err := preflightTaskArtifacts(j, opts); err != nil {
					t.Fatal(err)
				}
				if err := j.save(); err != nil {
					t.Fatal(err)
				}
			}
			stored, err := loadTaskArtifactManifest(opts.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			// Seed an already-submitted run deterministically, without a timing-based interruption.
			if !unknown {
				stored.Tasks[0].CLI.Build.RunID = "existing"
				stored.Tasks[0].CLI.Build.RegistryID = testArtifactRegistry
				stored.Tasks[0].CLI.Build.Status = "Running"
				writeArtifactManifest(t, opts.ManifestPath, stored)
			}
			beforeUploads, beforeSubmissions := client.uploads, client.submissions
			client.scheduleErr, client.status = nil, "Succeeded"
			opts.TasksetDir, opts.Registry = "", ""
			_, err = PrepareTaskArtifacts(t.Context(), opts, client)
			if unknown && (err == nil || !strings.Contains(err.Error(), "unknown")) {
				t.Fatalf("must require reconciliation, got %v", err)
			}
			if !unknown && err != nil {
				t.Fatal(err)
			}
			if client.uploads != beforeUploads || client.submissions != beforeSubmissions {
				t.Fatal("resume must not upload or submit another build")
			}
		})
	}
}

func TestTaskArtifactsFailPreflightBeforeCloudAndLockConcurrentWriters(t *testing.T) {
	opts := artifactOptions(t)
	writeArtifactArchive(t, filepath.Join(opts.TasksetDir, "first.tar.gz"), nil)
	writeArtifactArchive(t, filepath.Join(opts.TasksetDir, "invalid.tar.gz"), nil,
		&tar.Header{Name: "../escape", Typeflag: tar.TypeReg})
	client := &fakeArtifactClient{}
	if _, err := PrepareTaskArtifacts(t.Context(), opts, client); err == nil || client.uploads != 0 || client.digests != 0 {
		t.Fatalf("all archives must pass preflight before cloud operations: %v", err)
	}
	lock := flock.New(opts.ManifestPath + ".lock")
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	if _, err := PrepareTaskArtifacts(t.Context(), opts, client); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("second process must not modify the same manifest: %v", err)
	}
}

func TestTaskArtifactsContextChangesAndSharedImageReuse(t *testing.T) {
	opts := artifactOptions(t)
	first := filepath.Join(opts.TasksetDir, "first.tar.gz")
	second := filepath.Join(opts.TasksetDir, "second.tar.gz")
	writeArtifactArchive(t, first, nil)
	writeArtifactArchive(t, second, nil)
	client := &fakeArtifactClient{}
	manifest, err := PrepareTaskArtifacts(t.Context(), opts, client)
	if err != nil || client.submissions != 1 || manifest.Tasks[0].AcrImagePath != manifest.Tasks[1].AcrImagePath {
		t.Fatalf("identical contexts must share one build: %v, %+v", err, client)
	}
	writeArtifactArchive(t, first, map[string]string{"tests/test.sh": "changed verifier"})
	if _, err := PrepareTaskArtifacts(t.Context(), opts, client); err != nil || client.submissions != 1 {
		t.Fatalf("verifier-only changes must reuse the image: %v, %+v", err, client)
	}
	writeArtifactArchive(t, first, map[string]string{"environment/marker.txt": "changed context"})
	changed, err := PrepareTaskArtifacts(t.Context(), opts, client)
	if err != nil || client.submissions != 2 || changed.Tasks[0].AcrImagePath == manifest.Tasks[0].AcrImagePath {
		t.Fatalf("changed context must invalidate a CLI-built image: %v, %+v", err, client)
	}
	client.digest = "sha256:" + strings.Repeat("b", 64)
	if _, err := PrepareTaskArtifacts(t.Context(), opts, client); err == nil || client.submissions != 2 {
		t.Fatalf("a retagged image must fail without silently rebuilding: %v", err)
	}
}

func TestHarborArchiveCompressedSizeLimit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		size      int64
		wantError bool
	}{
		{name: "above old limit", size: (8 << 20) + 1},
		{name: "at backend limit", size: 64 << 20},
		{name: "above backend limit", size: (64 << 20) + 1, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "task-*.tar.gz")
			if err != nil {
				t.Fatal(err)
			}
			if err := f.Truncate(tc.size); err != nil {
				_ = f.Close()
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			raw, err := readHarborArchive(f.Name())
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "at most 67108864 bytes") {
					t.Fatalf("expected backend size limit error, got %v", err)
				}
			} else if err != nil || int64(len(raw)) != tc.size {
				t.Fatalf("expected %d bytes to be accepted, got %d: %v", tc.size, len(raw), err)
			}
		})
	}
}

func TestHarborArchiveContextIdentityAndUnsafeMembers(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "task.tar.gz")
	writeArtifactArchive(t, filename, nil)
	first, err := InspectHarborArchive(filename)
	if err != nil {
		t.Fatal(err)
	}
	writeArtifactArchive(t, filename, map[string]string{"tests/test.sh": "changed verifier"})
	second, err := InspectHarborArchive(filename)
	if err != nil || first.BundleSHA256 == second.BundleSHA256 || first.BuildContextSHA256 != second.BuildContextSHA256 {
		t.Fatalf("verifier changes must not change image identity: %v", err)
	}
	writeArtifactArchive(t, filename, map[string]string{"environment/marker.txt": "changed context"})
	third, err := InspectHarborArchive(filename)
	if err != nil || first.BuildContextSHA256 == third.BuildContextSHA256 {
		t.Fatalf("context changes must change image identity: %v", err)
	}
	for _, header := range []*tar.Header{
		{Name: "../escape", Typeflag: tar.TypeReg},
		{Name: "/absolute", Typeflag: tar.TypeReg},
		{Name: `C:\windows`, Typeflag: tar.TypeReg},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "environment"},
		{Name: "setuid", Typeflag: tar.TypeReg, Mode: 0o4644},
		{Name: "instruction.md", Typeflag: tar.TypeReg},
		{Name: "environment/Dockerfile/child", Typeflag: tar.TypeReg},
	} {
		writeArtifactArchive(t, filename, nil, header)
		if _, err := InspectHarborArchive(filename); err == nil {
			t.Fatalf("unsafe archive member was accepted: %+v", header)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "environment")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("archive inspection must not extract files")
	}
	var large bytes.Buffer
	gz := gzip.NewWriter(&large)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "large", Typeflag: tar.TypeReg, Size: maxHarborExpandedBytes + 1}); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectHarborArchiveBytes(large.Bytes()); err == nil || !strings.Contains(err.Error(), "expanded") {
		t.Fatalf("oversized members must be rejected before reading their payload: %v", err)
	}
}

func TestArtifactACRSubmissionUsesPrivateArchiveAndEnvironmentContext(t *testing.T) {
	oldRun := runAcrBuild
	t.Cleanup(func() { runAcrBuild = oldRun })
	runAcrBuild = func(_ context.Context, args azdexec.RunArgs) (azdexec.RunResult, error) {
		var body map[string]json.RawMessage
		for index, arg := range args.Args {
			if arg == "--body" {
				if err := json.Unmarshal([]byte(args.Args[index+1]), &body); err != nil {
					t.Fatal(err)
				}
			}
		}
		var encoded, source string
		if err := json.Unmarshal(body["encodedTaskContent"], &encoded); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body["sourceLocation"], &source); err != nil {
			t.Fatal(err)
		}
		yaml, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || source != "private/source.tar.gz" ||
			!strings.Contains(string(yaml), "workingDirectory: /workspace/environment\n") ||
			!strings.Contains(string(yaml), "-f Dockerfile .") || strings.Contains(string(yaml), "sig=") {
			t.Fatalf("incorrect private source or Docker context: %s, %v", yaml, err)
		}
		_, err = io.WriteString(args.StdOut, `{"properties":{"runId":"known-run","status":"Queued"}}`)
		return azdexec.RunResult{}, err
	}
	id, err := (ACRTaskArtifactClient{}).Schedule(t.Context(), testArtifactRegistry,
		"private/source.tar.gz", "example.azurecr.io/harbor-tasks:ctx-test")
	if err != nil || id != "known-run" {
		t.Fatalf("incorrect scheduled run: %q, %v", id, err)
	}
}
