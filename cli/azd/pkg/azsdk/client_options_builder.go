package azsdk

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type ClientOptionsBuilder struct {
	transport        policy.Transporter
	perCallPolicies  []policy.Policy
	perRetryPolicies []policy.Policy

	userAgentPolicy   policy.Policy
	correlationPolicy policy.Policy
}

func NewClientOptionsBuilder() *ClientOptionsBuilder {
	return &ClientOptionsBuilder{}
}

// Sets the underlying transport used for executing HTTP requests
func (b *ClientOptionsBuilder) WithTransport(transport policy.Transporter) *ClientOptionsBuilder {
	b.transport = transport
	return b
}

// Sets the user agent to be used for all requests. Set userAgent to "" to not use a user agent policy.
func (b *ClientOptionsBuilder) SetUserAgent(userAgent string) *ClientOptionsBuilder {
	if userAgent == "" {
		b.userAgentPolicy = nil
	} else {
		b.userAgentPolicy = NewUserAgentPolicy(userAgent)
	}
	return b
}

// Sets the context to be used for all requests. Set ctx to nil to not use a correlation policy.
func (b *ClientOptionsBuilder) SetContext(ctx context.Context) *ClientOptionsBuilder {
	if ctx == nil {
		b.correlationPolicy = nil
	} else {
		b.correlationPolicy = NewMsCorrelationPolicy(ctx)
	}
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

// Combines the per-call policies with the user agent and correlation policies
// TODO: there may be a more idomatic way to do this
func (b *ClientOptionsBuilder) buildPerCallPolicies() []policy.Policy {
	if b.perCallPolicies == nil && b.userAgentPolicy == nil && b.correlationPolicy == nil {
		return nil
	}

	policies := make([]policy.Policy, len(b.perCallPolicies))
	copy(policies, b.perCallPolicies)

	if b.userAgentPolicy != nil {
		policies = append(policies, b.userAgentPolicy)
	}
	if b.correlationPolicy != nil {
		policies = append(policies, b.correlationPolicy)
	}
	return policies
}

// Builds the az core client options for data plane operations
// These options include the underlying transport to be used.
func (b *ClientOptionsBuilder) BuildCoreClientOptions() *azcore.ClientOptions {
	return &azcore.ClientOptions{
		// Supports mocking for unit tests
		Transport: b.transport,
		// Per request policies to inject into HTTP pipeline
		PerCallPolicies: b.buildPerCallPolicies(),
		// Per retry policies to inject into HTTP pipeline
		PerRetryPolicies: b.perRetryPolicies,
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
			PerCallPolicies: b.buildPerCallPolicies(),
			// Per retry policies to inject into HTTP pipeline
			PerRetryPolicies: b.perRetryPolicies,
			// Logging policy options.
			// Always allow Azure correlation header
			Logging: policy.LogOptions{
				AllowedHeaders: []string{cMsCorrelationIdHeader},
			},
		},
	}
}
