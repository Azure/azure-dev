// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

const paritySourceCommit = "25d39b30116a840491b7b6f393d6db792b38d4a4"
const parityCaseSnapshotSHA256 = "727ab1a6dc1afd6320d03eb3bd34e5fe756d5ca8a546dd8c3e6570491225f206"

type parityCase struct {
	Source      string   `yaml:"source"`
	State       string   `yaml:"state"`
	Disposition string   `yaml:"disposition"`
	Rationale   string   `yaml:"rationale"`
	Approval    string   `yaml:"approval"`
	GoTests     []string `yaml:"go_tests"`
}

type parityCapability struct {
	ID             int      `yaml:"id"`
	SourcePaths    []string `yaml:"source_paths"`
	Target         string   `yaml:"target"`
	Classification string   `yaml:"classification"`
	State          string   `yaml:"state"`
	Approval       string   `yaml:"approval"`
}

type parityScenario struct {
	ID         int      `yaml:"id"`
	Name       string   `yaml:"name"`
	Kind       string   `yaml:"kind"`
	Mode       string   `yaml:"mode"`
	Recordings []string `yaml:"recordings"`
	State      string   `yaml:"state"`
	Approval   string   `yaml:"approval"`
}

type parityRecording struct {
	Name         string `yaml:"name"`
	File         string `yaml:"file"`
	SHA256       string `yaml:"sha256"`
	SourceSHA256 string `yaml:"source_sha256"`
}

type parityInventory struct {
	SchemaVersion int `yaml:"schema_version"`
	Source        struct {
		Repository     string `yaml:"repository"`
		Commit         string `yaml:"commit"`
		CaseCount      int    `yaml:"case_count"`
		ModuleCount    int    `yaml:"module_count"`
		CaseListSHA256 string `yaml:"case_list_sha256"`
		Collection     string `yaml:"collection"`
	} `yaml:"source"`
	Readiness string `yaml:"readiness"`
	Hygiene   struct {
		File   string `yaml:"file"`
		SHA256 string `yaml:"sha256"`
	} `yaml:"hygiene"`
	ScenarioSource struct {
		File       string `yaml:"file"`
		SHA256     string `yaml:"sha256"`
		SourcePath string `yaml:"source_path"`
		License    string `yaml:"license"`
	} `yaml:"scenario_source"`
	CapabilitySource struct {
		File       string `yaml:"file"`
		SHA256     string `yaml:"sha256"`
		SourcePath string `yaml:"source_path"`
		License    string `yaml:"license"`
	} `yaml:"capability_source"`
	Normalized struct {
		File   string `yaml:"file"`
		SHA256 string `yaml:"sha256"`
	} `yaml:"normalized"`
	GeneratedProjects struct {
		File   string `yaml:"file"`
		SHA256 string `yaml:"sha256"`
	} `yaml:"generated_projects"`
	CoreShowBindings struct {
		File   string `yaml:"file"`
		SHA256 string `yaml:"sha256"`
	} `yaml:"core_show_bindings"`
	Capabilities []parityCapability `yaml:"capabilities"`
	Scenarios    []parityScenario   `yaml:"scenarios"`
	Recordings   []parityRecording  `yaml:"recordings"`
	Cases        []parityCase       `yaml:"cases"`
}

func loadParityInventory(t *testing.T) parityInventory {
	t.Helper()
	file, err := os.Open("../../testdata/parity/pr2_test_inventory.yaml")
	require.NoError(t, err)
	defer file.Close()
	var inventory parityInventory
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	require.NoError(t, decoder.Decode(&inventory))
	return inventory
}

