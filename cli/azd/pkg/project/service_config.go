// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

type ServiceConfig struct {
	// Reference to the parent project configuration
	Project *ProjectConfig `yaml:"-"`
	// The friendly name/key of the project from the azure.yaml file
	Name string `yaml:"-"`
	// The azure resource group to deploy the service to
	ResourceGroupName osutil.ExpandableString `yaml:"resourceGroup,omitempty"`
	// The name used to override the default azure resource name
	ResourceName osutil.ExpandableString `yaml:"resourceName,omitempty"`
	// The ARM api version to use for the service. If not specified, the latest version is used.
	ApiVersion string `yaml:"apiVersion,omitempty"`
	// The relative path to the project folder from the project root
	RelativePath string `yaml:"project"`
	// The azure hosting model to use, ex) appservice, function, containerapp
	Host ServiceTargetKind `yaml:"host"`
	// The programming language of the project
	Language ServiceLanguageKind `yaml:"language"`
	// The output path for build artifacts
	OutputPath string `yaml:"dist,omitempty"`
	// The source image to use for container based applications
	Image osutil.ExpandableString `yaml:"image,omitempty"`
	// The optional docker options for configuring the output image
	Docker DockerProjectOptions `yaml:"docker,omitempty"`
	// The optional K8S / AKS options
	K8s AksOptions `yaml:"k8s,omitempty"`
	// The optional Azure Spring Apps options
	Spring SpringOptions `yaml:"spring,omitempty"`
	// The infrastructure provisioning configuration
	Infra provisioning.Options `yaml:"infra,omitempty"`
	// Hook configuration for service
	Hooks HooksConfig `yaml:"hooks,omitempty"`
	// Options specific to the DotNetContainerApp target. These are set by the importer and
	// can not be controlled via the project file today.
	DotNetContainerApp *DotNetContainerAppOptions `yaml:"-,omitempty"`
	// Custom configuration for the service target
	Config map[string]any `yaml:"config,omitempty"`
	// Computed lazily by useDotnetPublishForDockerBuild and cached. This is true when the project
	// is a dotnet project and there is not an explicit Dockerfile in the project directory.
	useDotNetPublishForDockerBuild *bool

	*ext.EventDispatcher[ServiceLifecycleEventArgs] `yaml:"-"`
}

type DotNetContainerAppOptions struct {
	Manifest    *apphost.Manifest
	AppHostPath string
	ProjectName string
	// ContainerImage is non-empty when a prebuilt container image is being used.
	ContainerImage string
}

// Path returns the fully qualified path to the project
func (sc *ServiceConfig) Path() string {
	if filepath.IsAbs(sc.RelativePath) {
		return sc.RelativePath
	}
	return filepath.Join(sc.Project.Path, sc.RelativePath)
}
