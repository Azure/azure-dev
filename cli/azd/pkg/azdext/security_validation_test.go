// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ValidateServiceName
// ---------------------------------------------------------------------------

func TestValidateServiceName_Valid(t *testing.T) {
	valid := []string{
		"web",
		"my-service",
		"my_service",
		"my.service",
		"A1",
		"a",
		"service-v2.0",
		"a23456789012345678901234567890123456789012345678901234567890123", // 63 chars
	}
	for _, name := range valid {
		if err := ValidateServiceName(name); err != nil {
			t.Errorf("ValidateServiceName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidateServiceName_Invalid(t *testing.T) {
	tests := []struct {
		name string
		rule string
	}{
		{"", "required"},
		{"-starts-with-dash", "format"},
		{".starts-with-dot", "format"},
		{"_starts-with-underscore", "format"},
		{"has space", "format"},
		{"has;semicolon", "format"},
		{"has|pipe", "format"},
		{"has$dollar", "format"},
		{"has/slash", "format"},
		{"has@at", "format"},
		// 64 chars (too long)
		{"a234567890123456789012345678901234567890123456789012345678901234", "format"},
	}
	for _, tc := range tests {
		err := ValidateServiceName(tc.name)
		if err == nil {
			t.Errorf("ValidateServiceName(%q) = nil, want error", tc.name)
			continue
		}
		var ve *ValidationError
		if ok := isValidationError(err, &ve); !ok {
			t.Errorf("ValidateServiceName(%q) returned %T, want *ValidationError", tc.name, err)
			continue
		}
		if ve.Rule != tc.rule {
			t.Errorf("ValidateServiceName(%q).Rule = %q, want %q", tc.name, ve.Rule, tc.rule)
		}
		if ve.Field != "service_name" {
			t.Errorf("ValidateServiceName(%q).Field = %q, want %q", tc.name, ve.Field, "service_name")
		}
	}
}

// ---------------------------------------------------------------------------
// ValidateHostname
// ---------------------------------------------------------------------------

func TestValidateHostname_Valid(t *testing.T) {
	valid := []string{
		"example.com",
		"sub.example.com",
		"a.b.c.d.example.com",
		"my-host",
		"a",
		"1",
		"192-168-1-1.nip.io",
		"xn--nxasmq6b.example.com", // punycode
	}
	for _, h := range valid {
		if err := ValidateHostname(h); err != nil {
			t.Errorf("ValidateHostname(%q) = %v, want nil", h, err)
		}
	}
}

func TestValidateHostname_Invalid(t *testing.T) {
	tests := []struct {
		hostname string
		rule     string
	}{
		{"", "required"},
		{"-starts-with-dash.com", "format"},
		{"ends-with-dash-.com", "format"},
		{"has space.com", "format"},
		{"has_underscore.com", "format"},
		{"has..double-dot.com", "format"},
		{".starts-with-dot.com", "format"},
		{"has;semicolon.com", "format"},
		// 254 chars (too long)
		{strings.Repeat("a", 254), "length"},
	}
	for _, tc := range tests {
		err := ValidateHostname(tc.hostname)
		if err == nil {
			t.Errorf("ValidateHostname(%q) = nil, want error", tc.hostname)
			continue
		}
		var ve *ValidationError
		if ok := isValidationError(err, &ve); !ok {
			t.Errorf("ValidateHostname(%q) returned %T, want *ValidationError", tc.hostname, err)
			continue
		}
		if ve.Rule != tc.rule {
			t.Errorf("ValidateHostname(%q).Rule = %q, want %q", tc.hostname, ve.Rule, tc.rule)
		}
	}
}

func TestValidateHostname_LabelLength(t *testing.T) {
	// Each label max 63 chars. A 64-char label should fail.
	longLabel := strings.Repeat("a", 64) + ".com"
	if err := ValidateHostname(longLabel); err == nil {
		t.Errorf("ValidateHostname with 64-char label = nil, want error")
	}

	// 63-char label should succeed.
	okLabel := strings.Repeat("a", 63) + ".com"
	if err := ValidateHostname(okLabel); err != nil {
		t.Errorf("ValidateHostname with 63-char label = %v, want nil", err)
	}
}

// ---------------------------------------------------------------------------
// ValidateScriptName
// ---------------------------------------------------------------------------

func TestValidateScriptName_Valid(t *testing.T) {
	valid := []string{
		"script.sh",
		"my-script.py",
		"build_project.ps1",
		"run",
		"deploy-v2.sh",
		"test.cmd",
		"start server", // spaces are OK in script names (not metacharacters)
	}
	for _, name := range valid {
		if err := ValidateScriptName(name); err != nil {
			t.Errorf("ValidateScriptName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidateScriptName_ShellMetacharacters(t *testing.T) {
	dangerous := []struct {
		name string
		desc string
	}{
		{"script;rm -rf /", "semicolon command chaining"},
		{"script|cat /etc/passwd", "pipe"},
		{"script&background", "ampersand"},
		{"script`whoami`", "backtick command substitution"},
		{"script$(id)", "dollar-paren command substitution"},
		{"script > /dev/null", "output redirect"},
		{"script < /etc/passwd", "input redirect"},
		{"script\nrm -rf /", "newline injection"},
		{"script\x00null", "null byte"},
		{"script'quoted'", "single quote"},
		{"script\"quoted\"", "double quote"},
		{"script\\escaped", "backslash"},
		{"script!history", "exclamation/history expansion"},
		{"script#comment", "hash/comment"},
		{"script~home", "tilde expansion"},
		{"script*glob", "glob star"},
		{"script?glob", "glob question"},
		{"script%env", "percent"},
		{"script(sub)", "open paren"},
		{"script{brace}", "open brace"},
		{"script[bracket]", "open bracket"},
	}
	for _, tc := range dangerous {
		err := ValidateScriptName(tc.name)
		if err == nil {
			t.Errorf("ValidateScriptName(%q) = nil, want error (%s)", tc.name, tc.desc)
			continue
		}
		var ve *ValidationError
		if ok := isValidationError(err, &ve); !ok {
			t.Errorf("ValidateScriptName(%q) returned %T, want *ValidationError", tc.name, err)
			continue
		}
		if ve.Rule != "characters" {
			t.Errorf("ValidateScriptName(%q).Rule = %q, want %q (%s)", tc.name, ve.Rule, "characters", tc.desc)
		}
	}
}

func TestValidateScriptName_PathTraversal(t *testing.T) {
	traversal := []string{
		"../etc/passwd",
		"../../secret.sh",
		"dir/../../../root.sh",
		"..\\windows\\system32",
	}
	for _, name := range traversal {
		err := ValidateScriptName(name)
		if err == nil {
			t.Errorf("ValidateScriptName(%q) = nil, want error", name)
			continue
		}
		var ve *ValidationError
		if ok := isValidationError(err, &ve); !ok {
			t.Errorf("ValidateScriptName(%q) returned %T, want *ValidationError", name, err)
			continue
		}
		if ve.Rule != "traversal" {
			t.Errorf("ValidateScriptName(%q).Rule = %q, want %q", name, ve.Rule, "traversal")
		}
	}
}

func TestValidateScriptName_Empty(t *testing.T) {
	err := ValidateScriptName("")
	if err == nil {
		t.Error("ValidateScriptName(\"\") = nil, want error")
		return
	}
	var ve *ValidationError
	if ok := isValidationError(err, &ve); !ok {
		t.Errorf("ValidateScriptName(\"\") returned %T, want *ValidationError", err)
		return
	}
	if ve.Rule != "required" {
		t.Errorf("ValidateScriptName(\"\").Rule = %q, want %q", ve.Rule, "required")
	}
}

// ---------------------------------------------------------------------------
// IsContainerEnvironment / ContainerRuntime
// ---------------------------------------------------------------------------

func TestIsContainerEnvironment_EnvVars(t *testing.T) {
	tests := []struct {
		envKey  string
		runtime string
	}{
		{"CODESPACES", "codespaces"},
		{"KUBERNETES_SERVICE_HOST", "kubernetes"},
		{"REMOTE_CONTAINERS", "devcontainer"},
		{"REMOTE_CONTAINERS_IPC", "devcontainer"},
	}
	for _, tc := range tests {
		t.Run(tc.envKey, func(t *testing.T) {
			// Ensure the env var is clean before/after.
			orig := os.Getenv(tc.envKey)
			t.Setenv(tc.envKey, "true")

			if !IsContainerEnvironment() {
				t.Errorf("IsContainerEnvironment() = false with %s set", tc.envKey)
			}

			rt := ContainerRuntime()
			if rt != tc.runtime {
				t.Errorf("ContainerRuntime() = %q with %s set, want %q", rt, tc.envKey, tc.runtime)
			}

			// Restore and verify negative case.
			if orig == "" {
				os.Unsetenv(tc.envKey)
			} else {
				os.Setenv(tc.envKey, orig)
			}
		})
	}
}

func TestIsContainerEnvironment_NoContainerEnv(t *testing.T) {
	// Clear all container-related env vars.
	for envKey := range containerEnvVars {
		if v := os.Getenv(envKey); v != "" {
			t.Setenv(envKey, "")
			os.Unsetenv(envKey)
		}
	}

	// In CI or local dev without Docker marker, this should return false.
	// We can't guarantee /.dockerenv doesn't exist, but in typical test
	// environments it won't.
	runtime := ContainerRuntime()
	// Only assert no env-var-based detection. Docker file detection is
	// environment-dependent and not worth mocking here.
	_ = runtime
}

// ---------------------------------------------------------------------------
// ValidationError
// ---------------------------------------------------------------------------

func TestValidationError_ErrorMessage(t *testing.T) {
	err := &ValidationError{
		Field:   "test_field",
		Value:   "bad-value",
		Rule:    "format",
		Message: "value is invalid",
	}

	msg := err.Error()
	if !strings.Contains(msg, "test_field") {
		t.Errorf("error message should contain field name, got: %s", msg)
	}
	if !strings.Contains(msg, "bad-value") {
		t.Errorf("error message should contain value, got: %s", msg)
	}
	if !strings.Contains(msg, "format") {
		t.Errorf("error message should contain rule, got: %s", msg)
	}
}

func TestTruncateValue(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is way too long", 10, "this is wa..."},
		{"", 5, ""},
	}
	for _, tc := range tests {
		got := truncateValue(tc.input, tc.maxLen)
		if got != tc.want {
			t.Errorf("truncateValue(%q, %d) = %q, want %q", tc.input, tc.maxLen, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// isValidationError is a type assertion helper for testing.
func isValidationError(err error, target **ValidationError) bool {
	return errors.As(err, target)
}
