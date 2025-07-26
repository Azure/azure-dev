// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"time"
)

// SessionStats provides statistics about an agent session
type SessionStats struct {
	TotalActions      int
	SuccessfulActions int
	FailedActions     int
	TotalDuration     time.Duration
}
