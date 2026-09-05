// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "azure.ai.projects/internal/version"

const projectOutputSchemaVersion = 1

func projectOutputProducerVersion() string {
	return version.Version
}

type projectAddOutput struct {
	SchemaVersion   int    `json:"schemaVersion"`
	ProducerVersion string `json:"producerVersion"`
	ServiceName     string `json:"serviceName"`
	Mode            string `json:"mode"`
	Mutation        string `json:"mutation"`
	Endpoint        string `json:"endpoint,omitempty"`
	ResourceID      string `json:"resourceId,omitempty"`
}

type projectDeploymentAddOutput struct {
	SchemaVersion   int                   `json:"schemaVersion"`
	ProducerVersion string                `json:"producerVersion"`
	ServiceName     string                `json:"serviceName"`
	DeploymentName  string                `json:"deploymentName"`
	Model           deploymentOutputModel `json:"model"`
	SKU             deploymentOutputSKU   `json:"sku"`
	Mutation        string                `json:"mutation"`
}

type deploymentOutputModel struct {
	Format  string `json:"format"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type deploymentOutputSKU struct {
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
}
