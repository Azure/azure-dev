// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type rlePublishFlags struct {
	dockerfile   string
	taskDirs     []string
	remote       bool
	taskManifest string
	tasksetDir   string
	prepareOnly  bool
	concurrency  int
	retryFailed  bool
}

type publishAction struct {
	cmd   *cobra.Command
	flags *rlePublishFlags
}

func newPublishCommand() *cobra.Command {
	flags := &rlePublishFlags{}

	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Prepare task artifacts or build, push, and publish the RLE release declared in rle.toml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&publishAction{cmd: cmd, flags: flags}).Run()
		},
	}

	cmd.Flags().StringVar(&flags.dockerfile, "dockerfile", "",
		"Dockerfile path relative to the current folder. Defaults to Dockerfile at the source root or server/Dockerfile.")
	cmd.Flags().BoolVar(&flags.remote, "remote", false,
		"Build and push images with ACR Tasks instead of the local container runtime. Uses paid cloud build resources.")
	cmd.Flags().StringArrayVar(&flags.taskDirs, "task-dir", nil,
		"Harbor task directory to publish with this environment version. Repeat for several tasks. "+
			"Each task image is pushed to ACR and its bundle uploaded to RLE; select its name with rollout --task or --task-file. "+
			"A Harbor environment has no environment-level image.")
	cmd.Flags().StringVar(&flags.taskManifest, "task-manifest", "",
		"Harbor task artifact JSON manifest. Existing ACR images are verified, not rebuilt; _cli metadata is optional.")
	cmd.Flags().StringVar(&flags.tasksetDir, "taskset-dir", "",
		"Directory of full Harbor .tar.gz task archives. Builds missing images in ACR without extracting locally. "+
			"Defaults --task-manifest to task-artifacts.json.")
	cmd.Flags().BoolVar(&flags.prepareOnly, "prepare-only", false,
		"Prepare or resume task artifacts without contacting RLE or requiring rle.toml.")
	cmd.Flags().IntVar(&flags.concurrency, "concurrency", 32,
		"Maximum concurrent archive uploads and submission requests (1-32). ACR controls build execution concurrency.")
	cmd.Flags().BoolVar(&flags.retryFailed, "retry-failed", false,
		"With --prepare-only, retry only terminal failed builds. Reuses successes; does not submit pending or uncertain tasks.")
	return cmd
}

var newTaskArtifactClient = func() project.TaskArtifactClient { return project.ACRTaskArtifactClient{} }

