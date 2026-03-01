// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// mockDoer is a test double for HTTPDoer.
type mockDoer struct {
	responses []*doerResponse
	calls     int
}

type doerResponse struct {
	resp *http.Response
	err  error
}

func (m *mockDoer) Do(_ context.Context, _, _ string, _ io.Reader) (*http.Response, error) {
	if m.calls >= len(m.responses) {
		return nil, errors.New("no more mock responses")
	}

	r := m.responses[m.calls]
	m.calls++

	return r.resp, r.err
}

// pageJSON builds a PageResponse JSON body.
func pageJSON[T any](value []T, nextLink string) string {
	page := PageResponse[T]{Value: value, NextLink: nextLink}
	data, _ := json.Marshal(page)
	return string(data)
}

func TestPager_SinglePage(t *testing.T) {
	t.Parallel()

	body := pageJSON([]string{"a", "b", "c"}, "")

	doer := &mockDoer{
		responses: []*doerResponse{
			{resp: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     http.Header{},
			}},
		},
	}

	pager := NewPager[string](doer, "https://example.com/api?page=1", nil)

	if !pager.More() {
		t.Fatal("expected More() = true before first page")
	}

	page, err := pager.NextPage(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(page.Value) != 3 {
		t.Fatalf("len(Value) = %d, want 3", len(page.Value))
	}

	if page.Value[0] != "a" || page.Value[1] != "b" || page.Value[2] != "c" {
		t.Errorf("Value = %v, want [a b c]", page.Value)
	}

	if pager.More() {
		t.Error("expected More() = false after last page")
	}
}

func TestPager_MultiplePages(t *testing.T) {
	t.Parallel()

	page1 := pageJSON([]int{1, 2}, "https://example.com/api?page=2")
	page2 := pageJSON([]int{3, 4}, "https://example.com/api?page=3")
	page3 := pageJSON([]int{5}, "")

	doer := &mockDoer{
		responses: []*doerResponse{
			{resp: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(page1)),
				Header:     http.Header{},
			}},
			{resp: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(page2)),
				Header:     http.Header{},
			}},
			{resp: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(page3)),
				Header:     http.Header{},
			}},
		},
	}

	pager := NewPager[int](doer, "https://example.com/api?page=1", nil)

	all, err := pager.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if len(all) != 5 {
		t.Fatalf("len(all) = %d, want 5", len(all))
	}

	for i, want := range []int{1, 2, 3, 4, 5} {
		if all[i] != want {
			t.Errorf("all[%d] = %d, want %d", i, all[i], want)
		}
	}
}

func TestPager_EmptyPage(t *testing.T) {
	t.Parallel()

	body := pageJSON([]string{}, "")

	doer := &mockDoer{
		responses: []*doerResponse{
			{resp: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     http.Header{},
			}},
		},
	}

	pager := NewPager[string](doer, "https://example.com/api", nil)

	page, err := pager.NextPage(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(page.Value) != 0 {
		t.Errorf("len(Value) = %d, want 0", len(page.Value))
	}

	if pager.More() {
		t.Error("expected More() = false after empty last page")
	}
}

func TestPager_HTTPError(t *testing.T) {
	t.Parallel()

	doer := &mockDoer{
		responses: []*doerResponse{
			{resp: &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader(`{"error":"forbidden"}`)),
				Header:     http.Header{},
			}},
		},
	}

	pager := NewPager[string](doer, "https://example.com/api", nil)

	_, err := pager.NextPage(context.Background())
	if err == nil {
		t.Fatal("expected error for HTTP 403")
	}

	var pagErr *PaginationError
	if !errors.As(err, &pagErr) {
		t.Fatalf("error type = %T, want *PaginationError", err)
	}

	if pagErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want %d", pagErr.StatusCode, http.StatusForbidden)
	}
}

func TestPager_NetworkError(t *testing.T) {
	t.Parallel()

	doer := &mockDoer{
		responses: []*doerResponse{
			{err: errors.New("connection reset")},
		},
	}

	pager := NewPager[string](doer, "https://example.com/api", nil)

	_, err := pager.NextPage(context.Background())
	if err == nil {
		t.Fatal("expected error for network failure")
	}
}

func TestPager_InvalidJSON(t *testing.T) {
	t.Parallel()

	doer := &mockDoer{
		responses: []*doerResponse{
			{resp: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("not json")),
				Header:     http.Header{},
			}},
		},
	}

	pager := NewPager[string](doer, "https://example.com/api", nil)

	_, err := pager.NextPage(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestPager_NoMorePages(t *testing.T) {
	t.Parallel()

	body := pageJSON([]string{"x"}, "")

	doer := &mockDoer{
		responses: []*doerResponse{
			{resp: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     http.Header{},
			}},
		},
	}

	pager := NewPager[string](doer, "https://example.com/api", nil)

	_, _ = pager.NextPage(context.Background())

	_, err := pager.NextPage(context.Background())
	if err == nil {
		t.Fatal("expected error when calling NextPage after last page")
	}
}

func TestPager_EmptyFirstURL(t *testing.T) {
	t.Parallel()

	doer := &mockDoer{}
	pager := NewPager[string](doer, "", nil)

	if pager.More() {
		t.Error("expected More() = false for empty initial URL")
	}
}

type testStruct struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestPager_StructType(t *testing.T) {
	t.Parallel()

	items := []testStruct{
		{Name: "alpha", Count: 1},
		{Name: "beta", Count: 2},
	}

	body := pageJSON(items, "")

	doer := &mockDoer{
		responses: []*doerResponse{
			{resp: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     http.Header{},
			}},
		},
	}

	pager := NewPager[testStruct](doer, "https://example.com/api", nil)

	page, err := pager.NextPage(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(page.Value) != 2 {
		t.Fatalf("len(Value) = %d, want 2", len(page.Value))
	}

	if page.Value[0].Name != "alpha" || page.Value[0].Count != 1 {
		t.Errorf("Value[0] = %+v, want {alpha 1}", page.Value[0])
	}

	if page.Value[1].Name != "beta" || page.Value[1].Count != 2 {
		t.Errorf("Value[1] = %+v, want {beta 2}", page.Value[1])
	}
}

func TestPager_CollectPartialError(t *testing.T) {
	t.Parallel()

	page1 := pageJSON([]string{"a"}, "https://example.com/api?page=2")

	doer := &mockDoer{
		responses: []*doerResponse{
			{resp: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(page1)),
				Header:     http.Header{},
			}},
			{err: errors.New("network timeout")},
		},
	}

	pager := NewPager[string](doer, "https://example.com/api?page=1", nil)

	all, err := pager.Collect(context.Background())
	if err == nil {
		t.Fatal("expected error from second page")
	}

	// Should still return items collected before the error.
	if len(all) != 1 {
		t.Errorf("len(all) = %d, want 1 (partial results before error)", len(all))
	}

	if all[0] != "a" {
		t.Errorf("all[0] = %q, want %q", all[0], "a")
	}
}

func TestNewPagerFromHTTPClient(t *testing.T) {
	t.Parallel()

	// Just test that the constructor works; actual HTTP calls tested above.
	pager := NewPagerFromHTTPClient[string](http.DefaultClient, "https://example.com/api", nil)
	if pager == nil {
		t.Fatal("NewPagerFromHTTPClient returned nil")
	}
}
