// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// projectService answers the one call remoteAgentName makes. newTestAzdClient
// serves only the environment service, so a generate test needs its own.
type projectService struct {
	azdext.UnimplementedProjectServiceServer
	proj      *azdext.ProjectConfig
	mu        sync.Mutex
	getErrors []error
}

func (s *projectService) Get(
	_ context.Context, _ *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.getErrors) > 0 {
		err := s.getErrors[0]
		s.getErrors = s.getErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	return &azdext.GetProjectResponse{Project: s.proj}, nil
}

func projectServingClient(t *testing.T, proj *azdext.ProjectConfig, getErrors ...error) *azdext.AzdClient {
	t.Helper()

	server := grpc.NewServer()
	azdext.RegisterProjectServiceServer(server, &projectService{proj: proj, getErrors: getErrors})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	return client
}

// publishingProject declares one agent service under a local key that publishes
// under a different name -- the case RemoteAgentName exists to handle.
func publishingProject(t *testing.T, key, published string) *azdext.ProjectConfig {
	t.Helper()
	return &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
		key: {
			Name:                 key,
			Host:                 project.AgentHost,
			AdditionalProperties: mustStruct(t, map[string]any{"name": published}),
		},
	}}
}

// capturingGenerationServer records the job request body generate submits.
func capturingGenerationServer(t *testing.T, submitted *[]byte) *evalContext {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body := new(bytes.Buffer)
			_, _ = body.ReadFrom(r.Body)
			*submitted = body.Bytes()
		}
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"id": "job_1", "status": "running",
		}))
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})

	return &evalContext{evalClient: eval_api.NewEvalClientFromPipeline(srv.URL, pipeline)}
}

// agentOnlyPlan seeds generation from the agent alone. It carries no
// instruction on purpose: an instruction adds a prompt source, and a plan that
// has one has its agent source stripped under --no-wait, which would hide the
// name these tests are about.
func agentOnlyPlan(agent string) generationPlan {
	return generationPlan{
		Name:       "golden",
		Model:      "gpt-4.1-nano",
		Agent:      agent,
		From:       []string{"agent"},
		SampleSize: 10,
	}
}

// `init` writes the azure.yaml service key as the eval target. The agent is
// published under whatever the service declares, so generation seeded with the
// unresolved key is attributed to an agent the service does not know -- the run
// path has always resolved it, and generate did not. ADO 5631288.
func TestGenerateDataset_SendsThePublishedAgentNameNotTheServiceKey(t *testing.T) {
	var submitted []byte
	ec := capturingGenerationServer(t, &submitted)
	ec.azdClient = projectServingClient(t, publishingProject(t, "support", "hero-agent"))

	var out bytes.Buffer
	var report generationReport
	_, err := ec.generateDataset(t.Context(), agentOnlyPlan("support"), &out, true, &report, refuseRetry)

	require.NoError(t, err)
	require.NotEmpty(t, submitted, "the job has to reach the service")

	assert.Contains(t, string(submitted), "hero-agent",
		"the published name is what the service knows the agent by")
	assert.NotContains(t, string(submitted), `"support"`,
		"the azure.yaml service key is a local label and must not be billed as an agent")
}

func TestGenerateRubric_SendsThePublishedAgentNameNotTheServiceKey(t *testing.T) {
	var submitted []byte
	ec := capturingGenerationServer(t, &submitted)
	ec.azdClient = projectServingClient(t, publishingProject(t, "support", "hero-agent"))

	var out bytes.Buffer
	var report generationReport
	_, err := ec.generateRubric(t.Context(), agentOnlyPlan("support"), &out, true, &report)

	require.NoError(t, err)
	require.NotEmpty(t, submitted, "the job has to reach the service")

	assert.Contains(t, string(submitted), "hero-agent",
		"a rubric seeded from the wrong agent grades against the wrong behavior")
	assert.NotContains(t, string(submitted), `"support"`)
}

// A key that publishes under itself is the ordinary case, and resolving it must
// not rewrite it into something else.
func TestGenerateDataset_KeyThatPublishesUnderItselfIsUnchanged(t *testing.T) {
	var submitted []byte
	ec := capturingGenerationServer(t, &submitted)
	ec.azdClient = projectServingClient(t, &azdext.ProjectConfig{
		Services: map[string]*azdext.ServiceConfig{
			"support-agent": {Name: "support-agent", Host: project.AgentHost},
		},
	})

	var out bytes.Buffer
	var report generationReport
	_, err := ec.generateDataset(t.Context(), agentOnlyPlan("support-agent"), &out, true, &report, refuseRetry)

	require.NoError(t, err)
	assert.Contains(t, string(submitted), "support-agent")
}

// Every atomic command runs outside azd too, where there is no azure.yaml to
// consult. The configuration is entitled to name a remote agent no local
// service declares, so an unreadable project leaves the name as written rather
// than failing generation.
func TestGenerateDataset_WithoutAProjectSendsTheNameAsWritten(t *testing.T) {
	var submitted []byte
	ec := capturingGenerationServer(t, &submitted)

	var out bytes.Buffer
	var report generationReport
	_, err := ec.generateDataset(t.Context(), agentOnlyPlan("deployed-elsewhere"), &out, true, &report, refuseRetry)

	require.NoError(t, err)
	assert.Contains(t, string(submitted), "deployed-elsewhere")
}

