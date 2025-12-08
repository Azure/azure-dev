// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
)

type DeploymentType string

const (
	DeploymentTypeStandard DeploymentType = "deployments.standard"
	DeploymentTypeStacks   DeploymentType = "deployments.stacks"
)

var ErrPreviewNotSupported = errors.New("preview not supported")

const emptySubscriptionArmTemplate = `{
	"$schema": "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
	"contentVersion": "1.0.0.0",
	"parameters": {},
	"variables": {},
	"resources": [],
	"outputs": {}
  }`

type ResourceDeployment struct {
	// The Azure resource id of the deployment operation
	Id string

	// The location of the deployment
	Location string

	// The Azure resource id of the actual deployment object
	DeploymentId string

	// The deployment name
	Name string

	// The deployment type
	Type string

	// The tags associated with the deployment
	Tags map[string]*string

	// The outputs from the deployment
	Outputs any

	// The hash produced for the template.
	TemplateHash *string

	// The timestamp of the template deployment.
	Timestamp time.Time

	// The resources created from the deployment
	Resources []*armresources.ResourceReference

	// The dependencies of the deployment
	Dependencies []*armresources.Dependency

	// The status of the deployment
	ProvisioningState DeploymentProvisioningState

	PortalUrl string

	OutputsUrl string

	DeploymentUrl string
}

type DeploymentProvisioningState string

const (
	DeploymentProvisioningStateAccepted                DeploymentProvisioningState = "Accepted"
	DeploymentProvisioningStateCanceled                DeploymentProvisioningState = "Canceled"
	DeploymentProvisioningStateCanceling               DeploymentProvisioningState = "Canceling"
	DeploymentProvisioningStateCreating                DeploymentProvisioningState = "Creating"
	DeploymentProvisioningStateDeleted                 DeploymentProvisioningState = "Deleted"
	DeploymentProvisioningStateDeleting                DeploymentProvisioningState = "Deleting"
	DeploymentProvisioningStateDeletingResources       DeploymentProvisioningState = "DeletingResources"
	DeploymentProvisioningStateDeploying               DeploymentProvisioningState = "Deploying"
	DeploymentProvisioningStateFailed                  DeploymentProvisioningState = "Failed"
	DeploymentProvisioningStateNotSpecified            DeploymentProvisioningState = "NotSpecified"
	DeploymentProvisioningStateReady                   DeploymentProvisioningState = "Ready"
	DeploymentProvisioningStateRunning                 DeploymentProvisioningState = "Running"
	DeploymentProvisioningStateSucceeded               DeploymentProvisioningState = "Succeeded"
	DeploymentProvisioningStateUpdatingDenyAssignments DeploymentProvisioningState = "UpdatingDenyAssignments"
	DeploymentProvisioningStateValidating              DeploymentProvisioningState = "Validating"
	DeploymentProvisioningStateWaiting                 DeploymentProvisioningState = "Waiting"
	DeploymentProvisioningStateUpdating                DeploymentProvisioningState = "Updating"
)