func (a *publishAction) Run() error {
	if a.flags.tasksetDir != "" && a.flags.taskManifest == "" {
		a.flags.taskManifest = "task-artifacts.json"
	}
	usingManifest := a.flags.taskManifest != ""
	if a.flags.retryFailed && !a.flags.prepareOnly {
		return fmt.Errorf("--retry-failed requires --prepare-only")
	}
	if (len(a.flags.taskDirs) > 0 && usingManifest) ||
		(usingManifest && a.flags.dockerfile != "") || (a.flags.prepareOnly && !usingManifest) ||
		(!usingManifest && a.cmd.Flags().Changed("concurrency")) {
		return fmt.Errorf("use --task-dir or --task-manifest/--taskset-dir, not both; " +
			"--prepare-only and --concurrency require a task manifest; --dockerfile is for source-directory builds")
	}
	if a.flags.prepareOnly {
		manifest, err := a.prepareTaskArtifactManifest()
		if err != nil {
			return err
		}
		if a.flags.retryFailed {
			_, err = fmt.Fprintf(a.cmd.OutOrStdout(), "Failed-build retry pass complete; inspect %s.report.json "+
				"for remaining tasks before publishing.\n", a.flags.taskManifest)
		} else {
			_, err = fmt.Fprintf(a.cmd.OutOrStdout(), "Prepared %d Harbor task(s); manifest: %s\n",
				len(manifest.Tasks), a.flags.taskManifest)
		}
		return err
	}
	config, projectEndpoint, client, creating, err := resolvePublishTarget(a.cmd.Context())
	if err != nil {
		return err
	}

	image := ""
	if !usingManifest {
		image, err = resolvePublishImage(config.Rle.Name, config.Rle.Version, projectEndpoint)
		if err != nil {
			return err
		}
		if !project.IsAcrImageReference(image) {
			return &azdext.LocalError{
				Message:    fmt.Sprintf("RLE publish image must be an ACR image reference, got %q.", image),
				Code:       "rle_acr_image_required",
				Category:   azdext.LocalErrorCategoryUser,
				Suggestion: "Set AZURE_CONTAINER_REGISTRY_ENDPOINT=<registry>.azurecr.io, then run publish again.",
			}
		}
	}
	var tasks []harborTask
	if usingManifest {
		if config.Task == nil || config.Task.Format != project.TaskFormatHarbor {
			return fmt.Errorf("--task-manifest requires [task] format = \"harbor\" in rle.toml")
		}
		if tasks, err = a.prepareManifestHarborTasks(client); err != nil {
			return err
		}
	} else if len(a.flags.taskDirs) > 0 {
		if tasks, err = a.prepareHarborTasks(client, projectEndpoint); err != nil {
			return err
		}
		image = ""
	} else {
		if err := project.BuildAndPushImage(
			a.cmd.Context(),
			a.cmd.OutOrStdout(),
			a.cmd.ErrOrStderr(),
			image,
			project.BuildOptions{
				Source:     ".",
				Dockerfile: a.flags.dockerfile,
			},
			a.flags.remote,
		); err != nil {
			return err
		}
	}
	request := buildEnvironmentCreateRequest(config, image)
	request.Tasks = tasks

	action := "Publishing"
	if creating {
		action = "Creating"
	}
	if _, err := fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"%s environment '%s' version %s (image=%s) ...\n",
		action,
		config.Rle.Name,
		config.Rle.Version,
		image,
	); err != nil {
		return err
	}
	environment, err := client.createV1Environment(a.cmd.Context(), request)
	if err != nil {
		return serviceError(err)
	}
	if err := verifyPublishedEnvironment(config, environment); err != nil {
		return err
	}
	if len(tasks) > 0 {
		fmt.Fprintf(a.cmd.OutOrStdout(), "Published %d Harbor task(s) for version %s\n",
			len(tasks), environment.Version)
	}

	if _, err := fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"\nPublished environment '%s' version %s\n",
		environment.Name,
		environment.Version,
	); err != nil {
		return err
	}
	body, err := json.MarshalIndent(environmentOutput{
		EnvironmentId:          environment.Id,
		EnvironmentVersion:     environment.Version,
		EnvironmentName:        environment.Name,
		FoundryProjectEndpoint: projectEndpoint,
		AcrImage:               environment.AcrImagePath,
		Type:                   environment.Harness.Type,
		Endpoint:               stringFromPointer(environment.Harness.Endpoint),
		Protocol:               environment.Harness.Protocol,
		SchemaVersion:          environment.SchemaVersion,
		Model:                  environment.Model,
		Train:                  environment.Train,
		Rollout:                environment.Rollout,
		CreatedAt:              environment.CreatedAt,
		UpdatedAt:              environment.UpdatedAt,
	}, "", "  ")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(a.cmd.OutOrStdout(), string(body)); err != nil {
		return err
	}
	return nil
}

func (a *publishAction) prepareTaskArtifactManifest() (project.TaskArtifactManifest, error) {
	return project.PrepareTaskArtifacts(a.cmd.Context(), project.TaskArtifactOptions{
		ManifestPath: a.flags.taskManifest, TasksetDir: a.flags.tasksetDir,
		Registry: os.Getenv("AZURE_CONTAINER_REGISTRY_ENDPOINT"), Concurrency: a.flags.concurrency,
		Output: a.cmd.ErrOrStderr(), RetryFailed: a.flags.retryFailed,
	}, newTaskArtifactClient())
}

