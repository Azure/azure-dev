package resource

import (
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
)

// Rules that apply when the specified environment variable is set to "true" (case-insensitive)
var ciVarBoolRules = []struct {
	envVar      string
	environment string
}{
	// Azure Pipelines -
	// https://docs.microsoft.com/en-us/azure/devops/pipelines/build/variables#system-variables-devops-servicesQ
	{"TF_BUILD", fields.EnvAzurePipelines},
	// GitHub Actions,
	// https://docs.github.com/en/actions/learn-github-actions/environment-variables#default-environment-variables
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

// Rules that apply when the specified environment variable is set to any value
var ciVarSetRules = []struct {
	envVar      string
	environment string
}{
	// AWS CodeBuild - https://docs.aws.amazon.com/codebuild/latest/userguide/build-env-ref-env-vars.html
	{"CODEBUILD_BUILD_ID", fields.EnvAwsCodeBuild},
	//nolint:lll
	// Jenkins -
	// https://github.com/jenkinsci/jenkins/blob/master/core/src/main/resources/jenkins/model/CoreEnvironmentContributor/buildEnv.groovy
	{"JENKINS_URL", fields.EnvJenkins},
	//nolint:lll
	// TeamCity - https://www.jetbrains.com/help/teamcity/predefined-build-parameters.html#Predefined+Server+Build+Parameters
	{"TEAMCITY_VERSION", fields.EnvTeamCity},
	//nolint:lll
	// JetBrains Space -
	// https://www.jetbrains.com/help/space/automation-environment-variables.html#when-does-automation-resolve-its-environment-variables
	{"JB_SPACE_API_URL", fields.EnvJetBrainsSpace},
	// Bamboo -
	// https://confluence.atlassian.com/bamboo/bamboo-variables-289277087.html#Bamboovariables-Build-specificvariables
	{"bamboo.buildKey", fields.EnvBamboo},
	// BitBucket - https://support.atlassian.com/bitbucket-cloud/docs/variables-and-secrets/
	{"BITBUCKET_BUILD_NUMBER", fields.EnvBitBucketPipelines},
	// Unknown CI cases
	{"CI", fields.EnvUnknownCI},
	{"BUILD_ID", fields.EnvUnknownCI},
}

// getExecutionEnvironmentForHosted detects the execution environment for CI/CD providers and returns the corresponding
// named environment.
//
// Returns an empty string if no CI/CD provider is detected.
func execEnvForCi() string {
	for _, rule := range ciVarBoolRules {
		// Some CI providers specify 'True' on Windows vs 'true' on Linux, while others use `True` always
		// Thus, it's better to err on the side of being generous and be case-insensitive
		if strings.ToLower(os.Getenv(rule.envVar)) == "true" {
			return rule.environment
		}
	}

	for _, rule := range ciVarSetRules {
		if _, ok := os.LookupEnv(rule.envVar); ok {
			return rule.environment
		}
	}

	return ""
}

// IsRunningOnCI returns true if the current process is running on a CI/CD provider.
func IsRunningOnCI() bool {
	return execEnvForCi() != ""
}
