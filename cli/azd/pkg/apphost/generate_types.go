package apphost

type genAppInsight struct{}

type genStorageAccount struct {
	Blobs  []string
	Tables []string
	Queues []string
}

type genServiceBus struct {
	Queues []string
	Topics []string
}

type genContainerAppEnvironmentServices struct {
	Type string
}

type genKeyVault struct{}

type genContainerApp struct {
	Image   string
	Ingress *genContainerAppIngress
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
}

type genDockerfile struct {
	Path     string
	Context  string
	Env      map[string]string
	Bindings map[string]*Binding
}

type genProject struct {
	Path     string
	Env      map[string]string
	Bindings map[string]*Binding
}

type genDapr struct {
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

type genBicepTemplateContext struct {
	HasContainerRegistry            bool
	HasContainerEnvironment         bool
	HasDaprStore                    bool
	HasLogAnalyticsWorkspace        bool
	AppInsights                     map[string]genAppInsight
	ServiceBuses                    map[string]genServiceBus
	StorageAccounts                 map[string]genStorageAccount
	KeyVaults                       map[string]genKeyVault
	ContainerAppEnvironmentServices map[string]genContainerAppEnvironmentServices
	ContainerApps                   map[string]genContainerApp
	DaprComponents                  map[string]genDaprComponent
}

type genContainerAppManifestTemplateContext struct {
	Name    string
	Ingress *genContainerAppIngress
	Env     map[string]string
	Dapr    *genContainerAppManifestTemplateContextDapr
}

type genProjectFileContext struct {
	Name     string
	Services map[string]string
}

type genContainerAppManifestTemplateContextIngress struct {
	External      bool
	Transport     string
	TargetPort    int
	AllowInsecure bool
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
