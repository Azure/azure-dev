package scaffold

import (
	"fmt"
	"strings"
)

type InfraSpec struct {
	Parameters []Parameter
	Services   []ServiceSpec

	// Databases to create
	DbPostgres    *DatabasePostgres
	DbCosmosMongo *DatabaseCosmosMongo
}

type Parameter struct {
	Name   string
	Value  any
	Type   string
	Secret bool
}

type DatabasePostgres struct {
	DatabaseUser string
	DatabaseName string
}

type DatabaseCosmosMongo struct {
	DatabaseName string
}

type ServiceSpec struct {
	Name string
	Port int

	// Front-end properties.
	Frontend *Frontend

	// Back-end properties
	Backend *Backend

	// Connection to a database
	DbPostgres    *DatabaseReference
	DbCosmosMongo *DatabaseReference
	DbRedis       *DatabaseReference
}

type Frontend struct {
	Backends []ServiceReference
}

type Backend struct {
	Frontends []ServiceReference
}

type ServiceReference struct {
	Name string
}

type DatabaseReference struct {
	DatabaseName string
}

func containerAppExistsParameter(serviceName string) Parameter {
	return Parameter{
		Name: BicepName(serviceName) + "Exists",
		Value: fmt.Sprintf("${SERVICE_%s_RESOURCE_EXISTS=false}",
			strings.ReplaceAll(strings.ToUpper(serviceName), "-", "_")),
		Type: "bool",
	}
}

type serviceDef struct {
	Settings []serviceDefSettings `json:"settings"`
}

type serviceDefSettings struct {
	Name         string `json:"name"`
	Value        string `json:"value"`
	Secret       bool   `json:"secret,omitempty"`
	SecretRef    string `json:"secretRef,omitempty"`
	CommentName  string `json:"_comment_name,omitempty"`
	CommentValue string `json:"_comment_value,omitempty"`
}

func serviceDefPlaceholder(serviceName string) Parameter {
	return Parameter{
		Name: BicepName(serviceName) + "Definition",
		Value: serviceDef{
			Settings: []serviceDefSettings{
				{
					Name:        "",
					Value:       "${VAR}",
					CommentName: "The name of the environment variable when running in Azure. If empty, ignored.",
					//nolint:lll
					CommentValue: "The value to provide. This can be a fixed literal, or an expression like ${VAR} to use the value of 'VAR' from the current environment.",
				},
				{
					Name:        "",
					Value:       "${VAR_S}",
					Secret:      true,
					CommentName: "The name of the environment variable when running in Azure. If empty, ignored.",
					//nolint:lll
					CommentValue: "The value to provide. This can be a fixed literal, or an expression like ${VAR_S} to use the value of 'VAR_S' from the current environment.",
				},
			},
		},
		Type:   "object",
		Secret: true,
	}
}
