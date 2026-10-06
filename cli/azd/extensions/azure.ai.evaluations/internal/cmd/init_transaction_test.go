// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func (s *initProjectServer) setAddServiceHandler(handler func(context.Context, *azdext.AddServiceRequest) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onAddService = handler
}

func (s *initProjectServer) setSaveFailureAcknowledgement(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ackSaveError = enabled
}

// The fixture acknowledges an unsaved completion; a host without that capability
// instead retains the scaffold, as TestInitRootSaveRequiresMatchingCompletionAcknowledgement covers.
func TestInitAcknowledgedRootSaveFailureRestoresConfigAndAllowsExactRetry(t *testing.T) {
	for _, rootFilename := range []string{"azure.yaml", "azure.yml"} {
		t.Run(rootFilename, func(t *testing.T) {
			for _, filename := range []string{project.EvalConfigBase, project.LegacyEvalConfigBase, "custom quality.yml"} {
				for _, existing := range []bool{false, true} {
					for _, format := range []string{"default", "json"} {
						name := filename + "/" + format
						if existing {
							name += "/existing"
						}
						t.Run(name, func(t *testing.T) {
							h := newInitHarness(t, nil)
							h.project.setSaveFailureAcknowledgement(true)
							if rootFilename != "azure.yaml" {
								require.NoError(t, os.Rename(
									filepath.Join(h.dir, "azure.yaml"), filepath.Join(h.dir, rootFilename)))
							}
							evalDir := filepath.Join(h.dir, "quality files")
							configPath := filepath.Join(evalDir, filename)
							require.NoError(t, os.MkdirAll(evalDir, 0o700))
							ignorePath := filepath.Join(evalDir, ".gitignore")
							require.NoError(t, os.WriteFile(ignorePath, []byte("# user rules\r\nprivate/\r\n"), 0o600))
							original := []byte("# keep exact formatting\r\nx-metadata: {owner: team}\r\n" +
								"datasets:\r\n  - {name: golden, file: ./golden.jsonl}\r\n" +
								"evals:\r\n  - {name: existing, dataset: golden}\r\n")
							if existing {
								require.NoError(t, os.WriteFile(configPath, original, 0o600))
							}
							rootPath := filepath.Join(h.dir, rootFilename)
							rootBefore, err := os.ReadFile(rootPath)
							require.NoError(t, err)
							rows := []byte("{\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}," +
								"{\"role\":\"assistant\",\"content\":\"world\"}]}\n")
							require.NoError(t, os.WriteFile(h.seedRows, rows, 0o600))

							var denySave atomic.Bool
							denySave.Store(true)
							var sawScaffold atomic.Bool
							h.project.setAddServiceHandler(func(_ context.Context, request *azdext.AddServiceRequest) error {
								body, err := os.ReadFile(configPath)
								if err != nil {
									return err
								}
								sawScaffold.Store(strings.Contains(string(body), "name: retry-quality"))
								if denySave.Load() {
									return &os.PathError{Op: "save", Path: rootPath, Err: os.ErrPermission}
								}
								var root map[string]any
								if err := yaml.Unmarshal(rootBefore, &root); err != nil {
									return err
								}
								services := map[string]any{
									"agent": map[string]any{"host": "containerapp", "project": "./agent"},
									request.Service.Name: map[string]any{
										"host": request.Service.Host,
										"$ref": request.Service.AdditionalProperties.AsMap()["$ref"],
									},
								}
								root["services"] = services
								body, err = yaml.Marshal(root)
								if err != nil {
									return err
								}
								return os.WriteFile(rootPath, body, 0o600)
							})
							args := []string{"--path", configPath, "--name", "retry-quality",
								"--conversation-mode", "static",
								"--dataset", h.seedRows, "--judge-model", "judge", "--no-prompt", "--output", format}
							text, err := executeConversationInit(t, args...)
							require.ErrorContains(t, err, "permission denied")
							assert.ErrorContains(t, err, "was rolled back")
							assert.True(t, sawScaffold.Load(), "the injected failure must follow the scaffold write")
							assert.Empty(t, text)
							assert.Empty(t, h.usage.reported())
							rootAfter, err := os.ReadFile(rootPath)
							require.NoError(t, err)
							assert.Equal(t, rootBefore, rootAfter)
							if existing {
								body, err := os.ReadFile(configPath)
								require.NoError(t, err)
								assert.Equal(t, original, body)
							} else {
								assert.NoFileExists(t, configPath)
							}
							ignore, err := os.ReadFile(ignorePath)
							require.NoError(t, err)
							assert.Equal(t, "# user rules\r\nprivate/\r\n", string(ignore))
							actualRows, err := os.ReadFile(h.seedRows)
							require.NoError(t, err)
							assert.Equal(t, rows, actualRows)

							denySave.Store(false)
							text, err = executeConversationInit(t, args...)
							require.NoError(t, err, "the identical command must recover after root writeability is restored")
							assert.Equal(t, format == "json", json.Valid([]byte(text)))
							if format == "default" {
								assert.Contains(t, text, rootFilename)
							}
							authored, err := project.ReadAuthoredConfig(configPath)
							require.NoError(t, err)
							cfg := declaredSoFar(authored)
							wantEvals := 1
							if existing {
								wantEvals++
							}
							require.Len(t, cfg.Evals, wantEvals)
							assert.Equal(t, "retry-quality", cfg.Evals[len(cfg.Evals)-1].Name)
							assert.Equal(t, 2, h.project.wiringAttempts())
							assertOneInitCompleted(t, h, "dataset")
							body, err := os.ReadFile(rootPath)
							require.NoError(t, err)
							assert.Contains(t, string(body), "conversation-evals:")
							assert.Contains(t, string(body),
								"$ref: ./"+filepath.ToSlash(filepath.Join("quality files", filename)))
						})
					}
				}
			}
		})
	}
}

