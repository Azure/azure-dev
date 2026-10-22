// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

const parityScenarioSnapshotSHA256 = "406e6e9d960c26ab88c233c2c0608407030f79d1fc8fb4bc3680f5294eeecd24"

type parityScenarioContract struct {
	ID         int      `yaml:"id"`
	Name       string   `yaml:"name"`
	Kind       string   `yaml:"kind"`
	Mode       string   `yaml:"mode"`
	Recordings []string `yaml:"recordings"`
}

func loadScenarioManifest(inventory parityInventory) ([]parityScenarioContract, error) {
	ref := inventory.ScenarioSource
	if ref.File != "testdata/parity/source_scenarios.yaml" ||
		ref.SourcePath != "tests/playback/scenarios/scenarios.yaml" || ref.License != "MIT" ||
		ref.SHA256 != parityScenarioSnapshotSHA256 {
		return nil, fmt.Errorf("invalid pinned source scenario reference")
	}
	body, err := os.ReadFile(filepath.Join("../..", ref.File))
	if err != nil {
		return nil, err
	}
	if sha256Text(body) != parityScenarioSnapshotSHA256 {
		return nil, fmt.Errorf("source scenario snapshot digest mismatch")
	}
	var document struct {
		SchemaVersion int                      `yaml:"schema_version"`
		Scenarios     []parityScenarioContract `yaml:"scenarios"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("source scenario snapshot has trailing YAML data")
	}
	if document.SchemaVersion != 1 || len(document.Scenarios) != 27 {
		return nil, fmt.Errorf("source scenario schema/count changed")
	}
	return document.Scenarios, nil
}

func validateScenarioContract(actual parityScenario, expected parityScenarioContract) error {
	if actual.ID != expected.ID || actual.Name != expected.Name ||
		actual.Kind != expected.Kind || actual.Mode != expected.Mode ||
		!slices.Equal(actual.Recordings, expected.Recordings) {
		return fmt.Errorf("scenario %d differs from the pinned five-field source contract", expected.ID)
	}
	return nil
}

func validateScenarioOwnership(source []parityScenarioContract, imports []parityRecording) error {
	owned := map[string]bool{}
	for _, row := range source {
		for _, name := range row.Recordings {
			owned[name] = true
		}
	}
	expected := make([]string, 0, len(owned))
	for name := range owned {
		expected = append(expected, name+".yaml")
	}
	files, err := filepath.Glob("../../testdata/playback/*.json")
	if err != nil {
		return err
	}
	return recordedImportCompleteness(expected, imports, files)
}

func validateScenarioManifest(inventory parityInventory) error {
	source, err := loadScenarioManifest(inventory)
	if err != nil {
		return err
	}
	if len(inventory.Scenarios) != len(source) {
		return fmt.Errorf("source scenario inventory is incomplete")
	}
	for index, expected := range source {
		if err := validateScenarioContract(inventory.Scenarios[index], expected); err != nil {
			return err
		}
	}
	return validateScenarioOwnership(source, inventory.Recordings)
}

func TestRecordedScenarioInventory(t *testing.T) {
	inventory := loadParityInventory(t)
	require.NoError(t, validateScenarioManifest(inventory))
	kinds, modes, names := map[string]int{}, map[string]int{}, map[string]bool{}
	for index, row := range inventory.Scenarios {
		require.Equal(t, index+1, row.ID)
		require.False(t, names[row.Name])
		names[row.Name] = true
		kinds[row.Kind]++
		modes[row.Mode]++
	}
	require.Len(t, names, 27)
	require.Equal(t, map[string]int{"command": 16, "hygiene": 11}, kinds)
	require.Equal(t, map[string]int{"playback": 14, "live-only": 2, "offline": 11}, modes)
}

func TestRecordedScenarioOwnership(t *testing.T) {
	inventory := loadParityInventory(t)
	source, err := loadScenarioManifest(inventory)
	require.NoError(t, err)
	require.NoError(t, validateScenarioOwnership(source, inventory.Recordings))
}

func TestRecordedScenarioContracts(t *testing.T) {
	inventory := loadParityInventory(t)
	source, err := loadScenarioManifest(inventory)
	require.NoError(t, err)
	require.Len(t, inventory.Scenarios, len(source))
	for index, expected := range source {
		t.Run(expected.Name, func(t *testing.T) {
			require.NoError(t, validateScenarioContract(inventory.Scenarios[index], expected))
			for _, name := range expected.Recordings {
				_ = loadRecordedFixture(t, name)
			}
		})
	}
}

func TestRecordedScenarioManifestRejectsDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*parityInventory)
	}{
		{"snapshot digest", func(i *parityInventory) { i.ScenarioSource.SHA256 = "invalid" }},
		{"snapshot path", func(i *parityInventory) { i.ScenarioSource.File = "../source_scenarios.yaml" }},
		{"source path", func(i *parityInventory) { i.ScenarioSource.SourcePath += ".changed" }},
		{"source license", func(i *parityInventory) { i.ScenarioSource.License = "" }},
		{"missing row", func(i *parityInventory) { i.Scenarios = i.Scenarios[1:] }},
		{"duplicate row", func(i *parityInventory) { i.Scenarios[0] = i.Scenarios[1] }},
		{"wrong id", func(i *parityInventory) { i.Scenarios[0].ID = 28 }},
		{"wrong kind", func(i *parityInventory) { i.Scenarios[0].Kind = "hygiene" }},
		{"wrong mode", func(i *parityInventory) { i.Scenarios[0].Mode = "live-only" }},
		{"reordered rows", func(i *parityInventory) { i.Scenarios[0], i.Scenarios[1] = i.Scenarios[1], i.Scenarios[0] }},
		{"renamed scenario", func(i *parityInventory) { i.Scenarios[0].Name += "-changed" }},
		{"blank name", func(i *parityInventory) { i.Scenarios[0].Name = " " }},
		{"padded name", func(i *parityInventory) { i.Scenarios[0].Name += " " }},
		{"renamed live-only scenario", func(i *parityInventory) { i.Scenarios[7].Name = "unrelated-live-case" }},
		{"duplicate cassette", func(i *parityInventory) {
			i.Scenarios[3].Recordings = append(i.Scenarios[3].Recordings, i.Scenarios[3].Recordings[0])
		}},
		{"swapped cassette ownership", func(i *parityInventory) {
			i.Scenarios[3].Recordings, i.Scenarios[13].Recordings = i.Scenarios[13].Recordings, i.Scenarios[3].Recordings
		}},
		{"reordered route cassettes", func(i *parityInventory) {
			row := &i.Scenarios[11]
			row.Recordings[0], row.Recordings[1] = row.Recordings[1], row.Recordings[0]
		}},
		{"scenario references missing cassette", func(i *parityInventory) {
			i.Scenarios[15].Recordings = []string{"missing"}
		}},
		{"unowned import", func(i *parityInventory) { i.Recordings = i.Recordings[1:] }},
		{"live-only recording", func(i *parityInventory) { i.Scenarios[7].Recordings = []string{"analyzer_create"} }},
		{"offline recording", func(i *parityInventory) { i.Scenarios[16].Recordings = []string{"analyzer_create"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			inventory := loadParityInventory(t)
			test.change(&inventory)
			require.Error(t, validateScenarioManifest(inventory))
			require.Error(t, validateParityInventory(inventory))
		})
	}
}
