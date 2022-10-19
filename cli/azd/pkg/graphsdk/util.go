package graphsdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// Creates a new Azure HTTP pipeline used for Graph SDK clients
func NewPipeline(
	credential azcore.TokenCredential,
	serviceConfig cloud.ServiceConfiguration,
	clientOptions *azcore.ClientOptions,
) runtime.Pipeline {
	scopes := []string{
		fmt.Sprintf("%s/.default", serviceConfig.Audience),
	}

	authPolicy := runtime.NewBearerTokenPolicy(credential, scopes, nil)
	pipelineOptions := runtime.PipelineOptions{
		PerRetry: []policy.Policy{authPolicy},
	}

	return runtime.NewPipeline("graph", "1.0.0", pipelineOptions, clientOptions)
}

// Creates a JSON serialized HTTP request body
func SetHttpRequestBody(req *policy.Request, value any) error {
	raw := req.Raw()
	raw.Header.Set("Content-Type", "application/json")

	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed serializing JSON: %w", err)
	}

	raw.Body = io.NopCloser(bytes.NewBuffer(jsonBytes))

	return nil
}
