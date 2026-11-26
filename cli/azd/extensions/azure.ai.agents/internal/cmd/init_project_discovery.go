// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// probeInitProject returns nil only for a project that has not been initialized.
func probeInitProject(ctx context.Context, client *azdext.AzdClient) (*azdext.ProjectConfig, error) {
	var trailers metadata.MD
	response, err := client.Project().Get(ctx, &azdext.EmptyRequest{}, grpc.Trailer(&trailers))
	if err == nil {
		if response.GetProject() == nil {
			return nil, exterrors.Dependency(
				exterrors.CodeProjectNotFound, "the azd host returned no project configuration", "",
			)
		}
		return response.GetProject(), nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	code := status.Code(err)
	if code == codes.Canceled || code == codes.DeadlineExceeded {
		return nil, err
	}
	reasons := trailers.Get("azd-project-get-error")
	if len(reasons) == 1 && reasons[0] == "not_found" ||
		len(reasons) == 0 && code == codes.NotFound {
		return nil, nil
	}
	if len(reasons) == 0 && code == codes.Unknown && len(status.Convert(err).Details()) == 0 {
		// Older hosts use Unknown for ErrNoProject. Check the same local project
		// search as core before permitting scaffolding on those hosts.
		var discoveryErr error
		if root := os.Getenv("AZD_EXEC_PROJECT_DIR"); root != "" {
			_, discoveryErr = azdcontext.NewAzdContextFromWd(root)
		} else {
			_, discoveryErr = azdcontext.NewAzdContext()
		}
		if errors.Is(discoveryErr, azdcontext.ErrNoProject) {
			return nil, nil
		}
		if discoveryErr != nil {
			return nil, fmt.Errorf("check for an existing azd project: %w", errors.Join(err, discoveryErr))
		}
	}
	return nil, fmt.Errorf("load existing azd project: %w", err)
}

func initScaffoldError(err error) error {
	switch {
	case exterrors.IsCancellation(err):
		return exterrors.Cancelled("project initialization was cancelled")
	case errors.Is(err, context.DeadlineExceeded), status.Code(err) == codes.DeadlineExceeded:
		return err
	case status.Code(err) == codes.AlreadyExists:
		return exterrors.Dependency(
			exterrors.CodeEnvironmentCreationFailed,
			fmt.Sprintf("could not initialize the project because the environment already exists: %s", err),
			"check the selected environment name and retry with a different --environment value",
		)
	default:
		return exterrors.Dependency(
			exterrors.CodeProjectInitFailed,
			fmt.Sprintf("failed to initialize project: %s", err),
			"",
		)
	}
}
