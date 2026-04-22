// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package apphost

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/custommaps"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/require"
)

func intPtr(i int) *int       { return &i }
func strPtr(s string) *string { return &s }

func TestAspireDashboardUrl(t *testing.T) {
	t.Run("container_env_domain", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{
			"AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN": "example.azurecontainerapps.io",
		})
		d := AspireDashboardUrl(context.Background(), env, nil)
		require.NotNil(t, d)
		require.Equal(t, "https://aspire-dashboard.ext.example.azurecontainerapps.io", d.Link)
		// ToString and MarshalJSON
		s := d.ToString("  ")
		require.Contains(t, s, "Aspire Dashboard:")
		b, err := d.MarshalJSON()
		require.NoError(t, err)
		require.Contains(t, string(b), "aspire-dashboard.ext.example")
	})

	t.Run("app_service_dashboard_url", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{
			environment.AppServiceAspireDashboardUrlEnvVarName: "https://dashboard.example.com",
		})
		d := AspireDashboardUrl(context.Background(), env, nil)
		require.NotNil(t, d)
		require.Equal(t, "https://dashboard.example.com", d.Link)
	})

	t.Run("no_env", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{})
		d := AspireDashboardUrl(context.Background(), env, nil)
		require.Nil(t, d)
	})
}

func TestProjectPaths(t *testing.T) {
	m := &Manifest{Resources: map[string]*Resource{
		"api":   {Type: "project.v0", Path: strPtr("/p/api.csproj")},
		"web":   {Type: "project.v1", Path: strPtr("/p/web.csproj")},
		"other": {Type: "container.v0", Image: strPtr("x")},
	}}
	paths := ProjectPaths(m)
	require.Equal(t, "/p/api.csproj", paths["api"])
	require.Equal(t, "/p/web.csproj", paths["web"])
	_, ok := paths["other"]
	require.False(t, ok)
}

func TestDockerfiles(t *testing.T) {
	m := &Manifest{Resources: map[string]*Resource{
		"df": {
			Type:      "dockerfile.v0",
			Path:      strPtr("/p/Dockerfile"),
			Context:   strPtr("/p"),
			Env:       map[string]string{"A": "1"},
			BuildArgs: map[string]string{"B": "2"},
			Args:      []string{"--flag"},
		},
		"ignored": {Type: "project.v0"},
	}}
	d := Dockerfiles(m)
	require.Len(t, d, 1)
	require.Equal(t, "/p/Dockerfile", d["df"].Path)
	require.Equal(t, "/p", d["df"].Context)
	require.Equal(t, "1", d["df"].Env["A"])
	require.Equal(t, "2", d["df"].BuildArgs["B"])
}

func TestContainers(t *testing.T) {
	m := &Manifest{Resources: map[string]*Resource{
		"c": {
			Type:  "container.v0",
			Image: strPtr("nginx:latest"),
			Env:   map[string]string{"A": "1"},
			Args:  []string{"-x"},
		},
		"ignored": {Type: "project.v0"},
	}}
	c := Containers(m)
	require.Len(t, c, 1)
	require.Equal(t, "nginx:latest", c["c"].Image)
}

func TestBuildContainers_Image(t *testing.T) {
	m := &Manifest{Resources: map[string]*Resource{
		"c": {Type: "container.v1", Image: strPtr("myimg:1.0")},
	}}
	got, err := BuildContainers(m)
	require.NoError(t, err)
	require.Equal(t, "myimg:1.0", got["c"].Image)
	require.Equal(t, 8080, got["c"].DefaultTargetPort)
}

