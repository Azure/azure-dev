package azsdk

import (
	"context"

	azdCloud "github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

func DefaultClientOptionsBuilder(
	ctx context.Context,
	httpClient httputil.HttpClient,
	userAgent string,
	cloud *azdCloud.Cloud,
) *ClientOptionsBuilder {
	return NewClientOptionsBuilder().
		WithTransport(httpClient).
		WithPerCallPolicy(NewUserAgentPolicy(userAgent)).
		WithPerCallPolicy(NewMsCorrelationPolicy(ctx)).
		WithCloud(*cloud.Configuration)
}

// // TODO: Is this allowed?
// func DefaultCLientOptionsBuilder(
// 	ctx context.Context,
// 	httpClient httputil.HttpClient,
// 	userAgent string,
// ) *ClientOptionsBuilder {
// 	return DefaultClientOptionsBuilder(ctx, httpClient, userAgent, cloud.GetAzurePublic())
// }
