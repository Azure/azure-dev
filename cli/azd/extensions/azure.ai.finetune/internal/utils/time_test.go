// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTimeFormat_Constant(t *testing.T) {
	require.Equal(t, "2006-01-02 15:04:05 UTC", TimeFormat)
}

func TestUnixTimestampToUTC(t *testing.T) {
	tests := []struct {
		name      string
		timestamp int64
		expected  time.Time
	}{
		{
			name:      "ZeroTimestamp",
			timestamp: 0,
			expected:  time.Time{},
		},
		{
			name:      "ValidTimestamp",
			timestamp: 1704067200, // 2024-01-01 00:00:00 UTC
			expected:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "EpochTimestamp",
			timestamp: 1, // 1970-01-01 00:00:01 UTC
			expected:  time.Date(1970, 1, 1, 0, 0, 1, 0, time.UTC),
		},
		{
			name:      "SpecificTimestamp",
			timestamp: 1718467200, // 2024-06-15 16:00:00 UTC
			expected:  time.Date(2024, 6, 15, 16, 0, 0, 0, time.UTC),
		},
		{
			name:      "NegativeTimestamp",
			timestamp: -86400, // 1969-12-31 00:00:00 UTC (day before epoch)
			expected:  time.Date(1969, 12, 31, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := UnixTimestampToUTC(tt.timestamp)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatTime(t *testing.T) {
	tests := []struct {
		name     string
		time     time.Time
		expected string
	}{
		{
			name:     "ZeroTime",
			time:     time.Time{},
			expected: "",
		},
		{
			name:     "ValidTime",
			time:     time.Date(2024, 6, 15, 14, 30, 45, 0, time.UTC),
			expected: "2024-06-15 14:30:45 UTC",
		},
		{
			name:     "MidnightTime",
			time:     time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			expected: "2024-01-01 00:00:00 UTC",
		},
		{
			name:     "EndOfDay",
			time:     time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC),
			expected: "2024-12-31 23:59:59 UTC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatTime(tt.time)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateDuration(t *testing.T) {
	tests := []struct {
		name       string
		createdAt  int64
		finishedAt int64
		expected   time.Duration
	}{
		{
			name:       "ZeroFinishedAt",
			createdAt:  1704067200,
			finishedAt: 0,
			expected:   0,
		},
		{
			name:       "NegativeFinishedAt",
			createdAt:  1704067200,
			finishedAt: -1,
			expected:   0,
		},
		{
			name:       "FinishedBeforeCreated",
			createdAt:  1704067200,
			finishedAt: 1704060000, // Before created
			expected:   0,
		},
		{
			name:       "OneHourDuration",
			createdAt:  1704067200,        // 2024-01-01 00:00:00
			finishedAt: 1704067200 + 3600, // + 1 hour
			expected:   1 * time.Hour,
		},
		{
			name:       "MultiHourDuration",
			createdAt:  1704067200,
			finishedAt: 1704067200 + 7200, // + 2 hours
			expected:   2 * time.Hour,
		},
		{
			name:       "MinutesDuration",
			createdAt:  1704067200,
			finishedAt: 1704067200 + 1800, // + 30 minutes
			expected:   30 * time.Minute,
		},
		{
			name:       "SecondsDuration",
			createdAt:  1704067200,
			finishedAt: 1704067200 + 45, // + 45 seconds
			expected:   45 * time.Second,
		},
		{
			name:       "SameTime",
			createdAt:  1704067200,
			finishedAt: 1704067200,
			expected:   0,
		},
		{
			name:       "LongDuration",
			createdAt:  1704067200,
			finishedAt: 1704067200 + 86400, // + 24 hours
			expected:   24 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateDuration(tt.createdAt, tt.finishedAt)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatTime_TimezoneHandling(t *testing.T) {
	// Even with a non-UTC timezone input, the format should show the time as provided
	// The function simply formats the time, it doesn't convert timezones
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	nyTime := time.Date(2024, 6, 15, 10, 30, 0, 0, loc)
	result := FormatTime(nyTime)

	// The result will show the time in the original timezone's values
	// but the format expects UTC suffix
	require.Contains(t, result, "2024-06-15 10:30:00")
}

func TestUnixTimestampToUTC_AlwaysReturnsUTC(t *testing.T) {
	timestamp := int64(1704067200)
	result := UnixTimestampToUTC(timestamp)

	require.Equal(t, time.UTC, result.Location())
}

func TestCalculateDuration_EdgeCases(t *testing.T) {
	t.Run("BothZero", func(t *testing.T) {
		result := CalculateDuration(0, 0)
		require.Equal(t, time.Duration(0), result)
	})

	t.Run("CreatedAtZeroFinishedAtPositive", func(t *testing.T) {
		result := CalculateDuration(0, 3600)
		require.Equal(t, time.Hour, result)
	})

	t.Run("VeryLargeValues", func(t *testing.T) {
		// Year 2100 timestamps
		created := int64(4102444800) // 2100-01-01
		finished := int64(4102444800 + 3600)
		result := CalculateDuration(created, finished)
		require.Equal(t, time.Hour, result)
	})
}
