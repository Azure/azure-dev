// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulator

import (
	"fmt"
	"time"
)

// Definition describes an emulator command and builds its local configuration.
type Definition struct {
	Name          string
	DisplayName   string
	Description   string
	Ports         []Port
	LicenseURLs   []string
	HasDirectory  bool
	HasUpstream   bool
	RequiresAMD64 bool
	Build         func(Options) (Spec, error)
}

// Port describes a configurable host port and its command flag.
type Port struct {
	Flag        string
	Default     int
	Description string
}

// Options holds the settings used to create an emulator instance.
type Options struct {
	// Ports maps command flag names to host ports.
	Ports       map[string]int `json:"ports"`
	Directory   string         `json:"directory,omitempty"`
	UpstreamURL string         `json:"upstreamUrl,omitempty"`
}

// URL returns a loopback HTTP endpoint for a configured port.
func (o Options) URL(flag string) string {
	return fmt.Sprintf("http://127.0.0.1:%d", o.Ports[flag])
}

// Binding publishes a container port exclusively on the host's loopback interface.
func (o Options) Binding(flag string, containerPort int) string {
	return fmt.Sprintf("127.0.0.1:%d:%d", o.Ports[flag], containerPort)
}

// Request describes a launch or stop operation and explicitly supplied settings.
type Request struct {
	Options       Options
	Changed       map[string]bool
	AcceptLicense bool
	Stop          bool
	Timeout       time.Duration
}

// Connection is a named connection setting intended for the local application.
type Connection struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Result contains connection information for a ready emulator or a stop result.
type Result struct {
	Emulator    string       `json:"emulator"`
	Status      string       `json:"status"`
	ProjectName string       `json:"projectName"`
	Connections []Connection `json:"connections,omitempty"`
	Notes       []string     `json:"notes,omitempty"`
	// RemovedConnectionNames identifies the environment outputs to invalidate after stopping an instance.
	RemovedConnectionNames []string `json:"-"`
}

// Spec contains the Compose configuration, readiness checks and connection settings.
type Spec struct {
	Compose     Compose      `json:"compose"`
	Connections []Connection `json:"connections"`
	Notes       []string     `json:"notes,omitempty"`
	Probes      []Probe      `json:"probes"`
	// Dockerfile is the source for the main service's locally built image.
	Dockerfile string `json:"dockerfile,omitempty"`
	// ExecProbe is an optional readiness command executed inside the main container.
	ExecProbe []string `json:"execProbe,omitempty"`
	// Secrets contains generated values that must not appear in tool diagnostics.
	Secrets []string `json:"secrets,omitempty"`
}

// Compose is the subset of the Compose specification used by the launchers.
type Compose struct {
	// Services maps stable service names to their container definitions.
	Services map[string]Service `json:"services"`
	// Configs maps configuration names to inline file contents.
	Configs map[string]Config `json:"configs,omitempty"`
}

// Service configures a container and any dependencies within its Compose project.
type Service struct {
	Image      string   `json:"image,omitempty"`
	Platform   string   `json:"platform,omitempty"`
	Build      *Build   `json:"build,omitempty"`
	Command    []string `json:"command,omitempty"`
	Entrypoint []string `json:"entrypoint,omitempty"`
	Ports      []string `json:"ports,omitempty"`
	// Environment maps container environment variable names to their values.
	Environment map[string]string `json:"environment,omitempty"`
	DependsOn   []string          `json:"depends_on,omitempty"`
	Configs     []ConfigMount     `json:"configs,omitempty"`
	Volumes     []Volume          `json:"volumes,omitempty"`
	ExtraHosts  []string          `json:"extra_hosts,omitempty"`
}

// Build identifies the isolated context for a locally built emulator image.
type Build struct {
	Context string `json:"context"`
}

// Config contains an inline configuration file.
type Config struct {
	Content string `json:"content"`
}

// ConfigMount places a Compose configuration file inside a container.
type ConfigMount struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// Volume describes a read-only host directory mounted into a container.
type Volume struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

// Probe describes an HTTP readiness check on a loopback endpoint.
type Probe struct {
	URL    string `json:"url"`
	Method string `json:"method,omitempty"`
	Body   string `json:"body,omitempty"`
	Status int    `json:"status"`
}
