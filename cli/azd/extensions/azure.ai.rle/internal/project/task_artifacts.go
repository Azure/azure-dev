// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/gofrs/flock"
)

// TaskArtifactManifest is independent of any RLE environment name, version, or project.
type TaskArtifactManifest struct {
	SchemaVersion int            `json:"schema_version"`
	Tasks         []TaskArtifact `json:"tasks"`
}

// TaskArtifact maps an original archive to an existing or CLI-built ACR image.
type TaskArtifact struct {
	TaskName        string           `json:"task_name"`
	LocalBundlePath string           `json:"local_bundle_path"`
	AcrImagePath    string           `json:"acr_image_path,omitempty"`
	CLI             *TaskArtifactCLI `json:"_cli,omitempty"`
}

// TaskArtifactCLI is optional, derived validation and recovery state, not user input.
type TaskArtifactCLI struct {
	BundleSHA256       string              `json:"bundle_sha256"`
	BuildContextSHA256 string              `json:"build_context_sha256"`
	ImagePath          string              `json:"image_path,omitempty"`
	ImageDigest        string              `json:"image_digest,omitempty"`
	Build              *TaskArtifactBuild  `json:"build,omitempty"`
	BuildHistory       []TaskArtifactBuild `json:"build_history,omitempty"`
}

// TaskArtifactBuild keeps enough identity to resume a known run without another submission.
type TaskArtifactBuild struct {
	RegistryID string `json:"registry_id"`
	ImagePath  string `json:"image_path"`
	RunID      string `json:"run_id,omitempty"`
	Status     string `json:"status"`
	LastError  string `json:"last_error,omitempty"`
}

// ArtifactRun is the remote state required to monitor an owned build.
type ArtifactRun struct {
	Status string
}

// TaskArtifactClient abstracts one-shot ACR uploads/builds and registry verification.
type TaskArtifactClient interface {
	Registry(context.Context, string) (string, error)
	Upload(context.Context, string, []byte) (string, error)
	Schedule(context.Context, string, string, string) (string, error)
	Run(context.Context, string, string) (ArtifactRun, error)
	Digest(context.Context, string) (string, error)
}

// TaskArtifactOptions selects archive preparation without requiring an RLE backend.
type TaskArtifactOptions struct {
	ManifestPath string
	TasksetDir   string
	Registry     string
	Concurrency  int
	Output       io.Writer
	PollInterval time.Duration
	RetryFailed  bool
}

type artifactJournal struct {
	path   string
	data   TaskArtifactManifest
	mu     sync.Mutex
	out    io.Writer
	builds map[string]*sync.Mutex
}

func loadTaskArtifactManifest(filename string) (TaskArtifactManifest, error) {
	var manifest TaskArtifactManifest
	//nolint:gosec // Explicit user-selected manifest, read through a bounded JSON decoder.
	file, err := os.Open(filename)
	if err != nil {
		return manifest, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 16<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, fmt.Errorf("read task manifest %q: %w", filename, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return manifest, fmt.Errorf("task manifest %q must contain one JSON object", filename)
	}
	if manifest.SchemaVersion != 1 {
		return manifest, fmt.Errorf("unsupported task manifest schema_version %d; expected 1", manifest.SchemaVersion)
	}
	return manifest, nil
}

func (j *artifactJournal) save() error {
	return writeTaskArtifactJSON(j.path, j.data)
}

func writeTaskArtifactJSON(filename string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(filename), ".task-artifacts-*.json")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(append(raw, '\n')); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// Recovery state must persist even if the cloud operation's context was canceled.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := osutil.Rename(ctx, name, filename); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		dir, err := os.Open(filepath.Dir(filename))
		if err != nil {
			return err
		}
		defer dir.Close()
		return dir.Sync()
	}
	return nil
}

func (j *artifactJournal) update(index int, mutate func(*TaskArtifact)) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	mutate(&j.data.Tasks[index])
	return j.save()
}

func (j *artifactJournal) task(index int) TaskArtifact {
	j.mu.Lock()
	defer j.mu.Unlock()
	task := j.data.Tasks[index]
	if task.CLI != nil {
		task.CLI = new(*task.CLI)
		if task.CLI.Build != nil {
			task.CLI.Build = new(*task.CLI.Build)
		}
	}
	return task
}

