// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resource

import (
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
)

// clearCIEnvVars unsets all CI-related environment variables so tests are deterministic.
func clearCIEnvVars(t *testing.T) {
	t.Helper()

	ciVars := []string{
		"TF_BUILD", "GITHUB_ACTIONS", "APPVEYOR", "TRAVIS", "CIRCLECI", "GITLAB_CI",
		"CODEBUILD_BUILD_ID", "JENKINS_URL", "TEAMCITY_VERSION", "JB_SPACE_API_URL",
		"bamboo.buildKey", "BITBUCKET_BUILD_NUMBER", "CI", "BUILD_ID",
	}

	for _, v := range ciVars {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}
}

func TestExecEnvForCi_GitHub_Actions(t *testing.T) {
	clearCIEnvVars(t)
	t.Setenv("GITHUB_ACTIONS", "true")

	result := execEnvForCi()
	if result != fields.EnvGitHubActions {
		t.Fatalf("execEnvForCi() = %q, want %q", result, fields.EnvGitHubActions)
	}
}

func TestExecEnvForCi_Azure_Pipelines(t *testing.T) {
	clearCIEnvVars(t)
	t.Setenv("TF_BUILD", "True")

	result := execEnvForCi()
	if result != fields.EnvAzurePipelines {
		t.Fatalf("execEnvForCi() = %q, want %q", result, fields.EnvAzurePipelines)
	}
}

func TestExecEnvForCi_case_insensitive_bool(t *testing.T) {
	clearCIEnvVars(t)
	t.Setenv("APPVEYOR", "TRUE")

	result := execEnvForCi()
	if result != fields.EnvAppVeyor {
		t.Fatalf("execEnvForCi() = %q, want %q", result, fields.EnvAppVeyor)
	}
}

func TestExecEnvForCi_Travis(t *testing.T) {
	clearCIEnvVars(t)
	t.Setenv("TRAVIS", "true")

	result := execEnvForCi()
	if result != fields.EnvTravisCI {
		t.Fatalf("execEnvForCi() = %q, want %q", result, fields.EnvTravisCI)
	}
}

func TestExecEnvForCi_CircleCI(t *testing.T) {
	clearCIEnvVars(t)
	t.Setenv("CIRCLECI", "true")

	result := execEnvForCi()
	if result != fields.EnvCircleCI {
		t.Fatalf("execEnvForCi() = %q, want %q", result, fields.EnvCircleCI)
	}
}

func TestExecEnvForCi_GitLabCI(t *testing.T) {
	clearCIEnvVars(t)
	t.Setenv("GITLAB_CI", "true")

	result := execEnvForCi()
	if result != fields.EnvGitLabCI {
		t.Fatalf("execEnvForCi() = %q, want %q", result, fields.EnvGitLabCI)
	}
}

func TestExecEnvForCi_Jenkins(t *testing.T) {
	clearCIEnvVars(t)
	t.Setenv("JENKINS_URL", "https://jenkins.example.com")

	result := execEnvForCi()
	if result != fields.EnvJenkins {
		t.Fatalf("execEnvForCi() = %q, want %q", result, fields.EnvJenkins)
	}
}

func TestExecEnvForCi_AWS_CodeBuild(t *testing.T) {
	clearCIEnvVars(t)
	t.Setenv("CODEBUILD_BUILD_ID", "build:123")

	result := execEnvForCi()
	if result != fields.EnvAwsCodeBuild {
		t.Fatalf("execEnvForCi() = %q, want %q", result, fields.EnvAwsCodeBuild)
	}
}

func TestExecEnvForCi_TeamCity(t *testing.T) {
	clearCIEnvVars(t)
	t.Setenv("TEAMCITY_VERSION", "2023.1")

	result := execEnvForCi()
	if result != fields.EnvTeamCity {
		t.Fatalf("execEnvForCi() = %q, want %q", result, fields.EnvTeamCity)
	}
}

func TestExecEnvForCi_JetBrains_Space(t *testing.T) {
	clearCIEnvVars(t)
	t.Setenv("JB_SPACE_API_URL", "https://space.example.com/api")

	result := execEnvForCi()
	if result != fields.EnvJetBrainsSpace {
		t.Fatalf("execEnvForCi() = %q, want %q", result, fields.EnvJetBrainsSpace)
	}
}

func TestExecEnvForCi_BitBucket_Pipelines(t *testing.T) {
	clearCIEnvVars(t)
	t.Setenv("BITBUCKET_BUILD_NUMBER", "42")

	result := execEnvForCi()
	if result != fields.EnvBitBucketPipelines {
		t.Fatalf("execEnvForCi() = %q, want %q", result, fields.EnvBitBucketPipelines)
	}
}

