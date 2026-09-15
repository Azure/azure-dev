// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

type showRecordingReader struct {
	voice, standard           int
	name, version, apiVersion string
	digitalWorker             bool
	err                       error
}

func (r *showRecordingReader) GetAgentVersion(
	_ context.Context, name, version, apiVersion string, digitalWorker bool,
) (*agent_api.AgentVersionObject, error) {
	r.standard++
	r.name, r.version, r.apiVersion, r.digitalWorker = name, version, apiVersion, digitalWorker
	return &agent_api.AgentVersionObject{Name: name, Version: version, Status: "active"}, r.err
}

func (r *showRecordingReader) GetVoiceAgentVersion(
	_ context.Context, name, version, apiVersion string,
) (*agent_api.AgentVersionObject, error) {
	r.voice++
	r.name, r.version, r.apiVersion = name, version, apiVersion
	return &agent_api.AgentVersionObject{Name: name, Version: version, Status: "active"}, r.err
}

func TestShowVoiceServiceRouting(t *testing.T) {
	for _, tt := range []struct {
		name           string
		fields         map[string]any
		override       string
		voice, invalid bool
	}{
		{"managed", map[string]any{"kind": "voice", "modelType": "managed"}, "", true, false},
		{"byom-alias", map[string]any{"kind": "prompt-voice", "modelType": "self_deployed"}, "", true, false},
		{"wrapper", map[string]any{"kind": "voice", "conversationEngine": map[string]any{
			"type": "hosted_agent", "name": "target",
		}}, "", true, false},
		{"wrapper-alias", map[string]any{"kind": "prompt-voice", "conversationEngine": map[string]any{
			"type": "hosted_agent", "name": "target",
		}}, "", true, false},
		{"hosted-target", map[string]any{"kind": "hosted", "metadata": map[string]any{
			"voiceLiveCompatible": "true",
		}}, "", false, false},
		{"prompt", map[string]any{"kind": "prompt"}, "", false, false},
		{"unspecified", map[string]any{}, "", false, false},
		{"voice-override", map[string]any{"kind": "hosted"}, "kind: voice\n", true, false},
		{"hosted-override", map[string]any{"kind": "voice"}, "kind: hosted\n", false, false},
		{"malformed-override", map[string]any{"kind": "voice"}, "kind: [\n", false, true},
		{"missing-override", map[string]any{"kind": "voice"}, "MISSING", false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AGENT_DEFINITION_PATH", "")
			if tt.override != "" {
				path := filepath.Join(root, "override.yaml")
				if tt.override != "MISSING" {
					require.NoError(t, os.WriteFile(path, []byte(tt.override), 0600))
				}
				t.Setenv("AGENT_DEFINITION_PATH", path)
			}
			props, err := structpb.NewStruct(tt.fields)
			require.NoError(t, err)
			project := &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: root, Services: map[string]*azdext.ServiceConfig{
					"voice-named-service": {Name: "voice-named-service", Host: AiAgentHost, AdditionalProperties: props},
				},
			}}
			env := &testEnvironmentServiceServer{
				current: &azdext.Environment{Name: "test"},
				values: map[string]map[string]string{"test": {
					"AGENT_VOICE_NAMED_SERVICE_NAME": "deployed", "AGENT_VOICE_NAMED_SERVICE_VERSION": "7",
				}},
			}
			client := newHelpersTestAzdClient(t, project, &helpersPromptServer{}, env)
			info, err := resolveAgentServiceFromProject(t.Context(), client, "voice-named-service", true, withVoiceKind())
			if tt.invalid {
				require.ErrorContains(t, err, "determining agent kind")
				require.Zero(t, env.getCurrentCalls)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.voice, info.IsVoice)
			action := &ShowAction{AgentContext: &AgentContext{Name: info.AgentName, Version: info.Version}, isVoice: info.IsVoice}
			reader := &showRecordingReader{}
			version, err := action.getVersion(t.Context(), reader)
			require.NoError(t, err)
			require.Equal(t, "active", version.Status)
			require.Equal(t, "deployed", reader.name)
			require.Equal(t, "7", reader.version)
			require.Equal(t, DefaultAgentAPIVersion, reader.apiVersion)
			require.Equal(t, tt.voice, reader.voice == 1)
			require.Equal(t, !tt.voice, reader.standard == 1)
			require.False(t, reader.digitalWorker)

			// Other shared consumers do not acquire a new classification dependency.
			plain, err := resolveAgentServiceFromProject(t.Context(), client, "voice-named-service", true)
			require.NoError(t, err)
			require.False(t, plain.IsVoice)
		})
	}
}

func TestShowVoiceReadErrorPreserved(t *testing.T) {
	t.Parallel()
	want := errors.New("service read failed")
	for _, voice := range []bool{false, true} {
		action := &ShowAction{AgentContext: &AgentContext{Name: "agent", Version: "1"}, isVoice: voice}
		_, err := action.getVersion(t.Context(), &showRecordingReader{err: want})
		require.ErrorIs(t, err, want)
	}
}