func (j *artifactJournal) log(format string, args ...any) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	_, err := fmt.Fprintf(j.out, format, args...)
	return err
}

func terminalArtifactRun(status string) bool {
	switch status {
	case "Succeeded", "Failed", "Canceled", "Error", "Timeout":
		return true
	default:
		return false
	}
}

func preflightTaskArtifacts(j *artifactJournal, opts TaskArtifactOptions) error {
	if opts.TasksetDir != "" {
		paths, err := filepath.Glob(filepath.Join(opts.TasksetDir, "*.tar.gz"))
		if err != nil {
			return err
		}
		if len(paths) == 0 {
			return fmt.Errorf("taskset directory %q contains no .tar.gz task archives", opts.TasksetDir)
		}
		for _, filename := range paths {
			name := strings.TrimSuffix(filepath.Base(filename), ".tar.gz")
			absolute, err := filepath.Abs(filename)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(filepath.Dir(j.path), absolute)
			if err != nil {
				return err
			}
			found := false
			for index, task := range j.data.Tasks {
				if task.TaskName == name {
					j.data.Tasks[index].LocalBundlePath = filepath.ToSlash(relative)
					found = true
					break
				}
			}
			if !found {
				j.data.Tasks = append(j.data.Tasks, TaskArtifact{
					TaskName: name, LocalBundlePath: filepath.ToSlash(relative),
				})
			}
		}
	}
	if len(j.data.Tasks) == 0 {
		return fmt.Errorf("task manifest must contain at least one task")
	}
	seen := map[string]bool{}
	for index := range j.data.Tasks {
		task := &j.data.Tasks[index]
		if !harborArtifactName.MatchString(task.TaskName) || seen[task.TaskName] || task.LocalBundlePath == "" {
			return fmt.Errorf("task names must be valid and unique, and each task requires local_bundle_path")
		}
		seen[task.TaskName] = true
		if task.AcrImagePath != "" && !validArtifactImage(task.AcrImagePath) {
			return fmt.Errorf("task %q requires a tagged or digest-pinned ACR image reference", task.TaskName)
		}
		info, err := InspectHarborArchive(artifactBundlePath(j.path, task.LocalBundlePath))
		if err != nil {
			return err
		}
		previous := task.CLI
		next := &TaskArtifactCLI{BundleSHA256: info.BundleSHA256, BuildContextSHA256: info.BuildContextSHA256}
		if previous != nil {
			next.BuildHistory = previous.BuildHistory
			build := previous.Build
			contextChanged := previous.BuildContextSHA256 != info.BuildContextSHA256
			imageChanged := previous.ImagePath != task.AcrImagePath
			if build != nil && build.Status != "pending" && !terminalArtifactRun(build.Status) &&
				(contextChanged || imageChanged) {
				return fmt.Errorf("task %q has an unfinished recorded build %s; reconcile it before changing inputs",
					task.TaskName, build.RunID)
			}
			if build != nil && contextChanged && terminalArtifactRun(build.Status) {
				next.BuildHistory = append(append([]TaskArtifactBuild(nil), previous.BuildHistory...), *build)
			}
			if build != nil && contextChanged && task.AcrImagePath == build.ImagePath && !imageChanged {
				task.AcrImagePath = ""
			} else if !imageChanged && !contextChanged {
				next.ImagePath, next.ImageDigest, next.Build = previous.ImagePath, previous.ImageDigest, build
			}
		}
		task.CLI = next
		if build := next.Build; build != nil {
			host, _, _ := strings.Cut(build.ImagePath, "/")
			registryName := strings.TrimSuffix(strings.ToLower(host), ".azurecr.io")
			if !validArtifactImage(build.ImagePath) ||
				(build.RegistryID != "" && (!artifactRegistryIDPattern.MatchString(build.RegistryID) ||
					!strings.EqualFold(filepath.Base(build.RegistryID), registryName))) {
				return fmt.Errorf("task %q has invalid or mismatched _cli build identity", task.TaskName)
			}
		}
		if task.AcrImagePath == "" {
			if next.Build == nil {
				host := strings.ToLower(strings.Trim(strings.TrimSpace(opts.Registry), "/"))
				image := host + "/harbor-tasks:ctx-" + info.BuildContextSHA256
				if !validArtifactImage(image) {
					return fmt.Errorf("task %q has no image; set AZURE_CONTAINER_REGISTRY_ENDPOINT=<registry>.azurecr.io",
						task.TaskName)
				}
				next.Build = &TaskArtifactBuild{ImagePath: image, Status: "pending"}
			}
		}
	}
	return nil
}

