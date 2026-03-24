// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

// DeploymentConfig contains configuration for creating a model deployment.
type DeploymentConfig struct {
	DeploymentName string
	ModelName      string
	ModelVersion   string
	ModelFormat    string
	ModelSource    string
	SkuName        string
	SkuCapacity    int32
	RaiPolicyName  string

	// Azure context
	SubscriptionID string
	ResourceGroup  string
	AccountName    string
	TenantID       string

	WaitForCompletion bool
}

// DeploymentResult represents the result of a deployment operation.
type DeploymentResult struct {
	ID                string
	Name              string
	ModelName         string
	ProvisioningState string
}

// DeploymentInfo represents a deployment returned from a list operation.
type DeploymentInfo struct {
	Name              string `json:"name" table:"Name"`
	ModelName         string `json:"modelName" table:"Model"`
	ModelFormat       string `json:"modelFormat" table:"Format"`
	ModelVersion      string `json:"modelVersion" table:"Version"`
	SkuName           string `json:"skuName" table:"SKU"`
	SkuCapacity       int32  `json:"skuCapacity" table:"Capacity"`
	ProvisioningState string `json:"provisioningState" table:"State"`
}

// DeploymentDetail represents the full details of a deployment for the show command.
type DeploymentDetail struct {
	ID                string `json:"id,omitempty"`
	Name              string `json:"name"`
	ModelName         string `json:"modelName"`
	ModelFormat       string `json:"modelFormat"`
	ModelVersion      string `json:"modelVersion"`
	ModelSource       string `json:"modelSource,omitempty"`
	SkuName           string `json:"skuName"`
	SkuCapacity       int32  `json:"skuCapacity"`
	ProvisioningState string `json:"provisioningState"`
	RaiPolicyName     string `json:"raiPolicyName,omitempty"`
	CreatedAt         string `json:"createdAt,omitempty"`
	LastModifiedAt    string `json:"lastModifiedAt,omitempty"`
}
