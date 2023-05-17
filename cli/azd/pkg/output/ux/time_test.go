// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDurationAsText(t *testing.T) {
	tcs := []struct {
		str      string
		expected string
	}{
		{str: "1.5s", expected: "1 second"},
		{str: "2.5s", expected: "2 seconds"},
		{str: "1m", expected: "1 minute"},
		{str: "2m", expected: "2 minutes"},
		{str: "1h", expected: "1 hour"},
		{str: "2h", expected: "2 hours"},
		{str: "1h2m3s", expected: "1 hour 2 minutes 3 seconds"},
	}

	MustParseDuration := func(s string) time.Duration {
		d, err := time.ParseDuration(s)
		require.NoError(t, err)
		return d
	}

	for _, tc := range tcs {
		require.Equal(t, tc.expected, DurationAsText(MustParseDuration(tc.str)))
	}
}
