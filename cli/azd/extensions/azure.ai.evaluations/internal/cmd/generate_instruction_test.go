// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// generateCmd is a command carrying the flags the interactive paths read.
func generateCmd(t *testing.T, noPromptSet bool) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.Flags().Bool("no-prompt", noPromptSet, "")
	require.NoError(t, cmd.Flags().Set("no-prompt", boolText(noPromptSet)))
	return cmd
}

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// Generation seeded from nothing comes back marked input_quality: the service
// says the input was insufficient, and the rubric it produced grades whatever
// it inferred. That is a billed job, so the absence has to stop it.
//
// It used to report "not detected" and carry on, which read as a note rather
// than a problem -- and the evaluator that came back was the one April found.
func TestGenerationRefusesToRunOnNoInstructionsUnderNoPrompt(t *testing.T) {
	ec := &evalContext{}
	var out bytes.Buffer

	_, _, err := ec.resolveGenerationInstruction(
		generateCmd(t, true), "", "", "", &out, false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--agent-instruction")
	assert.Contains(t, err.Error(), "--agent-instruction-file",
		"which flag to reach for depends on whether the text is already in a file")
	assert.Contains(t, out.String(), "not detected",
		"the reader still gets told why they are being asked")
}

// An explicit instruction is the caller's own answer and skips every lookup,
// so the prompt cannot fire for someone who already supplied one.
func TestAnExplicitInstructionIsNeverSecondGuessed(t *testing.T) {
	ec := &evalContext{}
	var out bytes.Buffer

	got, source, err := ec.resolveGenerationInstruction(
		generateCmd(t, true), "grade politeness", "--agent-instruction", "", &out, false)

	require.NoError(t, err)
	assert.Equal(t, "grade politeness", got)
	assert.Equal(t, "--agent-instruction", source)
	assert.Empty(t, out.String(), "nothing to report when nothing was detected for")
}

// The typed answer is prose about what the agent does. Echoing it into the plan
// turns the confirmation into something the reader scrolls past to reach the
// two lines that say what will be billed.
func TestTypedInstructionsAreNamedRatherThanQuoted(t *testing.T) {
	assert.Equal(t, "entered interactively", messages.InstructionSourceTyped())
	assert.Equal(t, "entered interactively",
		messages.InstructionsPlanValue(messages.InstructionSourceTyped()))
}

type instructionPromptServer struct {
	azdext.UnimplementedPromptServiceServer
	mu          sync.Mutex
	choice      *int32
	answer      string
	answers     []string
	cancelAfter int
	promptErr   error
	choices     []string
	prompts     int
}

func (s *instructionPromptServer) Select(
	_ context.Context, req *azdext.SelectRequest,
) (*azdext.SelectResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, choice := range req.GetOptions().GetChoices() {
		s.choices = append(s.choices, choice.GetLabel())
	}
	return &azdext.SelectResponse{Value: s.choice}, s.promptErr
}

func (s *instructionPromptServer) Prompt(
	_ context.Context, _ *azdext.PromptRequest,
) (*azdext.PromptResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts++
	if s.cancelAfter > 0 && s.prompts > s.cancelAfter {
		return nil, status.Error(codes.Canceled, "cancelled")
	}
	if len(s.answers) > 0 {
		answer := s.answers[0]
		s.answers = s.answers[1:]
		return &azdext.PromptResponse{Value: answer}, nil
	}
	return &azdext.PromptResponse{Value: s.answer}, s.promptErr
}

func TestGenerationInteractiveInstructionSources(t *testing.T) {
	for _, choice := range []int32{0, 1} {
		t.Run(map[int32]string{0: "type", 1: "file"}[choice], func(t *testing.T) {
			t.Setenv("AZD_NO_PROMPT", "false")
			prompts := &instructionPromptServer{choice: new(choice), answer: "Only use the provided facts."}
			h := newInitHarness(t, nil, prompts)
			wantSource := messages.InstructionSourceTyped()
			if choice == 1 {
				path := filepath.Join(h.dir, "instruction files", "agent instructions.txt")
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
				require.NoError(t, os.WriteFile(path, []byte("\xef\xbb\xbfOnly use the provided facts.\r\n"), 0o600))
				prompts.answer = path
				wantSource = filepath.ToSlash(path)
			}
			before := initFileSnapshot(t, h.dir)
			var out bytes.Buffer
			cmd := generateCmd(t, false)
			cmd.SetContext(t.Context())
			got, source, err := (&evalContext{}).resolveGenerationInstruction(cmd, "", "", "", &out, false)
			require.NoError(t, err)
			assert.Equal(t, "Only use the provided facts.", got, "file bytes, never the path, seed generation")
			assert.Equal(t, wantSource, source)
			assert.NotContains(t, out.String(), got, "instruction content is not terminal output")
			var submitted []byte
			ec := capturingGenerationServer(t, &submitted)
			var report generationReport
			_, err = ec.generateRubric(t.Context(), generationPlan{
				Name: "quality", Model: "generation", Instruction: got,
				From: []string{project.GenerateFromPrompt},
			}, &out, true, &report)
			require.NoError(t, err)
			assert.Contains(t, string(submitted), "Only use the provided facts.")
			assert.NotContains(t, out.String(), got, "submission must not echo the selected instructions")
			assert.Equal(t, before, initFileSnapshot(t, h.dir))
			prompts.mu.Lock()
			defer prompts.mu.Unlock()
			assert.Equal(t, []string{"Type instructions", "Load from file"}, prompts.choices)
			assert.Equal(t, 1, prompts.prompts)
		})
	}
}

func TestGenerationInstructionSelectionFailuresDoNotWrite(t *testing.T) {
	for _, tc := range []struct {
		name   string
		choice *int32
		answer string
		err    error
		want   string
	}{
		{"missing file", new(int32(1)), "missing instructions.txt", nil, "reading"},
		{"empty file", new(int32(1)), "empty.txt", nil, "empty"},
		{"directory", new(int32(1)), ".", nil, "reading"},
		{"blank typed", new(int32(0)), " \t ", nil, "instructions"},
		{"blank path", new(int32(1)), " ", nil, "file"},
		{"unanswered", nil, "", nil, "instructions"},
		{"invalid selection", new(int32(2)), "", nil, "instructions"},
		{"cancelled", new(int32(0)), "", status.Error(codes.Canceled, "cancelled"), "cancelled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AZD_NO_PROMPT", "false")
			prompts := &instructionPromptServer{choice: tc.choice, answer: tc.answer, promptErr: tc.err}
			h := newInitHarness(t, nil, prompts)
			require.NoError(t, os.WriteFile("empty.txt", []byte("\xef\xbb\xbf \r\n"), 0o600))
			before := initFileSnapshot(t, h.dir)
			var out bytes.Buffer
			cmd := generateCmd(t, false)
			cmd.SetContext(t.Context())
			_, _, err := (&evalContext{}).resolveGenerationInstruction(cmd, "", "", "", &out, false)
			require.ErrorContains(t, err, tc.want)
			assert.Equal(t, before, initFileSnapshot(t, h.dir))
			assert.Zero(t, h.project.wiringAttempts())
		})
	}
}

