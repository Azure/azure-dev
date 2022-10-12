package azsdk

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type ClientOptionsBuilder struct {
	transport policy.Transporter
	policies  []policy.Policy
}

func NewClientOptionsBuilder() *ClientOptionsBuilder {
	return &ClientOptionsBuilder{}
}

// Sets the underlying transport used for executing HTTP requests
func (b *ClientOptionsBuilder) WithTransport(transport policy.Transporter) *ClientOptionsBuilder {
	b.transport = transport
	return b
}

// Appends policies into the HTTP pipeline
func (b *ClientOptionsBuilder) WithPolicy(policy policy.Policy) *ClientOptionsBuilder {
	b.policies = append(b.policies, policy)
	return b
}

// Builds the az core client options for data plane operations
// These options include the underlying transport to be used.
func (b *ClientOptionsBuilder) BuildCoreClientOptions() *azcore.ClientOptions {
	return &azcore.ClientOptions{
		// Supports mocking for unit tests
		Transport: b.transport,
		// Per request policies to inject into HTTP pipeline
		PerCallPolicies: b.policies,
	}
}

// Builds the ARM module client options for control plan operations
// These options include the underlying transport to be used.
func (b *ClientOptionsBuilder) BuildArmClientOptions() *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			// Supports mocking for unit tests
			Transport: b.transport,
			// Per request policies to inject into HTTP pipeline
			PerCallPolicies: b.policies,
		},
	}
}
