// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"time"
)

// FormatUnixTimestampToUTC converts Unix timestamp (seconds) to UTC time string
func FormatUnixTimestampToUTC(timestamp int64) time.Time {
	if timestamp == 0 {
		return time.Time{}
	}
	return time.Unix(timestamp, 0).UTC()
}
