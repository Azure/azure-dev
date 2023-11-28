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
	Ingress *genContainerServiceIngress
}

type genContainerServiceIngress struct {
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

type genProject struct {
	Path     string
	Env      map[string]string
	Bindings map[string]*Binding
}

type genBicepTemplateContext struct {
	HasContainerRegistry            bool
	HasContainerEnvironment         bool
	HasLogAnalyticsWorkspace        bool
	AppInsights                     map[string]genAppInsight
	ServiceBuses                    map[string]genServiceBus
	StorageAccounts                 map[string]genStorageAccount
	KeyVaults                       map[string]genKeyVault
	ContainerAppEnvironmentServices map[string]genContainerAppEnvironmentServices
	ContainerApps                   map[string]genContainerApp
}

type genContainerAppManifestTemplateContext struct {
	Name    string
	Ingress *genContainerAppManifestTemplateContextIngress
	Env     map[string]string
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
