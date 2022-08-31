package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"runtime"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/events"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil/osversion"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
)

func newResource() *resource.Resource {
	r, _ := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("azd"),
			semconv.ServiceVersionKey.String(internal.GetVersionNumber()),
			semconv.OSTypeKey.String(runtime.GOOS),
			semconv.OSVersionKey.String(getOsVersion()),
			semconv.HostArchKey.String(runtime.GOARCH),
			semconv.ProcessRuntimeVersionKey.String(runtime.Version()),
			events.ExecutionEnvironmentKey.String(getExecutionEnvironment()),
			events.MachineIdKey.String(getMachineId()),
		),
	)

	return r
}

var invalidMacAddresses = map[string]struct{}{
	"00:00:00:00:00:00": {},
	"ff:ff:ff:ff:ff:ff": {},
	"ac:de:48:00:11:22": {},
}

func sha256Hash(val string) string {
	sha := sha256.Sum256([]byte(val))
	hash := hex.EncodeToString(sha[:])
	return hash
}

func getMachineId() string {
	mac, ok := getMacAddressHash()

	if ok {
		return sha256Hash(mac)
	} else {
		// No valid mac address, return a GUID instead.
		return uuid.NewString()
	}
}

func getMacAddressHash() (string, bool) {
	interfaces, _ := net.Interfaces()
	for _, ift := range interfaces {
		if len(ift.HardwareAddr) > 0 && ift.Flags&net.FlagLoopback == 0 {
			hwAddr, err := net.ParseMAC(ift.HardwareAddr.String())
			if err != nil {
				continue
			}

			mac := hwAddr.String()
			if isValidMacAddress(mac) {
				return mac, true
			}
		}
	}

	return "", false
}

func isValidMacAddress(addr string) bool {
	_, invalidAddr := invalidMacAddresses[addr]
	return !invalidAddr
}

func getOsVersion() string {
	ver, err := osversion.GetVersion()

	if err != nil {
		return "Unknown"
	}

	return ver
}

// All possible enumerations of Execution Environment
const (
	// desktop environments
	desktop          = "Desktop"
	visualStudio     = "Visual Studio"
	visualStudioCode = "Visual Studio Code"

	// Continuous Integration environments
	unknownCI        = "UnknownCI"
	azurePipelines   = "Azure Pipelines"
	gitHubActions    = "GitHub Actions"
	appVeyor         = "AppVeyor"
	travisCI         = "Travis CI"
	circleCI         = "Circle CI"
	gitLabCI         = "GitLab CI"
	jenkins          = "Jenkins"
	awsCodeBuild     = "AWS CodeBuild"
	googleCloudBuild = "Google Cloud Build"
	teamCity         = "TeamCity"
	jetBrainsSpace   = "JetBrains Space"
)

var booleanEnvVarRules = []struct {
	envVar      string
	environment string
}{
	// Azure Pipelines - https://docs.microsoft.com/en-us/azure/devops/pipelines/build/variables#system-variables-devops-servicesQ
	{"TF_BUILD", azurePipelines},
	// GitHub Actions, https://docs.github.com/en/actions/learn-github-actions/environment-variables#default-environment-variables
	{"GITHUB_ACTIONS", gitHubActions},
	// AppVeyor - https://www.appveyor.com/docs/environment-variables/
	{"APPVEYOR", appVeyor},
	// Travis CI - https://docs.travis-ci.com/user/environment-variables/#default-environment-variables
	{"TRAVIS", travisCI},
	// Circle CI - https://circleci.com/docs/env-vars#built-in-environment-variables
	{"CIRCLECI", circleCI},
	// GitLab CI
	{"GITLAB_CI", gitLabCI},
}

var nonNullEnvVarRules = []struct {
	envVar      string
	environment string
}{
	// AWS CodeBuild - https://docs.aws.amazon.com/codebuild/latest/userguide/build-env-ref-env-vars.html
	{"CODEBUILD_BUILD_ID", awsCodeBuild},
	// Jenkins - https://github.com/jenkinsci/jenkins/blob/master/core/src/main/resources/jenkins/model/CoreEnvironmentContributor/buildEnv.groovy
	{"JENKINS_URL", jenkins},
	// TeamCity - https://www.jetbrains.com/help/teamcity/predefined-build-parameters.html#Predefined+Server+Build+Parameters
	{"TEAMCITY_VERSION", teamCity},
	// https://www.jetbrains.com/help/space/automation-environment-variables.html#when-does-automation-resolve-its-environment-variables
	{"JB_SPACE_API_URL", jetBrainsSpace},

	// Unknown CI cases
	{"CI", unknownCI},
	{"BUILD_ID", unknownCI},
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

	return desktop
}
