// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"time"
)

// TimeFormat defines the standard time format used for display output
const TimeFormat = "2006-01-02 15:04:05 UTC"

// UnixTimestampToUTC converts a Unix timestamp (seconds since epoch) to a UTC time.Time.
// Returns zero time.Time if timestamp is 0.
func UnixTimestampToUTC(timestamp int64) time.Time {
	if timestamp == 0 {
		return time.Time{}
	}
	return time.Unix(timestamp, 0).UTC()
}

// FormatTime formats a time.Time to the standard display format.
// Returns empty string if time is zero.
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(TimeFormat)
}
