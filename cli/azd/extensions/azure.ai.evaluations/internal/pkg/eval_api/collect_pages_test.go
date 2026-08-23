// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A listing that cannot be finished is an error, not a short answer.
//
// Both of these used to log and return the rows gathered so far. log is pointed
// at io.Discard without --debug, so that was indistinguishable from a complete
// listing -- and these rows decide "is this name ambiguous?" and "which run is
// newest?", where a missing page turns a refusal into a wrong choice.
func TestAListingThatCannotFinishIsAnError(t *testing.T) {
	t.Run("a cursor that points back at itself", func(t *testing.T) {
		calls := 0
		err := collectPages(0, func(map[string]string) (int, bool, string, error) {
			calls++
			return 1, true, "same-cursor", nil
		})

		require.Error(t, err, "the service never advanced, so the rows gathered are partial")
		assert.Contains(t, err.Error(), "same-cursor")
		assert.Contains(t, err.Error(), "incomplete")
		assert.Equal(t, 2, calls, "the repeat is caught on the second page, not walked to the cap")
	})

	t.Run("a listing longer than the page cap", func(t *testing.T) {
		page := 0
		err := collectPages(0, func(map[string]string) (int, bool, string, error) {
			page++
			return 1, true, "cursor-" + strconv.Itoa(page), nil
		})

		require.Error(t, err, "the walk stopped at the cap with more to fetch")
		assert.Contains(t, err.Error(), "incomplete")
	})

	t.Run("a listing the service finishes", func(t *testing.T) {
		page := 0
		err := collectPages(0, func(map[string]string) (int, bool, string, error) {
			page++
			if page == 1 {
				return 2, true, "first", nil
			}
			return 1, false, "second", nil
		})

		require.NoError(t, err, "the service said there was no more, which is a complete answer")
		assert.Equal(t, 2, page)
	})

	t.Run("a limit the caller reaches", func(t *testing.T) {
		err := collectPages(2, func(map[string]string) (int, bool, string, error) {
			return 2, true, "more-to-come", nil
		})

		require.NoError(t, err, "the caller asked for two rows and got them; the rest is not missing")
	})
}
