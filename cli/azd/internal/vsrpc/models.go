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
	Resources      []*Resource
}

type Resource struct {
	Name string
	Type string
	Id   string
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

type InitializeServerOptions struct {
	// When non nil, AuthenticationEndpoint is the endpoint to connect to for authentication. It is in the same form as
	// expected by the AZD_AUTH_ENDPOINT environment variable. Note that both AuthenticationEndpoint and AuthenticationKey
	// need to be set for external authentication to be used.
	AuthenticationEndpoint *string `json:",omitempty"`
	// When non nil, AuthenticationKey is the key to use for authenticating to the server. It is in the same form as
	// expected by the AZD_AUTH_KEY environment variable. Note that both AuthenticationEndpoint and AuthenticationKey
	// need to be set for external authentication to be used.
	AuthenticationKey *string `json:",omitempty"`
	// When non nil, AuthenticationCertificate is a base64-encoded x509 certificate used to trust and
	// secure the communications to the server. It is in the same form as the AZD_AUTH_CERT environment variable.
	AuthenticationCertificate *string `json:",omitempty"`
}

func newInfoProgressMessage(message string) ProgressMessage {
	return ProgressMessage{
		Message:  message,
		Severity: Info,
		Time:     time.Now(),
		Kind:     Logging,
	}
}

func newImportantProgressMessage(message string) ProgressMessage {
	return ProgressMessage{
		Message:  message,
		Severity: Info,
		Time:     time.Now(),
		Kind:     Important,
	}
}

// WithMessage returns a new ProgressMessage with the given message and timestamp set to now.
func (m ProgressMessage) WithMessage(message string) ProgressMessage {
	m.Message = message
	m.Time = time.Now()
	return m
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

// RequestContext provides the context for a request to the server.
// It identifies the active session and the azd project being operated on.
type RequestContext struct {
	// The active session.
	Session Session

	// The app host project path.
	HostProjectPath string
}

// Session represents an active connection to the server.  It is returned by InitializeAsync and holds an opaque
// connection id that the server can use to identify the client across multiple RPC calls (since our service is exposed
// over multiple endpoints a single client may have multiple connections to the server, and we want a way to correlate them
// so we can cache state across connections).
type Session struct {
	Id string
}
