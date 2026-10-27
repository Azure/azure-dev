// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"io"

	"azure.ai.projects/internal/version"
)

const projectOutputSchemaVersion = 1

func projectOutputProducerVersion() string {
	return version.Version
}

type projectAddOutput struct {
	SchemaVersion     int                    `json:"schemaVersion"`
	ProducerVersion   string                 `json:"producerVersion"`
	ServiceName       string                 `json:"serviceName"`
	Mode              string                 `json:"mode"`
	Mutation          string                 `json:"mutation"`
	Endpoint          string                 `json:"endpoint,omitempty"`
	ResourceID        string                 `json:"resourceId,omitempty"`
	ContainerRegistry *projectRegistryOutput `json:"containerRegistry,omitempty"`
}

type projectRegistryOutput struct {
	ResourceID      string `json:"resourceId"`
	ExternallyOwned bool   `json:"externallyOwned"`
}

func writeProjectRegistryOutput(w io.Writer, registry *projectRegistryOutput) {
	if registry != nil && registry.ResourceID != "" {
		ownership := "managed by azd"
		if registry.ExternallyOwned {
			ownership = "externally owned; retained by azd down"
		}
		fmt.Fprintf(w, "Container registry: %s (%s).\n", registry.ResourceID, ownership)
	}
}

func writeProjectAddOutput(w io.Writer, format string, result projectAddOutput) error {
	if format == "none" {
		return nil
	}
	if format == "json" {
		return json.NewEncoder(w).Encode(result)
	}
	if result.Mutation == "unchanged" {
		fmt.Fprintf(w, "Foundry project configuration unchanged (%s).\n", result.ServiceName)
	} else {
		fmt.Fprintf(w, "Foundry project configuration %s in services.%s.\n", result.Mutation, result.ServiceName)
	}
	writeProjectRegistryOutput(w, result.ContainerRegistry)
	return nil
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