// PrepareTaskArtifacts validates every input before cloud operations, then verifies or builds images.
// A manifest lock and atomic updates preserve each run ID and successful image across interruptions.
func PrepareTaskArtifacts(
	ctx context.Context, opts TaskArtifactOptions, client TaskArtifactClient,
) (manifest TaskArtifactManifest, err error) {
	if opts.ManifestPath == "" || opts.Concurrency < 1 || opts.Concurrency > 32 {
		return manifest, fmt.Errorf("specify --task-manifest and --concurrency between 1 and 32")
	}
	absolute, err := filepath.Abs(opts.ManifestPath)
	if err != nil {
		return manifest, err
	}
	lock := flock.New(absolute + ".lock")
	locked, err := lock.TryLock()
	if err != nil || !locked {
		return manifest, fmt.Errorf("task manifest %q is locked or unavailable: %w", absolute, errors.Join(
			err, errors.New("another publisher may be using this file")))
	}
	defer func() { err = errors.Join(err, lock.Unlock()) }()
	j := &artifactJournal{path: absolute, out: opts.Output, builds: map[string]*sync.Mutex{}}
	if j.out == nil {
		j.out = io.Discard
	}
	j.data, err = loadTaskArtifactManifest(absolute)
	if errors.Is(err, os.ErrNotExist) && opts.TasksetDir != "" {
		j.data, err = TaskArtifactManifest{SchemaVersion: 1}, nil
	}
	if err != nil {
		return manifest, err
	}
	defer func() {
		manifest = j.data
		err = errors.Join(err, j.report())
	}()
	if err := preflightTaskArtifacts(j, opts); err != nil {
		return manifest, err
	}
	if err := j.save(); err != nil {
		return manifest, err
	}
	selected, err := retryFailedTaskArtifacts(ctx, j, opts.RetryFailed, client)
	if err != nil {
		return j.data, err
	}
	eligible := func(index int) bool {
		return !opts.RetryFailed || selected[index]
	}
	for _, task := range j.data.Tasks {
		if task.AcrImagePath == "" && (!opts.RetryFailed || selectedTaskRetry(task)) {
			if err := j.log("This workflow uses paid cloud resources and may incur charges.\n"); err != nil {
				return manifest, err
			}
			break
		}
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 5 * time.Second
	}
	if err := runArtifactStage(ctx, j, opts.Concurrency, true, func(index int) error {
		task := j.task(index)
		if task.AcrImagePath == "" {
			return nil
		}
		return verifyTaskArtifact(ctx, j, index, client)
	}); err != nil {
		return j.data, err
	}
	for index := range j.data.Tasks {
		if !eligible(index) || j.task(index).AcrImagePath != "" {
			continue
		}
		if err := j.log("Task %s: verifying canary build before submitting remaining builds\n",
			j.task(index).TaskName); err != nil {
			return j.data, err
		}
		if err := submitTaskArtifact(ctx, j, index, client); err != nil {
			return j.data, artifactTaskError(j, index, err)
		}
		if err := monitorTaskArtifact(ctx, j, index, opts.PollInterval, client); err != nil {
			return j.data, artifactTaskError(j, index, err)
		}
		break
	}
	submitErr := runArtifactStage(ctx, j, opts.Concurrency, true, func(index int) error {
		if !eligible(index) || j.task(index).AcrImagePath != "" {
			return nil
		}
		return submitTaskArtifact(ctx, j, index, client)
	})
	// Monitoring is independent of upload slots; all recorded builds can already be queued in ACR.
	monitorErr := runArtifactStage(ctx, j, 8, false, func(index int) error {
		task := j.task(index)
		if !eligible(index) || task.AcrImagePath != "" || task.CLI.Build.RunID == "" {
			return nil
		}
		return monitorTaskArtifact(ctx, j, index, opts.PollInterval, client)
	})
	return j.data, errors.Join(submitErr, monitorErr)
}

func artifactTaskError(j *artifactJournal, index int, err error) error {
	persistErr := j.update(index, func(task *TaskArtifact) {
		if task.CLI.Build != nil {
			task.CLI.Build.LastError = redactArtifactURLs(err.Error())
		}
	})
	return fmt.Errorf("task %q: %w", j.task(index).TaskName, errors.Join(err, persistErr))
}

