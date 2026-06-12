// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package synthesis turns the host: microsoft.foundry body of azure.yaml
// into the inputs needed to compile an ARM template in memory:
//
//   - the embedded main.bicep + modules tree, ready to be staged on disk
//     for the bicep compiler
//   - a Parameters map of the values the template's params consume
//
// Greenfield only: if the service has an endpoint: field, ErrEndpointBrownfield
// is returned so callers can short-circuit the provision path.
package synthesis

import (
	"errors"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// Sentinel errors returned by Synthesize.
var (
	// ErrEndpointBrownfield indicates the service points at an existing
	// Foundry project via endpoint:. The provider should skip ARM
	// provisioning and connect to the endpoint directly.
	ErrEndpointBrownfield = errors.New("synthesis: service has endpoint: (brownfield)")

	// ErrServiceNotFound indicates the requested service does not exist
	// in azure.yaml or is not a microsoft.foundry service.
	ErrServiceNotFound = errors.New("synthesis: service not found or host != microsoft.foundry")
)

// Input is the synthesizer's view of azure.yaml.
type Input struct {
	// RawAzureYAML is the full bytes of azure.yaml.
	RawAzureYAML []byte

	// ServiceName is the key under services: to synthesize for
	// (e.g. "my-project").
	ServiceName string
}

// Result bundles the bicep sources and the parameter values derived
// from the service body. Callers stage Templates on disk, compile
// main.bicep, and pass Parameters when invoking the resulting ARM
// deployment.
type Result struct {
	// Parameters maps bicep param names to plain Go values. Callers wrap
	// these in ARM's {"value": ...} envelope when serializing.
	Parameters map[string]any
}

// Deployment mirrors the deploymentType in main.bicep.
type Deployment struct {
	Name  string          `yaml:"name" json:"name"`
	Model DeploymentModel `yaml:"model" json:"model"`
	Sku   DeploymentSku   `yaml:"sku" json:"sku"`
}

// DeploymentModel mirrors the model field of deploymentType.
type DeploymentModel struct {
	Name    string `yaml:"name" json:"name"`
	Format  string `yaml:"format" json:"format"`
	Version string `yaml:"version" json:"version"`
}

// DeploymentSku mirrors the sku field of deploymentType.
type DeploymentSku struct {
	Name     string `yaml:"name" json:"name"`
	Capacity int    `yaml:"capacity" json:"capacity"`
}

// dockerBlock is the subset of an agent's docker: object we read to
// decide whether a registry is needed.
type dockerBlock struct {
	Path string `yaml:"path"`
}

// agentBlock is the subset of an agent entry we inspect.
type agentBlock struct {
	Name   string       `yaml:"name"`
	Docker *dockerBlock `yaml:"docker,omitempty"`
	Image  string       `yaml:"image,omitempty"`
}

// foundryService is the subset of a services.<name> body the synthesizer
// reads. Unknown fields (connections, toolboxes, skills, routines, tools,
// agents[].tools, agents[].toolboxes, agents[].skill, agents[].runtime, etc.)
// are intentionally ignored: they are reconciled in azd deploy, not provision.
type foundryService struct {
	Host        string       `yaml:"host"`
	Endpoint    string       `yaml:"endpoint,omitempty"`
	Deployments []Deployment `yaml:"deployments,omitempty"`
	Agents      []agentBlock `yaml:"agents,omitempty"`
}

// projectFile is the root of azure.yaml as we care about it: only services.
type projectFile struct {
	Services map[string]yaml.Node `yaml:"services"`
}

// Synthesize derives the parameter values needed by main.bicep from one
// host: microsoft.foundry service.
func Synthesize(in Input) (*Result, error) {
	if len(in.RawAzureYAML) == 0 {
		return nil, errors.New("synthesis: RawAzureYAML is empty")
	}
	if in.ServiceName == "" {
		return nil, errors.New("synthesis: ServiceName is empty")
	}

	var root projectFile
	if err := yaml.Unmarshal(in.RawAzureYAML, &root); err != nil {
		return nil, fmt.Errorf("parse azure.yaml: %w", err)
	}

	node, ok := root.Services[in.ServiceName]
	if !ok {
		return nil, ErrServiceNotFound
	}

	var svc foundryService
	if err := node.Decode(&svc); err != nil {
		return nil, fmt.Errorf("decode service %q: %w", in.ServiceName, err)
	}

	if svc.Host != "microsoft.foundry" {
		return nil, ErrServiceNotFound
	}
	if svc.Endpoint != "" {
		return nil, ErrEndpointBrownfield
	}

	includeAcr := false
	for _, a := range svc.Agents {
		if a.Docker != nil {
			includeAcr = true
			break
		}
	}

	deployments := svc.Deployments
	if deployments == nil {
		deployments = []Deployment{}
	}

	return &Result{
		Parameters: map[string]any{
			"deployments": deployments,
			"includeAcr":  includeAcr,
		},
	}, nil
}