func (a *publishAction) prepareManifestHarborTasks(client *rleClient) ([]harborTask, error) {
	manifest, err := a.prepareTaskArtifactManifest()
	if err != nil {
		return nil, err
	}
	tasks := make([]harborTask, len(manifest.Tasks))
	jobs, stop := make(chan int), make(chan struct{})
	failures := make(chan error, len(manifest.Tasks))
	var workers sync.WaitGroup
	var once sync.Once
	for range min(a.flags.concurrency, len(manifest.Tasks)) {
		workers.Go(func() {
			for index := range jobs {
				select {
				case <-stop:
					return
				default:
				}
				task, err := uploadManifestHarborTask(a.cmd.Context(), client, a.flags.taskManifest, manifest.Tasks[index])
				if err != nil {
					failures <- fmt.Errorf("task %q: %w", manifest.Tasks[index].TaskName, err)
					once.Do(func() { close(stop) })
					continue
				}
				tasks[index] = task
			}
		})
	}
send:
	for index := range manifest.Tasks {
		select {
		case jobs <- index:
		case <-stop:
			break send
		case <-a.cmd.Context().Done():
			failures <- a.cmd.Context().Err()
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
	return tasks, errors.Join(allErrors...)
}

func uploadManifestHarborTask(
	ctx context.Context, client *rleClient, manifestPath string, artifact project.TaskArtifact,
) (harborTask, error) {
	image, err := project.PinnedTaskArtifactImage(artifact)
	if err != nil {
		return harborTask{}, err
	}
	raw, err := project.ReadHarborArtifactBundle(manifestPath, artifact)
	if err != nil {
		return harborTask{}, err
	}
	bundle, err := client.uploadHarborBundle(ctx, raw)
	if err != nil {
		return harborTask{}, serviceError(err)
	}
	return harborTask{Name: artifact.TaskName, AcrImagePath: image, Bundle: bundle}, nil
}

func resolvePublishTarget(
	ctx context.Context,
) (project.RleConfig, string, *rleClient, bool, error) {
	config, err := project.LoadRleConfig(".")
	if err != nil {
		return project.RleConfig{}, "", nil, false, err
	}

	projectEndpoint, err := resolveFoundryProjectEndpoint()
	if err != nil {
		return project.RleConfig{}, "", nil, false, err
	}
	if projectEndpoint == "" {
		return project.RleConfig{}, "", nil, false, &azdext.LocalError{
			Message:  "Foundry project endpoint is required for publish.",
			Code:     "rle_project_required",
			Category: azdext.LocalErrorCategoryUser,
			Suggestion: fmt.Sprintf(
				"Set %s=https://<account>.services.ai.azure.com/api/projects/<project>.",
				foundryProjectEndpointEnvVar,
			),
		}
	}

	client, err := createRleClient(projectEndpoint)
	if err != nil {
		return project.RleConfig{}, "", nil, false, err
	}
	_, err = client.getEnvironmentByName(ctx, config.Rle.Name)
	creating := false
	switch {
	case err == nil:
	case isRleNotFound(err):
		creating = true
		if err := project.ValidateInitialRleVersion(config.Rle.Version); err != nil {
			return project.RleConfig{}, "", nil, false, err
		}
	default:
		return project.RleConfig{}, "", nil, false, serviceError(err)
	}

	return config, projectEndpoint, client, creating, nil
}

func buildEnvironmentCreateRequest(
	config project.RleConfig,
	image string,
) v1EnvironmentRequest {
	return v1EnvironmentRequest{
		Name:         config.Rle.Name,
		AcrImagePath: image,
		Version:      config.Rle.Version,
		Harness: rleHarness{
			Type:     string(config.Harness.Type),
			Endpoint: config.Harness.Endpoint,
			Protocol: string(config.Harness.Protocol),
			Name:     config.Harness.Name,
		},
		Task:          harborTaskFormat(config),
		SchemaVersion: config.SchemaVersion,
		Model:         config.Model,
		Train:         config.Train,
		Rollout:       config.Rollout,
	}
}

func verifyPublishedEnvironment(config project.RleConfig, environment *environmentResource) error {
	manifest := config.Rle
	harness := config.Harness
	if environment == nil {
		return publishedEnvironmentMismatchError(
			"RLE service did not return the published environment.",
			"Check the RLE service response, then retry.",
		)
	}
	if environment.Name != manifest.Name || environment.Version != manifest.Version {
		return publishedEnvironmentMismatchError(
			fmt.Sprintf(
				"RLE service returned %s/%s, but rle.toml declares %s/%s.",
				environment.Name,
				environment.Version,
				manifest.Name,
				manifest.Version,
			),
			"Resolve the concurrent or deleted-version conflict, then publish the version declared in rle.toml.",
		)
	}
	if environment.Harness.Type != string(harness.Type) {
		return publishedEnvironmentMismatchError(
			fmt.Sprintf(
				"RLE service returned harness type %s, but rle.toml declares %s.",
				environment.Harness.Type,
				harness.Type,
			),
			"Check the RLE service response and retry.",
		)
	}
	if harness.Endpoint != nil &&
		(environment.Harness.Endpoint == nil || *environment.Harness.Endpoint != *harness.Endpoint) {
		return publishedEnvironmentMismatchError(
			"RLE service returned a different harness endpoint than rle.toml.",
			"Check the RLE service response and retry.",
		)
	}
	// An omitted protocol is resolved by the service, so only a declared one is compared.
	if harness.Protocol != "" && environment.Harness.Protocol != string(harness.Protocol) {
		return publishedEnvironmentMismatchError(
			fmt.Sprintf(
				"RLE service returned harness protocol %s, but rle.toml declares %s.",
				environment.Harness.Protocol,
				harness.Protocol,
			),
			"Check the RLE service response and retry.",
		)
	}
	if !reflect.DeepEqual(config.SchemaVersion, environment.SchemaVersion) {
		return publishedEnvironmentMismatchError(
			"RLE service returned a different defaults schema version than rle.toml.",
			"Check the RLE service response and retry.",
		)
	}
	if !reflect.DeepEqual(config.Model, environment.Model) ||
		!reflect.DeepEqual(config.Train, environment.Train) ||
		!reflect.DeepEqual(config.Rollout, environment.Rollout) {
		return publishedEnvironmentMismatchError(
			"RLE service returned different reusable defaults than rle.toml.",
			"Check the RLE service response and retry.",
		)
	}
	return nil
}

func publishedEnvironmentMismatchError(message string, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       "rle_published_environment_mismatch",
		Category:   azdext.LocalErrorCategoryInternal,
		Suggestion: suggestion,
	}
}

func resolvePublishImage(environmentName string, version string, projectEndpoint string) (string, error) {
	registry := strings.Trim(strings.TrimSpace(os.Getenv("AZURE_CONTAINER_REGISTRY_ENDPOINT")), "/")
	if registry == "" {
		return "", &azdext.LocalError{
			Message:    "ACR registry is required for publish.",
			Code:       "rle_acr_registry_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Set AZURE_CONTAINER_REGISTRY_ENDPOINT=<registry>.azurecr.io, then run publish again.",
		}
	}
	projectName, err := projectRouteSegment(projectEndpoint)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"%s/%s-%s:%s",
		registry,
		project.Slug(projectName),
		project.Slug(environmentName),
		version,
	), nil
}

