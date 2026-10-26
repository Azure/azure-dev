//go:build integration

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/stretchr/testify/require"
)

func integrationCommand(t *testing.T, ctx context.Context, directory, tool string, arguments ...string) string {
	t.Helper()
	runner := azdexec.NewCommandRunner(&azdexec.RunnerOptions{Stdout: io.Discard, Stderr: io.Discard})
	result, err := runner.Run(ctx, azdexec.NewRunArgs(tool, arguments...).WithCwd(directory))
	require.NoErrorf(t, err, "%s failed:\n%s\n%s", tool, result.Stdout, result.Stderr)
	return strings.TrimSpace(result.Stdout)
}

func TestGeneratedContainer(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	t.Setenv("NO_COLOR", "1")
	directory := t.TempDir()
	require.NoError(t, os.CopyFS(directory, os.DirFS(filepath.Join("testdata", "container-smoke"))))
	integrationCommand(t, ctx, directory, "docker", "info")
	maven, err := mavenExecutable(directory)
	require.NoError(t, err)
	integrationCommand(t, ctx, directory, maven, "-B", "wrapper:wrapper", "-Dmaven=3.9.11")
	analysis, err := AnalyzeProject(ctx, directory, NewModelResolver(io.Discard), DeploymentInputs{
		Properties: map[string]string{"DEPLOYMENT_JDBC_URL": "jdbc:h2:mem:smoke"},
	})
	require.NoError(t, err)
	application, err := analysis.ResolvedApplication()
	require.NoError(t, err)
	require.True(t, application.MavenWrapper)
	require.Equal(t, "build output", application.BuildDirectory)
	plan, err := Generate(ctx, application)
	require.NoError(t, err)
	require.NoError(t, plan.Write(ctx, directory))
	root, err := os.OpenRoot(directory)
	require.NoError(t, err)
	defer root.Close()
	data, err := root.ReadFile("mvnw")
	require.NoError(t, err)
	require.NoError(t, root.WriteFile("mvnw",
		[]byte(strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n", "\r\n")), 0o600))

	image := fmt.Sprintf("azd-spring-smoke:%d", time.Now().UnixNano())
	integrationCommand(t, ctx, directory, "docker", "build", "--tag", image,
		"--file", filepath.Join(".azd-spring", "Dockerfile"), ".")
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		integrationCommand(t, cleanup, directory, "docker", "image", "rm", "--force", image)
	})
	container := integrationCommand(t, ctx, directory, "docker", "create",
		"--publish", "127.0.0.1::9090",
		"--env", "SPRING_CONFIG_ADDITIONAL_LOCATION=file:/app/.azd-spring/application.properties",
		"--env", "SERVER_ADDRESS=0.0.0.0", image)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if t.Failed() {
			runner := azdexec.NewCommandRunner(&azdexec.RunnerOptions{Stdout: io.Discard, Stderr: io.Discard})
			result, err := runner.Run(cleanup, azdexec.NewRunArgs("docker", "logs", container).WithCwd(directory))
			t.Logf("container logs: %s\n%s (error: %v)", result.Stdout, result.Stderr, err)
		}
		integrationCommand(t, cleanup, directory, "docker", "rm", "--force", container)
	})
	integrationCommand(t, ctx, directory, "docker", "start", container)
	address := integrationCommand(t, ctx, directory, "docker", "port", container, "9090/tcp")
	client := &http.Client{Timeout: 2 * time.Second}
	var body []byte
	require.Eventually(t, func() bool {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address, nil)
		if err != nil {
			return false
		}
		response, err := client.Do(request)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		body, err = io.ReadAll(response.Body)
		return err == nil && response.StatusCode == http.StatusOK
	}, time.Minute, time.Second, "generated image did not serve HTTP")
	require.Equal(t, "cloud|jdbc:h2:mem:smoke|42", string(body))
}

type compiledTemplate struct {
	Variables  map[string]json.RawMessage `json:"variables"`
	Parameters map[string]struct {
		Type string `json:"type"`
	} `json:"parameters"`
	Resources []struct {
		Name       string          `json:"name"`
		Type       string          `json:"type"`
		Condition  string          `json:"condition"`
		Properties json.RawMessage `json:"properties"`
		DependsOn  []string        `json:"dependsOn"`
		Copy       struct {
			Count string `json:"count"`
		} `json:"copy"`
	} `json:"resources"`
}

type compiledDeployment struct {
	Template   compiledTemplate           `json:"template"`
	Parameters map[string]json.RawMessage `json:"parameters"`
}

func deployment(t *testing.T, template compiledTemplate, name string) compiledDeployment {
	t.Helper()
	for _, resource := range template.Resources {
		if resource.Type == "Microsoft.Resources/deployments" && resource.Name == name {
			var module compiledDeployment
			require.NoError(t, json.Unmarshal(resource.Properties, &module))
			return module
		}
	}
	t.Fatalf("deployment %s was not generated", name)
	return compiledDeployment{}
}

