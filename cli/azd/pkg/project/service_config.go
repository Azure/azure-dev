package project

import (
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

type ServiceConfig struct {
	ComponentConfig `yaml:",inline"`

	// The name used to override the default azure resource name
	ResourceName osutil.ExpandableString `yaml:"resourceName,omitempty"`
	// The optional K8S / AKS options
	K8s AksOptions `yaml:"k8s,omitempty"`
	// The optional Azure Spring Apps options
	Spring SpringOptions `yaml:"spring,omitempty"`
	// Hook configuration for service
	Hooks map[string]*ext.HookConfig `yaml:"hooks,omitempty"`
	// Options specific to the DotNetContainerApp target. These are set by the importer and
	// can not be controlled via the project file today.
	DotNetContainerApp *DotNetContainerAppOptions `yaml:"-,omitempty"`
	// The optional container configuration for container based applications
	Containers map[string]*ComponentConfig `yaml:"containers,omitempty"`

	*ext.EventDispatcher[ServiceLifecycleEventArgs] `yaml:"-"`

	initialized bool
}

// ComponentConfig is the configuration for a container based projects
type ComponentConfig struct {
	// Reference to the parent project configuration
	Project *ProjectConfig `yaml:"-"`
	// Reference to the parent project configuration
	Service *ServiceConfig `yaml:"-"`
	// The azure hosting model to use, ex) appservice, function, containerapp
	Host ServiceTargetKind `yaml:"host"`
	// The friendly name/key of the project from the azure.yaml file
	Name string `yaml:"-"`
	// The relative path to the project folder from the project root
	RelativePath string `yaml:"project"`
	// The programming language of the project
	Language ServiceLanguageKind `yaml:"language"`
	// The output path for build artifacts
	OutputPath string `yaml:"dist,omitempty"`
	// The source image to use for container based applications
	Image string `yaml:"image,omitempty"`
	// The optional docker options for configuring the output image
	Docker DockerProjectOptions `yaml:"docker,omitempty"`
	// The optional K8S / AKS options
}

type ServiceComponent interface {
	Project() *ProjectConfig
	Service() *ServiceConfig
	Name() string
	Path() string
	Host() ServiceTargetKind
	Language() ServiceLanguageKind
	WithLanguage(language ServiceLanguageKind)
	OutputPath() string
	Image() string
	Docker() *DockerProjectOptions
}

type DotNetContainerAppOptions struct {
	Manifest    *apphost.Manifest
	ProjectName string
	ProjectPath string
}

// Path returns the fully qualified path to the project
func (sc *ServiceConfig) Path() string {
	if filepath.IsAbs(sc.RelativePath) {
		return sc.RelativePath
	}
	return filepath.Join(sc.Project.Path, sc.RelativePath)
}

// func (sc *ServiceConfig) MarshalYAML() (interface{}, error) {
// 	type serviceConfig ServiceConfig

// 	svc := serviceConfig(*sc)
// 	svcBytes, err := yaml.Marshal(svc)
// 	if err != nil {
// 		return nil, err
// 	}

// 	componentBytes, err := yaml.Marshal(sc.ComponentConfig)
// 	if err != nil {
// 		return nil, err
// 	}

// 	var svcMap map[string]interface{}
// 	if err := yaml.Unmarshal(svcBytes, &svcMap); err != nil {
// 		return nil, err
// 	}

// 	var componentMap map[string]interface{}
// 	if err := yaml.Unmarshal(componentBytes, &componentMap); err != nil {
// 		return nil, err
// 	}

// 	for k, v := range componentMap {
// 		svcMap[k] = v
// 	}

// 	yamlBytes, err := yaml.Marshal(svcMap)
// 	if err != nil {
// 		return nil, err
// 	}

// 	return string(yamlBytes), nil
// }

// func (sc *ServiceConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
// 	// Leverage the built-in unmarshaler to hydrate the service configuration
// 	type serviceConfig ServiceConfig
// 	var svc serviceConfig
// 	if err := unmarshal(&svc); err != nil {
// 		return err
// 	}

// 	// Reset the pointer
// 	*sc = ServiceConfig(svc)

// 	// Append the component configuration
// 	var componentConfig ComponentConfig
// 	if err := unmarshal(&componentConfig); err != nil {
// 		return err
// 	}

// 	componentConfig.Service = sc
// 	sc.ComponentConfig = componentConfig

// 	return nil
// }
