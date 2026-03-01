// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// Pager provides a generic, lazy iterator over paginated Azure REST API
// responses that use the standard { value, nextLink } pattern.
//
// Usage:
//
//	pager := azdext.NewPager[MyItem](client, firstURL, nil)
//	for pager.More() {
//	    page, err := pager.NextPage(ctx)
//	    if err != nil { ... }
//	    for _, item := range page.Value {
//	        // process item
//	    }
//	}
type Pager[T any] struct {
	client  HTTPDoer
	nextURL string
	done    bool
	opts    PagerOptions
}

// PageResponse is a single page returned by [Pager.NextPage].
type PageResponse[T any] struct {
	// Value contains the items for this page.
	Value []T `json:"value"`

	// NextLink is the URL to the next page, or empty if this is the last page.
	NextLink string `json:"nextLink,omitempty"`
}

// PagerOptions configures a [Pager].
type PagerOptions struct {
	// Method overrides the HTTP method used for page requests. Defaults to GET.
	Method string
}

// HTTPDoer abstracts the HTTP call so that [ResilientClient] or any
// *http.Client can power pagination.
type HTTPDoer interface {
	Do(ctx context.Context, method, url string, body io.Reader) (*http.Response, error)
}

// stdHTTPDoer wraps *http.Client to satisfy HTTPDoer.
type stdHTTPDoer struct {
	client *http.Client
}

func (s *stdHTTPDoer) Do(ctx context.Context, method, url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	return s.client.Do(req)
}

// NewPager creates a [Pager] that iterates over a paginated endpoint.
//
// client may be a [*ResilientClient] or any type satisfying [HTTPDoer].
// firstURL is the initial page URL.
func NewPager[T any](client HTTPDoer, firstURL string, opts *PagerOptions) *Pager[T] {
	if opts == nil {
		opts = &PagerOptions{}
	}

	if opts.Method == "" {
		opts.Method = http.MethodGet
	}

	return &Pager[T]{
		client:  client,
		nextURL: firstURL,
		opts:    *opts,
	}
}

// NewPagerFromHTTPClient creates a [Pager] backed by a standard [*http.Client].
func NewPagerFromHTTPClient[T any](client *http.Client, firstURL string, opts *PagerOptions) *Pager[T] {
	return NewPager[T](&stdHTTPDoer{client: client}, firstURL, opts)
}

// More reports whether there are more pages to fetch.
func (p *Pager[T]) More() bool {
	return !p.done && p.nextURL != ""
}

// NextPage fetches the next page of results. Returns an error if the request
// fails, the response is not 2xx, or the body cannot be decoded.
//
// After the last page is consumed, [More] returns false.
func (p *Pager[T]) NextPage(ctx context.Context) (*PageResponse[T], error) {
	if !p.More() {
		return nil, errors.New("azdext.Pager.NextPage: no more pages")
	}

	resp, err := p.client.Do(ctx, p.opts.Method, p.nextURL, nil)
	if err != nil {
		return nil, fmt.Errorf("azdext.Pager.NextPage: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, &PaginationError{
			StatusCode: resp.StatusCode,
			URL:        p.nextURL,
			Body:       string(body),
		}
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("azdext.Pager.NextPage: failed to read response: %w", err)
	}

	var page PageResponse[T]
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, fmt.Errorf("azdext.Pager.NextPage: failed to decode response: %w", err)
	}

	if page.NextLink == "" {
		p.done = true
	}

	p.nextURL = page.NextLink

	return &page, nil
}

// Collect is a convenience method that fetches all remaining pages and
// returns all items in a single slice. Use with caution on large result sets.
func (p *Pager[T]) Collect(ctx context.Context) ([]T, error) {
	var all []T

	for p.More() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return all, err
		}

		all = append(all, page.Value...)
	}

	return all, nil
}

// PaginationError is returned when a page request receives a non-2xx response.
type PaginationError struct {
	StatusCode int
	URL        string
	Body       string
}

func (e *PaginationError) Error() string {
	return fmt.Sprintf(
		"azdext.Pager: page request returned HTTP %d (url=%s)",
		e.StatusCode, e.URL,
	)
}
