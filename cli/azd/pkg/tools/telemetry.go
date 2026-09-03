// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

var telemetryToolNames = map[string]string{
	"Docker":             "docker",
	"Podman":             "podman",
	"Terraform CLI":      "terraform",
	"GitHub CLI":         "gh",
	".NET CLI":           "dotnet",
	"Go CLI":             "go",
	"git CLI":            "git",
	"Java JDK":           "javac",
	"npm CLI":            "npm",
	"pnpm CLI":           "pnpm",
	"yarn CLI":           "yarn",
	"Python CLI":         "python",
	"SWA CLI":            "swa",
	"kubectl":            "kubectl",
	"Maven":              "mvn",
	"GitHub Copilot CLI": "copilot",
	"docker":             "docker",
	"podman":             "podman",
	"terraform":          "terraform",
	"gh":                 "gh",
	"dotnet":             "dotnet",
	"go":                 "go",
	"git":                "git",
	"javac":              "javac",
	"npm":                "npm",
	"pnpm":               "pnpm",
	"yarn":               "yarn",
	"python":             "python",
	"swa":                "swa",
	"mvn":                "mvn",
	"copilot":            "copilot",
}

// TelemetryName returns a stable identifier for a known tool name.
// Unknown names return "other".
func TelemetryName(name string) string {
	if telemetryName, ok := telemetryToolNames[name]; ok {
		return telemetryName
	}

	return "other"
}
