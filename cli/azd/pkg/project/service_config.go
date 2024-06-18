package project

import (
	"fmt"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

const DefaultComponentName = "main"

type ServiceConfig struct {
	// The friendly name/key of the service from the azure.yaml file
	Name string `yaml:"-"`
	// Reference to the parent project configuration
	Project *ProjectConfig `yaml:"-"`
	// The relative path to the source folder from the project root
	RelativePath string `yaml:"project,omitempty"`
	// The azure hosting model to use, ex) appservice, function, containerapp
	Host ServiceTargetKind `yaml:"host,omitempty"`
	// The azure resource group to deploy the service to
	ResourceGroupName osutil.ExpandableString `yaml:"resourceGroup,omitempty"`
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
	// Custom configuration for the service target
	Config map[string]any `yaml:"config,omitempty"`
	// The optional container configuration for container based applications
	Components map[string]*ComponentConfig `yaml:"containers,omitempty"`

	*ext.EventDispatcher[ServiceLifecycleEventArgs] `yaml:"-"`
}

type singleComponentServiceConfig struct {
	// Reference to the parent project configuration
	Project *ProjectConfig `yaml:"-"`
	// The azure hosting model to use, ex) appservice, function, containerapp
	Host      ServiceTargetKind `yaml:"host,omitempty"`
	Component *ComponentConfig  `yaml:",inline"`
	// The azure resource group to deploy the service to
	ResourceGroupName osutil.ExpandableString `yaml:"resourceGroup,omitempty"`
	// The name used to override the default azure resource name
	ResourceName osutil.ExpandableString `yaml:"resourceName,omitempty"`
	// The optional K8S / AKS options
	K8s AksOptions `yaml:"k8s,omitempty"`
	// The optional Azure Spring Apps options
	Spring SpringOptions `yaml:"spring,omitempty"`
	// Hook configuration for service
	Hooks map[string]*ext.HookConfig `yaml:"hooks,omitempty"`
	// Custom configuration for the service target
	Config map[string]any `yaml:"config,omitempty"`
}

func (sc *ServiceConfig) Path() string {
	if filepath.IsAbs(sc.RelativePath) {
		return sc.RelativePath
	}
	return filepath.Join(sc.Project.Path, sc.RelativePath)
}

// ComponentConfig is the configuration for a container based projects
type ComponentConfig struct {
	// Reference to the parent project configuration
	Service *ServiceConfig `yaml:"-"`
	// The friendly name/key of the project from the azure.yaml file
	Name string `yaml:"-"`
	// The relative path to the source folder from the service root
	RelativePath string `yaml:"project,omitempty"`
	// The programming language of the project
	Language ServiceLanguageKind `yaml:"language,omitempty"`
	// The output path for build artifacts
	OutputPath string `yaml:"dist,omitempty"`
	// The source image to use for container based applications
	Image string `yaml:"image,omitempty"`
	// The optional docker options for configuring the output image
	Docker DockerProjectOptions `yaml:"docker,omitempty"`
}

func (cc *ComponentConfig) Path() string {
	if filepath.IsAbs(cc.RelativePath) {
		return cc.RelativePath
	}
	return filepath.Join(cc.Service.Path(), cc.RelativePath)
}

type DotNetContainerAppOptions struct {
	Manifest    *apphost.Manifest
	AppHostPath string
	ProjectName string
	// ContainerImage is non-empty when a prebuilt container image is being used.
	ContainerImage string
}

// Main returns the main or default component configuration for the service
func (sc *ServiceConfig) Main() (*ComponentConfig, error) {
	if len(sc.Components) > 1 {
		return nil, fmt.Errorf("Service '%s' has multiple components", sc.Name)
	}

	for _, value := range sc.Components {
		return value, nil
	}

	return nil, fmt.Errorf("Service '%s' has no components", sc.Name)
}

func (sc *ServiceConfig) MarshalYAML() (interface{}, error) {
	for key, component := range sc.Components {
		if component.Name == "" {
			component.Name = key
		}
	}

	if len(sc.Components) == 1 {
		main, err := sc.Main()
		if err == nil && main.Name == DefaultComponentName {
			singleComponentService := singleComponentServiceConfig{
				Component:         sc.Components[DefaultComponentName],
				Project:           sc.Project,
				Host:              sc.Host,
				ResourceGroupName: sc.ResourceGroupName,
				ResourceName:      sc.ResourceName,
				K8s:               sc.K8s,
				Spring:            sc.Spring,
				Hooks:             sc.Hooks,
				Config:            sc.Config,
			}

			// Override the component name to be the service name
			singleComponentService.Component.Name = sc.Name
			return singleComponentService, nil
		}
	}

	return *sc, nil
}

func (sc *ServiceConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// Leverage the built-in unmarshaler to hydrate the service configuration
	type serviceConfig ServiceConfig
	var svc serviceConfig
	if err := unmarshal(&svc); err != nil {
		return err
	}

	// When the service configuration does not contain any components, create a default component.
	if len(svc.Components) == 0 {
		var component ComponentConfig
		if err := unmarshal(&component); err != nil {
			return err
		}

		// We do not want to copy the relative path from the service because it is possible for components to have
		// their own relative path from the service path.
		component.RelativePath = ""

		svc.Components = map[string]*ComponentConfig{
			DefaultComponentName: &component,
		}
	}

	for key, component := range svc.Components {
		component.Name = key
	}

	*sc = ServiceConfig(svc)

	return nil
}
