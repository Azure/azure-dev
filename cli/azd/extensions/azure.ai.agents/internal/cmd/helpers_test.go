// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectStartupCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		files    []string // files to create in a temp directory
		expected string
	}{
		{
			name:     "python with pyproject.toml and main.py",
			files:    []string{"pyproject.toml", "main.py"},
			expected: "python main.py",
		},
		{
			name:     "python with pyproject.toml but no main.py",
			files:    []string{"pyproject.toml"},
			expected: "",
		},
		{
			name:     "python with requirements.txt and main.py",
			files:    []string{"requirements.txt", "main.py"},
			expected: "python main.py",
		},
		{
			name:     "python with requirements.txt but no main.py",
			files:    []string{"requirements.txt"},
			expected: "",
		},
		{
			name:     "python with main.py only",
			files:    []string{"main.py"},
			expected: "python main.py",
		},
		{
			name:     "dotnet with csproj",
			files:    []string{"MyAgent.csproj"},
			expected: "dotnet run",
		},
		{
			name:     "node with package.json",
			files:    []string{"package.json"},
			expected: "npm start",
		},
		{
			name:     "unknown project type",
			files:    []string{"README.md"},
			expected: "",
		},
		{
			name:     "empty directory",
			files:    nil,
			expected: "",
		},
		{
			name:     "pyproject.toml takes precedence over package.json",
			files:    []string{"pyproject.toml", "main.py", "package.json"},
			expected: "python main.py",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			for _, f := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(""), 0600); err != nil {
					t.Fatalf("failed to create test file %s: %v", f, err)
				}
			}

			got := detectStartupCommand(dir)
			if got != tt.expected {
				t.Errorf("detectStartupCommand() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestDetectProjectType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		files        []string
		wantLanguage string
		wantStartCmd string
	}{
		{
			name:         "python detected from pyproject.toml with main.py",
			files:        []string{"pyproject.toml", "main.py"},
			wantLanguage: "python",
			wantStartCmd: "python main.py",
		},
		{
			name:         "python detected but no start cmd without entry point",
			files:        []string{"pyproject.toml"},
			wantLanguage: "python",
			wantStartCmd: "",
		},
		{
			name:         "dotnet detected from csproj",
			files:        []string{"Agent.csproj"},
			wantLanguage: "dotnet",
			wantStartCmd: "dotnet run",
		},
		{
			name:         "node detected from package.json",
			files:        []string{"package.json"},
			wantLanguage: "node",
			wantStartCmd: "npm start",
		},
		{
			name:         "unknown when no markers",
			files:        []string{"Dockerfile"},
			wantLanguage: "unknown",
			wantStartCmd: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			for _, f := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(""), 0600); err != nil {
					t.Fatalf("failed to create test file %s: %v", f, err)
				}
			}

			pt := detectProjectType(dir)
			if pt.Language != tt.wantLanguage {
				t.Errorf("Language = %q, want %q", pt.Language, tt.wantLanguage)
			}
			if pt.StartCmd != tt.wantStartCmd {
				t.Errorf("StartCmd = %q, want %q", pt.StartCmd, tt.wantStartCmd)
			}
		})
	}
}

func TestToServiceKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "simple name", input: "myagent", want: "MYAGENT"},
		{name: "with dashes", input: "my-agent-svc", want: "MY_AGENT_SVC"},
		{name: "with spaces", input: "my agent svc", want: "MY_AGENT_SVC"},
		{name: "mixed dashes and spaces", input: "my-agent svc", want: "MY_AGENT_SVC"},
		{name: "already uppercase", input: "MY_AGENT", want: "MY_AGENT"},
		{name: "empty string", input: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := toServiceKey(tt.input)
			if got != tt.want {
				t.Errorf("toServiceKey(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCaptureResponseSession_NilClient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		sid       string
		headerVal string
	}{
		{name: "no header", sid: "", headerVal: ""},
		{name: "header present but nil client", sid: "", headerVal: "server-session-abc"},
		{name: "client sid set with header", sid: "existing-session", headerVal: "server-session-abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := &http.Response{Header: http.Header{}}
			if tt.headerVal != "" {
				resp.Header.Set("x-agent-session-id", tt.headerVal)
			}

			// Must not panic with nil azdClient.
			captureResponseSession(t.Context(), nil, "test-agent", tt.sid, resp, "Session: ")
		})
	}
}

func TestLoadSaveLocalContext(t *testing.T) {
	t.Parallel()

	t.Run("round trip", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		configPath := filepath.Join(dir, ConfigFile)

		agentCtx := &AgentLocalContext{
			AgentName: "my-agent",
			Sessions:  map[string]string{"agent1": "sess-123"},
		}

		if err := saveLocalContext(agentCtx, configPath); err != nil {
			t.Fatalf("saveLocalContext failed: %v", err)
		}

		loaded := loadLocalContext(configPath)
		if loaded.AgentName != "my-agent" {
			t.Errorf("AgentName = %q, want %q", loaded.AgentName, "my-agent")
		}
		if loaded.Sessions["agent1"] != "sess-123" {
			t.Errorf("Sessions[agent1] = %q, want %q", loaded.Sessions["agent1"], "sess-123")
		}
	})

	t.Run("missing file returns empty context", func(t *testing.T) {
		t.Parallel()

		loaded := loadLocalContext(filepath.Join(t.TempDir(), "nonexistent.json"))
		if loaded.Sessions != nil {
			t.Errorf("expected nil Sessions for missing file, got %v", loaded.Sessions)
		}
	})

	t.Run("corrupt file returns empty context", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		configPath := filepath.Join(dir, ConfigFile)
		if err := os.WriteFile(configPath, []byte("{bad json"), 0600); err != nil {
			t.Fatalf("failed to write corrupt file: %v", err)
		}

		loaded := loadLocalContext(configPath)
		if loaded.Sessions != nil {
			t.Errorf("expected nil Sessions for corrupt file, got %v", loaded.Sessions)
		}
	})
}

func TestContextMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		field string
	}{
		{name: "sessions", field: "sessions"},
		{name: "conversations", field: "conversations"},
		{name: "invocations", field: "invocations"},
		{name: "unknown", field: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			agentCtx := &AgentLocalContext{}
			m := contextMap(agentCtx, tt.field)
			if m == nil {
				t.Fatal("expected non-nil map")
			}
			m["key"] = "value"

			// For known fields, the map should be stored on the struct.
			switch tt.field {
			case "sessions":
				if agentCtx.Sessions["key"] != "value" {
					t.Error("sessions map not stored on struct")
				}
			case "conversations":
				if agentCtx.Conversations["key"] != "value" {
					t.Error("conversations map not stored on struct")
				}
			case "invocations":
				if agentCtx.Invocations["key"] != "value" {
					t.Error("invocations map not stored on struct")
				}
			case "unknown":
				// Detached map — verify it doesn't affect any struct field.
				if agentCtx.Sessions != nil || agentCtx.Conversations != nil || agentCtx.Invocations != nil {
					t.Error("unknown field should not initialize struct maps")
				}
			}
		})
	}
}