func TestInitRootSaveFailurePreservesConcurrentChanges(t *testing.T) {
	for _, change := range []string{"eval", "root", "eval replaced by directory", "root removed",
		"cancelled", "deadline", "unavailable"} {
		t.Run(change, func(t *testing.T) {
			h := newInitHarness(t, nil)
			if change == "cancelled" || change == "deadline" || change == "unavailable" {
				h.project.setSaveFailureAcknowledgement(false)
			}
			configPath := filepath.Join(h.dir, "quality.yml")
			rootPath := filepath.Join(h.dir, "azure.yaml")
			var mu sync.Mutex
			var changedBody []byte
			h.project.setAddServiceHandler(func(_ context.Context, _ *azdext.AddServiceRequest) error {
				mu.Lock()
				defer mu.Unlock()
				switch change {
				case "eval", "root":
					path := configPath
					if change == "root" {
						path = rootPath
					}
					body, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					changedBody = append(body, []byte("# concurrent edit\n")...)
					if err := os.WriteFile(path, changedBody, 0o600); err != nil {
						return err
					}
				case "eval replaced by directory":
					if err := os.Remove(configPath); err != nil {
						return err
					}
					if err := os.Mkdir(configPath, 0o700); err != nil {
						return err
					}
				case "root removed":
					if err := os.Remove(rootPath); err != nil {
						return err
					}
				case "cancelled":
					return status.Error(codes.Canceled, "request cancelled")
				case "deadline":
					return status.Error(codes.DeadlineExceeded, "request timed out")
				case "unavailable":
					return status.Error(codes.Unavailable, "connection lost")
				}
				return os.ErrPermission
			})
			text, err := executeConversationInit(t, "--path", configPath, "--name", "quality",
				"--source", "traces", "--target", "agent", "--judge-model", "judge", "--no-prompt", "--output", "json")
			require.Error(t, err)
			assert.Empty(t, text)
			assert.Empty(t, h.usage.reported())
			if change == "cancelled" || change == "deadline" || change == "unavailable" {
				assert.ErrorContains(t, err, "host may still finish")
				assert.ErrorContains(t, err, "could not safely roll back")
				assert.FileExists(t, configPath)
				return
			}
			assert.ErrorContains(t, err, "permission denied")
			assert.ErrorContains(t, err, "could not safely roll back")
			assert.ErrorContains(t, err, "Inspect")
			switch change {
			case "eval", "root":
				path := configPath
				if change == "root" {
					path = rootPath
				}
				body, err := os.ReadFile(path)
				require.NoError(t, err)
				mu.Lock()
				assert.Equal(t, changedBody, body)
				mu.Unlock()
				assert.FileExists(t, configPath)
			case "eval replaced by directory":
				assert.DirExists(t, configPath)
			case "root removed":
				assert.NoFileExists(t, rootPath)
				assert.FileExists(t, configPath)
			}
		})
	}
}

