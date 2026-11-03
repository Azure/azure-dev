// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"azureaiagent/internal/featureflags"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestInitProtocolPreviewCombinations(t *testing.T) {
	for _, voice := range []bool{false, true} {
		for _, a2a := range []bool{false, true} {
			t.Run(fmt.Sprintf("voice=%t/a2a=%t", voice, a2a), func(t *testing.T) {
				clearPreviewProcess(t)
				t.Setenv(featureflags.Voice.EnvVar, strconv.FormatBool(voice))
				t.Setenv(featureflags.A2A.EnvVar, strconv.FormatBool(a2a))
				text, err := executeRoot(t, "init", "--help")
				require.NoError(t, err)
				require.Contains(t, text, "Protocols supported by the agent ("+
					strings.Join(protocolNames(voice, a2a), ", ")+")")
				require.Equal(t, a2a, strings.Contains(text, "Hosted protocols:"))
				require.Equal(t, voice, strings.Contains(text, "Voice Agents\n"))

				// Construction keeps complete metadata, independently of help state.
				root := NewRootCommand()
				init, _, err := root.Find([]string{"init"})
				require.NoError(t, err)
				flag := init.Flags().Lookup("protocol")
				require.Contains(t, flag.Usage, "responses, invocations, invocations_ws, activity, a2a")
				require.Equal(t, []string{featureflags.Voice.ID}, flag.Annotations[featureAnnotation])

				client := &fakePromptClient{multiSelectFn: func(
					_ context.Context, request *azdext.MultiSelectRequest, _ ...grpc.CallOption,
				) (*azdext.MultiSelectResponse, error) {
					var names []string
					for _, choice := range request.Options.Choices {
						names = append(names, choice.Value)
						require.Equal(t, choice.Value, choice.Label)
						require.Equal(t, choice.Value == "responses", choice.Selected)
					}
					require.Equal(t, protocolNames(voice, a2a), names)
					return &azdext.MultiSelectResponse{Values: request.Options.Choices}, nil
				}}
				got, err := promptProtocols(t.Context(), client, false, nil, voice, a2a)
				require.NoError(t, err)
				want := []agent_yaml.ProtocolVersionRecord{{Protocol: "responses", Version: "2.0.0"}}
				require.Equal(t, want, got)
				got, err = selectHostedProtocols(t.Context(), nil, true, nil)
				require.NoError(t, err)
				require.Equal(t, want, got, "no-prompt default must not require any RPC or preview")

				for _, protocol := range []string{"a2a", "invocations_ws"} {
					_, err := selectHostedProtocols(t.Context(), nil, true, []string{protocol})
					enabled := a2a
					if protocol == "invocations_ws" {
						enabled = voice
					}
					if enabled {
						require.NoError(t, err)
					} else {
						require.ErrorContains(t, err, "preview feature")
					}
				}
			})
		}
	}
}

func TestInitA2AExplicitRejectedBeforeAuthAndRPC(t *testing.T) {
	for _, route := range []string{"code", "image"} {
		for _, selection := range [][]string{
			{"--protocol", "a2a"},
			{"--protocol", "responses,a2a"},
			{"--protocol", "responses", "--protocol", "a2a", "--protocol", "a2a"},
		} {
			t.Run(route+"/"+strings.Join(selection, "_"), func(t *testing.T) {
				clearPreviewProcess(t)
				// Voice enabled must not enable the independent A2A preview.
				t.Setenv(featureflags.Voice.EnvVar, "true")
				root := t.TempDir()
				t.Chdir(root)
				host := &initScopeHost{}
				host.start(t)
				auth := initScopeAuthFixture(t, false)
				args := []string{"--no-prompt", "--agent-name", "agent"}
				if route == "image" {
					args = append(args, "--image", "registry.example.com/agent:v1")
				} else {
					require.NoError(t, os.WriteFile("main.py", []byte("# existing code\n"), 0o600))
					args = append(args, "--src", root, "--deploy-mode", "container")
				}
				err := runInitScopeRoot(t, append(args, selection...)...)
				require.ErrorContains(t, err, featureflags.A2A.ID)
				require.NoFileExists(t, auth)
				require.Empty(t, host.methods)
				entries, err := os.ReadDir(root)
				require.NoError(t, err)
				if route == "image" {
					require.Empty(t, entries)
				} else {
					require.Len(t, entries, 1)
					require.Equal(t, "main.py", entries[0].Name())
				}
			})
		}
	}
}
