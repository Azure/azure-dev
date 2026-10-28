// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"encoding/json"
	"slices"
	"strings"
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

func TestResolveCommandFlagsReservedShorthand(t *testing.T) {
	for _, flagType := range []string{"bool", "string", "int", "intarray", "stringarray", "unknown"} {
		for _, shorthand := range []string{"e", "C", "o", "h"} {
			t.Run(flagType+"/"+shorthand, func(t *testing.T) {
				flag := Flag{Name: "project-endpoint", Shorthand: shorthand, Type: flagType}
				metadata := &ExtensionCommandMetadata{
					Commands: []Command{{Name: []string{"deploy"}, Flags: []Flag{flag}}},
				}
				args := []string{"deploy"}
				require.NotPanics(t, func() {
					require.Nil(t, ResolveCommandFlags(metadata, args))
				})
				require.Equal(t, []string{"deploy"}, args)

				value := "endpoint"
				switch flagType {
				case "bool":
					value = "true"
				case "int", "intarray":
					value = "42"
				}
				args = []string{"deploy", "--project-endpoint=" + value}
				original := slices.Clone(args)
				require.Equal(t, []string{"project-endpoint"}, ResolveCommandFlags(metadata, args))
				require.Equal(t, original, args)
				require.Equal(t, flag, metadata.Commands[0].Flags[0])
			})
		}
	}
}

func TestResolveCommandLaunch(t *testing.T) {
	t.Parallel()

	noAzdEnvironment := &CommandLaunch{NoAzdEnvironment: true}
	defaultLaunch := &CommandLaunch{NoAzdEnvironment: false}
	metadata := &ExtensionCommandMetadata{
		Commands: []Command{
			{
				Name:    []string{"connection"},
				Aliases: []string{"conn"},
				Subcommands: []Command{
					{
						Name:    []string{"connection", "add"},
						Aliases: []string{"create"},
						Launch:  noAzdEnvironment,
						Flags:   []Flag{{Name: "file", Type: "string"}},
					},
					{
						Name:    []string{"connection", "manage"},
						Aliases: []string{"mg"},
						Launch:  noAzdEnvironment,
						Subcommands: []Command{
							{
								Name:    []string{"connection", "manage", "add"},
								Aliases: []string{"create"},
								Launch:  noAzdEnvironment,
								Flags:   []Flag{{Name: "file", Type: "string"}},
							},
							{Name: []string{"connection", "manage", "list"}},
							{Name: []string{"connection", "manage", "show"}, Launch: defaultLaunch},
						},
					},
					{Name: []string{"connection", "list"}},
				},
			},
		},
	}

	for _, tt := range []struct {
		name       string
		args, path []string
		launch     *CommandLaunch
	}{
		{"canonical", []string{"connection", "add"}, []string{"connection", "add"}, noAzdEnvironment},
		{"parent alias", []string{"conn", "add"}, []string{"connection", "add"}, noAzdEnvironment},
		{"child alias", []string{"connection", "create"}, []string{"connection", "add"}, noAzdEnvironment},
		{"both aliases", []string{"conn", "create"}, []string{"connection", "add"}, noAzdEnvironment},
		{"marked parent", []string{"connection", "manage"}, []string{"connection", "manage"}, noAzdEnvironment},
		{"marked parent alias", []string{"conn", "mg"}, []string{"connection", "manage"}, noAzdEnvironment},
		{"nested canonical", []string{"connection", "manage", "add"},
			[]string{"connection", "manage", "add"}, noAzdEnvironment},
		{"nested parent alias", []string{"conn", "manage", "add"},
			[]string{"connection", "manage", "add"}, noAzdEnvironment},
		{"multiple parent aliases", []string{"conn", "mg", "add"},
			[]string{"connection", "manage", "add"}, noAzdEnvironment},
		{"all aliases", []string{"conn", "mg", "create"}, []string{"connection", "manage", "add"}, noAzdEnvironment},
		{"unmarked descendant", []string{"conn", "mg", "list"}, []string{"connection", "manage", "list"}, nil},
		{"explicit default", []string{"conn", "mg", "show"}, []string{"connection", "manage", "show"}, defaultLaunch},
		{"unmarked sibling", []string{"conn", "list"}, []string{"connection", "list"}, nil},
		{"unknown descendant", []string{"conn", "unknown"}, []string{"connection"}, nil},
		{"interspersed bool", []string{"conn", "--debug", "create"}, []string{"connection", "add"}, noAzdEnvironment},
		{"interspersed value", []string{"conn", "-e", "list", "create"}, []string{"connection", "add"}, noAzdEnvironment},
		{"interspersed attached", []string{"conn", "-odefault", "create"}, []string{"connection", "add"}, noAzdEnvironment},
		{"marked parent unmarked child", []string{"conn", "mg", "--no-prompt", "list"},
			[]string{"connection", "manage", "list"}, nil},
		{"marked parent default child", []string{"conn", "mg", "--environment=add", "show"},
			[]string{"connection", "manage", "show"}, defaultLaunch},
		{"boundary", []string{"conn", "--", "add"}, []string{"connection"}, nil},
		{"extension flag value", []string{"conn", "--file", "add"}, []string{"connection"}, nil},
		{"unknown bool not guessed", []string{"conn", "--unknown", "add"}, []string{"connection"}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args := append(slices.Clone(tt.args), "--file", "connection.yaml")
			command := ResolveCommand(metadata, args)
			require.NotNil(t, command)
			require.Equal(t, tt.path, command.Name)
			require.Equal(t, tt.launch, command.Launch)
			require.Same(t, ResolveCommand(metadata, tt.path), command)
			require.Equal(t, tt.path, ResolveCommandPath(metadata, args))
			var flags []string
			if slices.Equal(tt.path, []string{"connection", "add"}) ||
				slices.Equal(tt.path, []string{"connection", "manage", "add"}) {
				flags = []string{"file"}
			}
			require.Equal(t, flags, ResolveCommandFlags(metadata, args))
		})
	}
}

