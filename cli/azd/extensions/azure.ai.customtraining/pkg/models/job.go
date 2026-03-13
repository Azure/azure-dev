// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

// JobResource wraps job properties in an ARM resource envelope.
type JobResource struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty" table:"NAME"`
	Properties CommandJob        `json:"properties"`
	Tags       map[string]string `json:"tags,omitempty"`
}

// CommandJob represents the properties of a Foundry command job.
type CommandJob struct {
	JobType              string                `json:"jobType"`
	DisplayName          string                `json:"displayName,omitempty" table:"DISPLAY NAME"`
	Description          string                `json:"description,omitempty"`
	Status               string                `json:"status,omitempty" table:"STATUS"`
	Command              string                `json:"command,omitempty"`
	EnvironmentID        string                `json:"environmentId,omitempty"`
	CodeID               string                `json:"codeId,omitempty"`
	ComputeID            string                `json:"computeId,omitempty"`
	Inputs               map[string]JobInput   `json:"inputs,omitempty"`
	Outputs              map[string]JobOutput  `json:"outputs,omitempty"`
	Distribution         *Distribution         `json:"distribution,omitempty"`
	Resources            *ResourceConfig       `json:"resources,omitempty"`
	Limits               *CommandJobLimits     `json:"limits,omitempty"`
	EnvironmentVariables map[string]string     `json:"environmentVariables,omitempty"`
	QueueSettings        *QueueSettings        `json:"queueSettings,omitempty"`
	IsArchived           bool                  `json:"isArchived,omitempty"`
	CreatedDateTime      string                `json:"createdDateTime,omitempty"`
	Services             map[string]interface{} `json:"services,omitempty"`
}