func TestCompiledInfrastructure(t *testing.T) {
	plan, err := Generate(t.Context(), &Application{
		Name: "smoke", JavaVersion: 17, JarName: "smoke.jar", BuildDirectory: "target", Port: 9090,
	})
	require.NoError(t, err)
	directory := t.TempDir()
	require.NoError(t, plan.Write(t.Context(), directory))
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	data := integrationCommand(t, ctx, directory, "az", "bicep", "build",
		"--file", filepath.Join("infra", "main.bicep"), "--stdout")
	var template compiledTemplate
	require.NoError(t, json.Unmarshal([]byte(data), &template))
	require.Equal(t, "securestring", template.Parameters["databasePassword"].Type)

	application := deployment(t, template, "application")
	for parameter, source := range map[string]string{
		"port": "port", "profiles": "profiles", "engine": "databaseEngine", "authentication": "databaseAuth",
		"password": "databasePassword", "environmentBindings": "environmentBindings",
	} {
		require.JSONEq(t, `{"value":"[parameters('`+source+`')]"}`, string(application.Parameters[parameter]))
	}
	for _, resource := range template.Resources {
		if resource.Name == "application" {
			require.Contains(t, strings.Join(resource.DependsOn, " "), "'access'")
		}
	}
	require.Len(t, application.Template.Resources, 1)
	require.Equal(t, "Microsoft.App/containerApps", application.Template.Resources[0].Type)
	var app struct {
		Configuration struct {
			Ingress struct {
				TargetPort string `json:"targetPort"`
			} `json:"ingress"`
			Secrets string `json:"secrets"`
		} `json:"configuration"`
		Template struct {
			Containers []struct {
				Image  string `json:"image"`
				Env    string `json:"env"`
				Probes string `json:"probes"`
			} `json:"containers"`
		} `json:"template"`
	}
	require.NoError(t, json.Unmarshal(application.Template.Resources[0].Properties, &app))
	require.Contains(t, app.Configuration.Ingress.TargetPort, "parameters('port')")
	require.Contains(t, app.Configuration.Secrets, "parameters('password')")
	require.Len(t, app.Template.Containers, 1)
	container := app.Template.Containers[0]
	require.Equal(t, "[parameters('appImage')]", container.Image)
	// The four-minute startup allowance must exceed a typical Spring application's startup time.
	for _, probe := range []struct {
		name   string
		timing string
	}{
		{"Startup", "'initialDelaySeconds', 1, 'periodSeconds', 1, 'timeoutSeconds', 3, 'failureThreshold', 240"},
		{"Liveness", "'initialDelaySeconds', 10, 'periodSeconds', 10, 'timeoutSeconds', 1, 'failureThreshold', 3"},
		{"Readiness", "'initialDelaySeconds', 10, 'periodSeconds', 5, 'failureThreshold', 20"},
	} {
		require.Contains(t, container.Probes,
			fmt.Sprintf("'type', '%s', 'tcpSocket', createObject('port', parameters('port')), %s)",
				probe.name, probe.timing))
	}
	require.Contains(t, container.Probes, "if(variables('placeholder'), createArray(),")
	require.Contains(t, container.Env, "'SERVER_ADDRESS', 'value', '0.0.0.0'")
	require.Contains(t, container.Env, "'SPRING_CONFIG_ADDITIONAL_LOCATION'")

	access := deployment(t, template, "access")
	require.Contains(t, string(access.Parameters["outboundIps"]), "'bootstrap'")
	firewalls := 0
	for _, resource := range access.Template.Resources {
		if !strings.HasSuffix(resource.Type, "/firewallRules") {
			continue
		}
		firewalls++
		var rule struct {
			Start string `json:"startIpAddress"`
			End   string `json:"endIpAddress"`
		}
		require.NoError(t, json.Unmarshal(resource.Properties, &rule))
		require.Equal(t, "[union(parameters('outboundIps'), createArray())[copyIndex()]]", rule.Start)
		require.Equal(t, rule.Start, rule.End)
		require.Equal(t, "[length(union(parameters('outboundIps'), createArray()))]", resource.Copy.Count)
		parameter := "postgresServerName"
		if strings.Contains(resource.Type, "MySQL") {
			parameter = "mysqlServerName"
		}
		require.Equal(t, "[not(equals(parameters('"+parameter+"'), ''))]", resource.Condition)
		require.Contains(t, string(access.Parameters[parameter]), "parameters('databaseEngine')")
	}
	require.Equal(t, 2, firewalls)

	database := deployment(t, template, "database")
	require.JSONEq(t, `"[equals(parameters('authentication'), 'managedIdentity')]"`,
		string(database.Template.Variables["managedIdentity"]))
	firewalls = 0
	for _, resource := range database.Template.Resources {
		if !strings.HasSuffix(resource.Type, "/firewallRules") {
			continue
		}
		firewalls++
		require.Equal(t, "Microsoft.DBforPostgreSQL/flexibleServers/firewallRules", resource.Type)
		require.Contains(t, resource.Name, "bootstrap-local-ip")
		require.Equal(t, "[and(equals(parameters('engine'), 'postgresql'), variables('managedIdentity'))]",
			resource.Condition)
		require.JSONEq(t, `{"startIpAddress":"0.0.0.0","endIpAddress":"255.255.255.255"}`,
			string(resource.Properties))
	}
	require.Equal(t, 1, firewalls)
}
