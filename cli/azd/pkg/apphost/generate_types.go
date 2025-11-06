// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package apphost

import "github.com/azure/azure-dev/cli/azd/pkg/custommaps"

type genContainerAppEnvironmentServices struct {
	Type string
}

type genKeyVault struct {
	// when true, the bicep definition for tags is not generated
	NoTags bool
	// when provided, the principalId from the user provisioning the key vault gets read access
	ReadAccessPrincipalId bool
}

type genContainerApp struct {
	Volumes    []*Volume
	BindMounts []*BindMount
}

type genContainerAppIngressPort struct {
	External    bool
	TargetPort  int
	ExposedPort int
}

type genContainerAppIngressAdditionalPortMappings struct {
	genContainerAppIngressPort
	ExposedPort int
}

type genContainerAppIngress struct {
	genContainerAppIngressPort
	Transport              string
	AllowInsecure          bool
	UsingDefaultPort       bool
	AdditionalPortMappings []genContainerAppIngressAdditionalPortMappings
}

type genContainer struct {
	Image            string
	Env              map[string]string
	Bindings         custommaps.WithOrder[Binding]
	Inputs           map[string]Input
	Volumes          []*Volume
	BindMounts       []*BindMount
	Args             []string
	DeploymentParams map[string]any
	DeploymentSource string
}

type genDockerfile struct {
	Path             string
	Context          string
	Env              map[string]string
	Bindings         custommaps.WithOrder[Binding]
	BuildArgs        map[string]string
	Args             []string
	DeploymentParams map[string]any
	DeploymentSource string
}

type genBuildContainer struct {
	Image             string
	Entrypoint        string
	Args              []string
	Env               map[string]string
	Bindings          custommaps.WithOrder[Binding]
	Volumes           []*Volume
	Build             *genBuildContainerDetails
	DeploymentParams  map[string]any
	DeploymentSource  string
	BindMounts        []*BindMount
	DefaultTargetPort int
}

type genBuildContainerDetails struct {
	Context    string
	Dockerfile string
	Args       map[string]string
	Secrets    map[string]ContainerV1BuildSecrets
	BuildOnly  bool
}

type genProject struct {
	Path             string
	Env              map[string]string
	Args             []string
	Bindings         custommaps.WithOrder[Binding]
	DeploymentParams map[string]any
	DeploymentSource string
}

type genDapr struct {
	AppId                  string
	Application            string
	AppPort                *int
	AppProtocol            *string
	DaprHttpMaxRequestSize *int
	DaprHttpReadBufferSize *int
	EnableApiLogging       *bool
	LogLevel               *string
}

type genDaprComponentMetadata struct {
	SecretKeyRef *string
	Value        *string
}

type genDaprComponentSecret struct {
	Value string
}

type genDaprComponent struct {
	Metadata map[string]genDaprComponentMetadata
	Secrets  map[string]genDaprComponentSecret
	Type     string
	Version  string
}

type genOutputParameter struct {
	Type  string
	Value string
}

type genBicepModules struct {
	Path   string
	Params map[string]string
	Scope  string
}

type genBicepTemplateContext struct {
	HasContainerRegistry            bool
	HasContainerEnvironment         bool
	HasDaprStore                    bool
	HasLogAnalyticsWorkspace        bool
	RequiresPrincipalId             bool
	RequiresStorageVolume           bool
	HasBindMounts                   bool
	KeyVaults                       map[string]genKeyVault
	ContainerAppEnvironmentServices map[string]genContainerAppEnvironmentServices
	ContainerApps                   map[string]genContainerApp
	DaprComponents                  map[string]genDaprComponent
	InputParameters                 map[string]Input
	OutputParameters                map[string]genOutputParameter
	OutputSecretParameters          map[string]genOutputParameter
	BicepModules                    map[string]genBicepModules
	// parameters to be passed from main.bicep to resources.bicep
	mappedParameters []string
}

type genContainerAppManifestTemplateContext struct {
	Name            string
	Entrypoint      string
	Ingress         *genContainerAppIngress
	Env             map[string]string
	Secrets         map[string]string
	KeyVaultSecrets map[string]string
	Dapr            *genContainerAppManifestTemplateContextDapr
	Args            []string
	Volumes         []*Volume
	BindMounts      []*BindMount
	DeployParams    map[string]string
	DeploySource    string
}

type genProjectFileContext struct {
	Name     string
	Services map[string]string
}

type genContainerAppManifestTemplateContextDapr struct {
	AppId              string
	AppPort            *int
	AppProtocol        *string
	EnableApiLogging   *bool
	HttpMaxRequestSize *int
	HttpReadBufferSize *int
	LogLevel           *string
}