func TestResolveCommandFlagsRespectGlobalFlagValues(t *testing.T) {
	metadata := &ExtensionCommandMetadata{
		Commands: []Command{{
			Name: []string{"connection"},
			Subcommands: []Command{{
				Name:  []string{"connection", "add"},
				Flags: []Flag{{Name: "file", Type: "string"}},
			}},
		}},
	}
	for _, tt := range []struct {
		args  []string
		flags []string
	}{
		{[]string{"connection", "-e", "--file", "add"}, nil},
		{[]string{"connection", "-o", "--file", "add"}, nil},
		{[]string{"connection", "-e", "--file", "add", "--file", "config.yaml"}, []string{"file"}},
		{[]string{"connection", "--debug=false", "add", "--", "--file", "config.yaml"}, nil},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			require.Equal(t, []string{"connection", "add"}, ResolveCommandPath(metadata, tt.args))
			require.Equal(t, tt.flags, ResolveCommandFlags(metadata, tt.args))
		})
	}
}

func TestCommandLaunchJSONRoundTrip(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		input  string
		launch *CommandLaunch
		output string
	}{
		{
			name:   "opted out",
			input:  `{"name":["connection","add"],"launch":{"noAzdEnvironment":true}}`,
			launch: &CommandLaunch{NoAzdEnvironment: true},
			output: `{"name":["connection","add"],"short":"","launch":{"noAzdEnvironment":true}}`,
		},
		{
			name:   "explicit default",
			input:  `{"name":["connection","add"],"launch":{"noAzdEnvironment":false}}`,
			launch: &CommandLaunch{NoAzdEnvironment: false},
			output: `{"name":["connection","add"],"short":"","launch":{}}`,
		},
		{
			name:   "no launch",
			input:  `{"name":["connection","add"]}`,
			output: `{"name":["connection","add"],"short":""}`,
		},
		{
			name:   "legacy key ignored",
			input:  `{"name":["connection","add"],"environmentIndependent":true}`,
			output: `{"name":["connection","add"],"short":""}`,
		},
		{
			name:   "legacy key cannot override launch",
			input:  `{"name":["connection","add"],"environmentIndependent":true,"launch":{"noAzdEnvironment":false}}`,
			launch: &CommandLaunch{NoAzdEnvironment: false},
			output: `{"name":["connection","add"],"short":"","launch":{}}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var command Command
			require.NoError(t, json.Unmarshal([]byte(tt.input), &command))
			require.Equal(t, []string{"connection", "add"}, command.Name)
			require.Equal(t, tt.launch, command.Launch)

			data, err := json.Marshal(command)
			require.NoError(t, err)
			require.JSONEq(t, tt.output, string(data))

			var roundTrip Command
			require.NoError(t, json.Unmarshal(data, &roundTrip))
			require.Equal(t, command, roundTrip)
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
		{
			name: "OptionalStringFlagBeforeAnotherFlag",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name: []string{"run"},
						Flags: []Flag{
							{Name: "infra", Type: "string", ValueOptional: true},
							{Name: "verbose", Shorthand: "v", Type: "bool"},
						},
					},
				},
			},
			args: []string{"run", "--infra", "--verbose"},
			want: []string{"infra", "verbose"},
		},
		{
			name: "OptionalIntFlagBeforeAnotherFlag",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name: []string{"run"},
						Flags: []Flag{
							{Name: "count", Type: "int", ValueOptional: true},
							{Name: "verbose", Shorthand: "v", Type: "bool"},
						},
					},
				},
			},
			args: []string{"run", "--count", "--verbose"},
			want: []string{"count", "verbose"},
		},
		{
			name: "OptionalStringArrayFlagBeforeAnotherFlag",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name: []string{"run"},
						Flags: []Flag{
							{Name: "tags", Type: "stringArray", ValueOptional: true},
							{Name: "verbose", Shorthand: "v", Type: "bool"},
						},
					},
				},
			},
			args: []string{"run", "--tags", "--verbose"},
			want: []string{"tags", "verbose"},
		},
		{
			name: "OptionalIntArrayFlagBeforeAnotherFlag",
			metadata: &ExtensionCommandMetadata{
				Commands: []Command{
					{
						Name: []string{"run"},
						Flags: []Flag{
							{Name: "ports", Type: "intArray", ValueOptional: true},
							{Name: "verbose", Shorthand: "v", Type: "bool"},
						},
					},
				},
			},
			args: []string{"run", "--ports", "--verbose"},
			want: []string{"ports", "verbose"},
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
