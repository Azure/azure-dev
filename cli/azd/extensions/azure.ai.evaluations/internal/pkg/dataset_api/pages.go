// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/urlsafe"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// maxPages bounds a walk the service controls, so a nextLink that points at
// itself cannot hold the command open indefinitely.
const maxPages = 100

// followNextLink fetches one service-supplied page URL.
//
// The URL arrives in a response body and this client sends an Authorization
// header, so a link to another host would send the token there. Checked
// against the endpoint before it is used.
func (c *DatasetClient) followNextLink(ctx context.Context, nextLink string) ([]byte, error) {
	next, err := url.Parse(nextLink)
	if err != nil {
		return nil, messages.InvalidEndpointURL(err)
	}
	base, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, messages.InvalidEndpointURL(err)
	}
	if !strings.EqualFold(base.Host, next.Host) || !strings.EqualFold(base.Scheme, next.Scheme) {
		return nil, messages.PageLinkLeftTheService(base.Host, next.Host)
	}

	req, err := runtime.NewRequest(ctx, http.MethodGet, next.String())
	if err != nil {
		return nil, messages.CreatingRequest(err)
	}
	log.Printf("[dataset_api] GET %s", urlsafe.URL(next))

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, messages.RequestFailed(err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, messages.ReadingResponseBody(err)
	}
	if !runtime.HasStatusCode(resp, http.StatusOK) {
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		return nil, messages.ServiceRefused(resp.StatusCode, runtime.NewResponseError(resp))
	}
	return respBody, nil
}

// walkDatasetPages gathers every page of a dataset listing.
//
// The listing answered with one page and a nextLink, and the link was decoded
// and dropped. UploadVersion picks the next version from this listing, so a
// version on page two meant reusing one that already exists.
func (c *DatasetClient) walkDatasetPages(ctx context.Context, first *DatasetList) (*DatasetList, error) {
	link := first.NextLink
	for pages := 0; link != "" && pages < maxPages; pages++ {
		body, err := c.followNextLink(ctx, link)
		if err != nil {
			return nil, err
		}
		var page DatasetList
		if len(body) > 0 {
			if err := json.Unmarshal(body, &page); err != nil {
				return nil, messages.ParsingResponse(err)
			}
		}
		first.Value = append(first.Value, page.Value...)
		if page.NextLink == link {
			break
		}
		link = page.NextLink
	}
	return first, nil
}
