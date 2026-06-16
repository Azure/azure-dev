// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"
)

func TestSplitRuntime(t *testing.T) {
	cases := []struct {
		in          string
		wantStack   string
		wantVersion string
	}{
		{"python_3_13", "python", "3.13"},
		{"dotnet_8_0", "dotnet", "8.0"},
		{"python", "python", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		stack, version := splitRuntime(c.in)
		if stack != c.wantStack || version != c.wantVersion {
			t.Errorf("splitRuntime(%q) = (%q, %q), want (%q, %q)",
				c.in, stack, version, c.wantStack, c.wantVersion)
		}
	}
}

func TestEnvVarsToMap(t *testing.T) {
	if got := envVarsToMap(nil); got != nil {
		t.Errorf("envVarsToMap(nil) = %v, want nil", got)
	}
	empty := []agent_yaml.EnvironmentVariable{}
	if got := envVarsToMap(&empty); got != nil {
		t.Errorf("envVarsToMap(empty) = %v, want nil", got)
	}
	vars := []agent_yaml.EnvironmentVariable{{Name: "A", Value: "1"}, {Name: "B", Value: "2"}}
	got := envVarsToMap(&vars)
	if len(got) != 2 || got["A"] != "1" || got["B"] != "2" {
		t.Errorf("envVarsToMap = %v", got)
	}
}

func TestProtocolsToFoundry(t *testing.T) {
	if got := protocolsToFoundry(nil); got != nil {
		t.Errorf("protocolsToFoundry(nil) = %v, want nil", got)
	}
	recs := []agent_yaml.ProtocolVersionRecord{{Protocol: "responses", Version: "1.0.0"}}
	got := protocolsToFoundry(recs)
	if len(got) != 1 || got[0].Protocol != "responses" || got[0].Version != "1.0.0" {
		t.Errorf("protocolsToFoundry = %+v", got)
	}
}

// TestBuildFoundryServiceConfig_CodeDeploy verifies init emits the unified
// host: microsoft.foundry shape with the agent inline and the Foundry keys on
// AdditionalProperties (not config:), and that no service-level project/language
// is set.
func TestBuildFoundryServiceConfig_CodeDeploy(t *testing.T) {
	deployments := []project.Deployment{
		{
			Name:  "gpt-4.1-mini",
			Model: project.DeploymentModel{Name: "gpt-4.1-mini", Format: "OpenAI", Version: "2025-04-14"},
			Sku:   project.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
		},
	}
	in := foundryAgentInput{
		serviceName:     "agent project",
		agentName:       "basic-agent",
		kind:            foundryKindHosted,
		description:     "A basic agent",
		projectPath:     "src/basic-agent",
		env:             map[string]string{"FOUNDRY_MODEL_DEPLOYMENT_NAME": "gpt-4.1-mini"},
		protocols:       []project.AgentProtocol{{Protocol: "responses", Version: "1.0.0"}},
		containerCPU:    "0.5",
		containerMemory: "1Gi",
		isCodeDeploy:    true,
		runtime:         "python_3_13",
		entryPoint:      "main.py",
	}

	sc, err := buildFoundryServiceConfig(in, deployments)
	if err != nil {
		t.Fatalf("buildFoundryServiceConfig: %v", err)
	}

	if sc.Host != project.FoundryHost {
		t.Errorf("Host = %q, want %q", sc.Host, project.FoundryHost)
	}
	if sc.Name != "agentproject" {
		t.Errorf("Name = %q, want %q (spaces stripped)", sc.Name, "agentproject")
	}
	if sc.Config != nil {
		t.Errorf("Config must be nil for microsoft.foundry; got %v", sc.Config)
	}
	if sc.RelativePath != "" {
		t.Errorf("service-level RelativePath must be empty (project is on the agent); got %q", sc.RelativePath)
	}

	var props project.FoundryServiceProperties
	if err := project.UnmarshalStruct(sc.AdditionalProperties, &props); err != nil {
		t.Fatalf("UnmarshalStruct: %v", err)
	}
	if len(props.Deployments) != 1 || props.Deployments[0].Name != "gpt-4.1-mini" {
		t.Fatalf("deployments not carried at project level: %+v", props.Deployments)
	}
	if len(props.Agents) != 1 {
		t.Fatalf("want 1 inline agent, got %d", len(props.Agents))
	}
	a := props.Agents[0]
	if a.Name != "basic-agent" || a.Kind != "hosted" || a.Project != "src/basic-agent" {
		t.Errorf("agent core fields wrong: %+v", a)
	}
	if a.Runtime == nil || a.Runtime.Stack != "python" || a.Runtime.Version != "3.13" {
		t.Errorf("runtime wrong: %+v", a.Runtime)
	}
	if a.StartupCommand != "python main.py" {
		t.Errorf("startupCommand = %q, want %q", a.StartupCommand, "python main.py")
	}
	if a.Docker != nil || a.Image != "" {
		t.Errorf("code deploy must not set docker/image: %+v", a)
	}
	if a.Container == nil || a.Container.Resources == nil || a.Container.Resources.Cpu != "0.5" {
		t.Errorf("container resources not carried: %+v", a.Container)
	}
	if a.Env["FOUNDRY_MODEL_DEPLOYMENT_NAME"] != "gpt-4.1-mini" {
		t.Errorf("env not carried: %+v", a.Env)
	}
}

// TestBuildFoundryServiceConfig_ValidatesAsHostedAgent ensures init's output is
// accepted by the service target's own config validation, linking the writer to
// the reader's contract.
func TestBuildFoundryServiceConfig_ValidatesAsHostedAgent(t *testing.T) {
	in := foundryAgentInput{
		serviceName:  "svc",
		agentName:    "basic-agent",
		kind:         foundryKindHosted,
		projectPath:  "src/basic-agent",
		isCodeDeploy: true,
		runtime:      "python_3_13",
		entryPoint:   "main.py",
	}
	sc, err := buildFoundryServiceConfig(in, nil)
	if err != nil {
		t.Fatalf("buildFoundryServiceConfig: %v", err)
	}

	var cfg project.FoundryProjectConfig
	if err := project.UnmarshalStruct(sc.AdditionalProperties, &cfg); err != nil {
		t.Fatalf("UnmarshalStruct: %v", err)
	}
	agent, err := cfg.Validate()
	if err != nil {
		t.Fatalf("init output failed service-target validation: %v", err)
	}
	if agent.Name != "basic-agent" {
		t.Errorf("validated agent name = %q, want %q", agent.Name, "basic-agent")
	}
}

func TestBuildFoundryServiceConfig_ContainerMode(t *testing.T) {
	in := foundryAgentInput{
		serviceName:    "svc",
		agentName:      "a",
		kind:           foundryKindHosted,
		projectPath:    "src/a",
		isCodeDeploy:   false,
		startupCommand: "python app.py",
	}
	sc, err := buildFoundryServiceConfig(in, nil)
	if err != nil {
		t.Fatalf("buildFoundryServiceConfig: %v", err)
	}

	var props project.FoundryServiceProperties
	if err := project.UnmarshalStruct(sc.AdditionalProperties, &props); err != nil {
		t.Fatalf("UnmarshalStruct: %v", err)
	}
	a := props.Agents[0]
	if a.Docker == nil || a.Docker.Path != "Dockerfile" || !a.Docker.RemoteBuild {
		t.Errorf("container mode must set docker: %+v", a.Docker)
	}
	if a.StartupCommand != "python app.py" {
		t.Errorf("startupCommand = %q", a.StartupCommand)
	}
	if a.Runtime != nil || a.Image != "" {
		t.Errorf("container mode must not set runtime/image: %+v", a)
	}
}

func TestBuildFoundryServiceConfig_ImageMode(t *testing.T) {
	in := foundryAgentInput{
		serviceName: "svc",
		agentName:   "a",
		kind:        foundryKindHosted,
		projectPath: "src/a",
		image:       "myregistry.azurecr.io/agent:1",
	}
	sc, err := buildFoundryServiceConfig(in, nil)
	if err != nil {
		t.Fatalf("buildFoundryServiceConfig: %v", err)
	}

	var props project.FoundryServiceProperties
	if err := project.UnmarshalStruct(sc.AdditionalProperties, &props); err != nil {
		t.Fatalf("UnmarshalStruct: %v", err)
	}
	a := props.Agents[0]
	if a.Image != "myregistry.azurecr.io/agent:1" {
		t.Errorf("image = %q", a.Image)
	}
	if a.Docker != nil || a.Runtime != nil {
		t.Errorf("image mode must not set docker/runtime: %+v", a)
	}
}