func TestBuildContainerFromResource(t *testing.T) {
	t.Run("v0_image", func(t *testing.T) {
		r := &Resource{Type: "container.v0", Image: strPtr("redis")}
		bc, err := buildContainerFromResource(r)
		require.NoError(t, err)
		require.Equal(t, "redis", bc.Image)
		require.Equal(t, 80, bc.DefaultTargetPort)
		require.Nil(t, bc.Build)
	})
	t.Run("v1_default_port", func(t *testing.T) {
		r := &Resource{Type: "container.v1", Image: strPtr("x")}
		bc, err := buildContainerFromResource(r)
		require.NoError(t, err)
		require.Equal(t, 8080, bc.DefaultTargetPort)
	})
	t.Run("dockerfile_v0_with_context", func(t *testing.T) {
		r := &Resource{
			Type:    "dockerfile.v0",
			Path:    strPtr("/abs/Dockerfile"),
			Context: strPtr("/abs"),
		}
		bc, err := buildContainerFromResource(r)
		require.NoError(t, err)
		require.NotNil(t, bc.Build)
		require.Equal(t, "/abs", bc.Build.Context)
		require.Equal(t, "/abs/Dockerfile", bc.Build.Dockerfile)
	})
	t.Run("container_v1_build", func(t *testing.T) {
		r := &Resource{
			Type: "container.v1",
			Build: &ContainerV1Build{
				Context:    "/ctx",
				Dockerfile: "/ctx/Dockerfile",
				Args:       map[string]string{"K": "V"},
				BuildOnly:  true,
			},
		}
		bc, err := buildContainerFromResource(r)
		require.NoError(t, err)
		require.NotNil(t, bc.Build)
		require.Equal(t, "/ctx", bc.Build.Context)
		require.True(t, bc.Build.BuildOnly)
	})
	t.Run("error_no_image_no_build", func(t *testing.T) {
		r := &Resource{Type: "container.v1"}
		_, err := buildContainerFromResource(r)
		require.Error(t, err)
	})
	t.Run("with_deployment", func(t *testing.T) {
		r := &Resource{
			Type:  "container.v1",
			Image: strPtr("x"),
			Deployment: &DeploymentMetadata{
				Type:   "azure.bicep.v0",
				Path:   strPtr("/m/thing.bicep"),
				Params: map[string]any{"a": "b"},
			},
		}
		bc, err := buildContainerFromResource(r)
		require.NoError(t, err)
		require.Equal(t, "thing.bicep", bc.DeploymentSource)
		require.Equal(t, "b", bc.DeploymentParams["a"])
	})
}

func TestInputMetadata(t *testing.T) {
	lower := true
	upper := false
	cfg := InputDefaultGenerate{
		MinLength:  uintPtr(16),
		Lower:      &lower,
		Upper:      &upper,
		MinNumeric: uintPtr(2),
	}
	s, err := inputMetadata(cfg)
	require.NoError(t, err)
	require.Contains(t, s, "length:16")
	// Lower was true so NoLower should be false -> "minLower" not forced; NoLower exists as false
	require.Contains(t, s, "noLower:false")
	require.Contains(t, s, "noUpper:true")
}

func TestInputMetadata_ClusterLargerThanMin(t *testing.T) {
	// When cluster sum > MinLength, finalLength = cluster sum
	cfg := InputDefaultGenerate{
		MinLength:  uintPtr(4),
		MinLower:   uintPtr(5),
		MinUpper:   uintPtr(6),
		MinNumeric: uintPtr(7),
		MinSpecial: uintPtr(8),
	}
	s, err := inputMetadata(cfg)
	require.NoError(t, err)
	require.Contains(t, s, "length:26")
}

func uintPtr(u uint) *uint { return &u }

func TestIsComplexExpression(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		wantComplex bool
		wantVal     string
	}{
		{"simple", "'{{resource.outputs.x}}'", false, "resource.outputs.x"},
		{"complex_multi", "'{{a}}' + '{{b}}'", true, ""},
		{"plain_string", "'hello'", true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			complex, val := isComplexExpression(c.input)
			require.Equal(t, c.wantComplex, complex)
			require.Equal(t, c.wantVal, val)
		})
	}
}

func TestUrlPort(t *testing.T) {
	t.Run("main_http_no_port", func(t *testing.T) {
		p, err := urlPort(&Binding{Scheme: "http"}, true)
		require.NoError(t, err)
		require.Equal(t, "", p)
	})
	t.Run("main_https_no_port", func(t *testing.T) {
		p, err := urlPort(&Binding{Scheme: "https"}, true)
		require.NoError(t, err)
		require.Equal(t, "", p)
	})
	t.Run("port_defined", func(t *testing.T) {
		p, err := urlPort(&Binding{Scheme: "http", Port: intPtr(8080)}, false)
		require.NoError(t, err)
		require.Equal(t, "8080", p)
	})
	t.Run("target_port_fallback", func(t *testing.T) {
		p, err := urlPort(&Binding{Scheme: "tcp", TargetPort: intPtr(5432)}, false)
		require.NoError(t, err)
		require.Equal(t, "5432", p)
	})
	t.Run("templated", func(t *testing.T) {
		p, err := urlPort(&Binding{Scheme: "http"}, false)
		require.NoError(t, err)
		require.Equal(t, acaTemplatedTargetPort, p)
	})
}

