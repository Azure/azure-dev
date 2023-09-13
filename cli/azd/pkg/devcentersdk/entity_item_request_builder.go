package devcentersdk

import (
	"context"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type entityItemRequestInfo struct {
	selectParams []string
}

type EntityItemRequestBuilder[T any] struct {
	id          string
	client      *devCenterClient
	builder     *T
	requestInfo *entityItemRequestInfo
}

// Creates a new EntityItemRequestBuilder
// builder - The parent entity builder
func newEntityItemRequestBuilder[T any](builder *T, client *devCenterClient, id string) *EntityItemRequestBuilder[T] {
	return &EntityItemRequestBuilder[T]{
		client:      client,
		builder:     builder,
		id:          id,
		requestInfo: &entityItemRequestInfo{},
	}
}

// Creates a HTTP request for the specified method, URL and configured request information
func (b *EntityItemRequestBuilder[T]) createRequest(
	ctx context.Context,
	method string,
	path string,
) (*policy.Request, error) {
	req, err := b.client.createRequest(ctx, method, path)
	if err != nil {
		return nil, err
	}

	raw := req.Raw()
	query := raw.URL.Query()

	if len(b.requestInfo.selectParams) > 0 {
		query.Set("$select", strings.Join(b.requestInfo.selectParams, ","))
	}

	raw.URL.RawQuery = query.Encode()

	return req, err
}

func (b *EntityItemRequestBuilder[T]) Select(params []string) *T {
	b.requestInfo.selectParams = params

	return b.builder
}