func runArtifactStage(
	ctx context.Context, j *artifactJournal, concurrency int, stopOnError bool, action func(int) error,
) error {
	jobs, stop := make(chan int), make(chan struct{})
	failures := make(chan error, len(j.data.Tasks))
	var workers sync.WaitGroup
	var once sync.Once
	for range min(concurrency, len(j.data.Tasks)) {
		workers.Go(func() {
			for index := range jobs {
				select {
				case <-stop:
					return
				default:
				}
				if err := action(index); err != nil {
					failures <- artifactTaskError(j, index, err)
					if stopOnError {
						once.Do(func() { close(stop) })
					}
				}
			}
		})
	}
send:
	for index := range j.data.Tasks {
		select {
		case jobs <- index:
		case <-stop:
			break send
		case <-ctx.Done():
			failures <- ctx.Err()
			break send
		}
	}
	close(jobs)
	workers.Wait()
	close(failures)
	var allErrors []error
	for failure := range failures {
		allErrors = append(allErrors, failure)
	}
	return errors.Join(allErrors...)
}

func verifyTaskArtifact(ctx context.Context, j *artifactJournal, index int, client TaskArtifactClient) error {
	task := j.task(index)
	digest, err := client.Digest(ctx, task.AcrImagePath)
	if err != nil {
		return err
	}
	if task.CLI.ImageDigest != "" && task.CLI.ImageDigest != digest {
		return fmt.Errorf("image tag changed since verification; pin the intended digest or remove stale _cli metadata")
	}
	return j.update(index, func(t *TaskArtifact) {
		t.CLI.ImagePath, t.CLI.ImageDigest = t.AcrImagePath, digest
	})
}

func (j *artifactJournal) buildLock(image string) *sync.Mutex {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.builds[image] == nil {
		j.builds[image] = &sync.Mutex{}
	}
	return j.builds[image]
}

func reuseReadyTaskArtifact(
	ctx context.Context, j *artifactJournal, index int, client TaskArtifactClient,
) (bool, error) {
	task := j.task(index)
	if task.CLI.Build.RunID == "" && task.CLI.Build.Status != "pending" {
		return false, nil
	}
	j.mu.Lock()
	var digest string
	var sharedBuild *TaskArtifactBuild
	for _, other := range j.data.Tasks {
		if other.TaskName != task.TaskName && other.AcrImagePath == task.CLI.Build.ImagePath && other.CLI != nil {
			if task.CLI.Build.RunID != "" &&
				(other.CLI.Build == nil || other.CLI.Build.RunID != task.CLI.Build.RunID) {
				continue
			}
			digest = other.CLI.ImageDigest
			if other.CLI.Build != nil {
				sharedBuild = new(*other.CLI.Build)
			}
			break
		}
	}
	j.mu.Unlock()
	if digest == "" {
		return false, nil
	}
	actual, err := client.Digest(ctx, task.CLI.Build.ImagePath)
	if err != nil {
		return false, err
	}
	if actual != digest {
		return false, fmt.Errorf("shared task image changed during preparation")
	}
	if sharedBuild == nil {
		sharedBuild = &TaskArtifactBuild{ImagePath: task.CLI.Build.ImagePath, Status: "Succeeded"}
	}
	return true, j.update(index, func(t *TaskArtifact) {
		t.AcrImagePath = task.CLI.Build.ImagePath
		t.CLI.ImagePath, t.CLI.ImageDigest, t.CLI.Build = t.AcrImagePath, digest, sharedBuild
	})
}

