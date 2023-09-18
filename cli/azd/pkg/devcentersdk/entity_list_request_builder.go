package devcentersdk

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

type entityListRequestInfo struct {
	filter *string
	top    *int
}

type EntityListRequestBuilder[T any] struct {
	builder     *T
	client      *devCenterClient
	requestInfo *entityListRequestInfo
	devCenter   *DevCenter
}

// Creates a new EntityListRequestBuilder that provides common functionality for list operations
// include $filter, $top and $skip
func newEntityListRequestBuilder[T any](
	builder *T,
	client *devCenterClient,
	devCenter *DevCenter,
) *EntityListRequestBuilder[T] {
	return &EntityListRequestBuilder[T]{
		builder:     builder,
		client:      client,
		requestInfo: &entityListRequestInfo{},
		devCenter:   devCenter,
	}
}

// Creates a HTTP request for the specified method, URL and configured request information
func (b *EntityListRequestBuilder[T]) createRequest(
	ctx context.Context,
	method string,
	path string,
) (*policy.Request, error) {
	host, err := b.client.host(ctx, b.devCenter)
	if err != nil {
		return nil, fmt.Errorf("devcenter is not set, %w", err)
	}

	req, err := runtime.NewRequest(ctx, method, fmt.Sprintf("%s/%s", host, path))
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	raw := req.Raw()
	query := raw.URL.Query()

	if b.requestInfo.filter != nil {
		query.Set("$filter", *b.requestInfo.filter)
	}

	if b.requestInfo.top != nil {
		query.Set("$top", fmt.Sprint((*b.requestInfo.top)))
	}

	raw.URL.RawQuery = query.Encode()

	return req, err
}

func (b *EntityListRequestBuilder[T]) Filter(filterExpression string) *T {
	b.requestInfo.filter = &filterExpression

	return b.builder
}

func (b *EntityListRequestBuilder[T]) Top(count int) *T {
	b.requestInfo.top = &count

	return b.builder
}