func sha256Text(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func parityState(state, approval string) error {
	switch state {
	case "pending", "evidenced":
		if approval != "" {
			return fmt.Errorf("non-approved row carries approval")
		}
	case "approved":
		if strings.TrimSpace(approval) == "" {
			return fmt.Errorf("approved row lacks review evidence")
		}
	default:
		return fmt.Errorf("unknown parity state %q", state)
	}
	return nil
}

func validateParityInventory(inventory parityInventory) error {
	if inventory.SchemaVersion != 1 || inventory.Source.Commit != paritySourceCommit ||
		inventory.Source.Repository != "microsoft-foundry/content-understanding-cli" {
		return fmt.Errorf("unsupported parity schema or source revision")
	}
	if inventory.Source.CaseCount != 797 || len(inventory.Cases) != inventory.Source.CaseCount {
		return fmt.Errorf("source case count differs from pinned collection")
	}
	if err := validateCapabilityManifest(inventory); err != nil {
		return err
	}
	nodes, modules := map[string]bool{}, map[string]bool{}
	ordered := make([]string, 0, len(inventory.Cases))
	for _, row := range inventory.Cases {
		if nodes[row.Source] {
			return fmt.Errorf("duplicate source case %s", row.Source)
		}
		parts := strings.SplitN(row.Source, "::", 2)
		if len(parts) != 2 || !strings.HasSuffix(parts[0], ".py") || !strings.HasPrefix(parts[1], "test_") {
			return fmt.Errorf("invalid source node %s", row.Source)
		}
		nodes[row.Source], modules[parts[0]] = true, true
		ordered = append(ordered, row.Source)
		if err := parityState(row.State, row.Approval); err != nil {
			return fmt.Errorf("%s: %w", row.Source, err)
		}
		if row.State == "pending" {
			if row.Disposition != "" || len(row.GoTests) != 0 {
				return fmt.Errorf("pending case has undeclared mapping %s", row.Source)
			}
			continue
		}
		if row.Rationale == "" || !slices.Contains([]string{"equivalent", "difference", "not-applicable"}, row.Disposition) {
			return fmt.Errorf("mapped case lacks disposition/rationale %s", row.Source)
		}
		if row.Disposition != "not-applicable" && len(row.GoTests) == 0 {
			return fmt.Errorf("mapped case lacks Go evidence %s", row.Source)
		}
		for _, reference := range row.GoTests {
			if err := validateGoTestReference(reference, inventory); err != nil {
				return fmt.Errorf("%s: %w", row.Source, err)
			}
		}
	}
	slices.Sort(ordered)
	if inventory.Source.CaseListSHA256 != parityCaseSnapshotSHA256 ||
		sha256Text([]byte(strings.Join(ordered, "\n")+"\n")) != inventory.Source.CaseListSHA256 ||
		len(modules) != inventory.Source.ModuleCount || len(modules) != 65 {
		return fmt.Errorf("source case/module snapshot is stale")
	}
	if len(inventory.Capabilities) != 29 || len(inventory.Scenarios) != 27 {
		return fmt.Errorf("capability/scenario inventory is incomplete")
	}
	capabilities, scenarioIDs, scenarioNames := map[int]bool{}, map[int]bool{}, map[string]bool{}
	root := NewRootCommand()
	for _, row := range inventory.Capabilities {
		if row.ID < 1 || row.ID > 29 || capabilities[row.ID] || len(row.SourcePaths) == 0 {
			return fmt.Errorf("invalid/duplicate capability %d", row.ID)
		}
		capabilities[row.ID] = true
		if err := parityState(row.State, row.Approval); err != nil {
			return err
		}
		switch row.Classification {
		case "extension-path":
			if !(row.ID <= 11 || row.ID >= 24 && row.ID <= 28) {
				return fmt.Errorf("host capability incorrectly exposed as native")
			}
			command, remaining, err := root.Find(strings.Fields(strings.TrimPrefix(row.Target, "azd ai cu ")))
			if err != nil || command == root || len(remaining) != 0 || !command.Runnable() {
				return fmt.Errorf("missing native command %s", row.Target)
			}
		case "environment-mapping", "removed-helper", "host-owned-update":
			if row.Target == "" {
				return fmt.Errorf("capability lacks host disposition")
			}
			if !(row.ID >= 12 && row.ID <= 22 && row.Classification == "environment-mapping" ||
				row.ID == 23 && row.Classification == "removed-helper" ||
				row.ID == 29 && row.Classification == "host-owned-update") {
				return fmt.Errorf("native capability incorrectly marked host-owned")
			}
		default:
			return fmt.Errorf("unknown capability classification")
		}
	}
	if inventory.Hygiene.File != "testdata/parity/hygiene_scenarios.json" {
		return fmt.Errorf("invalid hygiene source-control path")
	}
	hygiene, err := os.ReadFile(filepath.Join("../..", inventory.Hygiene.File))
	if err != nil {
		return err
	}
	if sha256Text(hygiene) != inventory.Hygiene.SHA256 {
		return fmt.Errorf("hygiene source-control digest mismatch")
	}
	if inventory.Normalized.File != "testdata/parity/normalized_adapter.json" {
		return fmt.Errorf("invalid normalized adapter oracle path")
	}
	normalized, err := os.ReadFile(filepath.Join("../..", inventory.Normalized.File))
	if err != nil {
		return err
	}
	if sha256Text(normalized) != inventory.Normalized.SHA256 {
		return fmt.Errorf("normalized adapter oracle digest mismatch")
	}
	if inventory.GeneratedProjects.File != "testdata/parity/generated_projects.json" {
		return fmt.Errorf("invalid generated project oracle path")
	}
	generated, err := os.ReadFile(filepath.Join("../..", inventory.GeneratedProjects.File))
	if err != nil {
		return err
	}
	if sha256Text(generated) != inventory.GeneratedProjects.SHA256 {
		return fmt.Errorf("generated project oracle digest mismatch")
	}
	if inventory.CoreShowBindings.File != "testdata/parity/core_show_bindings.json" {
		return fmt.Errorf("invalid core show binding oracle path")
	}
	bindings, err := os.ReadFile(filepath.Join("../..", inventory.CoreShowBindings.File))
	if err != nil {
		return err
	}
	if sha256Text(bindings) != inventory.CoreShowBindings.SHA256 {
		return fmt.Errorf("core show binding oracle digest mismatch")
	}
	if err := validateScenarioManifest(inventory); err != nil {
		return err
	}
	imports := map[string]bool{}
	for _, row := range inventory.Recordings {
		if imports[row.Name] || row.Name == "" || row.File != "testdata/playback/"+row.Name+".json" {
			return fmt.Errorf("invalid/duplicate recording import")
		}
		content, err := os.ReadFile(filepath.Join("../..", row.File))
		if err != nil {
			return err
		}
		if sha256Text(content) != row.SHA256 || len(row.SourceSHA256) != 64 {
			return fmt.Errorf("recording provenance digest mismatch for %s", row.Name)
		}
		imports[row.Name] = true
	}
	sourceRecordings := map[string]bool{}
	for _, row := range inventory.Scenarios {
		if row.ID < 1 || row.ID > 27 || scenarioIDs[row.ID] || scenarioNames[row.Name] || row.Name == "" {
			return fmt.Errorf("invalid/duplicate scenario")
		}
		scenarioIDs[row.ID], scenarioNames[row.Name] = true, true
		if err := parityState(row.State, row.Approval); err != nil {
			return err
		}
		switch {
		case row.ID == 8 || row.ID == 9:
			if row.Kind != "command" || row.Mode != "live-only" || len(row.Recordings) != 0 {
				return fmt.Errorf("copy live-only disposition changed")
			}
		case row.ID >= 17:
			if row.Kind != "hygiene" || row.Mode != "offline" || len(row.Recordings) != 0 {
				return fmt.Errorf("hygiene disposition changed")
			}
		default:
			if row.Kind != "command" || row.Mode != "playback" || len(row.Recordings) == 0 {
				return fmt.Errorf("recorded scenario lacks fixtures")
			}
		}
		for _, name := range row.Recordings {
			sourceRecordings[name] = true
			if row.State != "pending" && !imports[name] {
				return fmt.Errorf("scenario claims evidence without imported recording")
			}
		}
	}
	if len(sourceRecordings) != 19 {
		return fmt.Errorf("source cassette ownership is incomplete")
	}
	return nil
}

func validateGoTestReference(reference string, inventory parityInventory) error {
	file, name, found := strings.Cut(reference, "::")
	if !found || path.Clean(file) != file || !strings.HasPrefix(file, "internal/") || strings.Contains(file, `\`) ||
		path.IsAbs(file) || !strings.HasSuffix(file, "_test.go") {
		return fmt.Errorf("invalid Go test reference %s", reference)
	}
	tree, err := parser.ParseFile(token.NewFileSet(), filepath.Join("../..", file), nil, 0)
	if err != nil {
		return err
	}
	function, subtest, hasSubtest := strings.Cut(name, "/")
	if hasSubtest {
		scenario, format, hasFormat := strings.Cut(subtest, "/")
		if hasFormat && format != "default" && format != "json" && format != "none" {
			return fmt.Errorf("recorded output evidence does not exist: %s", reference)
		}
		exists := false
		switch {
		case file == "internal/cmd/core_show_parity_test.go" && function == "TestCoreShowBindingParity":
			exists = !hasFormat && slices.Contains(coreShowNames, scenario)
		case file == "internal/cmd/capability_manifest_test.go" && function == "TestSourceCapabilityContracts":
			for _, capability := range inventory.Capabilities {
				exists = exists || !hasFormat && scenario == fmt.Sprintf("row-%d", capability.ID)
			}
		case file == "internal/cmd/generated_project_parity_test.go" &&
			(function == "TestGeneratedProjectTreeParity" || function == "TestGeneratedBashHookParity"):
			exists = !hasFormat && slices.Contains(generatedProjectFrontends, scenario)
		case file == "internal/cmd/normalized_parity_test.go" && function == "TestNormalizedAdapterParity":
			exists = !hasFormat && slices.Contains(normalizedParityNames, scenario)
		case file == "internal/cmd/playback_test.go" && function == "TestRecordedFixtureHygiene":
			exists = !hasFormat && slices.ContainsFunc(inventory.Recordings, func(row parityRecording) bool {
				return row.Name == scenario
			})
		case file == "internal/cmd/scenario_manifest_test.go" && function == "TestRecordedScenarioContracts":
			exists = !hasFormat && slices.ContainsFunc(inventory.Scenarios, func(row parityScenario) bool {
				return row.Name == scenario
			})
		case file == "internal/cmd/playback_test.go" && function == "TestRecordedServiceCommands":
			for _, row := range recordedCommandCases {
				exists = exists || row.name == scenario
			}
		case file == "internal/cmd/analysis_playback_test.go" && function == "TestRecordedAnalysisCommands":
			for _, row := range recordedAnalysisCases {
				exists = exists || row.name == scenario
			}
		case file == "internal/cmd/analyzer_playback_test.go" && function == "TestRecordedAnalyzerLifecycle":
			for _, row := range recordedLifecycleCases {
				exists = exists || row.name == scenario
			}
		case file == "internal/cmd/analyzer_testing_playback_test.go" && function == "TestRecordedAnalyzerFieldReport":
			for _, row := range recordedFieldReportCases {
				exists = exists || row.name == scenario
			}
		case file == "internal/cmd/template_classifier_playback_test.go" &&
			function == "TestRecordedTemplateClassifierLifecycle":
			for _, row := range recordedTemplateCases {
				exists = exists || row.name == scenario
			}
		case file == "internal/cmd/diagnostics_schema_playback_test.go" && function == "TestRecordedDoctorCommands":
			for _, row := range recordedDoctorCases {
				exists = exists || row == scenario
			}
		case file == "internal/cmd/diagnostics_schema_playback_test.go" && function == "TestRecordedSchemaSuggestion":
			for _, row := range recordedSuggestionCases {
				exists = exists || row == scenario
			}
		case file == "internal/cmd/hygiene_playback_test.go" && function == "TestRecordedHygieneScenarios":
			exists = slices.Contains(recordedHygieneNames, scenario)
		case file == "internal/cmd/hygiene_playback_test.go" && function == "TestRecordedScrubberHelpers":
			exists = !hasFormat && slices.Contains(recordedScrubberNames, scenario)
		default:
			return fmt.Errorf("subtest evidence requires a checked scenario registry: %s", reference)
		}
		if !exists {
			return fmt.Errorf("recorded scenario evidence does not exist: %s", reference)
		}
	}
	for _, declaration := range tree.Decls {
		if declaration, ok := declaration.(*ast.FuncDecl); ok && declaration.Recv == nil &&
			declaration.Name.Name == function && strings.HasPrefix(function, "Test") {
			return nil
		}
	}
	return fmt.Errorf("Go test evidence does not exist: %s", reference)
}

func parityReadiness(inventory parityInventory) error {
	pending, evidenced, unapprovedCapabilities, unapprovedScenarios := 0, 0, 0, 0
	for _, row := range inventory.Cases {
		switch row.State {
		case "approved":
		case "evidenced":
			evidenced++
		default:
			pending++
		}
	}
	for _, row := range inventory.Capabilities {
		if row.State != "approved" {
			unapprovedCapabilities++
		}
	}
	for _, row := range inventory.Scenarios {
		if row.State != "approved" {
			unapprovedScenarios++
		}
	}
	if pending+evidenced+unapprovedCapabilities+unapprovedScenarios != 0 {
		return fmt.Errorf("parity not ready: %d pending cases, %d evidenced but unapproved cases, "+
			"%d unapproved capabilities, %d unapproved scenarios",
			pending, evidenced, unapprovedCapabilities, unapprovedScenarios)
	}
	return nil
}

func TestParityInventoryCompleteness(t *testing.T) {
	inventory := loadParityInventory(t)
	require.NoError(t, validateParityInventory(inventory))
	t.Log(parityReadiness(inventory))
}

func TestParityInventoryRejectsInvalidRows(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*parityInventory)
	}{
		{"missing case", func(i *parityInventory) { i.Cases = i.Cases[1:] }},
		{"duplicate case", func(i *parityInventory) { i.Cases[0] = i.Cases[1] }},
		{"stale case", func(i *parityInventory) { i.Cases[0].Source += "[changed]" }},
		{"rewritten source snapshot", func(i *parityInventory) {
			i.Cases[0].Source += "[changed]"
			nodes := make([]string, 0, len(i.Cases))
			for _, row := range i.Cases {
				nodes = append(nodes, row.Source)
			}
			slices.Sort(nodes)
			i.Source.CaseListSHA256 = sha256Text([]byte(strings.Join(nodes, "\n") + "\n"))
		}},
		{"unknown state", func(i *parityInventory) { i.Cases[0].State = "passing" }},
		{"unreviewed approval", func(i *parityInventory) { i.Cases[0].State = "approved" }},
		{"missing capability", func(i *parityInventory) { i.Capabilities = i.Capabilities[1:] }},
		{"incorrect host disposition", func(i *parityInventory) {
			i.Capabilities[0].Classification = "host-owned-update"
		}},
		{"missing scenario", func(i *parityInventory) { i.Scenarios = i.Scenarios[1:] }},
		{"missing native command", func(i *parityInventory) { i.Capabilities[0].Target += " missing" }},
		{"fixture drift", func(i *parityInventory) { i.Recordings[0].SHA256 = strings.Repeat("0", 64) }},
		{"hygiene drift", func(i *parityInventory) { i.Hygiene.SHA256 = strings.Repeat("0", 64) }},
		{"hygiene path", func(i *parityInventory) { i.Hygiene.File = "../hygiene.json" }},
		{"normalized digest", func(i *parityInventory) { i.Normalized.SHA256 = strings.Repeat("0", 64) }},
		{"normalized path", func(i *parityInventory) { i.Normalized.File = "../normalized_adapter.json" }},
		{"generated project digest", func(i *parityInventory) { i.GeneratedProjects.SHA256 = strings.Repeat("0", 64) }},
		{"generated project path", func(i *parityInventory) { i.GeneratedProjects.File = "../generated_projects.json" }},
		{"core show binding digest", func(i *parityInventory) { i.CoreShowBindings.SHA256 = strings.Repeat("0", 64) }},
		{"core show binding path", func(i *parityInventory) { i.CoreShowBindings.File = "../core_show_bindings.json" }},
		{"missing test", func(i *parityInventory) {
			i.Cases[0].State, i.Cases[0].Disposition, i.Cases[0].Rationale = "evidenced", "equivalent", "fixture"
			i.Cases[0].GoTests = []string{"internal/cmd/root_test.go::TestDoesNotExist"}
		}},
		{"missing subtest", func(i *parityInventory) {
			i.Cases[0].State, i.Cases[0].Disposition, i.Cases[0].Rationale = "evidenced", "equivalent", "fixture"
			i.Cases[0].GoTests = []string{"internal/cmd/playback_test.go::TestRecordedServiceCommands/missing"}
		}},
		{"missing fixture subtest", func(i *parityInventory) {
			i.Cases[0].State, i.Cases[0].Disposition, i.Cases[0].Rationale = "evidenced", "difference", "fixture"
			i.Cases[0].GoTests = []string{"internal/cmd/playback_test.go::TestRecordedFixtureHygiene/missing"}
		}},
		{"fixture subtest format", func(i *parityInventory) {
			i.Cases[0].State, i.Cases[0].Disposition, i.Cases[0].Rationale = "evidenced", "difference", "fixture"
			i.Cases[0].GoTests = []string{"internal/cmd/playback_test.go::TestRecordedFixtureHygiene/analyzer_list/json"}
		}},
		{"missing scrubber helper", func(i *parityInventory) {
			i.Cases[0].State, i.Cases[0].Disposition, i.Cases[0].Rationale = "evidenced", "difference", "fixture"
			i.Cases[0].GoTests = []string{"internal/cmd/hygiene_playback_test.go::TestRecordedScrubberHelpers/missing"}
		}},
		{"scrubber helper format", func(i *parityInventory) {
			i.Cases[0].State, i.Cases[0].Disposition, i.Cases[0].Rationale = "evidenced", "difference", "fixture"
			i.Cases[0].GoTests = []string{
				"internal/cmd/hygiene_playback_test.go::TestRecordedScrubberHelpers/scrub-response-text/json",
			}
		}},
		{"missing manifest subtest", func(i *parityInventory) {
			i.Cases[0].State, i.Cases[0].Disposition, i.Cases[0].Rationale = "evidenced", "equivalent", "fixture"
			i.Cases[0].GoTests = []string{"internal/cmd/scenario_manifest_test.go::TestRecordedScenarioContracts/missing"}
		}},
		{"manifest subtest format", func(i *parityInventory) {
			i.Cases[0].State, i.Cases[0].Disposition, i.Cases[0].Rationale = "evidenced", "equivalent", "fixture"
			i.Cases[0].GoTests = []string{
				"internal/cmd/scenario_manifest_test.go::TestRecordedScenarioContracts/doctor-connectivity/json",
			}
		}},
		{"missing normalized subtest", func(i *parityInventory) {
			i.Cases[0].State, i.Cases[0].Disposition, i.Cases[0].Rationale = "evidenced", "equivalent", "fixture"
			i.Cases[0].GoTests = []string{"internal/cmd/normalized_parity_test.go::TestNormalizedAdapterParity/missing"}
		}},
		{"normalized subtest format", func(i *parityInventory) {
			i.Cases[0].State, i.Cases[0].Disposition, i.Cases[0].Rationale = "evidenced", "difference", "fixture"
			i.Cases[0].GoTests = []string{"internal/cmd/normalized_parity_test.go::TestNormalizedAdapterParity/list/json"}
		}},
		{"missing generated project frontend", func(i *parityInventory) {
			i.Cases[0].State, i.Cases[0].Disposition, i.Cases[0].Rationale = "evidenced", "equivalent", "fixture"
			i.Cases[0].GoTests = []string{
				"internal/cmd/generated_project_parity_test.go::TestGeneratedProjectTreeParity/missing",
			}
		}},
		{"generated hook subtest format", func(i *parityInventory) {
			i.Cases[0].State, i.Cases[0].Disposition, i.Cases[0].Rationale = "evidenced", "equivalent", "fixture"
			i.Cases[0].GoTests = []string{
				"internal/cmd/generated_project_parity_test.go::TestGeneratedBashHookParity/core/json",
			}
		}},
		{"missing source capability subtest", func(i *parityInventory) {
			i.Cases[0].State, i.Cases[0].Disposition, i.Cases[0].Rationale = "evidenced", "equivalent", "fixture"
			i.Cases[0].GoTests = []string{
				"internal/cmd/capability_manifest_test.go::TestSourceCapabilityContracts/row-30",
			}
		}},
		{"source capability subtest format", func(i *parityInventory) {
			i.Cases[0].State, i.Cases[0].Disposition, i.Cases[0].Rationale = "evidenced", "equivalent", "fixture"
			i.Cases[0].GoTests = []string{
				"internal/cmd/capability_manifest_test.go::TestSourceCapabilityContracts/row-1/json",
			}
		}},
		{"padded source capability subtest", func(i *parityInventory) {
			i.Cases[0].State, i.Cases[0].Disposition, i.Cases[0].Rationale = "evidenced", "equivalent", "fixture"
			i.Cases[0].GoTests = []string{
				"internal/cmd/capability_manifest_test.go::TestSourceCapabilityContracts/row-01",
			}
		}},
		{"missing core show binding subtest", func(i *parityInventory) {
			i.Cases[0].State, i.Cases[0].Disposition, i.Cases[0].Rationale = "evidenced", "difference", "fixture"
			i.Cases[0].GoTests = []string{"internal/cmd/core_show_parity_test.go::TestCoreShowBindingParity/missing"}
		}},
		{"core show binding subtest format", func(i *parityInventory) {
			i.Cases[0].State, i.Cases[0].Disposition, i.Cases[0].Rationale = "evidenced", "difference", "fixture"
			i.Cases[0].GoTests = []string{"internal/cmd/core_show_parity_test.go::TestCoreShowBindingParity/show-forms/json"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			inventory := loadParityInventory(t)
			test.change(&inventory)
			require.Error(t, validateParityInventory(inventory))
		})
	}
}

func TestParityReadinessDoesNotApproveEvidence(t *testing.T) {
	inventory := loadParityInventory(t)
	require.ErrorContains(t, parityReadiness(inventory), "pending cases")
	for index := range inventory.Cases {
		inventory.Cases[index].State = "evidenced"
	}
	require.ErrorContains(t, parityReadiness(inventory), "797 evidenced but unapproved cases")
	require.Error(t, validateGoTestReference("../outside_test.go::TestUnsafe", inventory))
}

func TestParityReadinessRequiresAllApprovals(t *testing.T) {
	inventory := loadParityInventory(t)
	for index := range inventory.Cases {
		inventory.Cases[index].State = "approved"
	}
	require.ErrorContains(t, parityReadiness(inventory), "29 unapproved capabilities")
	for index := range inventory.Capabilities {
		inventory.Capabilities[index].State = "approved"
	}
	require.ErrorContains(t, parityReadiness(inventory), "27 unapproved scenarios")
	for index := range inventory.Scenarios {
		inventory.Scenarios[index].State = "approved"
	}
	require.NoError(t, parityReadiness(inventory))
	inventory.Cases[0].State = "invalid"
	require.ErrorContains(t, parityReadiness(inventory), "1 pending cases")
}
