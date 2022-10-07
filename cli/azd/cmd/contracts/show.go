// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package contracts

// ShowType are the values for the language property of a ShowServiceProject
type ShowType string

const (
	ShowTypeDotNet ShowType = "dotnet"
	ShowTypePython ShowType = "python"
	ShowTypeNode   ShowType = "node"
)

// ShowResult is the contract for the output of `azd show`
type ShowResult struct {
	Name     string                 `json:"name"`
	Services map[string]ShowService `json:"services"`
}

// ShowService is the contract for a service returned by `azd show`
type ShowService struct {
	// Project contains information about the project that backs this service.
	Project ShowServiceProject `json:"project"`
	// Target contains information about the resource that the service is deployed
	// to.
	Target *ShowTargetArm `json:"target,omitempty"`
}

// ShowServiceProject is the contract for a service's project as returned by `azd show`
type ShowServiceProject struct {
	// Path contains the path to the project for this service.
	// When 'type' is 'dotnet', this includes the project file (i.e. Todo.Api.csproj).
	Path string `json:"path"`
	// The type of this project.
	Type ShowType `json:"language"`
}

// ShowTargetArm is the contract for information about the resources that a given service
// is deployed to.
type ShowTargetArm struct {
	ResourceIds []string `json:"resourceIds"`
}