func TestBindingPort(t *testing.T) {
	t.Run("main_http", func(t *testing.T) {
		p, err := bindingPort(&Binding{Scheme: "http"}, true)
		require.NoError(t, err)
		require.Equal(t, "80", p)
	})
	t.Run("main_https", func(t *testing.T) {
		p, err := bindingPort(&Binding{Scheme: "https"}, true)
		require.NoError(t, err)
		require.Equal(t, "443", p)
	})
	t.Run("port_priority", func(t *testing.T) {
		p, err := bindingPort(&Binding{Scheme: "tcp", Port: intPtr(9000), TargetPort: intPtr(1)}, false)
		require.NoError(t, err)
		require.Equal(t, "9000", p)
	})
	t.Run("target_port", func(t *testing.T) {
		p, err := bindingPort(&Binding{Scheme: "tcp", TargetPort: intPtr(1234)}, false)
		require.NoError(t, err)
		require.Equal(t, "1234", p)
	})
	t.Run("templated", func(t *testing.T) {
		p, err := bindingPort(&Binding{Scheme: "http"}, false)
		require.NoError(t, err)
		require.Equal(t, acaTemplatedTargetPort, p)
	})
}

func TestUrlPortFromTargetPort(t *testing.T) {
	p, err := urlPortFromTargetPort(&Binding{Scheme: "http"}, true)
	require.NoError(t, err)
	require.Equal(t, "80", p)
	p, err = urlPortFromTargetPort(&Binding{Scheme: "https"}, true)
	require.NoError(t, err)
	require.Equal(t, "443", p)
	p, err = urlPortFromTargetPort(&Binding{TargetPort: intPtr(42)}, false)
	require.NoError(t, err)
	require.Equal(t, "42", p)
	p, err = urlPortFromTargetPort(&Binding{}, false)
	require.NoError(t, err)
	require.Equal(t, acaTemplatedTargetPort, p)
}

func TestAsYamlString(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"true", `"true"`},
		{"hello", "hello"},
	}
	for _, c := range cases {
		got, err := asYamlString(c.in)
		require.NoError(t, err)
		require.Equal(t, c.want, got)
	}
}

func TestUniqueFnvNumber(t *testing.T) {
	a := uniqueFnvNumber("example")
	b := uniqueFnvNumber("example")
	c := uniqueFnvNumber("different")
	require.Equal(t, a, b)
	require.NotEqual(t, a, c)
	require.Len(t, a, 8)
}

func TestInputParameter_NoInputs(t *testing.T) {
	r := &Resource{Value: "just a plain value"}
	in, err := InputParameter("p", r)
	require.NoError(t, err)
	require.Nil(t, in)
}

func TestInputParameter_WithInputs(t *testing.T) {
	r := &Resource{
		Value: "{self.inputs.pw}",
		Inputs: map[string]Input{
			"pw": {Secret: true},
		},
	}
	in, err := InputParameter("self", r)
	require.NoError(t, err)
	require.NotNil(t, in)
	require.Equal(t, "string", in.Type)
	require.True(t, in.Secret)
}

func TestInputParameter_CrossResourceError(t *testing.T) {
	r := &Resource{
		Value:  "{other.inputs.pw}",
		Inputs: map[string]Input{"pw": {}},
	}
	in, err := InputParameter("self", r)
	require.Error(t, err)
	require.Nil(t, in)
	require.Contains(t, err.Error(), "does not use inputs from its own resource")
}

func TestInputParameter_MissingInputError(t *testing.T) {
	r := &Resource{
		Value:  "{self.inputs.pw}",
		Inputs: map[string]Input{},
	}
	in, err := InputParameter("self", r)
	require.Error(t, err)
	require.Nil(t, in)
	require.Contains(t, err.Error(), "does not have input")
}

