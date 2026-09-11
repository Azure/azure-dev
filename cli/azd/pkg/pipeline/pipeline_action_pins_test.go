package pipeline

import (
	"regexp"
	"testing"

	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/stretchr/testify/require"
)

// actionPinManifestPath is a real, Dependabot-managed GitHub Actions workflow that mirrors every
// third-party action reference used by the azure-dev.ymlt pipeline template. Because azure-dev.ymlt
// has a .ymlt extension (it's a Go template, not valid YAML), Dependabot cannot parse it directly and
// propose SHA bumps against it. This manifest exists solely so Dependabot has a real workflow file to
// version, and its pins are meant to be copied into the template whenever Dependabot updates them here.
const actionPinManifestPath = "pipeline/.github/workflows/azure-dev-actions.yml"

// actionPinTemplatePath is the generated-pipeline template that actually ships to users via
// `azd pipeline config`. Its action pins must be kept in sync with actionPinManifestPath by hand,
// since generatePipelineDefinition does not read the manifest at generation time.
const actionPinTemplatePath = "pipeline/.github/azure-dev.ymlt"

// usesLineRe matches `uses: <action>@<ref>  # <comment>` lines, capturing the action reference
// (repo@sha-or-tag) so pins can be compared independent of surrounding indentation or comments.
var usesLineRe = regexp.MustCompile(`(?m)^\s*(?:-\s*)?(?:id:\s*\S+\s*)?uses:\s*(\S+@\S+)`)

func extractActionPins(t *testing.T, path string) map[string]bool {
	t.Helper()
	contents, err := resources.PipelineFiles.ReadFile(path)
	require.NoError(t, err)

	matches := usesLineRe.FindAllStringSubmatch(string(contents), -1)
	pins := make(map[string]bool, len(matches))
	for _, match := range matches {
		pins[match[1]] = true
	}
	return pins
}

// Test_ActionPinManifest_MatchesTemplate ensures that every action pin declared in the
// Dependabot-managed manifest (azure-dev-actions.yml) is also present, verbatim (same commit SHA),
// in the generated-pipeline template (azure-dev.ymlt). If Dependabot bumps a pin in the manifest but
// the template isn't updated to match, this test fails, preventing the two files from silently
// drifting out of sync.
func Test_ActionPinManifest_MatchesTemplate(t *testing.T) {
	manifestPins := extractActionPins(t, actionPinManifestPath)
	require.NotEmpty(t, manifestPins, "expected at least one action pin in %s", actionPinManifestPath)

	templatePins := extractActionPins(t, actionPinTemplatePath)
	require.NotEmpty(t, templatePins, "expected at least one action pin in %s", actionPinTemplatePath)

	for pin := range manifestPins {
		require.Truef(t, templatePins[pin],
			"action pin %q from %s was not found in %s; when Dependabot updates the manifest, "+
				"the same pin must be copied into the template",
			pin, actionPinManifestPath, actionPinTemplatePath)
	}
}
