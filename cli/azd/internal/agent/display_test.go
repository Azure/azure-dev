// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/stretchr/testify/require"
)

func TestExtractToolInputSummary(t *testing.T) {
	t.Run("NilArgs", func(t *testing.T) {
		require.Empty(t, extractToolInputSummary(nil))
	})

	t.Run("NonMap", func(t *testing.T) {
		require.Empty(t, extractToolInputSummary("string"))
	})

	t.Run("PathParam", func(t *testing.T) {
		result := extractToolInputSummary(map[string]any{"path": "/src/main.go", "content": "data"})
		require.Equal(t, "path: /src/main.go", result)
	})

	t.Run("CommandParam", func(t *testing.T) {
		result := extractToolInputSummary(map[string]any{"command": "go build ./..."})
		require.Equal(t, "command: go build ./...", result)
	})

	t.Run("PatternParam", func(t *testing.T) {
		result := extractToolInputSummary(map[string]any{"pattern": "*.go"})
		require.Equal(t, "pattern: *.go", result)
	})

	t.Run("NoMatchingKey", func(t *testing.T) {
		result := extractToolInputSummary(map[string]any{"other": "value"})
		require.Empty(t, result)
	})

	t.Run("Truncation", func(t *testing.T) {
		longPath := "/very/long/path/" + string(make([]byte, 200))
		result := extractToolInputSummary(map[string]any{"path": longPath})
		require.LessOrEqual(t, len(result), 120)
	})
}

func TestExtractIntentFromArgs(t *testing.T) {
	t.Run("Nil", func(t *testing.T) {
		require.Empty(t, extractIntentFromArgs(nil))
	})

	t.Run("IntentField", func(t *testing.T) {
		result := extractIntentFromArgs(map[string]any{"intent": "Analyzing project"})
		require.Equal(t, "Analyzing project", result)
	})

	t.Run("DescriptionField", func(t *testing.T) {
		result := extractIntentFromArgs(map[string]any{"description": "Reading files"})
		require.Equal(t, "Reading files", result)
	})

	t.Run("TextField", func(t *testing.T) {
		result := extractIntentFromArgs(map[string]any{"text": "Working on it"})
		require.Equal(t, "Working on it", result)
	})

	t.Run("NoMatch", func(t *testing.T) {
		result := extractIntentFromArgs(map[string]any{"other": "value"})
		require.Empty(t, result)
	})

	t.Run("Truncation", func(t *testing.T) {
		long := string(make([]byte, 200))
		result := extractIntentFromArgs(map[string]any{"intent": long})
		require.LessOrEqual(t, len(result), 80)
	})
}

func TestToRelativePath(t *testing.T) {
	t.Run("RelativeUnderCwd", func(t *testing.T) {
		cwd, _ := os.Getwd()
		absPath := filepath.Join(cwd, "src", "main.go")
		result := toRelativePath(absPath)
		require.Equal(t, filepath.Join("src", "main.go"), result)
	})

	t.Run("AbsoluteOutsideCwd", func(t *testing.T) {
		// Path that's definitely outside cwd and plugins
		result := toRelativePath("/some/random/path/file.go")
		require.Equal(t, "/some/random/path/file.go", result)
	})

	t.Run("AlreadyRelative", func(t *testing.T) {
		result := toRelativePath("src/main.go")
		// Should return as-is since it's already relative
		require.Equal(t, "src/main.go", result)
	})
}

func TestGetUsageMetrics(t *testing.T) {
	d := &AgentDisplay{
		idleCh: make(chan struct{}, 1),
	}

	// Simulate usage events
	d.HandleEvent(copilot.SessionEvent{
		Type: copilot.AssistantUsage,
		Data: copilot.Data{
			InputTokens:  new(float64(1000)),
			OutputTokens: new(float64(500)),
			Cost:         new(1.0),
			Duration:     new(float64(5000)),
			Model:        new("gpt-4.1"),
		},
	})

	d.HandleEvent(copilot.SessionEvent{
		Type: copilot.AssistantUsage,
		Data: copilot.Data{
			InputTokens:  new(float64(2000)),
			OutputTokens: new(float64(800)),
			Cost:         new(1.0),
			Duration:     new(float64(3000)),
		},
	})

	metrics := d.GetUsageMetrics()
	require.Equal(t, float64(3000), metrics.InputTokens)
	require.Equal(t, float64(1300), metrics.OutputTokens)
	require.Equal(t, float64(1.0), metrics.BillingRate) // last value, not sum
	require.Equal(t, float64(8000), metrics.DurationMS)
	require.Equal(t, "gpt-4.1", metrics.Model)
}

