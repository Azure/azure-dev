// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "math"

// boundedInt32Index converts a slice index into the int32 form expected by the
// azdext Select API (SelectedIndex / *resp.Value).
//
// All callers in this package index into small, statically-bounded slices
// (enum values, resource tiers, curated template lists, model name lists)
// where overflow is impossible. Routing every such conversion through this
// one helper keeps the gosec G115 suppression in a single place and makes
// the safety argument explicit:
//
//   - Indices are non-negative (returned by range over a slice).
//   - Slice lengths are bounded well below math.MaxInt32 at every call site.
//
// The defensive clamp below means a future caller that violates the
// invariant returns the safe default (index 0 = first option) instead of
// panicking or wrapping.
func boundedInt32Index(i int) int32 {
	if i < 0 || i > math.MaxInt32 {
		return 0
	}
	return int32(i) //nolint:gosec // G115: bounds enforced by the check above
}