type environmentOutput struct {
	EnvironmentId          string                      `json:"environmentId"`
	EnvironmentVersion     string                      `json:"environmentVersion"`
	EnvironmentName        string                      `json:"environmentName"`
	FoundryProjectEndpoint string                      `json:"foundryProjectEndpoint"`
	AcrImage               string                      `json:"acrImage"`
	Type                   string                      `json:"type"`
	Endpoint               string                      `json:"endpoint,omitempty"`
	Protocol               string                      `json:"protocol,omitempty"`
	SchemaVersion          *string                     `json:"schema_version,omitempty"`
	Model                  *project.RleModelDefaults   `json:"model,omitempty"`
	Train                  *project.RleTrainDefaults   `json:"train,omitempty"`
	Rollout                *project.RleRolloutDefaults `json:"rollout,omitempty"`
	CreatedAt              string                      `json:"createdAt"`
	UpdatedAt              string                      `json:"updatedAt"`
}

// prepareHarborTasks builds and pushes each task image, uploads its bundle to project storage when
// absent, and returns the tasks to publish with the environment version.
func (a *publishAction) prepareHarborTasks(client *rleClient, projectEndpoint string) ([]harborTask, error) {
	var tasks []harborTask
	seen := map[string]bool{}
	for _, dir := range a.flags.taskDirs {
		ref, err := prepareHarborTask(
			a.cmd.Context(), a.cmd.OutOrStdout(), a.cmd.ErrOrStderr(), dir, projectEndpoint, a.flags.remote)
		if err != nil {
			return nil, err
		}
		if seen[ref.Name] {
			return nil, &azdext.LocalError{
				Message:  fmt.Sprintf("Harbor task name %q is published twice.", ref.Name),
				Code:     "rle_harbor_task_duplicate",
				Category: azdext.LocalErrorCategoryUser,
			}
		}
		seen[ref.Name] = true
		bundle, err := client.uploadHarborBundle(a.cmd.Context(), ref.Bundle)
		if err != nil {
			return nil, serviceError(err)
		}
		tasks = append(tasks, harborTask{Name: ref.Name, AcrImagePath: ref.Image, Bundle: bundle})
	}
	return tasks, nil
}

func harborTaskFormat(config project.RleConfig) *rleTaskFormat {
	if config.Task == nil || config.Task.Format != project.TaskFormatHarbor {
		return nil
	}
	return &rleTaskFormat{Format: string(project.TaskFormatHarbor)}
}
