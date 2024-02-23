// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"time"
)

type AspireHost struct {
	Name     string
	Path     string
	Services []*Service
}

type Environment struct {
	Name           string
	IsCurrent      bool
	Properties     map[string]string
	Services       []*Service
	Values         map[string]string
	LastDeployment *DeploymentResult `json:",omitempty"`
}

type EnvironmentInfo struct {
	Name       string
	IsCurrent  bool
	DotEnvPath string
}

type Service struct {
	Name       string
	IsExternal bool
	Path       string
	Endpoint   *string `json:",omitempty"`
	ResourceId *string `json:",omitempty"`
}

type DeploymentResult struct {
	Success      bool
	Time         time.Time
	Message      string
	DeploymentId string
}

type ProgressMessage struct {
	Message            string
	Severity           MessageSeverity
	Time               time.Time
	Kind               MessageKind
	Code               string
	AdditionalInfoLink string
}

func newInfoProgressMessage(message string) ProgressMessage {
	return ProgressMessage{
		Message:  message,
		Severity: Info,
		Time:     time.Now(),
		Kind:     Logging,
	}
}

type MessageSeverity int

const (
	Info MessageSeverity = iota
	Warning
	Error
)

type MessageKind int

const (
	Logging MessageKind = iota
	Important
)

// Session represents an active connection to the server.  It is returned by InitializeAsync and holds an opaque
// connection id that the server can use to identify the client across multiple RPC calls (since our service is exposed
// over multiple endpoints a single client may have multiple connections to the server, and we want a way to correlate them
// so we can cache state across connections).
type Session struct {
	Id string
}
