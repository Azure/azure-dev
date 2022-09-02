package telemetry

import (
	"os"
	"runtime"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil/osversion"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
)

// newResource creates a resource with all application-level fields populated.
func newResource() *resource.Resource {
	r, _ := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			fields.ServiceNameKey.String(azdAppName),
			fields.ServiceVersionKey.String(internal.GetVersionNumber()),
			fields.OSTypeKey.String(runtime.GOOS),
			fields.OSVersionKey.String(getOsVersion()),
			fields.HostArchKey.String(runtime.GOARCH),
			fields.ProcessRuntimeVersionKey.String(runtime.Version()),
			fields.ExecutionEnvironmentKey.String(getExecutionEnvironment()),
			fields.MachineIdKey.String(getMachineId()),
		),
	)

	return r
}

func getOsVersion() string {
	ver, err := osversion.GetVersion()

	if err != nil {
		return "Unknown"
	}

	return ver
}

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
	for _, rule := range booleanEnvVarRules {
		if os.Getenv(rule.envVar) == "true" {
			return rule.environment
		}
	}

	for _, rule := range nonNullEnvVarRules {
		if _, ok := os.LookupEnv(rule.envVar); ok {
			return rule.environment
		}
	}

	return fields.EnvDesktop
}
