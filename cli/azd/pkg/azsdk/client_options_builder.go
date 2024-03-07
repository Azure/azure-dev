package azsdk

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type ClientOptionsBuilder struct {
	transport        policy.Transporter
	perCallPolicies  []policy.Policy
	perRetryPolicies []policy.Policy
	cloud            cloud.Configuration
}

func NewClientOptionsBuilder() *ClientOptionsBuilder {
	return &ClientOptionsBuilder{}
}

// Sets the underlying transport used for executing HTTP requests
func (b *ClientOptionsBuilder) WithTransport(transport policy.Transporter) *ClientOptionsBuilder {
	b.transport = transport
	return b
}

// Appends per-call policies into the HTTP pipeline
func (b *ClientOptionsBuilder) WithPerCallPolicy(policy policy.Policy) *ClientOptionsBuilder {
	b.perCallPolicies = append(b.perCallPolicies, policy)
	return b
}

// Appends per-retry policies into the HTTP pipeline
func (b *ClientOptionsBuilder) WithPerRetryPolicy(policy policy.Policy) *ClientOptionsBuilder {
	b.perRetryPolicies = append(b.perRetryPolicies, policy)
	return b
}

func (b *ClientOptionsBuilder) WithCloud(cloud cloud.Configuration) *ClientOptionsBuilder {
	b.cloud = cloud
	return b
}

// Builds the az core client options for data plane operations
// These options include the underlying transport to be used.
func (b *ClientOptionsBuilder) BuildCoreClientOptions() *azcore.ClientOptions {
	return &azcore.ClientOptions{
		// Supports mocking for unit tests
		Transport: b.transport,
		// Per request policies to inject into HTTP pipeline
		PerCallPolicies: b.perCallPolicies,
		// Per retry policies to inject into HTTP pipeline
		PerRetryPolicies: b.perRetryPolicies,

		Cloud: b.cloud,
	}
}

// Builds the ARM module client options for control plane operations
// These options include the underlying transport to be used.
func (b *ClientOptionsBuilder) BuildArmClientOptions() *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			// Supports mocking for unit tests
			Transport: b.transport,
			// Per request policies to inject into HTTP pipeline
			PerCallPolicies: b.perCallPolicies,
			// Per retry policies to inject into HTTP pipeline
			PerRetryPolicies: b.perRetryPolicies,
			// Logging policy options.
			// Always allow Azure correlation header
			Logging: policy.LogOptions{
				AllowedHeaders: []string{cMsCorrelationIdHeader},
			},

			Cloud: b.cloud,
		},
	}
}
