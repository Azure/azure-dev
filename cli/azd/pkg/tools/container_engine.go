// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

// ContainerEngine identifies a container runtime used to build or publish images.
// Its zero value lets the consuming tool choose its default runtime.
type ContainerEngine string

const (
	ContainerEngineDocker ContainerEngine = "docker"
	ContainerEnginePodman ContainerEngine = "podman"
)