func TestExtractToolInputSummary_PrefersDescription(t *testing.T) {
	t.Run("DescriptionOverCommand", func(t *testing.T) {
		result := extractToolInputSummary(map[string]any{
			"description": "Build project",
			"command":     "go build ./...",
		})
		require.Equal(t, "Build project", result)
	})

	t.Run("IntentOverPath", func(t *testing.T) {
		result := extractToolInputSummary(map[string]any{
			"intent": "Exploring codebase",
			"path":   "/src/main.go",
		})
		require.Equal(t, "Exploring codebase", result)
	})

	t.Run("FallsBackToPath", func(t *testing.T) {
		result := extractToolInputSummary(map[string]any{
			"path": "/src/main.go",
		})
		require.Equal(t, "path: /src/main.go", result)
	})
}

func TestToolVerb(t *testing.T) {
	require.Equal(t, "Read", toolVerb("view"))
	require.Equal(t, "Edit", toolVerb("edit"))
	require.Equal(t, "Create", toolVerb("create"))
	require.Equal(t, "Search", toolVerb("grep"))
	require.Equal(t, "Find", toolVerb("glob"))
	require.Equal(t, "Fetched", toolVerb("web_fetch"))
	require.Equal(t, "Searched", toolVerb("web_search"))
	require.Equal(t, "Queried", toolVerb("sql"))
	// powershell and unknown include the tool name
	require.Contains(t, toolVerb("powershell"), "powershell")
	require.Contains(t, toolVerb("some_mcp_tool"), "some_mcp_tool")
}

func TestCountLines(t *testing.T) {
	require.Equal(t, 0, countLines(""))
	require.Equal(t, 1, countLines("hello"))
	require.Equal(t, 1, countLines("hello\n"))
	require.Equal(t, 2, countLines("hello\nworld"))
	require.Equal(t, 2, countLines("hello\nworld\n"))
	require.Equal(t, 3, countLines("a\nb\nc"))
}

func TestViewDetail(t *testing.T) {
	t.Run("PathOnly", func(t *testing.T) {
		result := viewDetail(map[string]any{"path": "src/main.go"})
		require.Contains(t, result, "main.go")
	})

	t.Run("WithRange", func(t *testing.T) {
		result := viewDetail(map[string]any{
			"path":       "src/main.go",
			"view_range": []any{float64(10), float64(20)},
		})
		require.Contains(t, result, "11 lines")
	})

	t.Run("OpenEndedRange", func(t *testing.T) {
		result := viewDetail(map[string]any{
			"path":       "src/main.go",
			"view_range": []any{float64(50), float64(-1)},
		})
		require.Contains(t, result, "from line 50")
	})

	t.Run("EmptyPath", func(t *testing.T) {
		require.Empty(t, viewDetail(map[string]any{}))
	})
}

func TestEditDetail(t *testing.T) {
	t.Run("WithDiff", func(t *testing.T) {
		result := editDetail(map[string]any{
			"path":    "src/main.go",
			"old_str": "line1\nline2\n",
			"new_str": "line1\nline2\nline3\nline4\n",
		})
		require.Contains(t, result, "main.go")
		require.Contains(t, result, "+4")
		require.Contains(t, result, "-2")
	})

	t.Run("PathOnly", func(t *testing.T) {
		result := editDetail(map[string]any{"path": "src/main.go"})
		require.Contains(t, result, "main.go")
	})

	t.Run("EmptyPath", func(t *testing.T) {
		require.Empty(t, editDetail(map[string]any{}))
	})
}

func TestCreateDetail(t *testing.T) {
	t.Run("WithContent", func(t *testing.T) {
		result := createDetail(map[string]any{
			"path":      "src/new.go",
			"file_text": "package main\n\nfunc main() {}\n",
		})
		require.Contains(t, result, "new.go")
		require.Contains(t, result, "+3")
	})

	t.Run("PathOnly", func(t *testing.T) {
		result := createDetail(map[string]any{"path": "src/new.go"})
		require.Contains(t, result, "new.go")
	})
}

func TestGrepDetail(t *testing.T) {
	t.Run("PatternOnly", func(t *testing.T) {
		result := grepDetail(map[string]any{"pattern": "TODO"})
		require.Contains(t, result, "TODO")
	})

	t.Run("PatternWithPath", func(t *testing.T) {
		result := grepDetail(map[string]any{"pattern": "TODO", "path": "src/"})
		require.Contains(t, result, "TODO")
		require.Contains(t, result, "in")
	})

	t.Run("Empty", func(t *testing.T) {
		require.Empty(t, grepDetail(map[string]any{}))
	})
}

