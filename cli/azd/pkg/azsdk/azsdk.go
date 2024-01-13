package azsdk

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

func DefaultClientOptionsBuilder(
	ctx context.Context,
	httpClient httputil.HttpClient,
	userAgent string,
	cloud *cloud.Cloud,
) *ClientOptionsBuilder {

	return NewClientOptionsBuilder().
		WithTransport(httpClient).
		WithPerCallPolicy(NewUserAgentPolicy(userAgent)).
		WithPerCallPolicy(NewMsCorrelationPolicy(ctx)).
		WithCloud(cloud.Configuration)
}
