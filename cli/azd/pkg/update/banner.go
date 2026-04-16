// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package update

import (
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

const (
	// stableReleaseNotesURL is the release notes URL template for stable versions.
	stableReleaseNotesURL = "https://github.com/Azure/azure-dev/releases/tag/azure-dev-cli_%s"
	// dailyReleaseNotesURL points to the main branch commits for daily builds.
	dailyReleaseNotesURL = "https://github.com/Azure/azure-dev/commits/main"
)

// BannerParams holds the data needed to render an update banner.
type BannerParams struct {
	CurrentVersion string
	LatestVersion  string
	Channel        Channel
	UpdateHint     UpdateHint
}

type updateHintKind int

// Zero-value (hintKindNone) represents an unset hint so the banner renders
// without any "To update, ..." line.
const (
	hintKindNone updateHintKind = iota
	hintKindRun
	hintKindVisit
)

// UpdateHint describes how a user should update azd. Construct one with
// RunUpdateHint or VisitUpdateHint.
type UpdateHint struct {
	kind    updateHintKind
	value   string
	details string
}

// HintOption configures an UpdateHint.
type HintOption func(*UpdateHint)

// WithDetails attaches supplemental instructions that are rendered on a
// separate paragraph below the main update line (e.g. a caveat about custom
// install parameters or a link to advanced install docs).
func WithDetails(details string) HintOption {
	return func(h *UpdateHint) { h.details = details }
}

// RunUpdateHint returns an update hint that renders a shell command.
func RunUpdateHint(command string, opts ...HintOption) UpdateHint {
	return newHint(hintKindRun, command, opts)
}

// VisitUpdateHint returns an update hint that renders a documentation URL.
func VisitUpdateHint(url string, opts ...HintOption) UpdateHint {
	return newHint(hintKindVisit, url, opts)
}

func newHint(kind updateHintKind, value string, opts []HintOption) UpdateHint {
	h := UpdateHint{kind: kind, value: value}
	for _, opt := range opts {
		opt(&h)
	}
	return h
}

// RenderUpdateBanner returns the formatted update notification string, including
// color/formatting escape codes, ready to be printed to stderr.
func RenderUpdateBanner(p BannerParams) string {
	var sb strings.Builder
	releaseNotes := p.releaseNotesLink()
	sb.WriteString(output.WithWarningFormat("Update available: " + p.versionDisplay()))
	fmt.Fprintf(&sb, " (%s)", output.WithHyperlink(releaseNotes.url, releaseNotes.label))

	if hint := formatUpdateHint(p.UpdateHint); hint != "" {
		sb.WriteString("\n")
		sb.WriteString(hint)
	}
	if p.UpdateHint.details != "" {
		sb.WriteString("\n\n")
		sb.WriteString(p.UpdateHint.details)
	}

	return sb.String()
}

func (p BannerParams) versionDisplay() string {
	if p.Channel == ChannelDaily {
		return p.LatestVersion
	}

	return fmt.Sprintf("%s -> %s", p.CurrentVersion, p.LatestVersion)
}

type releaseNotesLink struct {
	label string
	url   string
}

func (p BannerParams) releaseNotesLink() releaseNotesLink {
	// Daily builds don't have per-build GitHub releases, so link to the main
	// branch commit history instead. Stable releases have a tag of the form
	// `azure-dev-cli_<semver>` (e.g. azure-dev-cli_1.13.1).
	if p.Channel == ChannelDaily {
		return releaseNotesLink{
			label: "Recent Changes",
			url:   dailyReleaseNotesURL,
		}
	}

	return releaseNotesLink{
		label: "Release Notes",
		url:   fmt.Sprintf(stableReleaseNotesURL, p.LatestVersion),
	}
}

// formatUpdateHint renders the primary update instruction line.
func formatUpdateHint(h UpdateHint) string {
	switch h.kind {
	case hintKindRun:
		return fmt.Sprintf("To update, run `%s`", output.WithHighLightFormat(h.value))
	case hintKindVisit:
		return fmt.Sprintf("To update, visit %s", output.WithHyperlink(h.value, h.value))
	default:
		return ""
	}
}
