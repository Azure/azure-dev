package graphsdk

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
	client      *GraphClient
	requestInfo *entityListRequestInfo
}

// Creates a new EntityListRequestBuilder that provides common functionality for list operations
// include $filter, $top and $skip
func newEntityListRequestBuilder[T any](builder *T, client *GraphClient) *EntityListRequestBuilder[T] {
	return &EntityListRequestBuilder[T]{
		builder:     builder,
		client:      client,
		requestInfo: &entityListRequestInfo{},
	}
}

// Creates a HTTP request for the specified method, URL and configured request information
func (b *EntityListRequestBuilder[T]) createRequest(
	ctx context.Context,
	method string,
	rawUrl string,
) (*policy.Request, error) {
	req, err := runtime.NewRequest(ctx, method, rawUrl)
	if err != nil {
		return nil, err
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
