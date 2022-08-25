package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"runtime"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil/osversion"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
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
			attribute.String(machineIdKey, getMachineId()),
		),
	)

	return r
}

const (
	machineIdKey      = "machineId"
	osTypeKey         = "osType"
	osVersionKey      = "osVersion"
	runtimeVersionKey = "runtimeVersion"
	terminalTypeKey   = "terminalType"
	objectIdKey       = "objectId"
	tenantIdKey       = "tenantId"

	subscriptionIdKey = "subscriptionId"
	templateIdKey     = "templateId"
)

var invalidMadAddresses = map[string]struct{}{
	"00:00:00:00:00:00": {},
	"ff:ff:ff:ff:ff:ff": {},
	"ac:de:48:00:11:22": {},
}

func getMachineId() string {
	mac, ok := getMacAddressHash()

	if ok {
		sha := sha256.Sum256([]byte(mac))
		hash := hex.EncodeToString(sha[:])
		return hash
	} else {
		// No valid mac address, return a GUID instead.
		return uuid.NewString()
	}
}

func getMacAddressHash() (string, bool) {
	interfaces, _ := net.Interfaces()
	for _, ift := range interfaces {
		if len(ift.HardwareAddr) > 0 {
			hwAddr, err := net.ParseMAC(ift.HardwareAddr.String())
			if err != nil {
				mac := hwAddr.String()
				if isValidMacAddress(mac) {
					return mac, true
				}
			}
		}
	}

	return "", false
}

func isValidMacAddress(addr string) bool {
	_, invalidAddr := invalidMadAddresses[addr]
	return !invalidAddr
}

func getOsVersion() string {
	ver, err := osversion.GetVersion()

	if err != nil {
		return "Unknown"
	}

	return ver
}

var boolDetectors = map[string]string{
	"TF_BUILD":       "Azure DevOps",
	"GITHUB_ACTIONS": "Github Actions",
}

func getExecutionEnvironment() string {
	//TODO: add detectors
	return "Desktop"
}

func isRunningInGitHubActions() bool {
	// `GITHUB_ACTIONS` must be set to 'true' if running in GitHub Actions,
	// see https://docs.github.com/en/actions/learn-github-actions/environment-variables#default-environment-variables
	if isRunningInGithubActions := os.Getenv("GITHUB_ACTIONS"); isRunningInGithubActions == "true" {
		return true
	}

	return false
}

func WithTelemetryContext(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, environment.AzdContextKey, "test")
	return ctx
}