func TestGenerationMissingInstructionsKeepJSONClean(t *testing.T) {
	cmd := generateCmd(t, false)
	cmd.Flags().String("output", "json", "")
	cmd.SetContext(t.Context())
	var out bytes.Buffer
	_, _, err := (&evalContext{}).resolveGenerationInstruction(cmd, "", "", "", &out, true)
	require.ErrorContains(t, err, "--agent-instruction-file")
	assert.Empty(t, out.String())
}

func TestGenerationExplicitInstructionsSkipSelection(t *testing.T) {
	for _, file := range []bool{false, true} {
		for _, unattended := range []bool{false, true} {
			t.Run(boolText(file)+"/"+boolText(unattended), func(t *testing.T) {
				t.Setenv("AZD_NO_PROMPT", "false")
				prompts := &instructionPromptServer{promptErr: status.Error(codes.Internal, "must not prompt")}
				h := newInitHarness(t, nil, prompts)
				flags := &generateFlags{}
				cmd := generateCmd(t, unattended)
				addGenerateFlags(cmd, flags)
				cmd.SetContext(t.Context())
				want := "  Keep explicit instructions exactly.  "
				flag, value := "agent-instruction", want
				source := "--agent-instruction"
				if file {
					flag = "agent-instruction-file"
					value = filepath.Join(h.dir, "instructions with spaces.txt")
					want = "Read this file, not detected context."
					require.NoError(t, os.WriteFile(value, []byte(want+"\r\n"), 0o600))
					source = filepath.ToSlash(value)
				}
				require.NoError(t, cmd.Flags().Set(flag, value))
				require.NoError(t, validateInstructionFlags(cmd, flags))
				plan, err := resolvePlan(flags, "quality", project.DefaultEvaluatorsDir)
				require.NoError(t, err)
				var out bytes.Buffer
				got, gotSource, err := (&evalContext{}).resolveGenerationInstruction(
					cmd, plan.Instruction, plan.InstructionSource, "must-not-be-looked-up", &out, false)
				require.NoError(t, err)
				assert.Equal(t, want, got)
				assert.Equal(t, source, gotSource)
				assert.Empty(t, out.String())
				prompts.mu.Lock()
				defer prompts.mu.Unlock()
				assert.Empty(t, prompts.choices)
				assert.Zero(t, prompts.prompts)
			})
		}
	}
}