func TestInitCancelledRootSaveCanFinishWithoutLosingScaffold(t *testing.T) {
	h := newInitHarness(t, nil)
	configPath := filepath.Join(h.dir, "quality.yml")
	rootPath := filepath.Join(h.dir, "azure.yaml")
	started, finish := make(chan struct{}), make(chan struct{})
	saved := make(chan error, 1)
	var finishOnce sync.Once
	release := func() { finishOnce.Do(func() { close(finish) }) }
	t.Cleanup(release)
	h.project.setAddServiceHandler(func(_ context.Context, request *azdext.AddServiceRequest) error {
		close(started)
		<-finish
		body := []byte(usageAzureYaml + "  " + request.Service.Name + ":\n    host: " + project.EvalHost +
			"\n    $ref: ./quality.yml\n")
		err := os.WriteFile(rootPath, body, 0o600)
		saved <- err
		return err
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := newInitCommand()
	cmd.Flags().Bool("no-prompt", false, "")
	cmd.Flags().String("output", "", "")
	cmd.SetContext(ctx)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--path", configPath, "--name", "quality", "--source", "traces",
		"--target", "agent", "--judge-model", "judge", "--no-prompt", "--output", "json"})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("init never reached the root save")
	}
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
		assert.ErrorContains(t, err, "host may still finish")
	case <-time.After(10 * time.Second):
		t.Fatal("init did not return after cancellation")
	}
	assert.Empty(t, out.String())
	assert.Empty(t, h.usage.reported())
	assert.FileExists(t, configPath, "a still-running host save must not lose its referenced configuration")
	release()
	select {
	case err := <-saved:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the delayed root save did not finish")
	}
	root, err := os.ReadFile(rootPath)
	require.NoError(t, err)
	assert.Contains(t, string(root), "$ref: ./quality.yml")
	assert.FileExists(t, configPath)
}

// The missing/Unknown case models a host returning a save error without acknowledgment:
// init must retain the scaffold even when the root file was not changed.
func TestInitRootSaveRequiresMatchingCompletionAcknowledgement(t *testing.T) {
	for _, ack := range []string{"missing", "wrong", "duplicate", "content-type only"} {
		for _, code := range []codes.Code{codes.Unknown, codes.Internal, codes.PermissionDenied} {
			t.Run(ack+"/"+code.String(), func(t *testing.T) {
				h := newInitHarness(t, nil)
				h.project.setSaveFailureAcknowledgement(false)
				configPath := filepath.Join(h.dir, "quality.yml")
				h.project.setAddServiceHandler(func(ctx context.Context, _ *azdext.AddServiceRequest) error {
					incoming, _ := metadata.FromIncomingContext(ctx)
					tokens := incoming.Get("azd-project-add-service-operation")
					if len(tokens) != 1 || tokens[0] == "" {
						return status.Error(codes.Internal, "missing operation token")
					}
					var trailer metadata.MD
					switch ack {
					case "wrong":
						trailer = metadata.Pairs("azd-project-add-service-save-failed", "another-operation")
					case "duplicate":
						trailer = metadata.Pairs("azd-project-add-service-save-failed", tokens[0],
							"azd-project-add-service-save-failed", tokens[0])
					case "content-type only":
						trailer = metadata.Pairs("content-type", "application/grpc")
					}
					if err := grpc.SetTrailer(ctx, trailer); err != nil {
						return err
					}
					return status.Error(code, "save outcome unavailable")
				})
				text, err := executeConversationInit(t, "--path", configPath, "--name", "quality",
					"--source", "traces", "--target", "agent", "--judge-model", "judge", "--no-prompt", "-o", "json")
				require.ErrorContains(t, err, "could not safely roll back")
				assert.ErrorContains(t, err, "host may still finish")
				assert.Empty(t, text)
				assert.FileExists(t, configPath)
				assert.Empty(t, h.usage.reported())
			})
		}
	}
}

