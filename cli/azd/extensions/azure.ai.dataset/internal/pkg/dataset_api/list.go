// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"azureaidataset/internal/messages"
)

// DatasetList is the paged response returned when listing datasets or the
// versions of one dataset.
type DatasetList struct {
	Value    []Dataset `json:"value"`
	NextLink string    `json:"nextLink,omitempty"`
}

// ListDatasets returns the datasets registered on the project.
func (c *DatasetClient) ListDatasets(ctx context.Context, apiVersion string) (*DatasetList, error) {
	first, err := doRequestTyped[DatasetList](c, ctx, http.MethodGet, pathDatasets, nil, nil, apiVersion)
	if err != nil {
		return nil, err
	}
	return c.followPages(ctx, first)
}

// ListDatasetVersions returns every version of a single dataset.
func (c *DatasetClient) ListDatasetVersions(
	ctx context.Context,
	name string,
	apiVersion string,
) (*DatasetList, error) {
	path := fmt.Sprintf("%s/%s/versions", pathDatasets, url.PathEscape(name))
	first, err := doRequestTyped[DatasetList](c, ctx, http.MethodGet, path, nil, nil, apiVersion)
	if err != nil {
		return nil, err
	}
	return c.followPages(ctx, first)
}

// maxListPages bounds page following so a service that keeps handing back a
// nextLink cannot spin forever.
const maxListPages = 100

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
			// to fail the command, but the list is short and nobody would know.
			log.Printf("[dataset_api] stopped paging after %d pages; the listing may be incomplete", len(seen))
			break
		}
		seen[next] = true

		body, err := c.doRequestGetURL(ctx, next)
		if err != nil {
			return nil, err
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

// DeleteDatasetVersion removes a single dataset version.
func (c *DatasetClient) DeleteDatasetVersion(
	ctx context.Context,
	name string,
	version string,
	apiVersion string,
) error {
	path := fmt.Sprintf(
		"%s/%s/versions/%s",
		pathDatasets, url.PathEscape(name), url.PathEscape(version),
	)
	_, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil, apiVersion)
	return err
}

// VersionOrder returns a sortable value for a version string, matching the
// decimal convention NextVersion produces ("1.0", "2.0"). Unparseable versions
// sort lowest.
func VersionOrder(version string) float64 {
	v := strings.TrimSpace(version)
	if v == "" {
		return -1
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	// Fall back to trailing digits, e.g. "v3" -> 3.
	i := len(v)
	for i > 0 && v[i-1] >= '0' && v[i-1] <= '9' {
		i--
	}
	if i == len(v) {
		return -1
	}
	if n, err := strconv.Atoi(v[i:]); err == nil {
		return float64(n)
	}
	return -1
}

// VersionGreater reports whether a is a strictly newer version than b.
//
// Both must be orderable; when either is not, the answer is false so an
// unparseable version never triggers a drift failure on its own.
func VersionGreater(a, b string) bool {
	orderA, orderB := VersionOrder(a), VersionOrder(b)
	if orderA < 0 || orderB < 0 {
		return false
	}
	return orderA > orderB
}

// LatestVersion returns the highest version in the list, falling back to the
// last entry when none of the versions can be ordered.
func LatestVersion(datasets []Dataset) string {
	best := ""
	bestOrder := -1.0
	for _, d := range datasets {
		if o := VersionOrder(d.Version); o > bestOrder {
			bestOrder, best = o, d.Version
		}
	}
	if best == "" && len(datasets) > 0 {
		return datasets[len(datasets)-1].Version
	}
	return best
}
