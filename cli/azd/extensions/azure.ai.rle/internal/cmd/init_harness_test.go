// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestRleInitTargetOptionsGateHarnessChoices(t *testing.T) {
	withoutHarnesses := rleInitTargetOptions(false)
	if len(withoutHarnesses) != 1 ||
		withoutHarnesses[0].target != gymOpenEnvInitTarget ||
		withoutHarnesses[0].label != "Gym: OpenEnv" {
		t.Fatalf("expected only Gym: OpenEnv by default, got %#v", withoutHarnesses)
	}

	withHarnesses := rleInitTargetOptions(true)
	if len(withHarnesses) != 3 {
		t.Fatalf("expected three init choices with harness preview enabled, got %#v", withHarnesses)
	}
	if withHarnesses[1].target != (rleInitTarget{
		rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeHostedAgent,
	}) || withHarnesses[1].label != "Harness: HostedAgent" {
		t.Fatalf("unexpected HostedAgent choice: %#v", withHarnesses[1])
	}
	if withHarnesses[2].target != (rleInitTarget{
		rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeBYOH,
	}) || withHarnesses[2].label != "Harness: BYOH" {
		t.Fatalf("unexpected BYOH choice: %#v", withHarnesses[2])
	}
}

// fakeRleHarnessSampleCatalog is a test double for
// project.RleHarnessSampleCatalog that writes a minimal, working agent/+rle/
// pair without touching the network, mirroring the real samples' rle.toml
// defaults for its subtype. A nil sampleNames stands in for a samples repo
// still using the legacy flat layout.
type fakeRleHarnessSampleCatalog struct {
	subtype      project.RleSubtype
	sampleNames  []string
	copiedSample string
	closed       bool
}

func (f *fakeRleHarnessSampleCatalog) SampleNames() []string {
	return slices.Clone(f.sampleNames)
}

func (f *fakeRleHarnessSampleCatalog) Copy(
	sampleName string,
	folderName string,
	dest string,
	force bool,
) (string, error) {
	if len(f.sampleNames) == 0 && sampleName != "" {
		return "", fmt.Errorf("unexpected sample name %q for the legacy flat layout", sampleName)
	}
	if len(f.sampleNames) > 0 && !slices.Contains(f.sampleNames, sampleName) {
		return "", fmt.Errorf("unknown sample name %q", sampleName)
	}
	f.copiedSample = sampleName
	sessionDir := filepath.Join(dest, folderName)
	if err := os.MkdirAll(filepath.Join(sessionDir, "agent"), 0750); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "agent", "app.py"), []byte("# sample agent\n"), 0600); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(sessionDir, "rle"), 0750); err != nil {
		return "", err
	}
	var rleToml string
	switch f.subtype {
	case project.RleSubtypeBYOH:
		rleToml = "[rle]\n" +
			"name = \"code_repair_byoh\"\n" +
			"version = \"1.0.0\"\n" +
			"type = \"Harness\"\n" +
			"subtype = \"BYOH\"\n" +
			"baseUrl = \"https://harness.example.com/invoke\"\n"
	default:
		rleToml = "[rle]\n" +
			"name = \"code_repair_hosted_agent\"\n" +
			"version = \"1.0.0\"\n" +
			"type = \"Harness\"\n" +
			"subtype = \"HostedAgent\"\n" +
			"agentName = \"code-repair-agent\"\n" +
			"agentVersion = \"1\"\n"
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "rle", project.RleConfigFile), []byte(rleToml), 0600); err != nil {
		return "", err
	}
	return sessionDir, nil
}

func (f *fakeRleHarnessSampleCatalog) Close() error {
	f.closed = true
	return nil
}

