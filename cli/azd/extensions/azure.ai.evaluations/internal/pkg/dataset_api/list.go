// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
//
// Compared component by component rather than as a float. ParseFloat reads
// "1.10" as one-point-one, which sorts it *below* "1.9" -- so after publishing
// 1.9 and 1.10, `show`, `download` and `delete` all resolved "latest" to 1.9
// and quietly acted on the older version. A version is a sequence of numbers,
// not a decimal fraction, and only looks like one while the minor stays under
// ten.
func VersionOrder(version string) []int {
	v := strings.TrimSpace(version)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ".")
	order := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			// Fall back to trailing digits, e.g. "v3" -> 3.
			i := len(p)
			for i > 0 && p[i-1] >= '0' && p[i-1] <= '9' {
				i--
			}
			if i == len(p) {
				return nil
			}
			if n, err = strconv.Atoi(p[i:]); err != nil {
				return nil
			}
		}
		order = append(order, n)
	}
	return order
}

// VersionGreater reports whether a is a strictly newer version than b.
//
// Both must be orderable; when either is not, the answer is false so an
// unparseable version never triggers a drift failure on its own.
func VersionGreater(a, b string) bool {
	orderA, orderB := VersionOrder(a), VersionOrder(b)
	if orderA == nil || orderB == nil {
		return false
	}
	// A shorter version is the earlier one where they agree so far: 1.2 comes
	// before 1.2.1, and treating the missing component as zero says so.
	for i := 0; i < len(orderA) || i < len(orderB); i++ {
		x, y := 0, 0
		if i < len(orderA) {
			x = orderA[i]
		}
		if i < len(orderB) {
			y = orderB[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// LatestVersion returns the highest version in the list, falling back to the
// last entry when none of the versions can be ordered.
func LatestVersion(datasets []Dataset) string {
	best := ""
	for _, d := range datasets {
		if VersionOrder(d.Version) == nil {
			continue
		}
		// VersionGreater carries the component-wise comparison, so ordering here
		// and ordering a drift check cannot come to different conclusions about
		// which of 1.9 and 1.10 is newer.
		if best == "" || VersionGreater(d.Version, best) {
			best = d.Version
		}
	}
	if best == "" && len(datasets) > 0 {
		return datasets[len(datasets)-1].Version
	}
	return best
}
