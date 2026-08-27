// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"log"
	"strconv"
	"strings"
)

const (
	concurrencyMaxEnvVar       = "AZD_CONCURRENCY_MAX"
	packageConcurrencyEnvVar   = "AZD_PACKAGE_CONCURRENCY"
	provisionConcurrencyEnvVar = "AZD_PROVISION_CONCURRENCY"
	deployConcurrencyEnvVar    = "AZD_DEPLOY_CONCURRENCY"
	upConcurrencyEnvVar        = "AZD_UP_CONCURRENCY"

	packageConcurrencyGroup   = "package"
	provisionConcurrencyGroup = "provision"
	deployConcurrencyGroup    = "deploy"

	maxConfiguredConcurrency = 64
)

type environmentLookup func(string) (string, bool)

type concurrencySetting struct {
	value int
	set   bool
}

type graphConcurrencyOptions struct {
	max    int
	groups map[string]int
}

func resolveConcurrencySetting(lookup environmentLookup, envName string) concurrencySetting {
	envValue, ok := lookup(envName)
	if !ok {
		return concurrencySetting{}
	}

	setting := concurrencySetting{set: true}
	value, err := strconv.Atoi(envValue)
	if err != nil {
		log.Printf("warning: ignoring invalid %s=%q: %v", envName, envValue, err)
		return setting
	}
	if value <= 0 {
		return setting
	}

	setting.value = min(value, maxConfiguredConcurrency)
	if setting.value < value {
		label := strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(envName, "AZD_"), "_", " "))
		log.Printf("clamping %s from %d to %d", label, value, setting.value)
	}
	return setting
}

func firstConcurrency(settings ...concurrencySetting) int {
	for _, setting := range settings {
		if setting.set {
			return setting.value
		}
	}
	return 0
}

func resolveUpGraphConcurrency(lookup environmentLookup) graphConcurrencyOptions {
	maxSetting := resolveConcurrencySetting(lookup, concurrencyMaxEnvVar)
	upSetting := resolveConcurrencySetting(lookup, upConcurrencyEnvVar)
	packageSetting := resolveConcurrencySetting(lookup, packageConcurrencyEnvVar)
	provisionSetting := resolveConcurrencySetting(lookup, provisionConcurrencyEnvVar)
	deploySetting := resolveConcurrencySetting(lookup, deployConcurrencyEnvVar)

	// AZD_DEPLOY_CONCURRENCY remains the last hard-ceiling fallback so users who
	// tuned `azd deploy` parallelism before `azd up` gained its own variable do
	// not silently get the unbounded scheduler default for the whole graph.
	return graphConcurrencyOptions{
		max: firstConcurrency(maxSetting, upSetting, deploySetting),
		groups: map[string]int{
			packageConcurrencyGroup:   firstConcurrency(packageSetting, upSetting),
			provisionConcurrencyGroup: firstConcurrency(provisionSetting, upSetting),
			deployConcurrencyGroup:    firstConcurrency(deploySetting, upSetting),
		},
	}
}

func resolveDeployGraphConcurrency(lookup environmentLookup) graphConcurrencyOptions {
	maxSetting := resolveConcurrencySetting(lookup, concurrencyMaxEnvVar)
	packageSetting := resolveConcurrencySetting(lookup, packageConcurrencyEnvVar)
	deploySetting := resolveConcurrencySetting(lookup, deployConcurrencyEnvVar)

	return graphConcurrencyOptions{
		max: firstConcurrency(maxSetting, deploySetting),
		groups: map[string]int{
			packageConcurrencyGroup: firstConcurrency(packageSetting, deploySetting),
			deployConcurrencyGroup:  deploySetting.value,
		},
	}
}

func resolveProvisionGraphConcurrency(lookup environmentLookup) graphConcurrencyOptions {
	maxSetting := resolveConcurrencySetting(lookup, concurrencyMaxEnvVar)
	provisionSetting := resolveConcurrencySetting(lookup, provisionConcurrencyEnvVar)

	return graphConcurrencyOptions{
		max: firstConcurrency(maxSetting, provisionSetting),
		groups: map[string]int{
			provisionConcurrencyGroup: provisionSetting.value,
		},
	}
}
