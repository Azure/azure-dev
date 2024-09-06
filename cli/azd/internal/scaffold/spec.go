package scaffold

import (
	"fmt"
	"strings"
)

type InfraSpec struct {
	Parameters []Parameter

	// compute services
	Services []ServiceSpec

	// databases
	DbPostgres    *DatabasePostgres
	DbCosmosMongo *DatabaseCosmosMongo
	DbRedis       *DatabaseRedis

	// ai models
	AIModels []AIModel
}

type Parameter struct {
	Name   string
	Value  any
	Type   string
	Secret bool
}

// TODO(weilim): rename this
type ParameterValue struct {
	Name  string
	Value string
}

type DatabasePostgres struct {
	DatabaseUser string
	DatabaseName string

	Module     string
	Parameters []ParameterValue
}

type DatabaseCosmosMongo struct {
	DatabaseName string

	Module     string
	Parameters []ParameterValue
}

type DatabaseRedis struct {
	Module string

	Parameters []ParameterValue
}

// AIModel represents a deployed, ready to use AI model.
type AIModel struct {
	Name  string
	Model AIModelModel
}

// AIModelModel represents a model that backs the AIModel.
type AIModelModel struct {
	// The name of the underlying model.
	Name string
	// The version of the underlying model.
	Version string
}

type ServiceSpec struct {
	Name string
	Port int

	Env map[string]string

	// Front-end properties.
	Frontend *Frontend

	// Back-end properties
	Backend *Backend

	// Connection to a database
	DbPostgres    *DatabaseReference
	DbCosmosMongo *DatabaseReference
	DbRedis       *DatabaseReference

	// AI model connections
	AIModels []AIModelReference
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

type AIModelReference struct {
	Name string
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

var _ = serviceDefPlaceholder("")

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