func TestInitRootSaveDoesNotReuseAcknowledgementAcrossRetries(t *testing.T) {
	h := newInitHarness(t, nil)
	h.project.setSaveFailureAcknowledgement(false)
	configPath := filepath.Join(h.dir, "quality.yml")
	var mu sync.Mutex
	var tokens []string
	h.project.setAddServiceHandler(func(ctx context.Context, _ *azdext.AddServiceRequest) error {
		incoming, _ := metadata.FromIncomingContext(ctx)
		current := incoming.Get("azd-project-add-service-operation")
		if len(current) != 1 {
			return status.Error(codes.Internal, "missing operation token")
		}
		mu.Lock()
		defer mu.Unlock()
		tokens = append(tokens, current[0])
		if err := grpc.SetTrailer(ctx, metadata.Pairs("azd-project-add-service-save-failed", tokens[0])); err != nil {
			return err
		}
		return os.ErrPermission
	})
	args := []string{"--path", configPath, "--name", "quality", "--source", "traces",
		"--target", "agent", "--judge-model", "judge", "--no-prompt", "-o", "json"}
	_, err := executeConversationInit(t, args...)
	require.ErrorContains(t, err, "was rolled back")
	assert.NoFileExists(t, configPath)
	_, err = executeConversationInit(t, args...)
	require.ErrorContains(t, err, "could not safely roll back")
	assert.FileExists(t, configPath)
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, tokens, 2)
	assert.NotEqual(t, tokens[0], tokens[1], "every invocation must use a fresh operation token")
}

type malformedSaveResponseCodec struct{}

func (malformedSaveResponseCodec) Name() string { return "proto" }

func (malformedSaveResponseCodec) Marshal(value any) ([]byte, error) {
	if _, ok := value.(*azdext.EmptyResponse); ok {
		return []byte{0x0e}, nil // Invalid protobuf wire type after the handler has completed.
	}
	message, ok := value.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("not a protobuf message: %T", value)
	}
	return proto.Marshal(message)
}

func (malformedSaveResponseCodec) Unmarshal(body []byte, value any) error {
	message, ok := value.(proto.Message)
	if !ok {
		return fmt.Errorf("not a protobuf message: %T", value)
	}
	return proto.Unmarshal(body, message)
}

func TestInitMalformedRootSaveResponseRetainsScaffold(t *testing.T) {
	h := newInitHarnessWithOptions(t, nil, []grpc.ServerOption{grpc.ForceServerCodec(malformedSaveResponseCodec{})})
	configPath := filepath.Join(h.dir, "quality.yml")
	rootPath := filepath.Join(h.dir, "azure.yaml")
	h.project.setAddServiceHandler(func(_ context.Context, request *azdext.AddServiceRequest) error {
		body := []byte(usageAzureYaml + "  " + request.Service.Name +
			":\n    host: azure.ai.eval\n    $ref: ./quality.yml\n")
		return os.WriteFile(rootPath, body, 0o600)
	})
	text, err := executeConversationInit(t, "--path", configPath, "--name", "quality", "--source", "traces",
		"--target", "agent", "--judge-model", "judge", "--no-prompt", "-o", "json")
	require.ErrorContains(t, err, "could not safely roll back")
	assert.ErrorContains(t, err, "Internal")
	assert.Empty(t, text)
	assert.FileExists(t, configPath)
	root, err := os.ReadFile(rootPath)
	require.NoError(t, err)
	assert.Contains(t, string(root), "$ref: ./quality.yml")
	assert.Empty(t, h.usage.reported())
}