func TestInitHostedAgentSampleSourceCopiesWorkingSample(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(rleEnableAllEnvVar, "true")

	oldSelectTarget := selectRleInitTargetFunc
	oldLoadHarnessSample := loadRleHarnessSampleCatalogFunc
	oldLoadCatalog := loadRleSampleCatalogFunc
	selectRleInitTargetFunc = func(_ context.Context, includeHarnessTypes bool) (rleInitTarget, error) {
		return rleInitTarget{
			rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeHostedAgent,
		}, nil
	}
	var closedSample *fakeRleHarnessSampleCatalog
	loadRleHarnessSampleCatalogFunc = func(subtype project.RleSubtype) (rleHarnessSampleCatalog, error) {
		if subtype != project.RleSubtypeHostedAgent {
			t.Fatalf("expected HostedAgent subtype, got %s", subtype)
		}
		sample := &fakeRleHarnessSampleCatalog{subtype: subtype}
		closedSample = sample
		return sample, nil
	}
	loadRleSampleCatalogFunc = func() (rleSampleCatalog, error) {
		t.Fatal("harness sample initialization must not load the Gym sample catalog")
		return nil, nil
	}
	t.Cleanup(func() {
		selectRleInitTargetFunc = oldSelectTarget
		loadRleHarnessSampleCatalogFunc = oldLoadHarnessSample
		loadRleSampleCatalogFunc = oldLoadCatalog
	})

	noPrompt := false
	command := newInitCommand(&noPrompt)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"my_hosted_agent"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	if closedSample == nil || !closedSample.closed {
		t.Fatal("expected the harness sample to be closed after use")
	}

	sessionDir := filepath.Join(tempDir, "my_hosted_agent")
	if _, err := os.Stat(filepath.Join(sessionDir, "agent", "app.py")); err != nil {
		t.Fatalf("expected the sample agent to be copied: %v", err)
	}
	// #nosec G304 -- the path is generated under t.TempDir by the command under test.
	config, err := os.ReadFile(filepath.Join(sessionDir, "rle", project.RleConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`name = 'my_hosted_agent'`,
		`type = 'Harness'`,
		`subtype = 'HostedAgent'`,
		`agentName = 'code-repair-agent'`,
		`agentVersion = '1'`,
	} {
		if !strings.Contains(string(config), expected) {
			t.Fatalf("expected config to contain %q, got:\n%s", expected, config)
		}
	}
	if !strings.Contains(output.String(), "Copied a working HostedAgent sample (agent + rle)") {
		t.Fatalf("expected sample-copy confirmation, got %s", output.String())
	}
	if !strings.Contains(output.String(), filepath.Join("my_hosted_agent", "rle")) {
		t.Fatalf("expected next steps to reference the rle/ subfolder, got %s", output.String())
	}
}

func TestInitHarnessSampleSelectsNamedSample(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		sampleNames  []string
		sampleFlag   string
		wantSample   string
		wantFolder   string
		wantExecFail bool
	}{
		{
			name:        "single named sample is taken without prompting",
			sampleNames: []string{"code_repair"},
			wantSample:  "code_repair",
			wantFolder:  "code_repair",
		},
		{
			name:        "flag selects among several named samples",
			sampleNames: []string{"code_repair", "web_nav"},
			sampleFlag:  "web_nav",
			wantSample:  "web_nav",
			wantFolder:  "web_nav",
		},
		{
			name:        "legacy flat layout still copies its unnamed sample",
			sampleNames: nil,
			wantSample:  "",
			wantFolder:  "byoh_sample",
		},
		{
			name:         "unknown sample name is rejected",
			sampleNames:  []string{"code_repair"},
			sampleFlag:   "nope",
			wantExecFail: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tempDir := t.TempDir()
			t.Chdir(tempDir)
			t.Setenv(rleEnableAllEnvVar, "true")

			oldSelectTarget := selectRleInitTargetFunc
			oldLoadHarnessCatalog := loadRleHarnessSampleCatalogFunc
			selectRleInitTargetFunc = func(_ context.Context, includeHarnessTypes bool) (rleInitTarget, error) {
				return rleInitTarget{
					rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeBYOH,
				}, nil
			}
			var loadedCatalog *fakeRleHarnessSampleCatalog
			loadRleHarnessSampleCatalogFunc = func(subtype project.RleSubtype) (rleHarnessSampleCatalog, error) {
				catalog := &fakeRleHarnessSampleCatalog{subtype: subtype, sampleNames: testCase.sampleNames}
				loadedCatalog = catalog
				return catalog, nil
			}
			t.Cleanup(func() {
				selectRleInitTargetFunc = oldSelectTarget
				loadRleHarnessSampleCatalogFunc = oldLoadHarnessCatalog
			})

			noPrompt := false
			command := newInitCommand(&noPrompt)
			var output bytes.Buffer
			command.SetOut(&output)
			args := []string{}
			if testCase.sampleFlag != "" {
				args = append(args, "--sample", testCase.sampleFlag)
			}
			command.SetArgs(args)
			err := command.Execute()
			if testCase.wantExecFail {
				if err == nil {
					t.Fatal("expected an unknown sample name to fail")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if loadedCatalog == nil || loadedCatalog.copiedSample != testCase.wantSample {
				t.Fatalf("expected sample %q to be copied, got %+v", testCase.wantSample, loadedCatalog)
			}
			if !loadedCatalog.closed {
				t.Fatal("expected the harness sample catalog to be closed after use")
			}
			// The sample name, not the subtype, names the session folder once
			// samples are named -- otherwise every BYOH sample would scaffold
			// into the same byoh_sample directory.
			agentPath := filepath.Join(tempDir, testCase.wantFolder, "agent", "app.py")
			if _, err := os.Stat(agentPath); err != nil {
				t.Fatalf("expected the sample agent at %s: %v", agentPath, err)
			}
		})
	}
}

func TestInitBYOHSampleSourceAppliesBaseURLOverride(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(rleEnableAllEnvVar, "true")

	oldSelectTarget := selectRleInitTargetFunc
	oldLoadHarnessSample := loadRleHarnessSampleCatalogFunc
	selectRleInitTargetFunc = func(_ context.Context, includeHarnessTypes bool) (rleInitTarget, error) {
		return rleInitTarget{
			rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeBYOH,
		}, nil
	}
	loadRleHarnessSampleCatalogFunc = func(subtype project.RleSubtype) (rleHarnessSampleCatalog, error) {
		return &fakeRleHarnessSampleCatalog{subtype: subtype}, nil
	}
	t.Cleanup(func() {
		selectRleInitTargetFunc = oldSelectTarget
		loadRleHarnessSampleCatalogFunc = oldLoadHarnessSample
	})

	noPrompt := false
	command := newInitCommand(&noPrompt)
	command.SetArgs([]string{"my_byoh"})
	if err := command.Flags().Set("base-url", "https://my-real-harness.example.com/invoke"); err != nil {
		t.Fatal(err)
	}
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	// #nosec G304 -- the path is generated under t.TempDir by the command under test.
	config, err := os.ReadFile(filepath.Join(tempDir, "my_byoh", "rle", project.RleConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), `baseUrl = 'https://my-real-harness.example.com/invoke'`) {
		t.Fatalf("expected overridden BYOH base URL, got:\n%s", config)
	}
	if !strings.Contains(string(config), `name = 'my_byoh'`) {
		t.Fatalf("expected folder name applied to config, got:\n%s", config)
	}
}

func TestInitNoPromptHarnessDefaultsToSampleSource(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(rleEnableAllEnvVar, "true")

	oldLoadHarnessSample := loadRleHarnessSampleCatalogFunc
	loadedSubtype := project.RleSubtype("")
	loadRleHarnessSampleCatalogFunc = func(subtype project.RleSubtype) (rleHarnessSampleCatalog, error) {
		loadedSubtype = subtype
		return &fakeRleHarnessSampleCatalog{subtype: subtype}, nil
	}
	t.Cleanup(func() {
		loadRleHarnessSampleCatalogFunc = oldLoadHarnessSample
	})

	noPrompt := true
	command := newInitCommand(&noPrompt)
	command.SetArgs([]string{"support_agent"})
	if err := command.Flags().Set("type", "Harness"); err != nil {
		t.Fatal(err)
	}
	if err := command.Flags().Set("subtype", "HostedAgent"); err != nil {
		t.Fatal(err)
	}
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	if loadedSubtype != project.RleSubtypeHostedAgent {
		t.Fatalf("expected the HostedAgent sample catalog to be loaded, got %q", loadedSubtype)
	}
	if _, err := os.Stat(filepath.Join(tempDir, "support_agent", "rle", project.RleConfigFile)); err != nil {
		t.Fatalf("expected --no-prompt Harness init to copy a working sample: %v", err)
	}
}

func TestInitHarnessTargetRequiresPreviewFlag(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(rleEnableAllEnvVar, "")

	noPrompt := true
	command := newInitCommand(&noPrompt)
	command.SetArgs([]string{
		"support_rle",
		"--type", "Harness",
		"--subtype", "HostedAgent",
		"--agent-name", "support-agent",
		"--agent-version", "1",
	})
	err := command.Execute()
	localError, ok := errors.AsType[*azdext.LocalError](err)
	if !ok || localError.Code != "rle_harness_init_disabled" {
		t.Fatalf("expected disabled harness init error, got %v", err)
	}
}

func TestInitSelectedHarnessTargetRequiresPreviewFlag(t *testing.T) {
	t.Setenv(rleEnableAllEnvVar, "")

	oldSelectTarget := selectRleInitTargetFunc
	selectRleInitTargetFunc = func(_ context.Context, includeHarnessTypes bool) (rleInitTarget, error) {
		if includeHarnessTypes {
			t.Fatal("expected harness init choices to be disabled")
		}
		return rleInitTarget{
			rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeHostedAgent,
		}, nil
	}
	t.Cleanup(func() {
		selectRleInitTargetFunc = oldSelectTarget
	})

	noPrompt := false
	command := newInitCommand(&noPrompt)
	err := command.Execute()
	localError, ok := errors.AsType[*azdext.LocalError](err)
	if !ok || localError.Code != "rle_harness_init_disabled" {
		t.Fatalf("expected disabled selected harness type error, got %v", err)
	}
}

func TestParseRleInitTargetUsesControlPlanePairs(t *testing.T) {
	tests := []struct {
		name      string
		rleType   string
		subtype   string
		expected  rleInitTarget
		errorCode string
	}{
		{
			name:     "gym defaults to openenv",
			rleType:  "Gym",
			expected: gymOpenEnvInitTarget,
		},
		{
			name:     "hosted agent harness",
			rleType:  "Harness",
			subtype:  "HostedAgent",
			expected: rleInitTarget{rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeHostedAgent},
		},
		{
			name:      "harness needs subtype",
			rleType:   "Harness",
			errorCode: "rle_init_subtype_required",
		},
		{
			name:      "invalid pair",
			rleType:   "Gym",
			subtype:   "BYOH",
			errorCode: "rle_init_type_configuration_invalid",
		},
		{
			name:      "legacy agent type is rejected",
			rleType:   "Agent",
			subtype:   "HostedAgent",
			errorCode: "rle_init_type_invalid",
		},
		{
			name:      "legacy byoa subtype is rejected",
			rleType:   "Harness",
			subtype:   "BYOA",
			errorCode: "rle_init_subtype_invalid",
		},
		{
			name:      "legacy combined target is rejected",
			rleType:   "hosted-agent",
			errorCode: "rle_init_type_invalid",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, err := parseRleInitTarget(test.rleType, test.subtype)
			if test.errorCode == "" {
				if err != nil {
					t.Fatal(err)
				}
				if target != test.expected {
					t.Fatalf("expected %#v, got %#v", test.expected, target)
				}
				return
			}
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			if !ok || localErr.Code != test.errorCode {
				t.Fatalf("expected %s, got %v", test.errorCode, err)
			}
		})
	}
}

