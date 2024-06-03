// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
)

// Define an API for client methods that returns a page and a continuation token.

// getDefinitionsPager providers a pager to iterate for all the pages from GetDefinitions.
func getDefinitionsPager(
	ctx context.Context,
	client build.Client,
	projectId *string,
	pipelineName *string,
) *runtime.Pager[*build.GetDefinitionsResponseValue] {

	return runtime.NewPager(
		runtime.PagingHandler[*build.GetDefinitionsResponseValue]{
			More: func(current *build.GetDefinitionsResponseValue) bool {
				return current.ContinuationToken != ""
			},
			Fetcher: func(
				ctx context.Context,
				current **build.GetDefinitionsResponseValue) (*build.GetDefinitionsResponseValue, error) {
				var response *build.GetDefinitionsResponseValue
				var err error

				if current == nil {
					// first page
					response, err = client.GetDefinitions(ctx, build.GetDefinitionsArgs{
						Project: projectId,
						Name:    pipelineName,
					})
				} else {
					// not first page
					response, err = client.GetDefinitions(ctx, build.GetDefinitionsArgs{
						ContinuationToken: &((*current).ContinuationToken),
					})
				}
				if err != nil {
					return nil, err
				}
				return response, nil
			},
		},
	)
}
