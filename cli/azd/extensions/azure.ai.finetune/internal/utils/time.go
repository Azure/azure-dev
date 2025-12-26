// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"time"
)

// UnixTimestampToUTC converts a Unix timestamp (seconds since epoch) to a UTC time.Time.
// Returns zero time.Time if timestamp is 0.
func UnixTimestampToUTC(timestamp int64) time.Time {
	if timestamp == 0 {
		return time.Time{}
	}
	return time.Unix(timestamp, 0).UTC()
}
