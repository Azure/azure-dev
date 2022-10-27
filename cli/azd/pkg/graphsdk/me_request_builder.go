package graphsdk

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

type MeItemRequestBuilder struct {
	*EntityItemRequestBuilder[MeItemRequestBuilder]
}

func newMeItemRequestBuilder(client *GraphClient) *MeItemRequestBuilder {
	builder := &MeItemRequestBuilder{}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, client, "me")

	return builder
}

// Gets the user profile information for the current logged in user
func (b *MeItemRequestBuilder) Get(ctx context.Context) (*UserProfile, error) {
	req, err := b.createRequest(ctx, http.MethodGet, fmt.Sprintf("%s/me", b.client.host))
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	res, err := b.client.pipeline.Do(req)
	if err != nil {
		return nil, httputil.HandleRequestError(res, err)
	}

	if !runtime.HasStatusCode(res, http.StatusOK) {
		return nil, runtime.NewResponseError(res)
	}

	return httputil.ReadRawResponse[UserProfile](res)
}
