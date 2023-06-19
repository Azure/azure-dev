package graphsdk

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
)

// The host name for the Graph API.
const HostName = "graph.microsoft.com"

var ServiceConfig cloud.ServiceConfiguration = cloud.ServiceConfiguration{
	Audience: "https://graph.microsoft.com",
	Endpoint: "https://graph.microsoft.com/v1.0",
}
