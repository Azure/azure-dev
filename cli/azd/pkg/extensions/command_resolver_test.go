// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveCommandPathEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata *ExtensionCommandMetadata
		args     []string
		want     []string
	}{
		{
			name: "LongestMatchWins",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{Name: []string{"deploy"}},
					{
						Name: []string{"deploy"},
						Subcommands: []Command{
							{Name: []string{"deploy", "status"}},
						},
					},
				},
			},
			args: []string{"deploy", "status"},
			want: []string{"deploy", "status"},
		},
		{
			name: "ShortMatchWhenSubcommandDoesNotMatch",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{Name: []string{"deploy"}},
					{
						Name: []string{"deploy"},
						Subcommands: []Command{
							{Name: []string{"deploy", "status"}},
						},
					},
				},
			},
			args: []string{"deploy", "other"},
			want: []string{"deploy"},
		},
		{
			name: "CommandNameLongerThanArgsSkippedButSubcommandsSearched",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						// This parent command name is longer than what's provided,
						// but its subcommands should still be searched.
						Name: []string{"group", "sub", "deep"},
						Subcommands: []Command{
							{Name: []string{"single"}},
						},
					},
				},
			},
			args: []string{"single"},
			want: []string{"single"},
		},
		{
			name: "EmptyCommandNameSkippedButSubcommandsSearched",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						// Parent has empty name (acts as a grouping container).
						Name: []string{},
						Subcommands: []Command{
							{Name: []string{"leaf"}},
						},
					},
				},
			},
			args: []string{"leaf"},
			want: []string{"leaf"},
		},
		{
			name: "EmptyAliasSkipped",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name:    []string{"colors"},
						Aliases: []string{"", "colours"},
					},
				},
			},
			args: []string{"colours"},
			want: []string{"colors"},
		},
		{
			name: "AliasOnMultiSegmentCommand",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name: []string{"mcp"},
						Subcommands: []Command{
							{
								Name:    []string{"mcp", "start"},
								Aliases: []string{"run"},
							},
						},
					},
				},
			},
			args: []string{"mcp", "run"},
			want: []string{"mcp", "start"},
		},
		{
			name: "OnlyFlagsNoCommandArgs",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{Name: []string{"version"}},
				},
			},
			args: []string{"--verbose"},
			want: nil,
		},
		{
			name: "DoubleDashBeforeCommandArgs",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{Name: []string{"version"}},
				},
			},
			args: []string{"--", "version"},
			want: nil,
		},
		{
			name: "FlagBetweenCommandSegments",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{Name: []string{"mcp"}},
					{
						Name: []string{"mcp"},
						Subcommands: []Command{
							{Name: []string{"mcp", "start"}},
						},
					},
				},
			},
			// The flag stops command arg extraction at "mcp", so "start" is not considered a command segment.
			args: []string{"mcp", "--verbose", "start"},
			want: []string{"mcp"},
		},
		{
			name: "MultipleAliasesFirstMatchUsed",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name:    []string{"list"},
						Aliases: []string{"ls", "l"},
					},
				},
			},
			args: []string{"ls"},
			want: []string{"list"},
		},
		{
			name: "SingleArgExactMatch",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{Name: []string{"init"}},
					{Name: []string{"up"}},
					{Name: []string{"down"}},
				},
			},
			args: []string{"up"},
			want: []string{"up"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveCommandPath(tt.metadata, tt.args)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestResolveCommandFlagsAllTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata *ExtensionCommandMetadata
		args     []string
		want     []string
	}{
		{
			name: "IntFlag",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name: []string{"run"},
						Flags: []Flag{
							{Name: "count", Shorthand: "c", Type: "int"},
						},
					},
				},
			},
			args: []string{"run", "--count", "5"},
			want: []string{"count"},
		},
		{
			name: "IntFlagShorthand",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name: []string{"run"},
						Flags: []Flag{
							{Name: "count", Shorthand: "c", Type: "int"},
						},
					},
				},
			},
			args: []string{"run", "-c", "3"},
			want: []string{"count"},
		},
		{
			name: "StringArrayFlag",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name: []string{"run"},
						Flags: []Flag{
							{Name: "tags", Shorthand: "t", Type: "stringArray"},
						},
					},
				},
			},
			args: []string{"run", "--tags", "a,b,c"},
			want: []string{"tags"},
		},
		{
			name: "IntArrayFlag",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name: []string{"run"},
						Flags: []Flag{
							{Name: "ports", Shorthand: "p", Type: "intArray"},
						},
					},
				},
			},
			args: []string{"run", "--ports", "80,443"},
			want: []string{"ports"},
		},
		{
			name: "MixedFlagTypes",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name: []string{"run"},
						Flags: []Flag{
							{Name: "verbose", Shorthand: "v", Type: "bool"},
							{Name: "count", Shorthand: "c", Type: "int"},
							{Name: "output", Shorthand: "o", Type: "string"},
							{Name: "tags", Shorthand: "t", Type: "stringArray"},
							{Name: "ports", Shorthand: "p", Type: "intArray"},
						},
					},
				},
			},
			args: []string{"run", "-v", "--count", "5", "--output", "json", "--tags", "a,b", "--ports", "80"},
			want: []string{"verbose", "count", "output", "tags", "ports"},
		},
		{
			name: "UnknownFlagTypeDefaultsToString",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name: []string{"run"},
						Flags: []Flag{
							{Name: "custom", Shorthand: "x", Type: "special"},
						},
					},
				},
			},
			args: []string{"run", "--custom", "val"},
			want: []string{"custom"},
		},
		{
			name: "EmptyFlagNameSkipped",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name: []string{"run"},
						Flags: []Flag{
							{Name: "", Shorthand: "x", Type: "string"},
							{Name: "valid", Shorthand: "v", Type: "bool"},
						},
					},
				},
			},
			args: []string{"run", "-v"},
			want: []string{"valid"},
		},
		{
			name: "FlagsAfterDoubleDashIgnored",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name: []string{"run"},
						Flags: []Flag{
							{Name: "verbose", Shorthand: "v", Type: "bool"},
						},
					},
				},
			},
			args: []string{"run", "--", "--verbose"},
			want: nil,
		},
		{
			name: "BoolFlagWithEquals",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name: []string{"run"},
						Flags: []Flag{
							{Name: "verbose", Type: "bool"},
						},
					},
				},
			},
			args: []string{"run", "--verbose=true"},
			want: []string{"verbose"},
		},
		{
			name: "NoFlagsProvided",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name: []string{"run"},
						Flags: []Flag{
							{Name: "verbose", Type: "bool"},
						},
					},
				},
			},
			args: []string{"run"},
			want: nil,
		},
		{
			name:     "NilMetadata",
			metadata: nil,
			args:     []string{"run", "--verbose"},
			want:     nil,
		},
		{
			name: "CommandNotFoundReturnsNil",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name: []string{"run"},
						Flags: []Flag{
							{Name: "verbose", Type: "bool"},
						},
					},
				},
			},
			args: []string{"unknown", "--verbose"},
			want: nil,
		},
		{
			name: "FlagsResolvedViaAlias",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name:    []string{"list"},
						Aliases: []string{"ls"},
						Flags: []Flag{
							{Name: "all", Shorthand: "a", Type: "bool"},
						},
					},
				},
			},
			args: []string{"ls", "-a"},
			want: []string{"all"},
		},
		{
			name: "FlagWithNoShorthand",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name: []string{"run"},
						Flags: []Flag{
							{Name: "long-only", Type: "string"},
						},
					},
				},
			},
			args: []string{"run", "--long-only", "value"},
			want: []string{"long-only"},
		},
		{
			name: "CombinedShortBoolFlags",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name: []string{"run"},
						Flags: []Flag{
							{Name: "all", Shorthand: "a", Type: "bool"},
							{Name: "brief", Shorthand: "b", Type: "bool"},
							{Name: "color", Shorthand: "c", Type: "bool"},
						},
					},
				},
			},
			args: []string{"run", "-abc"},
			want: []string{"all", "brief", "color"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveCommandFlags(tt.metadata, tt.args)
			if tt.want == nil {
				require.Nil(t, got)
			} else {
				require.ElementsMatch(t, tt.want, got)
			}
		})
	}
}
