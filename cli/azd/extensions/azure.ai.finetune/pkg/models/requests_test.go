// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPauseJobRequest_Struct(t *testing.T) {
	request := &PauseJobRequest{
		JobID: "job-123",
	}
	require.Equal(t, "job-123", request.JobID)
}

func TestResumeJobRequest_Struct(t *testing.T) {
	request := &ResumeJobRequest{
		JobID: "job-456",
	}
	require.Equal(t, "job-456", request.JobID)
}

func TestCancelJobRequest_Struct(t *testing.T) {
	request := &CancelJobRequest{
		JobID: "job-789",
	}
	require.Equal(t, "job-789", request.JobID)
}

func TestGetJobDetailsRequest_Struct(t *testing.T) {
	request := &GetJobDetailsRequest{
		JobID: "job-details-123",
	}
	require.Equal(t, "job-details-123", request.JobID)
}

func TestGetJobEventsRequest_Struct(t *testing.T) {
	t.Run("WithAllFields", func(t *testing.T) {
		request := &GetJobEventsRequest{
			JobID: "job-events-123",
			Limit: 50,
			After: "cursor-abc",
		}

		require.Equal(t, "job-events-123", request.JobID)
		require.Equal(t, 50, request.Limit)
		require.Equal(t, "cursor-abc", request.After)
	})

	t.Run("MinimalRequest", func(t *testing.T) {
		request := &GetJobEventsRequest{
			JobID: "job-events-456",
		}

		require.Equal(t, "job-events-456", request.JobID)
		require.Equal(t, 0, request.Limit)
		require.Empty(t, request.After)
	})
}

func TestGetJobCheckpointsRequest_Struct(t *testing.T) {
	t.Run("WithAllFields", func(t *testing.T) {
		request := &GetJobCheckpointsRequest{
			JobID: "job-checkpoints-123",
			Limit: 100,
			After: "cursor-xyz",
		}

		require.Equal(t, "job-checkpoints-123", request.JobID)
		require.Equal(t, 100, request.Limit)
		require.Equal(t, "cursor-xyz", request.After)
	})

	t.Run("MinimalRequest", func(t *testing.T) {
		request := &GetJobCheckpointsRequest{
			JobID: "job-checkpoints-456",
		}

		require.Equal(t, "job-checkpoints-456", request.JobID)
		require.Equal(t, 0, request.Limit)
		require.Empty(t, request.After)
	})
}

func TestListDeploymentsRequest_Struct(t *testing.T) {
	t.Run("WithPagination", func(t *testing.T) {
		request := &ListDeploymentsRequest{
			Limit: 25,
			After: "deployment-cursor",
		}

		require.Equal(t, 25, request.Limit)
		require.Equal(t, "deployment-cursor", request.After)
	})

	t.Run("DefaultValues", func(t *testing.T) {
		request := &ListDeploymentsRequest{}

		require.Equal(t, 0, request.Limit)
		require.Empty(t, request.After)
	})
}

func TestGetDeploymentRequest_Struct(t *testing.T) {
	request := &GetDeploymentRequest{
		DeploymentID: "deployment-123",
	}
	require.Equal(t, "deployment-123", request.DeploymentID)
}

func TestDeleteDeploymentRequest_Struct(t *testing.T) {
	request := &DeleteDeploymentRequest{
		DeploymentID: "deployment-to-delete",
	}
	require.Equal(t, "deployment-to-delete", request.DeploymentID)
}

func TestUpdateDeploymentRequest_Struct(t *testing.T) {
	request := &UpdateDeploymentRequest{
		DeploymentID: "deployment-to-update",
		Capacity:     20,
	}

	require.Equal(t, "deployment-to-update", request.DeploymentID)
	require.Equal(t, int32(20), request.Capacity)
}
