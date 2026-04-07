// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

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
	// Infrastructure module path relative to the root infra folder
	Module string `yaml:"module,omitempty"`
	// The infrastructure provisioning configuration
	Infra provisioning.Options `yaml:"infra,omitempty"`
	// Hook configuration for service
	Hooks HooksConfig `yaml:"hooks,omitempty"`
	// Dependencies on other services and resources
	Uses []string `yaml:"uses,omitempty"`
	// Options specific to the DotNetContainerApp target. These are set by the importer and
	// can not be controlled via the project file today.
	DotNetContainerApp *DotNetContainerAppOptions `yaml:"-,omitempty"`
	// Custom configuration for the service target
	Config map[string]any `yaml:"config,omitempty"`
	// Computed lazily by useDotnetPublishForDockerBuild and cached. This is true when the project
	// is a dotnet project and there is not an explicit Dockerfile in the project directory.
	useDotNetPublishForDockerBuild *bool
	// Environment variables to set for the service
	Environment osutil.ExpandableMap `yaml:"env,omitempty"`
	// Condition for deploying the service. When evaluated, the service is only deployed if the value
	// is a truthy boolean (1, true, TRUE, True, yes). If not defined, the service is enabled by default.
	Condition osutil.ExpandableString `yaml:"condition,omitempty"`
	// Whether to build the service remotely. Only applicable to function app services.
	// When set to nil (unset), the default behavior based on language is used.
	RemoteBuild *bool `yaml:"remoteBuild,omitempty"`

	// AdditionalProperties captures any unknown YAML fields for extension support
	AdditionalProperties map[string]any `yaml:",inline"`

	*ext.EventDispatcher[ServiceLifecycleEventArgs] `yaml:"-"`

	configuredHookRegistrationMu sync.Mutex `yaml:"-"`
	configuredHookRegistration   *configuredHookRegistration

	// Turns service into a service that is only to be built but not deployed.
	// This is currently used by Aspire.
	BuildOnly bool `yaml:"-"`
}

type configuredHookRegistration struct {
	signature string
	ctx       context.Context
	cancel    context.CancelFunc
}

type DotNetContainerAppOptions struct {
	Manifest    *apphost.Manifest
	AppHostPath string
	ProjectName string
	// ContainerImage is non-empty when a prebuilt container image is being used.
	ContainerImage string
	// ContainerFiles is a list of files to include in the container image.
	ContainerFiles map[string]ContainerFile
}

type ContainerFile struct {
	ServiceConfig *ServiceConfig
	Sources       []string
	Destination   string
}

// Path returns the fully qualified path to the project
func (sc *ServiceConfig) Path() string {
	if filepath.IsAbs(sc.RelativePath) {
		return sc.RelativePath
	}
	return filepath.Join(sc.Project.Path, sc.RelativePath)
}

// IsEnabled evaluates the service condition and returns whether the service should be deployed.
// If no condition is specified, the service is enabled by default.
// The condition is evaluated as a boolean where truthy values are: 1, true, TRUE, True, yes, YES, Yes
// All other values are considered false.
// Returns an error if the condition template is malformed.
func (sc *ServiceConfig) IsEnabled(getenv func(string) string) (bool, error) {
	if sc.Condition.Empty() {
		return true, nil
	}

	value, err := sc.Condition.Envsubst(getenv)
	if err != nil {
		return false, fmt.Errorf("malformed deployment condition template: %w", err)
	}

	return isConditionTrue(value), nil
}

// isConditionTrue parses a string value as a boolean condition.
// Returns true for: "1", "true", "TRUE", "True", "yes", "YES", "Yes"
// Returns false for all other values.
func isConditionTrue(value string) bool {
	switch value {
	case "1", "true", "TRUE", "True", "yes", "YES", "Yes":
		return true
	default:
		return false
	}
}

// EnsureHooksRegistered ensures azure.yaml-configured service hooks are registered at most once
// for the current hook signature and registration context lifetime.
// Returns the registration context and true if hooks should be registered, or nil and false if
// registration should be skipped (already registered with matching signature and active context).
func (sc *ServiceConfig) EnsureHooksRegistered(
	parentCtx context.Context,
	signature string,
) (context.Context, bool) {
	sc.configuredHookRegistrationMu.Lock()
	defer sc.configuredHookRegistrationMu.Unlock()

	if sc.configuredHookRegistration != nil &&
		sc.configuredHookRegistration.signature == signature &&
		sc.configuredHookRegistration.ctx.Err() == nil {
		return nil, false
	}

	if sc.configuredHookRegistration != nil {
		sc.configuredHookRegistration.cancel()
	}

	//nolint:gosec // G118 - cancel is stored and called by Reset/Rollback
	registrationCtx, cancel := context.WithCancel(parentCtx)
	sc.configuredHookRegistration = &configuredHookRegistration{
		signature: signature,
		ctx:       registrationCtx,
		cancel:    cancel,
	}

	return registrationCtx, true
}

// RollbackHookRegistration clears the current hook registration after a failed install.
func (sc *ServiceConfig) RollbackHookRegistration(signature string) {
	sc.configuredHookRegistrationMu.Lock()
	defer sc.configuredHookRegistrationMu.Unlock()

	if sc.configuredHookRegistration == nil || sc.configuredHookRegistration.signature != signature {
		return
	}

	sc.configuredHookRegistration.cancel()
	sc.configuredHookRegistration = nil
}

// ResetHookRegistration removes any active service-hook registration,
// allowing hooks to be re-registered on the next middleware pass.
func (sc *ServiceConfig) ResetHookRegistration() {
	sc.configuredHookRegistrationMu.Lock()
	defer sc.configuredHookRegistrationMu.Unlock()

	if sc.configuredHookRegistration == nil {
		return
	}

	sc.configuredHookRegistration.cancel()
	sc.configuredHookRegistration = nil
}

// CopyRuntimeStateTo preserves in-memory runtime state that should survive config reloads.
func (sc *ServiceConfig) CopyRuntimeStateTo(target *ServiceConfig) {
	if sc == nil || target == nil {
		return
	}

	if sc.EventDispatcher != nil {
		target.EventDispatcher = sc.EventDispatcher
	}

	sc.configuredHookRegistrationMu.Lock()
	registration := sc.configuredHookRegistration
	sc.configuredHookRegistrationMu.Unlock()

	target.configuredHookRegistrationMu.Lock()
	target.configuredHookRegistration = registration
	target.configuredHookRegistrationMu.Unlock()
}
