package project

import (
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
)

type ServiceConfig struct {
	// Reference to the parent project configuration
	Project *ProjectConfig `yaml:"omitempty"`
	// The friendly name/key of the project from the azure.yaml file
	Name string
	// The name used to override the default azure resource name
	ResourceName ExpandableString `yaml:"resourceName"`
	// The relative path to the project folder from the project root
	RelativePath string `yaml:"project"`
	// The azure hosting model to use, ex) appservice, function, containerapp
	Host string `yaml:"host"`
	// The programming language of the project
	Language string `yaml:"language"`
	// The output path for build artifacts
	OutputPath string `yaml:"dist"`
	// The infrastructure module path relative to the root infra folder to use for this project
	Module string `yaml:"module"`
	// The optional docker options
	Docker DockerProjectOptions `yaml:"docker"`
	// The optional K8S / AKS options
	K8s AksOptions `yaml:"k8s"`
	// The infrastructure provisioning configuration
	Infra provisioning.Options `yaml:"infra"`
	// Hook configuration for service
	Hooks map[string]*ext.HookConfig `yaml:"hooks,omitempty"`
	// The optional dotnet project file if there're multiple project file in folder
	DotnetProjectFile string `yaml:"dotnetProjectFile"`

	*ext.EventDispatcher[ServiceLifecycleEventArgs] `yaml:",omitempty"`

	initialized bool
}

// Path returns the fully qualified path to the project
func (sc *ServiceConfig) Path() string {
	return filepath.Join(sc.Project.Path, sc.RelativePath)
}
