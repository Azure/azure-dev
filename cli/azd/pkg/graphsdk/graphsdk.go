package graphsdk

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
)

var ServiceConfig cloud.ServiceConfiguration = cloud.ServiceConfiguration{
	Audience: "https://graph.microsoft.com",
	Endpoint: "https://graph.microsoft.com/v1.0",
}