func TestInitAcknowledgedPreSaveRejectionRollsBack(t *testing.T) {
	for _, rejection := range []struct {
		name string
		err  error
	}{
		{"layered", status.Error(codes.Unimplemented, "adding services to layered projects is not supported")},
		{"reload", status.Error(codes.Unknown, "cannot reload project configuration")},
		{"mapper", status.Error(codes.Unknown, "failed converting service configuration")},
	} {
		t.Run(rejection.name, func(t *testing.T) {
			h := newInitHarness(t, rejection.err)
			path := filepath.Join(h.dir, "quality.yml")
			before, err := os.ReadFile(filepath.Join(h.dir, "azure.yaml"))
			require.NoError(t, err)
			text, err := executeConversationInit(t, "--path", path, "--name", "quality",
				"--source", "traces", "--target", "agent", "--judge-model", "judge", "--no-prompt", "-o", "json")
			require.ErrorContains(t, err, "was rolled back")
			assert.ErrorContains(t, err, rejection.err.Error())
			assert.Empty(t, text)
			assert.NoFileExists(t, path)
			after, err := os.ReadFile(filepath.Join(h.dir, "azure.yaml"))
			require.NoError(t, err)
			assert.Equal(t, before, after)
			assert.Equal(t, 1, h.project.wiringAttempts())
		})
	}
}

func TestInitRetainsScaffoldWithoutStableRootSnapshot(t *testing.T) {
	for _, rootState := range []string{"missing", "yml replaced by yaml"} {
		t.Run(rootState, func(t *testing.T) {
			h := newInitHarness(t, nil)
			rootYaml := filepath.Join(h.dir, "azure.yaml")
			if rootState == "missing" {
				require.NoError(t, os.Remove(rootYaml))
			} else {
				require.NoError(t, os.Rename(rootYaml, filepath.Join(h.dir, "azure.yml")))
			}
			h.project.setAddServiceHandler(func(context.Context, *azdext.AddServiceRequest) error {
				if rootState != "missing" {
					body := []byte("# another root appeared\nname: concurrent\n")
					if err := os.WriteFile(rootYaml, body, 0o600); err != nil {
						return err
					}
				}
				return os.ErrPermission
			})
			path := filepath.Join(h.dir, "quality.yml")
			text, err := executeConversationInit(t, "--path", path, "--name", "quality",
				"--source", "traces", "--target", "agent", "--judge-model", "judge", "--no-prompt", "-o", "json")
			require.ErrorContains(t, err, "could not safely roll back")
			assert.ErrorContains(t, err, "path was unavailable or changed")
			assert.Empty(t, text)
			assert.FileExists(t, path)
			assert.Equal(t, 1, h.project.wiringAttempts())
		})
	}
}

func TestInitRejectsRootSelectionDriftAfterConfirmation(t *testing.T) {
	for _, change := range []string{"preferred yaml appears", "root disappears", "missing root appears"} {
		t.Run(change, func(t *testing.T) {
			t.Setenv("AZD_NO_PROMPT", "false")
			prompts := &conversationPromptServer{}
			h := newInitHarness(t, nil, prompts)
			yamlPath := filepath.Join(h.dir, "azure.yaml")
			ymlPath := filepath.Join(h.dir, "azure.yml")
			if change == "missing root appears" {
				require.NoError(t, os.Remove(yamlPath))
			} else {
				require.NoError(t, os.Rename(yamlPath, ymlPath))
			}
			afterChange := make(chan map[string]string, 1)
			prompts.onConfirm = func() error {
				if change == "root disappears" {
					if err := os.Remove(ymlPath); err != nil {
						return err
					}
				} else {
					if err := os.WriteFile(yamlPath, []byte("name: another-project\n"), 0o600); err != nil {
						return err
					}
				}
				select {
				case afterChange <- initFileSnapshot(t, h.dir):
				default:
					return fmt.Errorf("unexpected repeated confirmation")
				}
				return nil
			}
			text, err := executeConversationInit(t, "--name", "root-drift", "--path", "quality",
				"--source", "traces", "--target", "agent", "--judge-model", "judge",
				"--evaluation-level", "turn", "--trace-days", "7")
			require.ErrorContains(t, err, "root project configuration changed")
			assert.Zero(t, h.project.wiringAttempts(), "do not wire a host whose cached root differs from selection")
			assert.Empty(t, h.usage.reported())
			assert.Equal(t, <-afterChange, initFileSnapshot(t, h.dir), "only the concurrent root change may survive")
			assert.NotContains(t, text, "Next:")
		})
	}
}
