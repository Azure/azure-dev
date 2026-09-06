// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// `job show` collects the artifact of a job that finished with one, so this is
// the question it asks. Terminal is not the same question: a cancelled job is
// finished and has nothing to download, and reading "not failed" as success
// would try to download from a job still running.
func TestSucceededIsNotMerelyTerminal(t *testing.T) {
	cases := map[string]bool{
		"completed": true,
		"succeeded": true,
		"Succeeded": true,
		"running":   false,
		"":          false,
		"failed":    false,
		"cancelled": false,
		"canceled":  false,
	}
	for status, want := range cases {
		job := &GenerationJob{Status: status}
		assert.Equal(t, want, job.Succeeded(), "status %q", status)
	}
}

func TestSucceededIsFalseForNoJob(t *testing.T) {
	var job *GenerationJob
	assert.False(t, job.Succeeded())
}
