// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resource

import (
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/fields"
)

var booleanEnvVarRules = []struct {
	envVar      string
	environment string
}{
	// Azure Pipelines - https://docs.microsoft.com/en-us/azure/devops/pipelines/build/variables#system-variables-devops-servicesQ
	{"TF_BUILD", fields.EnvAzurePipelines},
	// GitHub Actions, https://docs.github.com/en/actions/learn-github-actions/environment-variables#default-environment-variables
	{"GITHUB_ACTIONS", fields.EnvGitHubActions},
	// AppVeyor - https://www.appveyor.com/docs/environment-variables/
	{"APPVEYOR", fields.EnvAppVeyor},
	// Travis CI - https://docs.travis-ci.com/user/environment-variables/#default-environment-variables
	{"TRAVIS", fields.EnvTravisCI},
	// Circle CI - https://circleci.com/docs/env-vars#built-in-environment-variables
	{"CIRCLECI", fields.EnvCircleCI},
	// GitLab CI
	{"GITLAB_CI", fields.EnvGitLabCI},
}

var nonNullEnvVarRules = []struct {
	envVar      string
	environment string
}{
	// AWS CodeBuild - https://docs.aws.amazon.com/codebuild/latest/userguide/build-env-ref-env-vars.html
	{"CODEBUILD_BUILD_ID", fields.EnvAwsCodeBuild},
	// Jenkins - https://github.com/jenkinsci/jenkins/blob/master/core/src/main/resources/jenkins/model/CoreEnvironmentContributor/buildEnv.groovy
	{"JENKINS_URL", fields.EnvJenkins},
	// TeamCity - https://www.jetbrains.com/help/teamcity/predefined-build-parameters.html#Predefined+Server+Build+Parameters
	{"TEAMCITY_VERSION", fields.EnvTeamCity},
	// https://www.jetbrains.com/help/space/automation-environment-variables.html#when-does-automation-resolve-its-environment-variables
	{"JB_SPACE_API_URL", fields.EnvJetBrainsSpace},

	// Unknown CI cases
	{"CI", fields.EnvUnknownCI},
	{"BUILD_ID", fields.EnvUnknownCI},
}

func getExecutionEnvironment() string {
	ciEnv, ok := getExecutionEnvironmentForCI()
	if ok {
		return ciEnv
	}

	return getExecutionEnvironmentForDesktop()
}

func getExecutionEnvironmentForCI() (string, bool) {
	for _, rule := range booleanEnvVarRules {
		if os.Getenv(rule.envVar) == "true" {
			return rule.environment, true
		}
	}

	for _, rule := range nonNullEnvVarRules {
		if _, ok := os.LookupEnv(rule.envVar); ok {
			return rule.environment, true
		}
	}

	return "", false
}

func getExecutionEnvironmentForDesktop() string {
	userAgent := internal.GetCallerUserAgent()

	if strings.HasPrefix(userAgent, internal.VsCodeAgentPrefix) {
		return fields.EnvVisualStudioCode
	}

	return fields.EnvDesktop
}
