// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type generateCancellationPrompts struct {
	azdext.UnimplementedPromptServiceServer
	mu               sync.Mutex
	source           int32
	decision         int32
	cancelCorrection bool
	file             string
	instruction      string
	filePrompts      int
	confirmations    int
}

func (s *generateCancellationPrompts) Select(
	_ context.Context, request *azdext.SelectRequest,
) (*azdext.SelectResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch request.GetOptions().GetMessage() {
	case messages.SelectInstructionSourcePrompt():
		return &azdext.SelectResponse{Value: new(s.source)}, nil
	case messages.SelectGenerateScopePrompt():
		return &azdext.SelectResponse{Value: new(int32(0))}, nil
	case messages.ConfirmGenerationPrompt():
		s.confirmations++
		return &azdext.SelectResponse{Value: new(s.decision)}, nil
	default:
		return nil, fmt.Errorf("unexpected selection: %s", request.GetOptions().GetMessage())
	}
}

func (s *generateCancellationPrompts) Prompt(
	_ context.Context, request *azdext.PromptRequest,
) (*azdext.PromptResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch request.GetOptions().GetMessage() {
	case messages.EnterAgentInstructionPrompt():
		return &azdext.PromptResponse{Value: s.instruction}, nil
	case messages.EnterInstructionFilePrompt():
		s.filePrompts++
		if s.cancelCorrection {
			if s.filePrompts == 1 {
				return &azdext.PromptResponse{Value: "missing instruction file.txt"}, nil
			}
			return nil, status.Error(codes.Canceled, "reader cancelled file correction")
		}
		return &azdext.PromptResponse{Value: s.file}, nil
	default:
		return nil, fmt.Errorf("unexpected input prompt: %s", request.GetOptions().GetMessage())
	}
}

func TestGenerateCommandCancellationPrecedesSubmissionAndWrites(t *testing.T) {
	for _, scenario := range []string{"file correction", "type then cancel", "load then cancel", "confirmed control"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("AZD_NO_PROMPT", "false")
			const instruction = "Synthetic private instruction contents must not appear in the plan."
			prompts := &generateCancellationPrompts{decision: generateCancel, instruction: instruction}
			h := newInitHarness(t, nil, prompts)
			file := filepath.Join("instruction files", "generation notes.txt")
			require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o700))
			require.NoError(t, os.WriteFile(file, []byte(instruction), 0o600))
			prompts.file = file
			switch scenario {
			case "file correction":
				prompts.source = 1
				prompts.cancelCorrection = true
			case "load then cancel":
				prompts.source = 1
			case "confirmed control":
				prompts.decision = generateProceed
			}
			location := filepath.Join("team evals", "custom.yml")
			require.NoError(t, os.MkdirAll(filepath.Dir(location), 0o700))
			require.NoError(t, os.WriteFile(location, []byte("# existing catalog\nx-owner: fixture\nevals: []\n"), 0o600))
			before := initFileSnapshot(t, h.dir)
			var datasetPosts, rubricPosts, unexpectedRequests atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/data_generation_jobs"):
					datasetPosts.Add(1)
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/evaluator_generation_jobs"):
					rubricPosts.Add(1)
				default:
					unexpectedRequests.Add(1)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{"code": "FixtureStop", "message": "recorded submission; no job created"},
				}))
			}))
			t.Cleanup(backend.Close)
			var contextCalls int
			cmd := newGenerateCommandWithContext(func(ctx context.Context, endpoint string) (*evalContext, error) {
				contextCalls++
				require.Equal(t, backend.URL, endpoint)
				require.NoError(t, ctx.Err())
				client, err := azdext.NewAzdClient()
				if err != nil {
					return nil, err
				}
				pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
					&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
				return &evalContext{
					azdClient: client, endpoint: endpoint,
					evalClient: eval_api.NewEvalClientFromPipeline(endpoint, pipeline),
				}, nil
			})
			cmd.Flags().Bool("no-prompt", false, "")
			cmd.Flags().String("output", "", "")
			cmd.SetContext(t.Context())
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{"--path", location, "--project-endpoint", backend.URL,
				"--dataset-name", "test-data", "--evaluator-name", "test-rubric",
				"--generation-model", "fixture-model", "--evaluation-level", "turn", "--from", "prompt"})
			err := cmd.Execute()
			assert.Equal(t, 1, contextCalls, "the actual command must reach generation setup")
			if scenario == "confirmed control" {
				require.ErrorContains(t, err, "recorded submission")
				assert.Equal(t, int32(1), datasetPosts.Load(), "the recorder must observe a real dataset POST")
				assert.Equal(t, int32(1), rubricPosts.Load(), "the recorder must observe a real rubric POST")
			} else {
				assert.Zero(t, datasetPosts.Load())
				assert.Zero(t, rubricPosts.Load())
				if scenario == "file correction" {
					require.Error(t, err)
					assert.Equal(t, codes.Canceled, status.Code(err))
					assert.Contains(t, out.String(), "Enter a corrected file path")
					assert.NotContains(t, out.String(), "Generation plan")
				} else {
					require.NoError(t, err)
					assert.Contains(t, out.String(), messages.GenerationCancelled())
				}
			}
			assert.Zero(t, unexpectedRequests.Load())
			assert.Equal(t, before, initFileSnapshot(t, h.dir), "no artifact, catalog, or private-state changes")
			assert.NotContains(t, out.String(), instruction)
			prompts.mu.Lock()
			defer prompts.mu.Unlock()
			if scenario == "file correction" {
				assert.Equal(t, 2, prompts.filePrompts)
				assert.Zero(t, prompts.confirmations)
			} else {
				assert.Equal(t, 1, prompts.confirmations)
				assert.Contains(t, out.String(), "Generation plan")
				if scenario == "load then cancel" {
					assert.Contains(t, out.String(), filepath.ToSlash(file))
				} else {
					assert.Contains(t, out.String(), messages.InstructionSourceTyped())
				}
			}
		})
	}
}
