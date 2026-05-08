// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

// JobInput represents a discriminated union for job inputs.
type JobInput struct {
	JobInputType string `json:"jobInputType"` // "uri_folder", "uri_file", "literal"
	URI          string `json:"uri,omitempty"`
	Value        string `json:"value,omitempty"`
	Mode         string `json:"mode,omitempty"` // "download", "ro_mount"
}

// JobOutput represents a discriminated union for job outputs.
type JobOutput struct {
	JobOutputType string `json:"jobOutputType"` // "uri_folder", "uri_file", "safetensors_model"
	URI           string `json:"uri,omitempty"`
	Mode          string `json:"mode,omitempty"` // "rw_mount", "upload"
	AssetName     string `json:"assetName,omitempty"`
	AssetVersion  string `json:"assetVersion,omitempty"`
}

// Distribution represents distributed training configuration.
type Distribution struct {
	DistributionType        string `json:"distributionType"` // "PyTorch", "Mpi", "TensorFlow"
	ProcessCountPerInstance int    `json:"processCountPerInstance,omitempty"`
}

// ResourceConfig represents compute resource specifications.
type ResourceConfig struct {
	InstanceCount int            `json:"instanceCount,omitempty"`
	InstanceType  string         `json:"instanceType,omitempty"`
	ShmSize       string         `json:"shmSize,omitempty"`
	DockerArgs    string         `json:"dockerArgs,omitempty"`
	Properties    map[string]any `json:"properties,omitempty"`
}

// JobServiceRequest is the request-side shape for a job service (e.g., SSH).
// The API expects: { jobServiceType, nodes: { nodesValueType }, properties: {...} }
type JobServiceRequest struct {
	JobServiceType string         `json:"jobServiceType"`  // e.g., "SSH"
	Nodes          *NodesValue    `json:"nodes,omitempty"` // nil means single node 0
	Port           int            `json:"port,omitempty"`
	Properties     map[string]any `json:"properties,omitempty"` // e.g., { sshPublicKeys: "..." }
}

// NodesValue represents which nodes a service runs on.
type NodesValue struct {
	NodesValueType string `json:"nodesValueType"` // "All" (only supported value)
}

// CommandJobLimits represents job limits.
type CommandJobLimits struct {
	Timeout string `json:"timeout,omitempty"` // ISO 8601 duration
}

// QueueSettings represents job priority/tier settings.
type QueueSettings struct {
	JobTier  string `json:"jobTier,omitempty"`
	Priority string `json:"priority,omitempty"`
}

// PagedResponse represents a paginated API response.
type PagedResponse struct {
	Value    []JobResource `json:"value"`
	NextLink string        `json:"nextLink,omitempty"`
}

// ErrorResponse represents an API error envelope.
type ErrorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// JobListItem is a flattened view of a job for table display.
type JobListItem struct {
	Name        string `json:"name" table:"NAME"`
	DisplayName string `json:"displayName" table:"DISPLAY NAME"`
	Status      string `json:"status" table:"STATUS"`
	JobType     string `json:"jobType" table:"TYPE"`
	ComputeID   string `json:"computeId" table:"COMPUTE"`
	CreatedBy   string `json:"createdBy" table:"CREATED BY"`
	Created     string `json:"createdDateTime" table:"CREATED"`
}

// SystemData contains metadata about who created/modified the resource.
type SystemData struct {
	CreatedAt     string `json:"createdAt,omitempty"`
	CreatedBy     string `json:"createdBy,omitempty"`
	CreatedByType string `json:"createdByType,omitempty"`
}