func TestGenerationEmptyOrConflictingInstructionFlagsFailBeforeWrites(t *testing.T) {
	for _, args := range [][]string{
		{"--agent-instruction="}, {"--agent-instruction", " \t"},
		{"--agent-instruction-file="}, {"--agent-instruction-file", " "},
		{"--agent-instruction", "explicit", "--agent-instruction-file", "missing.txt"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h := newInitHarness(t, nil)
			before := initFileSnapshot(t, h.dir)
			root := NewRootCommand()
			root.SetContext(t.Context())
			root.SilenceErrors, root.SilenceUsage = true, true
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(append([]string{"generate", "--no-prompt", "--output", "json"}, args...))
			require.ErrorContains(t, root.Execute(), "agent-instruction")
			if len(args) == 4 {
				// Cobra rejects mutually exclusive flags before the JSON command wrapper runs.
				assert.Empty(t, out.String())
			} else {
				var doc map[string]any
				require.NoError(t, json.Unmarshal(out.Bytes(), &doc), "refusal must be one JSON document")
				assert.Contains(t, doc, "error")
			}
			assert.Equal(t, before, initFileSnapshot(t, h.dir))
			assert.Zero(t, h.project.wiringAttempts())
		})
	}
}

func TestGenerationInstructionHelpNamesBothInputRoutes(t *testing.T) {
	cmd := newGenerateCommand()
	assert.Contains(t, cmd.Long, "Type instructions or Load from file")
	assert.Contains(t, cmd.Long, "--no-prompt and --output json never ask")
	assert.Contains(t, cmd.Flags().Lookup("agent-instruction-file").Usage, "local text file")
	assert.Contains(t, messages.EnterInstructionFileHelp(), "spaces are supported")
}

func TestGenerationInstructionFileCorrectionAndNondisclosure(t *testing.T) {
	for _, absolute := range []bool{false, true} {
		t.Run(boolText(absolute), func(t *testing.T) {
			t.Setenv("AZD_NO_PROMPT", "false")
			prompts := &instructionPromptServer{choice: new(int32(1))}
			h := newInitHarness(t, nil, prompts)
			require.NoError(t, os.MkdirAll("instruction files", 0o700))
			require.NoError(t, os.WriteFile("empty.txt", []byte(" \r\n"), 0o600))
			path := filepath.Join("instruction files", "corrected.txt")
			const instructions = "Use only synthetic information from this fixture."
			require.NoError(t, os.WriteFile(path, []byte(instructions), 0o600))
			if absolute {
				path = filepath.Join(h.dir, path)
			}
			prompts.answers = []string{"missing.txt", ".", "empty.txt", path}
			before := initFileSnapshot(t, h.dir)
			var out bytes.Buffer
			cmd := generateCmd(t, false)
			cmd.SetContext(t.Context())
			cmd.SetOut(&out)
			got, source, err := (&evalContext{}).resolveGenerationInstruction(cmd, "", "", "", &out, false)
			require.NoError(t, err)
			fromFlag, err := resolveInstruction("", path)
			require.NoError(t, err)
			assert.Equal(t, fromFlag, got, "interactive and flag paths use the same resolution base and reader")
			assert.Equal(t, instructions, got)
			writeGenerationPlan(&out, generationSummary{instructed: source,
				plans: []generationPlan{{Name: "quality", Kind: generateKindEvaluator, Instruction: got}}})
			assert.Contains(t, out.String(), filepath.ToSlash(path))
			assert.Contains(t, out.String(), "Enter a corrected file path")
			assert.NotContains(t, out.String(), instructions)
			assert.Equal(t, before, initFileSnapshot(t, h.dir))
			prompts.mu.Lock()
			defer prompts.mu.Unlock()
			assert.Equal(t, 4, prompts.prompts)
			assert.Equal(t, []string{"Type instructions", "Load from file"}, prompts.choices)
		})
	}
}

func TestGenerationInstructionFileCorrectionCancellationAndBound(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(boolText(cancel), func(t *testing.T) {
			t.Setenv("AZD_NO_PROMPT", "false")
			prompts := &instructionPromptServer{choice: new(int32(1)), answer: "missing.txt"}
			if cancel {
				prompts.cancelAfter = 1
			}
			h := newInitHarness(t, nil, prompts)
			before := initFileSnapshot(t, h.dir)
			cmd := generateCmd(t, false)
			cmd.SetContext(t.Context())
			var out bytes.Buffer
			_, _, err := (&evalContext{}).resolveGenerationInstruction(cmd, "", "", "", &out, false)
			require.Error(t, err)
			prompts.mu.Lock()
			defer prompts.mu.Unlock()
			if cancel {
				assert.Equal(t, codes.Canceled, status.Code(err))
				assert.Equal(t, 2, prompts.prompts)
			} else {
				assert.ErrorContains(t, err, "missing.txt")
				assert.Equal(t, 8, prompts.prompts)
			}
			assert.Equal(t, before, initFileSnapshot(t, h.dir))
		})
	}
}
