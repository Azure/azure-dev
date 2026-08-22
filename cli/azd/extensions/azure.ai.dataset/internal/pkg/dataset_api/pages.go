// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"azureaidataset/internal/messages"
	"azureaidataset/internal/urlsafe"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// maxListPages bounds page following so a service that keeps handing back a
// nextLink cannot spin forever.
const maxListPages = 100

// pageWalkError marks a failure that happened after the first page.
//
// The first page answered, so the dataset exists; a 404 on a later page is the
// continuation failing, not the dataset being unknown. IsNotFound refuses this
// wrapper for that reason -- but the cause is still reachable, so a cancelled
// context or an auth failure part-way through a walk classifies as itself
// rather than as an unreadable listing.
type pageWalkError struct{ cause error }

func (e pageWalkError) Error() string {
	return "reading a later page of the listing: " + e.cause.Error()
}

func (e pageWalkError) Unwrap() error { return e.cause }

// followPages walks nextLink until the service stops sending one, returning a
// single list holding every page. Without this, a project with more than one
// page lists incompletely and a latest-version check can decide from a stale
// first page.
func (c *DatasetClient) followPages(ctx context.Context, first *DatasetList) (*DatasetList, error) {
	if first == nil {
		return nil, nil
	}

	// Copied rather than aliased: appending to first.Value could write into the
	// caller's backing array when it has spare capacity.
	out := &DatasetList{Value: append([]Dataset(nil), first.Value...)}
	seen := map[string]bool{}
	for next := first.NextLink; next != ""; {
		if seen[next] || len(seen) >= maxListPages {
			// A repeated or endless link is the service misbehaving, not a reason
			// to fail the command -- but the list is short and, said through log,
			// nobody would know: log goes to io.Discard unless --debug.
			fmt.Fprint(os.Stderr, messages.Warning(messages.ListingTruncated(len(seen))))
			break
		}
		seen[next] = true

		body, err := c.doRequestGetURL(ctx, next)
		if err != nil {
			return nil, pageWalkError{cause: err}
		}
		var page DatasetList
		// A page that answers 200 with no body ends the walk; unmarshaling it
		// would throw away every page already collected.
		if len(body) > 0 {
			if err := json.Unmarshal(body, &page); err != nil {
				return nil, messages.ParsingResponse(err)
			}
		}
		out.Value = append(out.Value, page.Value...)
		next = page.NextLink
	}
	return out, nil
}

// sameOrigin reports whether two URLs share a scheme and host.
func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

// doRequestGetURL issues a GET against an absolute URL the service supplied,
// such as a nextLink. The URL is refused unless it shares the endpoint's
// origin: the pipeline attaches the caller's token, so a link pointing
// elsewhere would hand that token to another host.
func (c *DatasetClient) doRequestGetURL(ctx context.Context, rawURL string) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, messages.InvalidNextLink(rawURL, err)
	}
	base, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, messages.InvalidEndpointURL(err)
	}

	// A nextLink is allowed to be relative. Resolving it against the endpoint
	// first keeps the origin check meaningful instead of rejecting a legitimate
	// relative link for having no scheme or host of its own.
	u := base.ResolveReference(parsed)
	if !sameOrigin(u, base) {
		return nil, messages.NextLinkOffOrigin(u.Scheme + "://" + u.Host)
	}

	req, err := runtime.NewRequest(ctx, http.MethodGet, u.String())
	if err != nil {
		return nil, messages.CreatingRequest(err)
	}

	log.Printf("[dataset_api] GET %s", urlsafe.URL(u))

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
