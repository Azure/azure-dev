package graphsdk

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// Creates a new Azure HTTP pipeline used for Graph SDK clients
func NewPipeline(credential azcore.TokenCredential, serviceConfig cloud.ServiceConfiguration, clientOptions *azcore.ClientOptions) runtime.Pipeline {
	scopes := []string{
		fmt.Sprintf("%s/.default", serviceConfig.Audience),
	}

	authPolicy := runtime.NewBearerTokenPolicy(credential, scopes, nil)
	pipelineOptions := runtime.PipelineOptions{
		PerRetry: []policy.Policy{authPolicy},
	}

	return runtime.NewPipeline("graph", "1.0.0", pipelineOptions, clientOptions)
}
