// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

import "time"

// DeploymentStatus represents the status of a deployment
type DeploymentStatus string

const (
	DeploymentPending  DeploymentStatus = "pending"
	DeploymentActive   DeploymentStatus = "active"
	DeploymentUpdating DeploymentStatus = "updating"
	DeploymentFailed   DeploymentStatus = "failed"
	DeploymentDeleting DeploymentStatus = "deleting"
)

// Deployment represents a model deployment
type Deployment struct {
	// Core identification
	ID       string
	VendorID string // Vendor-specific ID

	// Deployment details
	Name           string
	Status         DeploymentStatus
	FineTunedModel string
	BaseModel      string

	// Endpoint
	Endpoint string

	// Timestamps
	CreatedAt time.Time
	UpdatedAt *time.Time
	DeletedAt *time.Time

	// Metadata
	VendorMetadata map[string]interface{}
	ErrorDetails   *ErrorDetail
}

// DeploymentRequest represents a request to create a deployment
type DeploymentRequest struct {
	ModelName         string
	DeploymentName    string
	ModelFormat       string
	SKU               string
	Version           string
	Capacity          int32
	SubscriptionID    string
	ResourceGroup     string
	AccountName       string
	TenantID          string
	WaitForCompletion bool
}

// DeploymentConfig contains configuration for deploying a model
type DeploymentConfig struct {
	JobID             string
	DeploymentName    string
	ModelFormat       string
	SKU               string
	Version           string
	Capacity          int32
	SubscriptionID    string
	ResourceGroup     string
	AccountName       string
	TenantID          string
	WaitForCompletion bool
}

type DeployModelResult struct {
	Deployment Deployment
	Status     string
	Message    string
}

// BaseModel represents information about a base model
type BaseModel struct {
	ID          string
	Name        string
	Description string
	Deprecated  bool
}