// TestInitNoPromptBYOHWithoutFolderNameUsesSampleDefault covers the behavior
// that replaced the removed placeholder scaffold: a BYOH init with no folder
// name no longer fails, it lands on the sample's own default folder.
func TestInitNoPromptBYOHWithoutFolderNameUsesSampleDefault(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(rleEnableAllEnvVar, "true")

	oldLoadHarnessSample := loadRleHarnessSampleCatalogFunc
	loadRleHarnessSampleCatalogFunc = func(subtype project.RleSubtype) (rleHarnessSampleCatalog, error) {
		return &fakeRleHarnessSampleCatalog{subtype: subtype}, nil
	}
	t.Cleanup(func() {
		loadRleHarnessSampleCatalogFunc = oldLoadHarnessSample
	})

	noPrompt := true
	command := newInitCommand(&noPrompt)
	command.SetArgs([]string{"--type", "Harness", "--subtype", "BYOH"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	sessionDir := filepath.Join(tempDir, defaultRleHarnessSampleFolderName(project.RleSubtypeBYOH))
	if _, err := os.Stat(filepath.Join(sessionDir, "rle", project.RleConfigFile)); err != nil {
		t.Fatalf("expected the BYOH sample to be copied without a folder name: %v", err)
	}
}

// TestInitNoPromptHostedAgentFlagsOverrideSampleDefaults keeps the coverage the
// removed placeholder test had for --agent-name/--agent-version, which are now
// overrides applied on top of the sample's own manifest.
func TestInitNoPromptHostedAgentFlagsOverrideSampleDefaults(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(rleEnableAllEnvVar, "true")

	oldLoadHarnessSample := loadRleHarnessSampleCatalogFunc
	loadRleHarnessSampleCatalogFunc = func(subtype project.RleSubtype) (rleHarnessSampleCatalog, error) {
		return &fakeRleHarnessSampleCatalog{subtype: subtype}, nil
	}
	t.Cleanup(func() {
		loadRleHarnessSampleCatalogFunc = oldLoadHarnessSample
	})

	noPrompt := true
	command := newInitCommand(&noPrompt)
	command.SetArgs([]string{
		"support_rle",
		"--type", "Harness",
		"--subtype", "HostedAgent",
		"--agent-name", "support-agent",
		"--agent-version", "202609",
	})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	// #nosec G304 -- the path is generated under t.TempDir by the command under test.
	config, err := os.ReadFile(filepath.Join(tempDir, "support_rle", "rle", project.RleConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`name = 'support_rle'`,
		`subtype = 'HostedAgent'`,
		`agentName = 'support-agent'`,
		`agentVersion = '202609'`,
	} {
		if !strings.Contains(string(config), expected) {
			t.Fatalf("expected config to contain %q, got:\n%s", expected, config)
		}
	}
}