type DeploymentService interface {
	GenerateDeploymentName(baseName string) string
	CalculateTemplateHash(
		ctx context.Context,
		subscriptionId string,
		template azure.RawArmTemplate) (string, error)
	ListSubscriptionDeploymentResources(
		ctx context.Context,
		subscriptionId string,
		deploymentName string,
	) ([]*armresources.ResourceReference, error)
	ListResourceGroupDeploymentResources(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		deploymentName string,
	) ([]*armresources.ResourceReference, error)
	ListSubscriptionDeployments(
		ctx context.Context,
		subscriptionId string,
	) ([]*ResourceDeployment, error)
	GetSubscriptionDeployment(
		ctx context.Context,
		subscriptionId string,
		deploymentName string,
	) (*ResourceDeployment, error)
	ListResourceGroupDeployments(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
	) ([]*ResourceDeployment, error)
	GetResourceGroupDeployment(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		deploymentName string,
	) (*ResourceDeployment, error)
	DeployToSubscription(
		ctx context.Context,
		subscriptionId string,
		location string,
		deploymentName string,
		armTemplate azure.RawArmTemplate,
		parameters azure.ArmParameters,
		tags map[string]*string,
		options map[string]any,
	) (*ResourceDeployment, error)
	DeployToResourceGroup(
		ctx context.Context,
		subscriptionId,
		resourceGroup,
		deploymentName string,
		armTemplate azure.RawArmTemplate,
		parameters azure.ArmParameters,
		tags map[string]*string,
		options map[string]any,
	) (*ResourceDeployment, error)
	ListSubscriptionDeploymentOperations(
		ctx context.Context,
		subscriptionId string,
		deploymentName string,
	) ([]*armresources.DeploymentOperation, error)
	ListResourceGroupDeploymentOperations(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		deploymentName string,
	) ([]*armresources.DeploymentOperation, error)
	ValidatePreflightToSubscription(
		ctx context.Context,
		subscriptionId string,
		location string,
		deploymentName string,
		armTemplate azure.RawArmTemplate,
		parameters azure.ArmParameters,
		tags map[string]*string,
		options map[string]any,
	) error
	ValidatePreflightToResourceGroup(
		ctx context.Context,
		subscriptionId,
		resourceGroup,
		deploymentName string,
		armTemplate azure.RawArmTemplate,
		parameters azure.ArmParameters,
		tags map[string]*string,
		options map[string]any,
	) error
	WhatIfDeployToSubscription(
		ctx context.Context,
		subscriptionId string,
		location string,
		deploymentName string,
		armTemplate azure.RawArmTemplate,
		parameters azure.ArmParameters,
	) (*armresources.WhatIfOperationResult, error)
	WhatIfDeployToResourceGroup(
		ctx context.Context,
		subscriptionId,
		resourceGroup,
		deploymentName string,
		armTemplate azure.RawArmTemplate,
		parameters azure.ArmParameters,
	) (*armresources.WhatIfOperationResult, error)
	DeleteSubscriptionDeployment(
		ctx context.Context,
		subscriptionId string,
		deploymentName string,
		options map[string]any,
		progress *async.Progress[DeleteDeploymentProgress],
	) error
	DeleteResourceGroupDeployment(
		ctx context.Context,
		subscriptionId,
		resourceGroupName string,
		deploymentName string,
		options map[string]any,
		progress *async.Progress[DeleteDeploymentProgress],
	) error
}

type DeleteResourceState string

const (
	DeleteResourceStateInProgress DeleteResourceState = "InProgress"
	DeleteResourceStateSucceeded  DeleteResourceState = "Succeeded"
	DeleteResourceStateFailed     DeleteResourceState = "Failed"
)

type DeleteDeploymentProgress struct {
	Name    string
	Message string
	State   DeleteResourceState
}

type ReportDeleteProgress func(progress *DeleteDeploymentProgress)

var (
	ErrDeploymentNotFound = errors.New("deployment not found")
)

type AzCliDeploymentPropertiesDependency struct {
	AzCliDeploymentPropertiesBasicDependency
	DependsOn []AzCliDeploymentPropertiesBasicDependency `json:"dependsOn"`
}

type AzCliDeploymentPropertiesBasicDependency struct {
	Id           string `json:"id"`
	ResourceName string `json:"resourceName"`
	ResourceType string `json:"resourceType"`
}

type AzCliDeploymentErrorResponse struct {
	Code           string                         `json:"code"`
	Message        string                         `json:"message"`
	Target         string                         `json:"target"`
	Details        []AzCliDeploymentErrorResponse `json:"details"`
	AdditionalInfo AzCliDeploymentAdditionalInfo  `json:"additionalInfo"`
}

type AzCliDeploymentAdditionalInfo struct {
	Type string      `json:"type"`
	Info interface{} `json:"info"`
}

