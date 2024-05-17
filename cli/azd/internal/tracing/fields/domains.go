package fields

type Domain struct {
	// The domain name.
	Name string
	// The name of the service that is responsible for the domain name.
	Service string
}

// Well-known domains. Domains can also be subdomains, thus should be evaluated as such.
//
// Taken from https://learn.microsoft.com/azure/security/fundamentals/azure-domains.
var Domains = []Domain{
	// Order here matters, as it likely determines evaluation precedence due to short-circuiting.
	{"dev.azure.com", "azdo"},
	{"management.azure.com", "arm"},
	{"management.core.windows.net", "arm"},
	{"graph.microsoft.com", "graph"},
	{"graph.windows.net", "graph"},
	{"azmk8s.io", "aks"},
	{"azure-api.net", "apim"},
	{"azure-mobile.net", "mobile"},
	{"azurecontainerapps.io", "aca"},
	{"azurecr.io", "acr"},
	{"azureedge.net", "edge"},
	{"azurefd.net", "frontdoor"},
	{"scm.azurewebsites.net", "kudu"},
	{"azurewebsites.net", "websites"},
	{"blob.core.windows.net", "blob"},
	{"cloudapp.azure.com", "vm"},
	{"cloudapp.net", "vm"},
	{"cosmos.azure.com", "cosmos"},
	{"database.windows.net", "sql"},
	{"documents.azure.com", "cosmos"},
	{"file.core.windows.net", "files"},
	{"origin.mediaservices.windows.net", "media"},
	{"queue.core.windows.net", "queue"},
	{"servicebus.windows.net", "servicebus"},
	{"table.core.windows.net", "table"},
	{"trafficmanager.net", "trafficmanager"},
	{"vault.azure.net", "keyvault"},
	{"visualstudio.com", "vs"},
	{"vo.msecnd.net", "cdn"},
}
