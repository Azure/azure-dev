// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// rolloutHarborTask is the Execute Rollout task for a published Harbor task. RLE resolves the
// image, disk image and bundle from the environment version, and the format from its task.
type rolloutHarborTask struct {
	Name string `json:"name"`
}

// preparedHarborTask is a task directory whose image was built and pushed, ready to publish.
type preparedHarborTask struct {
	Name   string
	Image  string
	Bundle []byte
}

// prepareHarborTask validates the task directory, then builds and pushes its content-addressed
// image to ACR so publish only registers a prepared image.
func prepareHarborTask(
	ctx context.Context,
	out io.Writer,
	errOut io.Writer,
	dir string,
	projectEndpoint string,
	remote bool,
) (*preparedHarborTask, error) {
	task, err := project.LoadHarborTask(dir)
	if err != nil {
		return nil, err
	}
	image, err := resolveHarborTaskImage(task, projectEndpoint)
	if err != nil {
		return nil, err
	}
	bundle, err := project.BuildHarborBundle(task)
	if err != nil {
		return nil, err
	}
	dockerfile, err := filepath.Rel(task.EnvironmentDir, task.Dockerfile)
	if err != nil {
		return nil, err
	}
	if err := project.BuildAndPushImage(ctx, out, errOut, image, project.BuildOptions{
		Source:     task.EnvironmentDir,
		Dockerfile: dockerfile,
	}, remote); err != nil {
		return nil, err
	}
	return &preparedHarborTask{Name: task.Name, Image: image, Bundle: bundle}, nil
}

func resolveHarborTaskImage(task *project.HarborTask, projectEndpoint string) (string, error) {
	registry := strings.Trim(strings.TrimSpace(os.Getenv("AZURE_CONTAINER_REGISTRY_ENDPOINT")), "/")
	if registry == "" {
		return "", &azdext.LocalError{
			Message:    "ACR registry is required to run a Harbor task.",
			Code:       "rle_acr_registry_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Set AZURE_CONTAINER_REGISTRY_ENDPOINT=<registry>.azurecr.io, then run rollout again.",
		}
	}
	projectName, err := projectRouteSegment(projectEndpoint)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%s-harbor-tasks:%s", registry, project.Slug(projectName), task.ImageTag), nil
}
