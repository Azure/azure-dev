// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

// showResult is the model type that represents the JSON output of `azd show`
type ShowResult struct {
	Name     string                 `json:"name"`
	Services map[string]ShowService `json:"services"`
}

type ShowService struct {
	// Project contains information about the project that backs this service.
	Project ShowServiceProject `json:"project"`
	// Target contains information about the resource that the service is deployed
	// to.
	Target *ShowTargetArm `json:"target,omitempty"`
}

type ShowServiceProject struct {
	// Path contains the path to the project for this service.
	// When 'type' is 'dotnet', this includes the project file (i.e. Todo.Api.csproj).
	Path string `json:"path"`
	// The type of this project. One of "dotnet", "python", or "node"
	Type string `json:"language"`
}

type ShowTargetArm struct {
	ResourceIds []string `json:"resourceIds"`
}