func TestPowershellDetail(t *testing.T) {
	t.Run("DescAndCommand", func(t *testing.T) {
		detail, sub := powershellDetail(map[string]any{
			"description": "Build project",
			"command":     "go build ./...",
		})
		require.Contains(t, detail, "Build project")
		require.Contains(t, sub, "go build")
	})

	t.Run("CommandOnly", func(t *testing.T) {
		detail, sub := powershellDetail(map[string]any{
			"command": "npm install",
		})
		require.Empty(t, detail)
		require.Contains(t, sub, "npm install")
	})

	t.Run("Empty", func(t *testing.T) {
		detail, sub := powershellDetail(map[string]any{})
		require.Empty(t, detail)
		require.Empty(t, sub)
	})
}

func TestMcpToolDetail(t *testing.T) {
	t.Run("StringArgs", func(t *testing.T) {
		_, sub := mcpToolDetail("azure-deploy", map[string]any{
			"resource_group": "my-rg",
			"subscription":   "abc-123",
		})
		require.Contains(t, sub, "resource_group: my-rg")
		require.Contains(t, sub, "subscription: abc-123")
		// Should be multi-line
		require.Contains(t, sub, "\n")
	})

	t.Run("MixedTypes", func(t *testing.T) {
		_, sub := mcpToolDetail("tool", map[string]any{
			"count":   float64(42),
			"enabled": true,
			"items":   []any{"a", "b", "c"},
			"nested":  map[string]any{"key": "val"},
		})
		require.Contains(t, sub, "count: 42")
		require.Contains(t, sub, "enabled: true")
		require.Contains(t, sub, "[3 items]")
		require.Contains(t, sub, "{...}")
	})

	t.Run("EmptyArgs", func(t *testing.T) {
		detail, sub := mcpToolDetail("tool", map[string]any{})
		require.Empty(t, detail)
		require.Empty(t, sub)
	})

	t.Run("NilValues", func(t *testing.T) {
		_, sub := mcpToolDetail("tool", map[string]any{
			"name": "test",
			"skip": nil,
		})
		// nil values should be omitted
		require.NotContains(t, sub, "skip")
		require.Contains(t, sub, "name: test")
	})
}

func TestFormatArgValue(t *testing.T) {
	require.Equal(t, "hello", formatArgValue("hello"))
	require.Equal(t, "42", formatArgValue(float64(42)))
	require.Equal(t, "3.14", formatArgValue(float64(3.14)))
	require.Equal(t, "true", formatArgValue(true))
	require.Equal(t, "", formatArgValue(nil))
	require.Equal(t, "{...}", formatArgValue(map[string]any{"k": "v"}))
	require.Equal(t, "[]", formatArgValue([]any{}))
	require.Equal(t, "[2 items]", formatArgValue([]any{"a", "b"}))

	// Long strings are truncated
	long := strings.Repeat("x", 200)
	result := formatArgValue(long)
	require.LessOrEqual(t, len(result), 125) // 120 + "..." suffix
}

func TestExtractToolDetail(t *testing.T) {
	t.Run("NilArgs", func(t *testing.T) {
		d, s := extractToolDetail("view", nil)
		require.Empty(t, d)
		require.Empty(t, s)
	})

	t.Run("NonMapArgs", func(t *testing.T) {
		d, s := extractToolDetail("view", "string")
		require.Empty(t, d)
		require.Empty(t, s)
	})

	t.Run("ViewRoutes", func(t *testing.T) {
		d, s := extractToolDetail("view", map[string]any{"path": "file.go"})
		require.Contains(t, d, "file.go")
		require.Empty(t, s)
	})

	t.Run("EditRoutes", func(t *testing.T) {
		d, _ := extractToolDetail("edit", map[string]any{
			"path": "file.go", "old_str": "a", "new_str": "b\nc",
		})
		require.Contains(t, d, "file.go")
		require.Contains(t, d, "+2")
		require.Contains(t, d, "-1")
	})

	t.Run("PowershellRoutes", func(t *testing.T) {
		d, s := extractToolDetail("powershell", map[string]any{
			"description": "Run tests",
			"command":     "go test ./...",
		})
		require.Contains(t, d, "Run tests")
		require.Contains(t, s, "go test")
	})

	t.Run("UnknownFallsToMCP", func(t *testing.T) {
		_, s := extractToolDetail("azure_some_tool", map[string]any{
			"param1": "value1",
		})
		require.Contains(t, s, "param1: value1")
	})
}
