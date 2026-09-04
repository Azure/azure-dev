// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"strings"

	"azureaiagent/internal/pkg/azure"
)

// A memory store can be declared from two different surfaces: `memoryStores:`
// on an agent service in azure.yaml (hosted agents) and `memory:` in agent.yaml
// (prompt agents). The two authoring shapes differ, but everything downstream of
// them -- the request the service accepts, the rule for when options may be
// omitted, and what counts as drift against an existing store -- is identical.
// That shared half lives here, keyed off the wire types, so the two surfaces
// cannot disagree about how a store is created or compared.

// memoryStoreOptionsOrNil returns options, or nil when every field is unset.
//
// The nil matters: a store is only configured at creation, and the service
// applies its own defaults for an omitted options object. Sending an empty
// object instead risks the service reading it as "explicitly default
// everything", which is not what an author who wrote no options asked for.
func memoryStoreOptionsOrNil(options *azure.MemoryStoreOptions) *azure.MemoryStoreOptions {
	if options == nil {
		return nil
	}
	if options.ChatSummaryEnabled == nil &&
		options.UserProfileEnabled == nil &&
		options.ProceduralMemoryEnabled == nil &&
		options.DefaultTTLSeconds == nil &&
		options.UserProfileDetails == "" {
		return nil
	}
	return options
}

// memoryStoreDrift is one field whose declared value diverges from the live
// store. It is reported rather than applied: azd creates memory stores but never
// updates them, so an edit to a store that already exists has no effect, and
// silently ignoring it would leave the manifest and the resource disagreeing
// indefinitely.
type memoryStoreDrift struct {
	// Field is the wire field path, e.g. "chat_model" or
	// "options.chat_summary_enabled". Callers map it to the key name used by
	// the surface the author actually wrote.
	Field string
	// Declared is the value in the manifest, formatted for display.
	Declared string
	// Live is the store's current value, formatted for display. It is empty
	// when the store leaves the field at its service default, which is not
	// something the caller can usefully print back to the author.
	Live string
}

// diffMemoryStoreDefinition reports the fields where declared diverges from
// live. Only fields the author explicitly declared are compared, so unset
// options -- which fall back to service defaults -- never produce false drift,
// and a live-only field the author never mentioned is ignored.
//
// A model the service did not echo back is treated as unknown rather than as
// drift: a response that omits the definition is not evidence the store differs,
// and reporting it would warn on every deploy.
func diffMemoryStoreDefinition(declared, live azure.MemoryStoreDefinition) []memoryStoreDrift {
	var drift []memoryStoreDrift

	add := func(field, declaredVal, liveVal string) {
		drift = append(drift, memoryStoreDrift{Field: field, Declared: declaredVal, Live: liveVal})
	}

	if want, got := strings.TrimSpace(declared.ChatModel), strings.TrimSpace(live.ChatModel); //
	got != "" && want != got {
		add("chat_model", want, got)
	}
	if want, got := strings.TrimSpace(declared.EmbeddingModel), strings.TrimSpace(live.EmbeddingModel); //
	got != "" && want != got {
		add("embedding_model", want, got)
	}

	if declared.Options == nil {
		return drift
	}

	var liveOpts azure.MemoryStoreOptions
	if live.Options != nil {
		liveOpts = *live.Options
	}

	for _, opt := range []struct {
		field           string
		declared, live_ *bool
	}{
		{"options.chat_summary_enabled", declared.Options.ChatSummaryEnabled, liveOpts.ChatSummaryEnabled},
		{"options.user_profile_enabled", declared.Options.UserProfileEnabled, liveOpts.UserProfileEnabled},
		{
			"options.procedural_memory_enabled",
			declared.Options.ProceduralMemoryEnabled,
			liveOpts.ProceduralMemoryEnabled,
		},
	} {
		if boolPtrDiffers(opt.declared, opt.live_) {
			add(opt.field, fmt.Sprintf("%v", *opt.declared), formatBoolPtr(opt.live_))
		}
	}

	if declared.Options.DefaultTTLSeconds != nil &&
		(liveOpts.DefaultTTLSeconds == nil || *declared.Options.DefaultTTLSeconds != *liveOpts.DefaultTTLSeconds) {
		liveTTL := ""
		if liveOpts.DefaultTTLSeconds != nil {
			liveTTL = fmt.Sprintf("%d", *liveOpts.DefaultTTLSeconds)
		}
		add("options.default_ttl_seconds", fmt.Sprintf("%d", *declared.Options.DefaultTTLSeconds), liveTTL)
	}

	if declared.Options.UserProfileDetails != "" &&
		declared.Options.UserProfileDetails != liveOpts.UserProfileDetails {
		add("options.user_profile_details", declared.Options.UserProfileDetails, liveOpts.UserProfileDetails)
	}

	return drift
}

// boolPtrDiffers reports whether a declared bool pointer is set and differs from
// the live value. An unset live value differs from any declared one: the store
// is on the service default, not on what the author asked for.
func boolPtrDiffers(declared, live *bool) bool {
	if declared == nil {
		return false
	}
	return live == nil || *declared != *live
}

// formatBoolPtr renders a bool pointer for a drift message, using the empty
// string for unset so callers can suppress the "current" half.
func formatBoolPtr(value *bool) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%v", *value)
}

// describeMemoryStoreDrift renders drift entries as human-readable phrases,
// mapping each wire field path through labels so the message names the key the
// author actually wrote. A field absent from labels is printed as-is, which is
// what the agent.yaml surface wants since it uses the wire names verbatim.
func describeMemoryStoreDrift(drift []memoryStoreDrift, labels map[string]string) []string {
	described := make([]string, 0, len(drift))
	for _, d := range drift {
		label := d.Field
		if mapped, ok := labels[d.Field]; ok {
			label = mapped
		}
		if d.Live == "" {
			described = append(described, fmt.Sprintf("%s (declared %q)", label, d.Declared))
			continue
		}
		described = append(described, fmt.Sprintf("%s (declared %q, current %q)", label, d.Declared, d.Live))
	}
	return described
}