func TestEvaluateForOutputs_MultipleAndMigration(t *testing.T) {
	value := "{r.outputs.AZURE_CONTAINER_REGISTRY_ENDPOINT} and " +
		"{r.outputs.AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN} and " +
		"{r.outputs.AZURE_APP_SERVICE_DASHBOARD_URI}"
	outs, err := evaluateForOutputs(value, true)
	require.NoError(t, err)
	// well-known environment keys are injected when appHostOwnsCompute=true
	_, ok := outs[environment.ContainerRegistryEndpointEnvVarName]
	require.True(t, ok)
	_, ok = outs[environment.ContainerEnvironmentEndpointEnvVarName]
	require.True(t, ok)
	_, ok = outs[environment.AppServiceAspireDashboardUrlEnvVarName]
	require.True(t, ok)
	// normal uppercase keys also present
	require.Contains(t, outs, "R_AZURE_CONTAINER_REGISTRY_ENDPOINT")
}

func TestEvaluateForOutputs_NoMatches(t *testing.T) {
	outs, err := evaluateForOutputs("just a plain value", false)
	require.NoError(t, err)
	require.Empty(t, outs)
}

func TestEvaluateForOutputs_SecretOutputs(t *testing.T) {
	outs, err := evaluateForOutputs("{resource.secretOutputs.password}", false)
	require.NoError(t, err)
	require.Contains(t, outs, "RESOURCE_PASSWORD")
	require.Equal(t, "resource.secretOutputs.password", outs["RESOURCE_PASSWORD"].Value)
}

func TestGenerateProjectArtifacts(t *testing.T) {
	tmp := t.TempDir()
	m := &Manifest{Resources: map[string]*Resource{}}
	appHostProject := tmp + "/MyApp/MyApp.AppHost.csproj"
	files, err := GenerateProjectArtifacts(context.Background(), tmp, "demo", m, appHostProject)
	require.NoError(t, err)
	require.Contains(t, files, "azure.yaml")
	require.Contains(t, files, "next-steps.md")
	require.Contains(t, files["azure.yaml"].Contents, "demo")
}