// Two services claiming one agent is a question about which agent to bill, and
// it has no answer here. Refusing before the POST is what keeps a guess from
// being charged to the wrong one.
func TestGenerateDataset_RefusesWhenTwoServicesClaimTheSameAgent(t *testing.T) {
	var submitted []byte
	ec := capturingGenerationServer(t, &submitted)
	ec.azdClient = projectServingClient(t, &azdext.ProjectConfig{
		Services: map[string]*azdext.ServiceConfig{
			"first": {
				Name:                 "first",
				Host:                 project.AgentHost,
				AdditionalProperties: mustStruct(t, map[string]any{"name": "hero-agent"}),
			},
			"second": {
				Name:                 "second",
				Host:                 project.AgentHost,
				AdditionalProperties: mustStruct(t, map[string]any{"name": "hero-agent"}),
			},
		},
	})

	var out bytes.Buffer
	var report generationReport
	_, err := ec.generateDataset(t.Context(), agentOnlyPlan("hero-agent"), &out, true, &report, refuseRetry)

	require.Error(t, err)
	assert.Empty(t, submitted, "an ambiguous target must not reach the service at all")
}

func TestGenerationDefaultNamesUseCanonicalDeployedAgent(t *testing.T) {
	const local, deployed = "travel-planner", "aprilk-b43-hands-on-travel"
	for _, target := range []string{"", local, deployed} {
		for _, unattended := range []bool{false, true} {
			for _, level := range []string{"turn", "conversation"} {
				t.Run(target+"/"+boolText(unattended)+"/"+level, func(t *testing.T) {
					ec := &evalContext{azdClient: projectServingClient(t, publishingProject(t, local, deployed))}
					cmd := generateCmd(t, unattended)
					cmd.SetContext(t.Context())
					selected := target
					var err error
					if selected == "" {
						selected, err = ec.detectAgentTarget(cmd)
						require.NoError(t, err)
					}
					prefix, err := ec.generationNameTarget(t.Context(), selected)
					require.NoError(t, err)
					assert.Equal(t, deployed, prefix)
					plans, err := buildGeneratePlans(generateRequest{
						flags:  &generateFlags{path: t.TempDir(), target: target},
						target: prefix, dataset: true, evaluator: true, evaluationLevel: level,
					})
					require.NoError(t, err)
					require.Len(t, plans, 2)
					want := deployed + "-" + level + "-tests"
					assert.Equal(t, want, plans[0].Name)
					assert.Equal(t, deployed+"-evaluator", plans[1].Name)
					assert.Equal(t, deployed, plans[0].Agent)
					assert.Equal(t, want+".jsonl", filepath.Base(project.ArtifactPath(
						plans[0].BaseDir, plans[0].OutputDir, plans[0].Name, ".jsonl")))
					var planOutput bytes.Buffer
					writeGenerationPlan(&planOutput, generationSummary{plans: plans})
					assert.Contains(t, planOutput.String(), want)
					ref := &project.ArtifactRef{
						Name: plans[0].Name, Source: "./datasets/" + plans[0].Name + ".jsonl", EvaluationLevel: level,
					}
					require.NoError(t, addDatasetToCatalog(cmd, plans[0].BaseDir, ref))
					catalog, err := project.OpenEvalConfig(plans[0].BaseDir)
					require.NoError(t, err)
					require.NotNil(t, catalog)
					decl, found := catalog.DatasetDeclaration(want)
					require.True(t, found)
					assert.Equal(t, ref.Source, decl.File)
					handoff := initHandoff([]generationOutcome{{
						plan: plans[0], ref: ref,
					}}, "")
					assert.Contains(t, handoff, "--dataset "+want)

					var submitted []byte
					writer := &bytes.Buffer{}
					backend := capturingGenerationServer(t, &submitted)
					plans[0].Instruction, plans[0].Model = "Synthetic recovered instructions.", "generation"
					plans[0].From = []string{project.GenerateFromPrompt}
					_, err = backend.generateDataset(t.Context(), plans[0], writer, true, &generationReport{}, refuseRetry)
					require.NoError(t, err)
					assert.Contains(t, string(submitted), want)
					assert.NotContains(t, string(submitted), `"name":"`+local+"-"+level+"-tests\"")
				})
			}
		}
	}
}

func TestGenerationNameLookupFailureDoesNotFallBackToLocalKey(t *testing.T) {
	const local, deployed = "travel-planner", "aprilk-b43-hands-on-travel"
	ec := &evalContext{azdClient: projectServingClient(t, publishingProject(t, local, deployed), assert.AnError)}
	name, err := ec.generationNameTarget(t.Context(), local)
	require.ErrorContains(t, err, "resolving the deployed agent name")
	assert.ErrorContains(t, err, "--dataset-name")
	assert.Empty(t, name)
	name, err = ec.generationNameTarget(t.Context(), local)
	require.NoError(t, err)
	assert.Equal(t, deployed, name)
	for _, level := range []string{"turn", "conversation"} {
		derived, err := generatedName("", name, "dataset", datasetNameSuffix(level))
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(derived, deployed+"-"))
	}
}
