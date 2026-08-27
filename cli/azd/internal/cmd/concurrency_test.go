// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func lookupEnvironment(values map[string]string) environmentLookup {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func TestResolveConcurrencySetting(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		want   concurrencySetting
	}{
		{"unset", nil, concurrencySetting{}},
		{"valid", map[string]string{packageConcurrencyEnvVar: "4"}, concurrencySetting{value: 4, set: true}},
		{"clamped", map[string]string{packageConcurrencyEnvVar: "100"}, concurrencySetting{value: 64, set: true}},
		{"maximum", map[string]string{packageConcurrencyEnvVar: "64"}, concurrencySetting{value: 64, set: true}},
		{"invalid", map[string]string{packageConcurrencyEnvVar: "abc"}, concurrencySetting{set: true}},
		{"zero", map[string]string{packageConcurrencyEnvVar: "0"}, concurrencySetting{set: true}},
		{"negative", map[string]string{packageConcurrencyEnvVar: "-1"}, concurrencySetting{set: true}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := resolveConcurrencySetting(lookupEnvironment(test.values), packageConcurrencyEnvVar)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestResolveUpGraphConcurrency(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		max    int
		groups map[string]int
	}{
		{
			name: "unset",
			groups: map[string]int{
				packageConcurrencyGroup:   0,
				provisionConcurrencyGroup: 0,
				deployConcurrencyGroup:    0,
			},
		},
		{
			name:   "up fallback",
			values: map[string]string{upConcurrencyEnvVar: "4"},
			max:    4,
			groups: map[string]int{
				packageConcurrencyGroup:   4,
				provisionConcurrencyGroup: 4,
				deployConcurrencyGroup:    4,
			},
		},
		{
			name: "specific phase limits",
			values: map[string]string{
				upConcurrencyEnvVar:        "10",
				packageConcurrencyEnvVar:   "2",
				provisionConcurrencyEnvVar: "8",
				deployConcurrencyEnvVar:    "6",
			},
			max: 10,
			groups: map[string]int{
				packageConcurrencyGroup:   2,
				provisionConcurrencyGroup: 8,
				deployConcurrencyGroup:    6,
			},
		},
		{
			name: "explicit global maximum",
			values: map[string]string{
				concurrencyMaxEnvVar: "3",
				upConcurrencyEnvVar:  "10",
			},
			max: 3,
			groups: map[string]int{
				packageConcurrencyGroup:   10,
				provisionConcurrencyGroup: 10,
				deployConcurrencyGroup:    10,
			},
		},
		{
			name: "invalid phase override blocks fallback",
			values: map[string]string{
				upConcurrencyEnvVar:      "10",
				packageConcurrencyEnvVar: "invalid",
			},
			max: 10,
			groups: map[string]int{
				packageConcurrencyGroup:   0,
				provisionConcurrencyGroup: 10,
				deployConcurrencyGroup:    10,
			},
		},
		{
			// Backward compatibility: before azd up had its own variable, users
			// capped the whole graph with AZD_DEPLOY_CONCURRENCY.
			name:   "deploy concurrency remains the legacy hard-ceiling fallback",
			values: map[string]string{deployConcurrencyEnvVar: "2"},
			max:    2,
			groups: map[string]int{
				packageConcurrencyGroup:   0,
				provisionConcurrencyGroup: 0,
				deployConcurrencyGroup:    2,
			},
		},
		{
			name: "up concurrency wins over deploy concurrency for the hard ceiling",
			values: map[string]string{
				upConcurrencyEnvVar:     "5",
				deployConcurrencyEnvVar: "2",
			},
			max: 5,
			groups: map[string]int{
				packageConcurrencyGroup:   5,
				provisionConcurrencyGroup: 5,
				deployConcurrencyGroup:    2,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := resolveUpGraphConcurrency(lookupEnvironment(test.values))
			assert.Equal(t, test.max, got.max)
			assert.Equal(t, test.groups, got.groups)
		})
	}
}

func TestResolveDeployGraphConcurrency(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		max    int
		groups map[string]int
	}{
		{
			name: "unset",
			groups: map[string]int{
				packageConcurrencyGroup: 0,
				deployConcurrencyGroup:  0,
			},
		},
		{
			name:   "deploy fallback",
			values: map[string]string{deployConcurrencyEnvVar: "4"},
			max:    4,
			groups: map[string]int{
				packageConcurrencyGroup: 4,
				deployConcurrencyGroup:  4,
			},
		},
		{
			name: "specific package and global limits",
			values: map[string]string{
				concurrencyMaxEnvVar:     "3",
				packageConcurrencyEnvVar: "2",
				deployConcurrencyEnvVar:  "6",
			},
			max: 3,
			groups: map[string]int{
				packageConcurrencyGroup: 2,
				deployConcurrencyGroup:  6,
			},
		},
		{
			name: "invalid global maximum blocks fallback",
			values: map[string]string{
				concurrencyMaxEnvVar:    "invalid",
				deployConcurrencyEnvVar: "4",
			},
			groups: map[string]int{
				packageConcurrencyGroup: 4,
				deployConcurrencyGroup:  4,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := resolveDeployGraphConcurrency(lookupEnvironment(test.values))
			assert.Equal(t, test.max, got.max)
			assert.Equal(t, test.groups, got.groups)
		})
	}
}

func TestResolveProvisionGraphConcurrency(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		max    int
		group  int
	}{
		{"unset", nil, 0, 0},
		{"provision fallback", map[string]string{provisionConcurrencyEnvVar: "4"}, 4, 4},
		{
			"global maximum",
			map[string]string{concurrencyMaxEnvVar: "2", provisionConcurrencyEnvVar: "4"},
			2,
			4,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := resolveProvisionGraphConcurrency(lookupEnvironment(test.values))
			assert.Equal(t, test.max, got.max)
			assert.Equal(t, test.group, got.groups[provisionConcurrencyGroup])
		})
	}
}
