// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

const paritySourceCommit = "25d39b30116a840491b7b6f393d6db792b38d4a4"

type parityCapability struct {
	ID             int      `yaml:"id"`
	SourcePaths    []string `yaml:"source_paths"`
	Target         string   `yaml:"target"`
	Classification string   `yaml:"classification"`
}

type parityScenario struct {
	ID         int      `yaml:"id"`
	Name       string   `yaml:"name"`
	Kind       string   `yaml:"kind"`
	Mode       string   `yaml:"mode"`
	Recordings []string `yaml:"recordings"`
}

type parityRecording struct {
	Name         string `yaml:"name"`
	File         string `yaml:"file"`
	SHA256       string `yaml:"sha256"`
	SourceSHA256 string `yaml:"source_sha256"`
}

type parityAsset struct {
	File   string `yaml:"file"`
	SHA256 string `yaml:"sha256"`
}

type paritySourceAsset struct {
	File       string `yaml:"file"`
	SHA256     string `yaml:"sha256"`
	SourcePath string `yaml:"source_path"`
	License    string `yaml:"license"`
}

type parityInventory struct {
	SchemaVersion int `yaml:"schema_version"`
	Source        struct {
		Repository string `yaml:"repository"`
		Commit     string `yaml:"commit"`
	} `yaml:"source"`
	Hygiene             parityAsset        `yaml:"hygiene"`
	ScenarioSource      paritySourceAsset  `yaml:"scenario_source"`
	CapabilitySource    paritySourceAsset  `yaml:"capability_source"`
	Normalized          parityAsset        `yaml:"normalized"`
	GeneratedProjects   parityAsset        `yaml:"generated_projects"`
	CoreShowBindings    parityAsset        `yaml:"core_show_bindings"`
	CoreCreateBinding   parityAsset        `yaml:"core_create_binding"`
	CoreTestBinding     parityAsset        `yaml:"core_test_binding"`
	CoreValidateBinding parityAsset        `yaml:"core_validate_binding"`
	CoreCopyBinding     parityAsset        `yaml:"core_copy_binding"`
	Capabilities        []parityCapability `yaml:"capabilities"`
	Scenarios           []parityScenario   `yaml:"scenarios"`
	Recordings          []parityRecording  `yaml:"recordings"`
}

func loadParityInventory(t *testing.T) parityInventory {
	t.Helper()
	file, err := os.Open("../../testdata/parity/fixtures.yaml")
	require.NoError(t, err)
	defer file.Close()
	var inventory parityInventory
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	require.NoError(t, decoder.Decode(&inventory))
	require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
	return inventory
}

func sha256Text(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validateParityInventory(inventory parityInventory) error {
	if inventory.SchemaVersion != 1 || inventory.Source.Commit != paritySourceCommit ||
		inventory.Source.Repository != "microsoft-foundry/content-understanding-cli" {
		return fmt.Errorf("unsupported fixture schema or source revision")
	}
	for name, asset := range map[string]parityAsset{
		"hygiene_scenarios": inventory.Hygiene, "normalized_adapter": inventory.Normalized,
		"generated_projects": inventory.GeneratedProjects, "core_show_bindings": inventory.CoreShowBindings,
		"core_create_binding": inventory.CoreCreateBinding,
		"core_test_binding":   inventory.CoreTestBinding, "core_validate_binding": inventory.CoreValidateBinding,
		"core_copy_binding": inventory.CoreCopyBinding,
	} {
		if asset.File != "testdata/parity/"+name+".json" {
			return fmt.Errorf("invalid fixture path for %s", name)
		}
		body, err := os.ReadFile(filepath.Join("../..", asset.File))
		if err != nil {
			return err
		}
		if sha256Text(body) != asset.SHA256 {
			return fmt.Errorf("fixture digest mismatch for %s", name)
		}
	}
	if err := validateCapabilityManifest(inventory); err != nil {
		return err
	}
	root := NewRootCommand()
	for _, row := range inventory.Capabilities {
		if row.Classification == "extension-path" {
			command, remaining, err := root.Find(strings.Fields(strings.TrimPrefix(row.Target, "azd ai cu ")))
			if err != nil || command == root || len(remaining) != 0 {
				return fmt.Errorf("missing native command %s", row.Target)
			}
		}
	}
	if err := validateScenarioManifest(inventory); err != nil {
		return err
	}
	imports := map[string]bool{}
	for _, row := range inventory.Recordings {
		if row.Name == "" || strings.ContainsAny(row.Name, `/\`) ||
			imports[row.Name] || row.File != "testdata/playback/"+row.Name+".json" {
			return fmt.Errorf("invalid/duplicate recording import")
		}
		body, err := os.ReadFile(filepath.Join("../..", row.File))
		if err != nil {
			return err
		}
		sourceDigest, err := hex.DecodeString(row.SourceSHA256)
		if err != nil || len(sourceDigest) != sha256.Size || sha256Text(body) != row.SHA256 {
			return fmt.Errorf("recording provenance digest mismatch for %s", row.Name)
		}
		imports[row.Name] = true
	}
	return nil
}

func TestParityFixtureInventory(t *testing.T) {
	require.NoError(t, validateParityInventory(loadParityInventory(t)))
}

func TestParityFixtureInventoryRejectsInvalidMetadata(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*parityInventory)
	}{
		{"source revision", func(i *parityInventory) { i.Source.Commit = "changed" }},
		{"source repository", func(i *parityInventory) { i.Source.Repository = "changed" }},
		{"schema version", func(i *parityInventory) { i.SchemaVersion++ }},
		{"asset path", func(i *parityInventory) { i.Hygiene.File = "../outside.json" }},
		{"asset digest", func(i *parityInventory) { i.Normalized.SHA256 = "changed" }},
		{"recording path", func(i *parityInventory) { i.Recordings[0].File = "../outside.json" }},
		{"recording name", func(i *parityInventory) { i.Recordings[0].Name = "../outside" }},
		{"recording digest", func(i *parityInventory) { i.Recordings[0].SHA256 = "changed" }},
		{"source digest", func(i *parityInventory) { i.Recordings[0].SourceSHA256 = strings.Repeat("x", 64) }},
		{"duplicate import", func(i *parityInventory) { i.Recordings = append(i.Recordings, i.Recordings[0]) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			inventory := loadParityInventory(t)
			test.change(&inventory)
			require.Error(t, validateParityInventory(inventory))
		})
	}
}