// Additional aca_ingress test cases — targeting branches not covered in existing tests.
func TestBuildAcaIngress_MoreCases(t *testing.T) {
	t.Run("http_non_default_port_rejected", func(t *testing.T) {
		// Scheme http with port != 80 and no target port => main ingress only supports port 80 for http.
		bindingsManifest := `{
			"http": {
				"scheme": "http",
				"port": 8081
			}}`
		var bindings custommaps.WithOrder[Binding]
		require.NoError(t, json.Unmarshal([]byte(bindingsManifest), &bindings))
		_, _, err := buildAcaIngress(bindings, 8080)
		require.Error(t, err)
		require.Contains(t, err.Error(), "main ingress only supports port 80")
	})

	t.Run("https_non_default_port_rejected", func(t *testing.T) {
		bindingsManifest := `{
			"https": {
				"scheme": "https",
				"port": 8443
			}}`
		var bindings custommaps.WithOrder[Binding]
		require.NoError(t, json.Unmarshal([]byte(bindingsManifest), &bindings))
		_, _, err := buildAcaIngress(bindings, 8080)
		require.Error(t, err)
		require.Contains(t, err.Error(), "main ingress only supports port 443")
	})

	t.Run("external_http_pinning", func(t *testing.T) {
		// Two http groups with different target ports; only one external -> external wins.
		bindingsManifest := `{
			"internal": {
				"scheme":     "http",
				"targetPort": 9000
			},
			"public": {
				"scheme":     "http",
				"targetPort": 9001,
				"external":   true
			}}`
		var bindings custommaps.WithOrder[Binding]
		require.NoError(t, json.Unmarshal([]byte(bindingsManifest), &bindings))
		ingress, names, err := buildAcaIngress(bindings, 8080)
		require.NoError(t, err)
		require.NotNil(t, ingress)
		require.True(t, ingress.External)
		require.Equal(t, 9001, ingress.TargetPort)
		require.Equal(t, []string{"public"}, names)
		require.Len(t, ingress.AdditionalPortMappings, 1)
		require.Equal(t, 9000, ingress.AdditionalPortMappings[0].TargetPort)
	})

	t.Run("tcp_only_with_exposed_port", func(t *testing.T) {
		// Single tcp binding, not http -> transport tcp with exposed port preserved.
		bindingsManifest := `{
			"db": {
				"scheme":     "tcp",
				"targetPort": 5432,
				"port":       5433
			}}`
		var bindings custommaps.WithOrder[Binding]
		require.NoError(t, json.Unmarshal([]byte(bindingsManifest), &bindings))
		ingress, names, err := buildAcaIngress(bindings, 8080)
		require.NoError(t, err)
		require.NotNil(t, ingress)
		require.Equal(t, acaIngressSchemaTcp, ingress.Transport)
		require.Equal(t, 5432, ingress.TargetPort)
		require.Equal(t, 5433, ingress.ExposedPort)
		require.Equal(t, []string{"db"}, names)
	})

	t.Run("many_additional_ports_warning", func(t *testing.T) {
		// More than 5 additional ports: the code logs a warning but still succeeds.
		bindings := custommaps.WithOrder[Binding]{}
		manifest := `{
			"main":  {"scheme": "http"},
			"extra1":{"scheme":"tcp","targetPort":1001},
			"extra2":{"scheme":"tcp","targetPort":1002},
			"extra3":{"scheme":"tcp","targetPort":1003},
			"extra4":{"scheme":"tcp","targetPort":1004},
			"extra5":{"scheme":"tcp","targetPort":1005},
			"extra6":{"scheme":"tcp","targetPort":1006},
			"extra7":{"scheme":"tcp","targetPort":1007}
		}`
		require.NoError(t, json.Unmarshal([]byte(manifest), &bindings))
		ingress, _, err := buildAcaIngress(bindings, 8080)
		require.NoError(t, err)
		require.NotNil(t, ingress)
		require.GreaterOrEqual(t, len(ingress.AdditionalPortMappings), 6)
	})

	t.Run("empty_binding_error", func(t *testing.T) {
		// A nil binding in the ordered map triggers validateBindings error path.
		bindings := custommaps.WithOrder[Binding]{}
		require.NoError(t, json.Unmarshal([]byte(`{"a": null}`), &bindings))
		_, _, err := buildAcaIngress(bindings, 8080)
		require.Error(t, err)
		require.Contains(t, strings.ToLower(err.Error()), "empty")
	})
}

// Extra resolvePublishMode cases.
func TestResolvePublishMode_Extra(t *testing.T) {
	t.Run("container_v1_with_global_outputs_is_hybrid", func(t *testing.T) {
		m := &Manifest{Resources: map[string]*Resource{
			"c": {
				Type: "container.v1",
				Env:  map[string]string{"X": "{.outputs.foo}"},
			},
		}}
		require.Equal(t, publishModeHybrid, resolvePublishMode(m))
	})
}

// Test that EvalString correctly propagates UnrecognizedExpressionError by keeping original text.
func TestEvalString_UnrecognizedKept(t *testing.T) {
	res, err := EvalString("prefix-{unknown.thing}-suffix", func(s string) (string, error) {
		return "", UnrecognizedExpressionError{}
	})
	require.NoError(t, err)
	require.Equal(t, "prefix-{unknown.thing}-suffix", res)
}

// --- infraGenerator integration tests (exercise LoadManifest + Compile) ---

func TestInfraGenerator_UnsupportedResourceType(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"x": {Type: "totally.unknown.v0"},
	}}
	err := g.LoadManifest(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported resource type")
}

func TestInfraGenerator_ValueResource(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"v": {Type: "value.v0", Value: "hello"},
	}}
	require.NoError(t, g.LoadManifest(m))
	require.Equal(t, "hello", g.valueStrings["v"])
}

func TestInfraGenerator_AnnotatedString(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"a": {Type: "annotated.string", Filter: "upper", Value: "x"},
	}}
	require.NoError(t, g.LoadManifest(m))
	require.Equal(t, "upper", g.annotatedStrings["a"].Filter)
	require.Equal(t, "x", g.annotatedStrings["a"].Value)
}

