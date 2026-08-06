// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package gen_api

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The generated dataset's name and version come back inside the job result,
// and the service answers in two shapes. Reading only one leaves the download
// looking for a dataset the job never named.
func TestGenerationJob_ResolvedNameVersion(t *testing.T) {
	tests := []struct {
		name        string
		result      string
		wantName    string
		wantVersion string
	}{
		{
			name:        "top-level fields",
			result:      `{"name":"support-regression","version":"3"}`,
			wantName:    "support-regression",
			wantVersion: "3",
		},
		{
			name:        "nested under outputs",
			result:      `{"outputs":[{"name":"support-regression","version":"2"}]}`,
			wantName:    "support-regression",
			wantVersion: "2",
		},
		{
			name:        "a name with no version resolves as latest",
			result:      `{"name":"support-regression"}`,
			wantName:    "support-regression",
			wantVersion: "latest",
		},
		{
			name:        "top-level wins over outputs",
			result:      `{"name":"top","outputs":[{"name":"nested"}]}`,
			wantName:    "top",
			wantVersion: "latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := &GenerationJob{Result: json.RawMessage(tt.result)}

			gotName, gotVersion := job.ResolvedNameVersion()

			assert.Equal(t, tt.wantName, gotName)
			assert.Equal(t, tt.wantVersion, gotVersion)
		})
	}
}

// No name means no result to read, and both halves come back empty so a caller
// checking either sees the same thing.
func TestGenerationJob_ResolvedNameVersion_NoResult(t *testing.T) {
	for _, result := range []string{``, `{}`, `not json`, `{"version":"3"}`} {
		job := &GenerationJob{Result: json.RawMessage(result)}

		name, version := job.ResolvedNameVersion()

		assert.Emptyf(t, name, "result %q", result)
		assert.Emptyf(t, version, "result %q", result)
	}
}

// The agent's newest instructions are what generation is seeded from, so an
// agent with no published version has nothing to offer rather than an error.
func TestAgent_Instructions(t *testing.T) {
	var agent *Agent
	assert.Empty(t, agent.Instructions(), "a nil agent is not a panic")

	agent = &Agent{Name: "support"}
	assert.Empty(t, agent.Instructions(), "no published version, nothing to read")

	agent.Versions.Latest = &AgentVersion{Version: "3"}
	agent.Versions.Latest.Definition.Instructions = "\n  Answer politely.\n"
	assert.Equal(t, "Answer politely.", agent.Instructions(),
		"surrounding whitespace would travel into the prompt")
}

// A status the service spells differently must not read as still-running, or
// the poller waits out its budget on a job that finished.
func TestParseJobStatus(t *testing.T) {
	assert.Equal(t, JobStatusRunning, ParseJobStatus(""), "unset means still running")
	assert.Equal(t, JobStatusCompleted, ParseJobStatus("Completed"))
	assert.Equal(t, JobStatusSucceeded, ParseJobStatus("SUCCEEDED"))
	assert.Equal(t, JobStatusFailed, ParseJobStatus("failed"))
}

func TestJobStatus_TerminalAndFailed(t *testing.T) {
	terminal := []JobStatus{
		JobStatusCompleted, JobStatusSucceeded,
		JobStatusFailed, JobStatusCancelled, JobStatusCanceled,
	}
	for _, s := range terminal {
		assert.Truef(t, s.IsTerminal(), "%s is a final state", s)
	}
	assert.False(t, JobStatusRunning.IsTerminal())

	// Both spellings of cancelled count as a failure, because the service uses
	// one and the other is what half the callers will type.
	for _, s := range []JobStatus{JobStatusFailed, JobStatusCancelled, JobStatusCanceled} {
		assert.Truef(t, s.IsFailed(), "%s did not produce an artifact", s)
	}
	for _, s := range []JobStatus{JobStatusCompleted, JobStatusSucceeded, JobStatusRunning} {
		assert.Falsef(t, s.IsFailed(), "%s is not a failure", s)
	}
}

// The poller reports why it gave up, and a job that failed with a service
// message has to carry that message rather than only its status.
func TestJobFailedError(t *testing.T) {
	bare := &JobFailedError{Status: JobStatusFailed}
	assert.Contains(t, bare.Error(), "failed")

	withMessage := &JobFailedError{
		Status: JobStatusFailed,
		Job:    &GenerationJob{Error: &JobError{Message: "quota exceeded"}},
	}
	assert.Contains(t, withMessage.Error(), "quota exceeded",
		"the service said why; repeating only the status loses it")
}

// A transient failure is retried and a terminal one is not, so the difference
// decides whether a caller waits or is told.
func TestIsTransientError(t *testing.T) {
	assert.False(t, IsTransientError(nil))

	for _, code := range []int{http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable} {
		err := &azcore.ResponseError{StatusCode: code}
		assert.Truef(t, IsTransientError(err), "%d is worth retrying", code)
	}
	for _, code := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict} {
		err := &azcore.ResponseError{StatusCode: code}
		assert.Falsef(t, IsTransientError(err), "%d will not change on a retry", code)
	}

	for _, msg := range []string{"connection reset by peer", "connection refused", "unexpected EOF"} {
		assert.Truef(t, IsTransientError(errors.New(msg)), "%q is a dropped connection", msg)
	}
	assert.False(t, IsTransientError(errors.New("invalid dataset name")))
}

func TestIsNotFoundAndIsConflict(t *testing.T) {
	notFound := &azcore.ResponseError{StatusCode: http.StatusNotFound}
	conflict := &azcore.ResponseError{StatusCode: http.StatusConflict}

	assert.True(t, IsNotFound(notFound))
	assert.False(t, IsNotFound(conflict))
	assert.False(t, IsNotFound(errors.New("boom")))

	assert.True(t, IsConflict(conflict))
	assert.False(t, IsConflict(notFound))
	assert.False(t, IsConflict(nil))
}

// Wrapped errors have to keep answering, because the client wraps everything it
// returns with context about the call.
func TestIsNotFound_ThroughAWrap(t *testing.T) {
	wrapped := errors.Join(
		errors.New("reading dataset \"x\""),
		&azcore.ResponseError{StatusCode: http.StatusNotFound},
	)

	require.True(t, IsNotFound(wrapped))
}