func submitTaskArtifact(ctx context.Context, j *artifactJournal, index int, client TaskArtifactClient) error {
	task := j.task(index)
	build := *task.CLI.Build
	buildLock := j.buildLock(build.ImagePath)
	buildLock.Lock()
	defer buildLock.Unlock()
	if reused, err := reuseReadyTaskArtifact(ctx, j, index, client); reused || err != nil {
		return err
	}
	if build.RunID == "" && build.Status == "pending" {
		j.mu.Lock()
		var sharedBuild *TaskArtifactBuild
		var unresolved string
		for _, other := range j.data.Tasks {
			if other.TaskName != task.TaskName && other.CLI != nil && other.CLI.Build != nil &&
				other.CLI.Build.ImagePath == build.ImagePath && other.CLI.Build.Status != "pending" {
				if len(task.CLI.BuildHistory) > 0 && terminalArtifactRun(other.CLI.Build.Status) &&
					other.CLI.Build.Status != "Succeeded" {
					continue
				}
				if other.CLI.Build.RunID != "" {
					sharedBuild = new(*other.CLI.Build)
					break
				}
				unresolved = other.CLI.Build.Status
			}
		}
		j.mu.Unlock()
		if sharedBuild != nil {
			return j.update(index, func(t *TaskArtifact) {
				t.CLI.Build = sharedBuild
			})
		}
		if unresolved != "" {
			return fmt.Errorf("another task recorded this image build as %s; reconcile it before submitting again", unresolved)
		}
	}
	if build.RegistryID == "" {
		host, _, _ := strings.Cut(build.ImagePath, "/")
		registryID, err := client.Registry(ctx, host)
		if err != nil {
			return err
		}
		build.RegistryID = registryID
		if err := j.update(index, func(t *TaskArtifact) { t.CLI.Build = new(build) }); err != nil {
			return err
		}
	}
	if build.RunID == "" {
		if build.Status != "pending" {
			return fmt.Errorf("submission outcome is unknown; attach the existing ACR run_id before retrying")
		}
		raw, err := ReadHarborArtifactBundle(j.path, task)
		if err != nil {
			return err
		}
		source, err := client.Upload(ctx, build.RegistryID, raw)
		if err != nil {
			return err
		}
		build.Status = "submitting"
		if err := j.update(index, func(t *TaskArtifact) { t.CLI.Build = new(build) }); err != nil {
			return err
		}
		build.RunID, err = client.Schedule(ctx, build.RegistryID, source, build.ImagePath)
		if err != nil || build.RunID == "" {
			build.Status = "submission_unknown"
			persistErr := j.update(index, func(t *TaskArtifact) { t.CLI.Build = new(build) })
			return errors.Join(err, persistErr, errors.New("submission outcome unknown; reconcile ACR runs before retrying"))
		}
		build.Status = "Queued"
		if err := j.update(index, func(t *TaskArtifact) { t.CLI.Build = new(build) }); err != nil {
			return fmt.Errorf("ACR run %s was submitted but could not be recorded: %w", build.RunID, err)
		}
		if err := j.log("Task %s: ACR run %s\n", task.TaskName, build.RunID); err != nil {
			return err
		}
	}
	return nil
}

func monitorTaskArtifact(
	ctx context.Context, j *artifactJournal, index int, poll time.Duration, client TaskArtifactClient,
) error {
	task := j.task(index)
	build := *task.CLI.Build
	buildLock := j.buildLock(build.ImagePath)
	buildLock.Lock()
	defer buildLock.Unlock()
	if reused, err := reuseReadyTaskArtifact(ctx, j, index, client); reused || err != nil {
		return err
	}
	monitorCtx, cancel := context.WithTimeout(ctx, 17*time.Minute)
	defer cancel()
	for {
		run, err := client.Run(monitorCtx, build.RegistryID, build.RunID)
		if err != nil {
			return fmt.Errorf("monitor ACR run %s (may still be active; rerun this manifest to resume): %w", build.RunID, err)
		}
		if run.Status != build.Status {
			build.Status = run.Status
			if err := j.update(index, func(t *TaskArtifact) { t.CLI.Build = new(build) }); err != nil {
				return err
			}
		}
		if terminalArtifactRun(run.Status) {
			if run.Status != "Succeeded" {
				return fmt.Errorf("ACR run %s finished with %s; inspect its logs before retrying", build.RunID, run.Status)
			}
			break
		}
		select {
		case <-monitorCtx.Done():
			return fmt.Errorf("ACR run %s may still be active; rerun this manifest to resume: %w",
				build.RunID, monitorCtx.Err())
		case <-time.After(poll):
		}
	}
	digest, err := client.Digest(ctx, build.ImagePath)
	if err != nil {
		return err
	}
	if err := j.update(index, func(t *TaskArtifact) {
		t.AcrImagePath = build.ImagePath
		t.CLI.ImagePath, t.CLI.ImageDigest = build.ImagePath, digest
		t.CLI.Build.LastError = ""
	}); err != nil {
		return err
	}
	return j.log("Task %s: image ready\n", task.TaskName)
}