func TestInfraGenerator_ParameterResource(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"pw": {
			Type:  "parameter.v0",
			Value: "{pw.inputs.secret}",
			Inputs: map[string]Input{
				"secret": {Secret: true, Type: "string"},
			},
		},
	}}
	require.NoError(t, g.LoadManifest(m))
	// Compile should succeed with no projects/containers
	require.NoError(t, g.Compile())
	require.Contains(t, g.bicepContext.InputParameters, "pw")
	require.True(t, g.bicepContext.InputParameters["pw"].Secret)
}

func TestInfraGenerator_ConnectionString(t *testing.T) {
	cs := "Server=tcp:{db.outputs.serverName};Database=db"
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"conn": {Type: "value.v0", Value: "placeholder", ConnectionString: &cs},
	}}
	require.NoError(t, g.LoadManifest(m))
	require.Equal(t, cs, g.connectionStrings["conn"])
	// output from connection string should be captured
	require.Contains(t, g.bicepContext.OutputParameters, "DB_SERVERNAME")
}

func TestInfraGenerator_ContainerV0(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"redis": {Type: "container.v0", Image: strPtr("redis:7")},
	}}
	require.NoError(t, g.LoadManifest(m))
	require.NoError(t, g.Compile())
	require.Contains(t, g.buildContainers, "redis")
	require.True(t, g.bicepContext.HasContainerEnvironment)
}

func TestInfraGenerator_ContainerV0_ImageAndBuildError(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"bad": {
			Type:  "container.v1",
			Image: strPtr("x"),
			Build: &ContainerV1Build{Context: "/c", Dockerfile: "/c/Dockerfile"},
		},
	}}
	err := g.LoadManifest(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot have both an image and a build")
}

func TestInfraGenerator_DaprRequiresMetadata(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"d": {Type: "dapr.v0"},
	}}
	err := g.LoadManifest(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "required metadata")
}

func TestInfraGenerator_DaprComponentRequiresType(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"c": {Type: "dapr.component.v0"},
	}}
	err := g.LoadManifest(m)
	require.Error(t, err)
}

func TestInfraGenerator_DaprFullFlow(t *testing.T) {
	app := "frontend"
	appID := "frontendapp"
	appPort := 3500
	appProto := "http"

	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"frontend": {Type: "project.v0", Path: strPtr("/p/f.csproj")},
		"dsidecar": {
			Type: "dapr.v0",
			Dapr: &DaprResourceMetadata{
				Application: &app,
				AppId:       &appID,
				AppPort:     &appPort,
				AppProtocol: &appProto,
			},
		},
	}}
	require.NoError(t, g.LoadManifest(m))
	require.NoError(t, g.Compile())
	require.Contains(t, g.dapr, "dsidecar")
	// Project template context gets Dapr config
	require.Contains(t, g.containerAppTemplateContexts, "frontend")
	require.NotNil(t, g.containerAppTemplateContexts["frontend"].Dapr)
}

func TestInfraGenerator_BicepV0_NoPathNoParent(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"mod": {Type: "azure.bicep.v0"},
	}}
	err := g.LoadManifest(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not have a path or a parent")
}

func TestInfraGenerator_BicepV0_WithPath(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"mod": {
			Type:   "azure.bicep.v0",
			Path:   strPtr("mod/mod.bicep"),
			Params: map[string]any{"keyVaultName": "", "other": "v"},
		},
	}}
	require.NoError(t, g.LoadManifest(m))
	require.NoError(t, g.Compile())
	require.Contains(t, g.bicepContext.BicepModules, "mod")
	// keyVaultName == "" should trigger auto-injection of a KeyVault
	require.NotEmpty(t, g.bicepContext.KeyVaults)
}

func TestInfraGenerator_BicepV0_ScopeWithoutResourceGroup(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"mod": {
			Type:  "azure.bicep.v0",
			Path:  strPtr("mod/mod.bicep"),
			Scope: &BicepModuleScope{},
		},
	}}
	err := g.LoadManifest(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "scope without a resource group")
}

func TestInfraGenerator_IgnoreUnsupportedEnvVar(t *testing.T) {
	t.Setenv("AZD_DEBUG_DOTNET_APPHOST_IGNORE_UNSUPPORTED_RESOURCES", "true")
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"x": {Type: "totally.unknown.v0"},
	}}
	require.NoError(t, g.LoadManifest(m))
}
