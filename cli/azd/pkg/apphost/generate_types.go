package apphost

type genAppInsight struct{}

type genStorageAccount struct {
	Blobs  []string
	Tables []string
	Queues []string
}

type genCosmosAccount struct {
	Databases []string
}

type genServiceBus struct {
	Queues []string
	Topics []string
}

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
	Image   string
	Dapr    *genContainerAppManifestTemplateContextDapr
	Env     map[string]string
	Secrets map[string]string
	Ingress *genContainerAppIngress
	Volumes []*Volume
}

type genContainerAppIngress struct {
	External      bool
	TargetPort    int
	Transport     string
	AllowInsecure bool
}

type genContainer struct {
	Image    string
	Env      map[string]string
	Bindings map[string]*Binding
	Inputs   map[string]Input
	Volumes  []*Volume
}

type genDockerfile struct {
	Path      string
	Context   string
	Env       map[string]string
	Bindings  map[string]*Binding
	BuildArgs map[string]string
}

type genProject struct {
	Path     string
	Env      map[string]string
	Bindings map[string]*Binding
}

type genAppConfig struct{}

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

type genSqlServer struct {
	Databases []string
}

type genOutputParameter struct {
	Type  string
	Value string
}

type genBicepModules struct {
	Path   string
	Params map[string]string
}

type genBicepTemplateContext struct {
	HasContainerRegistry            bool
	HasContainerEnvironment         bool
	HasDaprStore                    bool
	HasLogAnalyticsWorkspace        bool
	RequiresPrincipalId             bool
	RequiresStorageVolume           bool
	AppInsights                     map[string]genAppInsight
	ServiceBuses                    map[string]genServiceBus
	StorageAccounts                 map[string]genStorageAccount
	KeyVaults                       map[string]genKeyVault
	ContainerAppEnvironmentServices map[string]genContainerAppEnvironmentServices
	ContainerApps                   map[string]genContainerApp
	AppConfigs                      map[string]genAppConfig
	DaprComponents                  map[string]genDaprComponent
	CosmosDbAccounts                map[string]genCosmosAccount
	SqlServers                      map[string]genSqlServer
	InputParameters                 map[string]Input
	OutputParameters                map[string]genOutputParameter
	OutputSecretParameters          map[string]genOutputParameter
	BicepModules                    map[string]genBicepModules
	// parameters to be passed from main.bicep to resources.bicep
	mappedParameters []string
}

type genContainerAppManifestTemplateContext struct {
	Name            string
	Ingress         *genContainerAppIngress
	Env             map[string]string
	Secrets         map[string]string
	KeyVaultSecrets map[string]string
	Dapr            *genContainerAppManifestTemplateContextDapr
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
