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
	"net/url"
	"strings"
	"unicode"
)

const (
	// defaultMaxPages is the default upper bound on pages fetched by [Pager.Collect].
	// Individual callers can override this via [PagerOptions.MaxPages].
	// A value of 0 means unlimited (no cap), which is the default for manual
	// NextPage iteration. Collect uses this default when MaxPages is unset.
	defaultMaxPages = 500
)

const (
	// maxPageResponseSize limits the maximum size of a single page response
	// body to prevent excessive memory consumption from malicious or
	// misconfigured servers. 10 MB is intentionally above typical Azure list
	// payloads while still bounding memory use.
	maxPageResponseSize int64 = 10 << 20 // 10 MB

	// maxErrorBodySize limits the size of error response bodies captured
	// for diagnostic purposes.
	maxErrorBodySize int64 = 64 << 10 // 64 KB
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
	client     HTTPDoer
	nextURL    string
	done       bool
	truncated  bool
	opts       PagerOptions
	originHost string // host of the initial URL for SSRF protection
	pageCount  int    // number of pages fetched so far
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

	// MaxPages limits the maximum number of pages that [Pager.Collect] will
	// fetch. When set to a positive value, Collect stops after fetching that
	// many pages. A value of 0 means unlimited (no cap) for manual NextPage
	// iteration; Collect applies [defaultMaxPages] when this is 0.
	MaxPages int

	// MaxItems limits the maximum total items that [Pager.Collect] will
	// accumulate. When the collected items reach this count, Collect stops
	// and returns the items gathered so far (truncated to MaxItems).
	// A value of 0 means unlimited (no cap).
	MaxItems int
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

	return s.client.Do(req) //nolint:gosec // G704: URL from pagination
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

	var originHost string
	if u, err := url.Parse(firstURL); err == nil {
		originHost = strings.ToLower(u.Hostname())
	}

	return &Pager[T]{
		client:     client,
		nextURL:    firstURL,
		opts:       *opts,
		originHost: originHost,
	}
}

// NewPagerFromHTTPClient creates a [Pager] backed by a standard [*http.Client].
// If client is nil, [http.DefaultClient] is used.
func NewPagerFromHTTPClient[T any](client *http.Client, firstURL string, opts *PagerOptions) *Pager[T] {
	if client == nil {
		client = http.DefaultClient
	}
	return NewPager[T](&stdHTTPDoer{client: client}, firstURL, opts)
}

// More reports whether there are more pages to fetch.
func (p *Pager[T]) More() bool {
	return !p.done && p.nextURL != ""
}

// Truncated reports whether the last [Collect] call stopped early because
// a collection bound (MaxPages or MaxItems) was hit.
func (p *Pager[T]) Truncated() bool {
	return p.truncated
}

// NextPage fetches the next page of results. Returns an error if the request
// fails, the response is not 2xx, or the body cannot be decoded.
//
// Response bodies are bounded to [maxPageResponseSize] to prevent
// excessive memory consumption. nextLink URLs are validated to prevent
// SSRF attacks (must stay on the same host with HTTPS).
//
// After the last page is consumed, [More] returns false.
func (p *Pager[T]) NextPage(ctx context.Context) (*PageResponse[T], error) {
	if !p.More() {
		return nil, errors.New("azdext.Pager.NextPage: no more pages")
	}

	if p.client == nil {
		return nil, errors.New("azdext.Pager.NextPage: client must not be nil")
	}

	resp, err := p.client.Do(ctx, p.opts.Method, p.nextURL, nil)
	if err != nil {
		return nil, fmt.Errorf("azdext.Pager.NextPage: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return nil, &PaginationError{
			StatusCode: resp.StatusCode,
			URL:        p.nextURL,
			Body:       sanitizeControlChars(string(body)),
		}
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPageResponseSize))
	if err != nil {
		return nil, fmt.Errorf("azdext.Pager.NextPage: failed to read response: %w", err)
	}

	var page PageResponse[T]
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, fmt.Errorf("azdext.Pager.NextPage: failed to decode response: %w", err)
	}

	if page.NextLink == "" {
		p.done = true
		p.nextURL = ""
	} else if err := p.validateNextLink(page.NextLink); err != nil {
		p.done = true
		p.nextURL = ""
		return &page, fmt.Errorf("azdext.Pager.NextPage: %w", err)
	} else {
		p.nextURL = page.NextLink
	}

	// Track page count for MaxPages enforcement in Collect.
	p.pageCount++

	return &page, nil
}

// validateNextLink checks that a nextLink URL is safe to follow.
// It rejects non-HTTPS schemes, URLs with embedded credentials, and
// URLs pointing to a different host than the original request (SSRF protection).
func (p *Pager[T]) validateNextLink(nextLink string) error {
	u, err := url.Parse(nextLink)
	if err != nil {
		return fmt.Errorf("invalid nextLink URL: %w", err)
	}

	// Reject relative URLs (empty scheme) — they would fail at request time
	// with a "missing protocol scheme" error, and non-absolute URLs may be
	// used for path-based SSRF attacks.
	if u.Scheme == "" {
		return fmt.Errorf("nextLink must be an absolute URL with an HTTPS scheme")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("nextLink must use HTTPS (got %q)", u.Scheme)
	}

	if u.User != nil {
		return errors.New("nextLink must not contain user credentials")
	}

	host := strings.ToLower(u.Hostname())
	if host != "" && p.originHost != "" && host != p.originHost {
		return fmt.Errorf("nextLink host %q does not match origin host %q (possible SSRF)", host, p.originHost)
	}

	return nil
}

// Collect is a convenience method that fetches all remaining pages and
// returns all items in a single slice.
//
// To prevent unbounded memory growth from runaway pagination, Collect
// enforces [PagerOptions.MaxPages] (defaults to [defaultMaxPages] when
// unset) and [PagerOptions.MaxItems]. When either limit is reached,
// iteration stops and the items collected so far are returned.
//
// If NextPage returns both page data and an error (e.g. rejected nextLink),
// the page data is included in the returned slice before returning the error.
func (p *Pager[T]) Collect(ctx context.Context) ([]T, error) {
	var all []T
	p.truncated = false

	maxPages := p.opts.MaxPages
	if maxPages <= 0 {
		maxPages = defaultMaxPages
	}

	for p.More() {
		page, err := p.NextPage(ctx)
		if page != nil {
			all = append(all, page.Value...)
		}
		if err != nil {
			return all, err
		}

		// Enforce MaxItems: truncate and stop if exceeded.
		if p.opts.MaxItems > 0 && len(all) >= p.opts.MaxItems {
			truncatedByItems := len(all) > p.opts.MaxItems
			if len(all) > p.opts.MaxItems {
				all = all[:p.opts.MaxItems]
			}
			if truncatedByItems || p.More() {
				p.truncated = true
			}
			break
		}

		// Enforce MaxPages: stop after collecting the configured number of pages.
		if p.pageCount >= maxPages {
			if p.More() {
				p.truncated = true
			}
			break
		}
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
		e.StatusCode, redactURL(e.URL),
	)
}

// redactURL strips query parameters and fragments from a URL to avoid leaking
// tokens, SAS signatures, or other secrets in log/error messages.
func redactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<invalid-url>"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// sanitizeControlChars replaces control characters (except newlines and tabs)
// with spaces to prevent log-forging attacks in stored error bodies.
func sanitizeControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, s)
}
