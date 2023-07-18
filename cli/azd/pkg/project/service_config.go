package project

import (
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
)

type ServiceConfig struct {
	// Reference to the parent project configuration
	Project *ProjectConfig `yaml:"projectConfig,omitempty"`
	// The friendly name/key of the project from the azure.yaml file
	Name string `yaml:"-,omitempty"`
	// The name used to override the default azure resource name
	ResourceName ExpandableString `yaml:"resourceName,omitempty"`
	// The relative path to the project folder from the project root
	RelativePath string `yaml:"project"`
	// The azure hosting model to use, ex) appservice, function, containerapp
	Host ServiceTargetKind `yaml:"host"`
	// The programming language of the project
	Language ServiceLanguageKind `yaml:"language"`
	// The output path for build artifacts
	OutputPath string `yaml:"dist,omitempty"`
	// The optional docker options
	Docker DockerProjectOptions `yaml:"docker,omitempty"`
	// The optional K8S / AKS options
	K8s AksOptions `yaml:"k8s,omitempty"`
	// The optional Azure Spring Apps options
	Spring SpringOptions `yaml:"spring,omitempty"`
	// The infrastructure provisioning configuration
	Infra provisioning.Options `yaml:"infra,omitempty"`
	// Hook configuration for service
	Hooks map[string]*ext.HookConfig `yaml:"hooks,omitempty"`

	*ext.EventDispatcher[ServiceLifecycleEventArgs] `yaml:",omitempty"`

	initialized bool
}

// Path returns the fully qualified path to the project
func (sc *ServiceConfig) Path() string {
	return filepath.Join(sc.Project.Path, sc.RelativePath)
}
