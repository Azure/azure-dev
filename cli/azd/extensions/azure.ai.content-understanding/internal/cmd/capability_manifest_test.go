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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

const parityCapabilitySnapshotSHA256 = "7573f107038183b21cac2698f3d454823f62c8ed83bcc2a049d2ed391cd71fed"

type parityCapabilityContract struct {
	ID             int      `yaml:"id"`
	SourcePaths    []string `yaml:"source_paths"`
	Target         string   `yaml:"target"`
	Classification string   `yaml:"classification"`
}

func loadCapabilityManifest(inventory parityInventory) ([]parityCapabilityContract, error) {
	ref := inventory.CapabilitySource
	if ref.File != "testdata/parity/source_capabilities.yaml" ||
		ref.SourcePath != "tests/parity/capability_dispositions.yaml" || ref.License != "MIT" ||
		ref.SHA256 != parityCapabilitySnapshotSHA256 {
		return nil, fmt.Errorf("invalid pinned source capability reference")
	}
	body, err := os.ReadFile(filepath.Join("../..", ref.File))
	if err != nil {
		return nil, err
	}
	if sha256Text(body) != parityCapabilitySnapshotSHA256 {
		return nil, fmt.Errorf("source capability snapshot digest mismatch")
	}
	var document struct {
		SchemaVersion int                        `yaml:"schema_version"`
		Capabilities  []parityCapabilityContract `yaml:"capabilities"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("source capability snapshot has trailing YAML data")
	}
	if document.SchemaVersion != 1 || len(document.Capabilities) != 29 {
		return nil, fmt.Errorf("source capability schema/count changed")
	}
	return document.Capabilities, nil
}

func validateCapabilityContract(actual parityCapability, expected parityCapabilityContract) error {
	if actual.ID != expected.ID || actual.Target != expected.Target || actual.Classification != expected.Classification ||
		!slices.Equal(actual.SourcePaths, expected.SourcePaths) {
		return fmt.Errorf("capability %d differs from the pinned four-field source contract", expected.ID)
	}
	return nil
}

func validateCapabilityManifest(inventory parityInventory) error {
	source, err := loadCapabilityManifest(inventory)
	if err != nil {
		return err
	}
	if len(inventory.Capabilities) != len(source) {
		return fmt.Errorf("source capability inventory is incomplete")
	}
	for index, expected := range source {
		if err := validateCapabilityContract(inventory.Capabilities[index], expected); err != nil {
			return err
		}
	}
	return nil
}

func TestSourceCapabilityInventory(t *testing.T) {
	inventory := loadParityInventory(t)
	require.NoError(t, validateCapabilityManifest(inventory))
	classifications := map[string]int{}
	for index, row := range inventory.Capabilities {
		require.Equal(t, index+1, row.ID)
		classifications[row.Classification]++
	}
	require.Equal(t, map[string]int{
		"extension-path": 16, "environment-mapping": 11, "removed-helper": 1, "host-owned-update": 1,
	}, classifications)
}

func TestSourceCapabilityUniqueness(t *testing.T) {
	inventory := loadParityInventory(t)
	require.NoError(t, validateCapabilityManifest(inventory))
	sources, targets := map[string]bool{}, map[string]bool{}
	for _, row := range inventory.Capabilities {
		for _, path := range row.SourcePaths {
			require.False(t, sources[path], path)
			sources[path] = true
		}
		if row.Classification == "extension-path" {
			require.False(t, targets[row.Target], row.Target)
			targets[row.Target] = true
		}
	}
	require.Len(t, targets, 16)
}

func TestSourceCapabilityContracts(t *testing.T) {
	inventory := loadParityInventory(t)
	source, err := loadCapabilityManifest(inventory)
	require.NoError(t, err)
	require.Len(t, inventory.Capabilities, len(source))
	for index, expected := range source {
		t.Run(fmt.Sprintf("row-%d", expected.ID), func(t *testing.T) {
			row := inventory.Capabilities[index]
			require.NoError(t, validateCapabilityContract(row, expected))
			require.NotEmpty(t, row.SourcePaths)
			for _, path := range row.SourcePaths {
				require.NotEmpty(t, strings.TrimSpace(path))
			}
			require.NotEmpty(t, strings.TrimSpace(row.Target))
			switch row.Classification {
			case "extension-path":
				require.True(t, strings.HasPrefix(row.Target, "azd ai cu "))
			case "environment-mapping":
				require.GreaterOrEqual(t, row.ID, 12)
				require.LessOrEqual(t, row.ID, 22)
			case "removed-helper":
				require.Equal(t, []string{"cu profile _has-values"}, row.SourcePaths)
				require.Contains(t, row.Target, "through the host")
			case "host-owned-update":
				require.Equal(t, "azd extension update azure.ai.content-understanding", row.Target)
			default:
				t.Fatalf("unsupported source capability classification")
			}
		})
	}
}

func TestSourceCapabilityManifestRejectsDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*parityInventory)
	}{
		{"snapshot digest", func(i *parityInventory) { i.CapabilitySource.SHA256 = "invalid" }},
		{"snapshot path", func(i *parityInventory) { i.CapabilitySource.File = "../source_capabilities.yaml" }},
		{"source path", func(i *parityInventory) { i.CapabilitySource.SourcePath += ".changed" }},
		{"source license", func(i *parityInventory) { i.CapabilitySource.License = "" }},
		{"missing row", func(i *parityInventory) { i.Capabilities = i.Capabilities[1:] }},
		{"extra row", func(i *parityInventory) { i.Capabilities = append(i.Capabilities, i.Capabilities[0]) }},
		{"duplicate row", func(i *parityInventory) { i.Capabilities[0] = i.Capabilities[1] }},
		{"wrong id", func(i *parityInventory) { i.Capabilities[0].ID = 30 }},
		{"reordered rows", func(i *parityInventory) {
			i.Capabilities[0], i.Capabilities[1] = i.Capabilities[1], i.Capabilities[0]
		}},
		{"renamed source path", func(i *parityInventory) { i.Capabilities[0].SourcePaths[0] += "-changed" }},
		{"blank source path", func(i *parityInventory) { i.Capabilities[0].SourcePaths[0] = " " }},
		{"padded source path", func(i *parityInventory) { i.Capabilities[0].SourcePaths[0] += " " }},
		{"missing source paths", func(i *parityInventory) { i.Capabilities[0].SourcePaths = nil }},
		{"duplicate source path", func(i *parityInventory) {
			i.Capabilities[0].SourcePaths = append(i.Capabilities[0].SourcePaths, i.Capabilities[0].SourcePaths[0])
		}},
		{"duplicate cross-row source", func(i *parityInventory) {
			i.Capabilities[0].SourcePaths[0] = i.Capabilities[1].SourcePaths[0]
		}},
		{"swapped source associations", func(i *parityInventory) {
			i.Capabilities[0].SourcePaths, i.Capabilities[1].SourcePaths =
				i.Capabilities[1].SourcePaths, i.Capabilities[0].SourcePaths
		}},
		{"misrouted extension target", func(i *parityInventory) { i.Capabilities[0].Target = i.Capabilities[1].Target }},
		{"changed host target", func(i *parityInventory) { i.Capabilities[11].Target = "unrelated host mapping" }},
		{"blank host target", func(i *parityInventory) { i.Capabilities[11].Target = " " }},
		{"changed helper source", func(i *parityInventory) { i.Capabilities[22].SourcePaths[0] = "unrelated helper" }},
		{"changed host update target", func(i *parityInventory) { i.Capabilities[28].Target += ".changed" }},
		{"wrong classification", func(i *parityInventory) { i.Capabilities[0].Classification = "environment-mapping" }},
		{"reordered aliases", func(i *parityInventory) {
			row := &i.Capabilities[26]
			row.SourcePaths[0], row.SourcePaths[1] = row.SourcePaths[1], row.SourcePaths[0]
		}},
		{"missing alias", func(i *parityInventory) { i.Capabilities[26].SourcePaths = i.Capabilities[26].SourcePaths[:1] }},
	} {
		t.Run(test.name, func(t *testing.T) {
			inventory := loadParityInventory(t)
			test.change(&inventory)
			require.Error(t, validateCapabilityManifest(inventory))
			require.Error(t, validateParityInventory(inventory))
		})
	}
}
