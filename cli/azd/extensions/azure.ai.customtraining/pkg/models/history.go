// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

// RunHistory represents the run history details for a job.
type RunHistory struct {
	RunID           string            `json:"runId,omitempty"`
	RunUUID         string            `json:"runUuid,omitempty"`
	RootRunID       string            `json:"rootRunId,omitempty"`
	Status          string            `json:"status,omitempty"`
	StartTimeUTC    string            `json:"startTimeUtc,omitempty"`
	EndTimeUTC      string            `json:"endTimeUtc,omitempty"`
	Duration        string            `json:"duration,omitempty"`
	ComputeDuration string            `json:"computeDuration,omitempty"`
	CreatedUTC      string            `json:"createdUtc,omitempty"`
	LastModifiedUTC string            `json:"lastModifiedUtc,omitempty"`
	DisplayName     string            `json:"displayName,omitempty"`
	Description     string            `json:"description,omitempty"`
	Target          string            `json:"target,omitempty"`
	RunType         string            `json:"runType,omitempty"`
	Error           *RunHistoryError  `json:"error,omitempty"`
	CreatedBy       *RunHistoryUser   `json:"createdBy,omitempty"`
	Compute         *RunHistoryCompute `json:"compute,omitempty"`
	Properties      map[string]string `json:"properties,omitempty"`
	Tags            map[string]string `json:"tags,omitempty"`
	Inputs          map[string]RunHistoryAsset `json:"inputs,omitempty"`
	Outputs         map[string]RunHistoryAsset `json:"outputs,omitempty"`
}

// RunHistoryError represents the error details in run history.
type RunHistoryError struct {
	Error *RunHistoryErrorDetail `json:"error,omitempty"`
}

// RunHistoryErrorDetail contains the error code and message.
type RunHistoryErrorDetail struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// RunHistoryUser represents who created or modified the run.
type RunHistoryUser struct {
	UserName     string `json:"userName,omitempty"`
	UserObjectID string `json:"userObjectId,omitempty"`
	UPN          string `json:"upn,omitempty"`
}

// RunHistoryCompute represents compute details from run history.
type RunHistoryCompute struct {
	Target        string `json:"target,omitempty"`
	TargetType    string `json:"targetType,omitempty"`
	VMSize        string `json:"vmSize,omitempty"`
	InstanceType  string `json:"instanceType,omitempty"`
	InstanceCount int    `json:"instanceCount,omitempty"`
	GPUCount      int    `json:"gpuCount,omitempty"`
	Priority      string `json:"priority,omitempty"`
}

// RunHistoryAsset represents an input or output asset reference in run history.
type RunHistoryAsset struct {
	AssetID string `json:"assetId,omitempty"`
	Type    string `json:"type,omitempty"`
}

// RunHistoryList represents a paginated list of run history entries.
type RunHistoryList struct {
	Value    []RunHistory `json:"value"`
	NextLink string       `json:"nextLink,omitempty"`
}
