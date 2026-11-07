// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"fmt"
	"io"
	"regexp"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
)

var imageIDPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type runtimeImageBuilder interface {
	Build(context.Context, *stagedContainerContext) (*azdext.Artifact, error)
}

type nativeRuntimeImageBuilder struct {
	docker *docker.Cli
}

func newRuntimeImageBuilder(runner azdexec.CommandRunner) runtimeImageBuilder {
	return &nativeRuntimeImageBuilder{docker: docker.NewCli(runner)}
}

func (b *nativeRuntimeImageBuilder) Build(ctx context.Context, staged *stagedContainerContext) (*azdext.Artifact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.docker.CheckInstalled(ctx); err != nil {
		return nil, fmt.Errorf("checking the runtime image builder: %w", nonDisclosureError(err))
	}
	id, err := b.docker.Build(
		ctx, staged.directory, staged.recipePath, docker.DefaultPlatform, "", staged.directory, "",
		nil, nil, nil, "", io.Discard,
	)
	if err != nil {
		return nil, fmt.Errorf("building the Spring runtime image: %w", nonDisclosureError(err))
	}
	if !imageIDPattern.MatchString(id) {
		return nil, validationError("invalid_runtime_image_id", "the runtime build did not return a valid local image ID",
			"verify the container engine and retry azd package")
	}
	return &azdext.Artifact{
		Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
		LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
		Location:     id,
		Metadata:     map[string]string{"framework": "docker", "imageId": id},
	}, nil
}