func TestExecEnvForCi_unknown_CI_var(t *testing.T) {
	clearCIEnvVars(t)
	t.Setenv("CI", "1")

	result := execEnvForCi()
	if result != fields.EnvUnknownCI {
		t.Fatalf("execEnvForCi() = %q, want %q", result, fields.EnvUnknownCI)
	}
}

func TestExecEnvForCi_unknown_BUILD_ID(t *testing.T) {
	clearCIEnvVars(t)
	t.Setenv("BUILD_ID", "some-build")

	result := execEnvForCi()
	if result != fields.EnvUnknownCI {
		t.Fatalf("execEnvForCi() = %q, want %q", result, fields.EnvUnknownCI)
	}
}

func TestExecEnvForCi_no_CI_vars(t *testing.T) {
	clearCIEnvVars(t)

	result := execEnvForCi()
	if result != "" {
		t.Fatalf("execEnvForCi() = %q, want empty string", result)
	}
}

func TestExecEnvForCi_bool_false_not_matched(t *testing.T) {
	clearCIEnvVars(t)
	t.Setenv("GITHUB_ACTIONS", "false")

	result := execEnvForCi()
	if result != "" {
		t.Fatalf("execEnvForCi() with GITHUB_ACTIONS=false = %q, want empty string", result)
	}
}

func TestExecEnvForCi_bool_precedence(t *testing.T) {
	clearCIEnvVars(t)
	// Bool rules are checked before set rules.
	// TF_BUILD (bool) should win over CI (set)
	t.Setenv("TF_BUILD", "true")
	t.Setenv("CI", "1")

	result := execEnvForCi()
	if result != fields.EnvAzurePipelines {
		t.Fatalf("execEnvForCi() = %q, want %q (bool rule should win)", result, fields.EnvAzurePipelines)
	}
}

func TestIsRunningOnCI_true(t *testing.T) {
	clearCIEnvVars(t)
	t.Setenv("GITHUB_ACTIONS", "true")

	if !IsRunningOnCI() {
		t.Fatal("IsRunningOnCI() should return true when GITHUB_ACTIONS=true")
	}
}

func TestIsRunningOnCI_false(t *testing.T) {
	clearCIEnvVars(t)

	if IsRunningOnCI() {
		t.Fatal("IsRunningOnCI() should return false with no CI env vars")
	}
}

func TestIsValidMacAddress(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		isValid bool
	}{
		{"valid address", "01:23:45:67:89:ab", true},
		{"all zeros", "00:00:00:00:00:00", false},
		{"all ff", "ff:ff:ff:ff:ff:ff", false},
		{"hyper-v default", "ac:de:48:00:11:22", false},
		{"another valid", "de:ad:be:ef:ca:fe", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidMacAddress(tt.addr)
			if result != tt.isValid {
				t.Fatalf("isValidMacAddress(%q) = %v, want %v", tt.addr, result, tt.isValid)
			}
		})
	}
}

func TestExecEnvForHosts_codespaces(t *testing.T) {
	clearCIEnvVars(t)
	// Clear host vars — t.Setenv registers cleanup, os.Unsetenv removes for LookupEnv checks
	t.Setenv("AZD_IN_CLOUDSHELL", "")
	os.Unsetenv("AZD_IN_CLOUDSHELL")
	t.Setenv("CODESPACES", "")
	os.Unsetenv("CODESPACES")

	t.Setenv("CODESPACES", "true")

	result := execEnvForHosts()
	if result != fields.EnvCodespaces {
		t.Fatalf("execEnvForHosts() = %q, want %q", result, fields.EnvCodespaces)
	}
}

func TestExecEnvForHosts_no_host(t *testing.T) {
	t.Setenv("AZD_IN_CLOUDSHELL", "")
	os.Unsetenv("AZD_IN_CLOUDSHELL")
	t.Setenv("CODESPACES", "")
	os.Unsetenv("CODESPACES")

	result := execEnvForHosts()
	if result != "" {
		t.Fatalf("execEnvForHosts() = %q, want empty string", result)
	}
}

func TestNew_returns_non_nil_resource(t *testing.T) {
	clearCIEnvVars(t)
	t.Setenv("AZD_IN_CLOUDSHELL", "")
	os.Unsetenv("AZD_IN_CLOUDSHELL")
	t.Setenv("CODESPACES", "")
	os.Unsetenv("CODESPACES")
	t.Setenv("AZURE_DEV_USER_AGENT", "")
	os.Unsetenv("AZURE_DEV_USER_AGENT")

	r := New()
	if r == nil {
		t.Fatal("New() returned nil")
	}
}
