package devcentersdk

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

type entityItemRequestInfo struct {
	selectParams []string
}

type EntityItemRequestBuilder[T any] struct {
	id          string
	client      *devCenterClient
	builder     *T
	requestInfo *entityItemRequestInfo
	devCenter   *DevCenter
}

// Creates a new EntityItemRequestBuilder
// builder - The parent entity builder
func newEntityItemRequestBuilder[T any](
	builder *T,
	client *devCenterClient,
	devCenter *DevCenter,
	id string,
) *EntityItemRequestBuilder[T] {
	return &EntityItemRequestBuilder[T]{
		client:      client,
		devCenter:   devCenter,
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
	host, err := b.client.host(ctx, b.devCenter)
	if err != nil {
		return nil, fmt.Errorf("devcenter is not set, %w", err)
	}

	req, err := runtime.NewRequest(ctx, method, fmt.Sprintf("%s/%s", host, path))
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

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