type AzCliDeployment struct {
	Id         string                    `json:"id"`
	Name       string                    `json:"name"`
	Properties AzCliDeploymentProperties `json:"properties"`
}

type AzCliDeploymentProperties struct {
	CorrelationId   string                                `json:"correlationId"`
	Error           AzCliDeploymentErrorResponse          `json:"error"`
	Dependencies    []AzCliDeploymentPropertiesDependency `json:"dependencies"`
	OutputResources []AzCliDeploymentResourceReference    `json:"outputResources"`
	Outputs         map[string]AzCliDeploymentOutput      `json:"outputs"`
}

type AzCliDeploymentResourceReference struct {
	Id string `json:"id"`
}

type AzCliDeploymentOutput struct {
	Type  string      `json:"type"`
	Value interface{} `json:"value"`
}

func (o AzCliDeploymentOutput) Secured() bool {
	return azure.IsSecuredARMType(o.Type)
}

type AzCliDeploymentResult struct {
	Properties AzCliDeploymentResultProperties `json:"properties"`
}

type AzCliDeploymentResultProperties struct {
	Outputs map[string]AzCliDeploymentOutput `json:"outputs"`
}

type AzCliResourceOperation struct {
	Id          string                           `json:"id"`
	OperationId string                           `json:"operationId"`
	Properties  AzCliResourceOperationProperties `json:"properties"`
}

type AzCliResourceOperationProperties struct {
	ProvisioningOperation string                               `json:"provisioningOperation"`
	ProvisioningState     string                               `json:"provisioningState"`
	TargetResource        AzCliResourceOperationTargetResource `json:"targetResource"`
	StatusCode            string                               `json:"statusCode"`
	StatusMessage         AzCliDeploymentStatusMessage         `json:"statusMessage"`
	// While the operation is in progress, this timestamp effectively represents "InProgressTimestamp".
	// When the operation ends, this timestamp effectively represents "EndTimestamp".
	Timestamp time.Time `json:"timestamp"`
}

type AzCliResourceOperationTargetResource struct {
	Id            string `json:"id"`
	ResourceType  string `json:"resourceType"`
	ResourceName  string `json:"resourceName"`
	ResourceGroup string `json:"resourceGroup"`
}

type AzCliDeploymentStatusMessage struct {
	Err    AzCliDeploymentErrorResponse `json:"error"`
	Status string                       `json:"status"`
}

// convert from: sdk client outputs: interface{} to map[string]azapi.AzCliDeploymentOutput
// sdk client parses http response from network as an interface{}
// this function keeps the compatibility with the previous AzCliDeploymentOutput model
func CreateDeploymentOutput(rawOutputs interface{}) (result map[string]AzCliDeploymentOutput) {
	if rawOutputs == nil {
		return make(map[string]AzCliDeploymentOutput, 0)
	}

	castInput := rawOutputs.(map[string]interface{})
	result = make(map[string]AzCliDeploymentOutput, len(castInput))
	for key, output := range castInput {
		innerValue := output.(map[string]interface{})
		result[key] = AzCliDeploymentOutput{
			Type:  innerValue["type"].(string),
			Value: innerValue["value"],
		}
	}
	return result
}

func responseToDeploymentError(title string, respErr *azcore.ResponseError) error {
	var errorText string
	rawBody, err := io.ReadAll(respErr.RawResponse.Body)
	if err != nil {
		errorText = respErr.Error()
	} else {
		errorText = string(rawBody)
	}
	return NewAzureDeploymentError(title, errorText)
}

// Attempts to create an Azure Deployment error from the HTTP response error
func createDeploymentError(err error, input string) error {
	var responseErr *azcore.ResponseError
	if errors.As(err, &responseErr) {
		return responseToDeploymentError(fmt.Sprintf("%s Error Details", input), responseErr)
	}

	return err
}
