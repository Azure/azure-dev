package osutil

import (
	"os"
	"strings"
	"time"
)

type RetryStrategy struct {
	// The maximum number of retries before failing
	MaxRetries uint64
	// The time between each retry attempt
	RetryBackoff time.Duration
}

// Creates a new retry strategy that also reduces the time for when running in test
func NewRetryStrategy(maxRetries uint64, retryBackoff time.Duration) *RetryStrategy {
	if RunningFromPipeline() && !isFuncTest() {
		maxRetries = 1
		retryBackoff = time.Millisecond * 1
	}

	return &RetryStrategy{
		MaxRetries:   maxRetries,
		RetryBackoff: retryBackoff,
	}
}

func RunningFromPipeline() bool {
	teamProjectId := os.Getenv("SYSTEM_TEAMPROJECTID")
	return strings.TrimSpace(teamProjectId) != ""
}

func isFuncTest() bool {
	azdFuncTest := os.Getenv("AZD_FUNC_TEST")
	return strings.TrimSpace(azdFuncTest) == "TRUE"
}
