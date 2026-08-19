// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"azureaieval/internal/messages"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"go.yaml.in/yaml/v3"
)

// AgentHost is the service host the agents extension registers. Services
// declaring it are the ones that could be a generation target.
const AgentHost = "azure.ai.agent"

// Where `azd ai agent optimize` leaves the configuration it settled on.
//
// These are the agents extension's file names, repeated rather than imported:
// azd extensions are separate Go modules and share no code, so the only way to
// read another one's output is to know its layout. That makes this a coupling
// worth naming — if the agents extension moves these, generation quietly stops
// finding instructions locally and falls back to the service.
const (
	agentConfigsDir   = ".agent_configs"
	agentBaselineDir  = "baseline"
	agentMetadataFile = "metadata.yaml"
)

// agentConfigMetadata is the part of the optimize configuration's metadata.yaml
// that says where the instructions are. It points at a file rather than
// carrying the text, because the text is what a reviewer diffs.
type agentConfigMetadata struct {
	InstructionFile string `yaml:"instruction_file"`
}

// ErrAmbiguousAgentService reports that a target name matched more than one
// service, so there is no single set of instructions to read.
var ErrAmbiguousAgentService = messages.ErrAmbiguousAgentService

// AgentInstructionsFromProject reads the target agent's instructions out of the
// project, returning empty when the project does not hold them.
//
// The instructions an agent was optimized with are the best description of what
// it is supposed to do, and they are already on disk, so generating from them
// needs no service call. Coming back empty is ordinary — most projects have
// never run `azd ai agent optimize` — and leaves the caller free to ask the
// service instead.
//
// The returned path is where the text came from, for a caller that wants to say
// so.
func AgentInstructionsFromProject(
	proj *azdext.ProjectConfig,
	agentName string,
) (instruction string, path string, err error) {
	svc, err := findAgentService(proj, agentName)
	if err != nil || svc == nil {
		return "", "", err
	}

	configDir := filepath.Join(
		baseDirUnder(proj.GetPath(), svc), agentConfigsDir, agentBaselineDir)

	data, err := os.ReadFile(filepath.Join(configDir, agentMetadataFile)) //nolint:gosec // under the project
	if err != nil {
		// An agent that was never optimized has no such directory, which is
		// the common case rather than a problem.
		return "", "", nil
	}

	var meta agentConfigMetadata
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return "", "", messages.ReadingPath(filepath.Join(configDir, agentMetadataFile), err)
	}
	if meta.InstructionFile == "" {
		return "", "", nil
	}

	instructionPath := meta.InstructionFile
	if !filepath.IsAbs(instructionPath) {
		instructionPath = filepath.Join(configDir, instructionPath)
	}
	// The pointer comes out of the checkout, so it carries the checkout's
	// trust. Left alone, an absolute path or one climbing out with `..` reads a
	// file the project does not contain and sends it on as agent instructions.
	if !withinDir(proj.GetPath(), instructionPath) {
		return "", "", messages.InstructionFileOutsideProject(
			filepath.Join(configDir, agentMetadataFile), meta.InstructionFile)
	}
	text, err := os.ReadFile(instructionPath) //nolint:gosec // checked to be inside the project
	if err != nil {
		// The metadata named a file that is not there. That is worth saying:
		// something wrote the pointer and not the target.
		return "", "", messages.InstructionFileUnreadable(
			filepath.Join(configDir, agentMetadataFile), meta.InstructionFile, err)
	}

	return strings.TrimSpace(string(text)), instructionPath, nil
}

// withinDir reports whether path resolves to somewhere inside root.
//
// Asked twice: once of the path as written, and once of what it resolves to.
// The read that follows this check follows links, so a link committed to the
// repository would otherwise satisfy the written form while naming a file the
// project does not contain — the same escape `..` is refused for, needing no
// more privilege to commit.
func withinDir(root, path string) bool {
	if !liesWithin(root, path) {
		return false
	}

	realRoot, rootErr := filepath.EvalSymlinks(root)
	realPath, pathErr := filepath.EvalSymlinks(path)
	if rootErr != nil || pathErr != nil {
		// Resolution failing is not the same as there being nothing to
		// resolve. A Windows junction is the difference: Go reads it as an
		// irregular file rather than a link, so EvalSymlinks refuses the path
		// while the OS walks the read straight through it. Only a path that is
		// genuinely absent is let past, and the read reports that as missing
		// rather than as an escape.
		_, lstatErr := os.Lstat(path)
		return errors.Is(lstatErr, fs.ErrNotExist)
	}
	return liesWithin(realRoot, realPath)
}

// liesWithin compares two paths as text, after cleaning both so that `..`
// segments are resolved before the question is asked rather than matched.
func liesWithin(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// findAgentService resolves a target name to the one service that is it.
//
// A name can match either the azure.yaml service key or the agent name the
// service declares, because the two need not agree and a user has only ever
// seen one of them. Matching both is what makes `--target` mean what they
// typed; refusing a tie is what stops it silently meaning one of two things.
func findAgentService(
	proj *azdext.ProjectConfig,
	agentName string,
) (*azdext.ServiceConfig, error) {
	if proj == nil || agentName == "" {
		return nil, nil
	}

	var matched []string
	services := map[string]*azdext.ServiceConfig{}
	for name, svc := range proj.GetServices() {
		if svc.GetHost() != AgentHost {
			continue
		}
		if name == agentName || declaredAgentName(svc) == agentName {
			matched = append(matched, name)
			services[name] = svc
		}
	}

	switch len(matched) {
	case 0:
		return nil, nil
	case 1:
		return services[matched[0]], nil
	default:
		sort.Strings(matched)
		return nil, messages.AmbiguousAgentService(agentName, matched)
	}
}

// declaredAgentName is the name the service gives the agent, which is what the
// service publishes under and so what the eval configuration's target refers
// to. It is absent when the service key is also the agent name.
func declaredAgentName(svc *azdext.ServiceConfig) string {
	props := serviceProps(svc)
	if props == nil {
		return ""
	}
	name, _ := props.AsMap()["name"].(string)
	return name
}
