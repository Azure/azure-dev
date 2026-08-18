// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

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

	"azureaieval/internal/messages"
	"azureaieval/internal/urlsafe"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// maxPages bounds a walk the service controls.
//
// A nextLink that points at itself, or a service that keeps offering one,
// would otherwise spin forever holding the command open. The listings this
// walks are catalogues, not run output, so the bound is generous enough that
// reaching it means something is wrong rather than that a project is large.
const maxPages = 100

// followNextLink fetches one service-supplied page URL.
//
// The URL arrives in a response body, so it is checked against the endpoint
// before it is used: this client sends an Authorization header, and following
// a body-supplied link to another host would send the token there.
func (c *EvalClient) followNextLink(ctx context.Context, nextLink string) ([]byte, error) {
	parsed, err := url.Parse(nextLink)
	if err != nil {
		return nil, messages.InvalidNextLink(nextLink, err)
	}
	base, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, messages.InvalidEndpointURL(err)
	}

	// A nextLink is allowed to be relative, and a relative one carries no host
	// or scheme of its own. Resolving it against the endpoint first keeps the
	// origin check meaningful instead of refusing a legitimate link.
	next := base.ResolveReference(parsed)
	if !sameService(base, next) {
		return nil, messages.PageLinkLeftTheService(base.Host, next.Host)
	}

	req, err := runtime.NewRequest(ctx, http.MethodGet, next.String())
	if err != nil {
		return nil, messages.CreatingRequest(err)
	}
	log.Printf("[eval_api] GET %s", urlsafe.URL(next))

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

// sameService reports whether a page link stays on the host the client was
// pointed at. Scheme is compared too, so a link cannot downgrade to http.
func sameService(base, next *url.URL) bool {
	return strings.EqualFold(base.Host, next.Host) && strings.EqualFold(base.Scheme, next.Scheme)
}

// walkNextLinks gathers every page of an ARM-shaped listing.
//
// These listings answer with one page and a nextLink, and the link was decoded
// and dropped. That is a silent wrong answer rather than a short one: the
// evaluator listings settle which version is latest, and a version sitting on
// page two makes the answer an older one.
func walkNextLinks[T any](
	ctx context.Context,
	c *EvalClient,
	first *T,
	nextLinkOf func(*T) string,
	merge func(into, page *T),
) (*T, error) {
	seen := map[string]bool{}
	link := nextLinkOf(first)
	for link != "" {
		// A repeated link, not just a self-referencing one, ends the walk: a
		// two-page cycle would otherwise run to maxPages for no benefit.
		if seen[link] || len(seen) >= maxPages {
			fmt.Fprint(os.Stderr, messages.Warning(messages.ListingTruncated(len(seen))))
			break
		}
		seen[link] = true

		body, err := c.followNextLink(ctx, link)
		if err != nil {
			return nil, err
		}
		var page T
		if len(body) > 0 {
			if err := json.Unmarshal(body, &page); err != nil {
				return nil, messages.ParsingResponse(err)
			}
		}
		merge(first, &page)
		link = nextLinkOf(&page)
	}
	return first, nil
}
