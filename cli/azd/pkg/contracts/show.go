// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package contracts

import "encoding/json"

// ShowType are the values for the language property of a ShowServiceProject
type ShowType string

const (
	ShowTypeNone   ShowType = ""
	ShowTypeDotNet ShowType = "dotnet"
	ShowTypePython ShowType = "python"
	ShowTypeNode   ShowType = "node"
	ShowTypeJava   ShowType = "java"
	ShowTypeCustom ShowType = "custom"
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
	// IngresUrl is the deployed service's ingress URL. When marshaled to JSON,
	// it is emitted under both "ingresUrl" (back-compat) and "ingressUrl"
	// (correctly spelled, preferred). Only this field needs to be set.
	IngresUrl string `json:"-"`
}

// MarshalJSON implements json.Marshaler for ShowService.
// It emits the ingress URL under both "ingresUrl" and "ingressUrl" keys
// so that existing consumers using the historical misspelling continue to work
// while new consumers can use the correct spelling.
func (s ShowService) MarshalJSON() ([]byte, error) {
	type alias struct {
		Project    ShowServiceProject `json:"project"`
		Target     *ShowTargetArm     `json:"target,omitempty"`
		IngresUrl  string             `json:"ingresUrl,omitempty"`
		IngressUrl string             `json:"ingressUrl,omitempty"`
	}
	return json.Marshal(alias{
		Project:    s.Project,
		Target:     s.Target,
		IngresUrl:  s.IngresUrl,
		IngressUrl: s.IngresUrl,
	})
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
