// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// skill_install.go implements `azd ai doc install skill` -- installs an
// embedded skill pack into a tool-specific destination directory in the
// user's project. The destination is decided by --target (claude, codex,
// gemini, copilot, opencode) or --path (when --target=custom).
//
// Install is file-ownership safe: --force only overwrites files we
// shipped in the embedded pack. Foreign files in the destination (user
// edits, files from another skill) are never touched. Without --force,
// the install refuses to clobber an owned file whose content differs
// from the bundled version.

package cmd

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// skillFilesFS holds the skill files shipped by this extension. The
// install command writes these into a tool-specific destination
// directory in the user's project. Add a new bundled file by extending
// the embed directive below (e.g. `skills/SKILL.md skills/helpers`).
//
// Note: this FS deliberately does NOT embed the topic-doc subdirs
// (skills/<category>/*.md) used by doc_agent.go -- those live in a
// separate embed.FS so the install surface stays a flat, well-known set
// of files.
//
//go:embed skills/SKILL.md
var skillFilesFS embed.FS

// skillsRootDir is the in-FS directory the embedded skill files live
// under. Used as the WalkDir root and stripped from each file's
// relative path when copying to the install destination.
const skillsRootDir = "skills"

// defaultPackName is the destination directory name written into the
// user's project (e.g. .claude/skills/azd-ai-skill/). It is NOT a
// source-side folder anymore -- the bundled files live directly under
// skills/. When additional install bundles are added, this becomes a
// --pack flag with this string as its default.
const defaultPackName = "azd-ai-skill"

// targetSpec maps a --target value to a display name and an install path
// (relative to cwd). The install path is final -- the install does not
// nest the pack name beneath it.
type targetSpec struct {
	name        string // --target value (e.g. "claude")
	displayName string // human-readable label
	installDir  string // path under cwd; empty for "custom" (uses --path)
}

// knownTargets is the ordered list of built-in install targets. The
// order here drives both the interactive Select choice order in
// azure.ai.agents init and the help text below.
var knownTargets = []targetSpec{
	{name: "claude", displayName: "Claude Code", installDir: filepath.Join(".claude", "skills", defaultPackName)},
	{name: "codex", displayName: "Codex", installDir: filepath.Join(".agents", "skills", defaultPackName)},
	{name: "gemini", displayName: "Gemini CLI", installDir: filepath.Join(".agents", "skills", defaultPackName)},
	{name: "copilot", displayName: "GitHub Copilot", installDir: filepath.Join(".agents", "skills", defaultPackName)},
	{name: "opencode", displayName: "Opencode", installDir: filepath.Join(".agents", "skills", defaultPackName)},
	{name: "custom", displayName: "Custom path", installDir: ""},
}

// skillInstallFlags collects every user-controllable input for the
// install command.
type skillInstallFlags struct {
	target   string
	path     string
	force    bool
	noPrompt bool
	output   string
}

// SkillInstallAction executes the install workflow. The action is
// constructed by the RunE wrapper after flag validation and dispatched
// via Run(ctx) -- matches the action-object pattern used across the
// azure.ai.* extensions (see CONTRIBUTING / AGENTS.md for rationale).
type SkillInstallAction struct {
	flags *skillInstallFlags
	out   io.Writer
	// cwd is the resolved working directory all relative paths root
	// under. Injected for testability so unit tests can drive the
	// action without t.Chdir-ing the whole test binary.
	cwd string
	// packs is the embedded skill-file filesystem. Injected so a future
	// test can swap in a tmpfs/iotest.MapFS without going through
	// //go:embed reload.
	packs fs.FS
	// packName is the destination directory name written into the
	// user's project. Reserved for a future --pack flag; today always
	// defaultPackName.
	packName string
}

// skillInstallResult is the JSON wire shape for --output json.
type skillInstallResult struct {
	Status string   `json:"status"`
	Target string   `json:"target"`
	Path   string   `json:"path"`
	Files  []string `json:"files"`
}

// newInstallSkillCommand wires the cobra command. The RunE follows the
// established action-object pattern: parse output context, validate
// flags, construct SkillInstallAction, call Run(ctx).
func newInstallSkillCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &skillInstallFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Install an agent-friendly skill pack into your project.",
		Long: `Install an agent-friendly skill pack (SKILL.md and supporting files)
into your project at a tool-specific path.

Built-in targets:

  claude     -> .claude/skills/azd-ai-skill/
  codex      -> .agents/skills/azd-ai-skill/
  gemini     -> .agents/skills/azd-ai-skill/
  copilot    -> .agents/skills/azd-ai-skill/
  opencode   -> .agents/skills/azd-ai-skill/
  custom     -> uses --path

Safety:

  * Only files shipped in the embedded pack are touched. Foreign files
    in the destination directory (user edits, files from another skill)
    are never modified or removed.
  * Without --force, the install refuses to overwrite an owned file
    whose content differs from the bundled version.
  * --path values are rejected when absolute, when they escape the
    current working directory, or when an existing parent symlinks
    outside the project root.`,
		Example: `  # Install for GitHub Copilot
  azd ai doc install skill --target copilot

  # Force overwrite of previously installed (modified) files
  azd ai doc install skill --target copilot --force

  # Install to a custom directory
  azd ai doc install skill --target custom --path .my-tool/skills/foundry

  # JSON output for scripting
  azd ai doc install skill --target copilot --output json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.output = extCtx.OutputFormat
			flags.noPrompt = extCtx.NoPrompt

			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve working directory: %w", err)
			}

			if err := validateSkillInstallFlags(flags, cwd); err != nil {
				return err
			}

			action := &SkillInstallAction{
				flags:    flags,
				out:      cmd.OutOrStdout(),
				cwd:      cwd,
				packs:    skillFilesFS,
				packName: defaultPackName,
			}

			return action.Run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&flags.target, "target", "",
		"Target tool (claude, codex, gemini, copilot, opencode, custom). Required.")
	cmd.Flags().StringVar(&flags.path, "path", "",
		"Install path (required when --target=custom). Must be relative and under the current directory.")
	cmd.Flags().BoolVar(&flags.force, "force", false,
		"Overwrite owned files in the destination even when their content has been modified.")

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json", "text"},
		Default:       "text",
	})

	return cmd
}

// ensureExtensionContext returns extCtx unchanged when non-nil; otherwise
// returns a zero-value context so tests can construct commands without
// the SDK present. Mirrors the agents extension's helper of the same
// name.
func ensureExtensionContext(extCtx *azdext.ExtensionContext) *azdext.ExtensionContext {
	if extCtx == nil {
		return &azdext.ExtensionContext{}
	}
	return extCtx
}

// validateSkillInstallFlags rejects malformed input BEFORE any side
// effects. Covers: required flags, target whitelist, custom-path
// safety. The cwd argument lets callers and tests pin the relative-path
// root without depending on os.Getwd internally.
func validateSkillInstallFlags(flags *skillInstallFlags, cwd string) error {
	if flags.target == "" {
		return fmt.Errorf("--target is required (one of: %s)", joinTargetNames())
	}

	spec, ok := lookupTarget(flags.target)
	if !ok {
		return fmt.Errorf("unknown --target %q (valid values: %s)", flags.target, joinTargetNames())
	}

	if spec.name == "custom" {
		if strings.TrimSpace(flags.path) == "" {
			return fmt.Errorf("--path is required when --target=custom")
		}
		if err := validateCustomPath(flags.path, cwd); err != nil {
			return err
		}
	} else if flags.path != "" {
		return fmt.Errorf("--path is only valid with --target=custom (got --target=%s)", flags.target)
	}

	return nil
}

// validateCustomPath enforces the safety contract for --path:
//
//   - non-empty after trim
//   - not ".", "..", or any absolute / drive-qualified path
//   - resolves to a directory under cwd (defeats ../escape)
//   - if any existing parent is a symlink, the resolved target still
//     lives under cwd (defeats symlink escape)
func validateCustomPath(path, cwd string) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return fmt.Errorf("--path must not be empty")
	}
	if trimmed == "." || trimmed == ".." {
		return fmt.Errorf("--path %q is not a valid install location", trimmed)
	}
	if filepath.IsAbs(trimmed) {
		return fmt.Errorf("--path %q must be relative to the project root", trimmed)
	}
	// On Windows, filepath.IsAbs("/foo") is false; reject leading separators
	// explicitly so a forward-slash absolute path is also caught.
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, `\`) {
		return fmt.Errorf("--path %q must be relative to the project root", trimmed)
	}

	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}
	absTarget, err := filepath.Abs(filepath.Join(absCwd, trimmed))
	if err != nil {
		return fmt.Errorf("resolve --path: %w", err)
	}

	if !pathUnder(absCwd, absTarget) {
		return fmt.Errorf("--path %q escapes the project root", trimmed)
	}

	// Walk up the absTarget chain looking for the first existing dir; if
	// it is a symlink, EvalSymlinks resolves it and we re-check
	// containment. This catches the case where an existing parent dir
	// links outside cwd.
	if resolved, ok := resolveExistingAncestor(absTarget); ok {
		if !pathUnder(absCwd, resolved) {
			return fmt.Errorf("--path %q resolves via a symlink to outside the project root", trimmed)
		}
	}

	return nil
}

// pathUnder reports whether target sits inside (or equals) root, using
// filepath.Rel under the hood so the comparison is OS-correct (case-
// insensitive on Windows, etc.).
func pathUnder(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// resolveExistingAncestor walks up from path and returns
// (EvalSymlinks(firstExistingAncestor), true). When no ancestor exists
// (impossible on a normal filesystem but defensive), returns ("", false).
func resolveExistingAncestor(path string) (string, bool) {
	cur := path
	for {
		if _, err := os.Lstat(cur); err == nil {
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return cur, true
			}
			return resolved, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		cur = parent
	}
}

// joinTargetNames returns the built-in target names as a comma-separated
// string for use in help text and error messages.
func joinTargetNames() string {
	names := make([]string, 0, len(knownTargets))
	for _, t := range knownTargets {
		names = append(names, t.name)
	}
	return strings.Join(names, ", ")
}

// lookupTarget returns the targetSpec for a --target value (case-
// insensitive). The second return is false when the value is not a known
// target.
func lookupTarget(name string) (targetSpec, bool) {
	for _, t := range knownTargets {
		if strings.EqualFold(t.name, name) {
			return t, true
		}
	}
	return targetSpec{}, false
}

// Run executes the install. The flow:
//
//  1. Resolve destination dir from --target (or --path for custom).
//  2. Enumerate the embedded skill files -> list of (relPath, content)
//     pairs.
//  3. For each owned file: compare to destination. Conflicts gated by
//     --force.
//  4. Create destination dir(s) and write owned files.
//  5. Emit success result (text or JSON).
func (a *SkillInstallAction) Run(ctx context.Context) error {
	spec, _ := lookupTarget(a.flags.target) // validated already

	destRel := spec.installDir
	if spec.name == "custom" {
		destRel = filepath.Clean(a.flags.path)
	}
	destAbs := filepath.Join(a.cwd, destRel)

	files, err := readPack(a.packs)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("skill files for %q are missing (extension build is missing embedded content)", a.packName)
	}

	// Conflict check: refuse without --force if any owned file already
	// exists with different content.
	if !a.flags.force {
		conflicts, err := findOwnedConflicts(destAbs, files)
		if err != nil {
			return err
		}
		if len(conflicts) > 0 {
			return fmt.Errorf(
				"refusing to overwrite modified files in %s: %s. Re-run with --force to replace them.",
				destRel, strings.Join(conflicts, ", "))
		}
	}

	if err := os.MkdirAll(destAbs, 0o755); err != nil {
		return fmt.Errorf("create install directory %s: %w", destRel, err)
	}

	written := make([]string, 0, len(files))
	for _, f := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := writeOwnedFile(destAbs, f); err != nil {
			return err
		}
		written = append(written, f.relPath)
	}
	sort.Strings(written)

	return a.renderResult(destRel, written)
}

// packFile is one file from the embedded skill set.
type packFile struct {
	relPath string // forward-slash path under skills/ (e.g. "SKILL.md")
	content []byte
}

// readPack walks the embedded filesystem for skill files and returns
// every regular file's relative path + content. Skips directories.
// Returns an error when the skills root does not exist.
func readPack(packs fs.FS) ([]packFile, error) {
	var files []packFile

	err := fs.WalkDir(packs, skillsRootDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		body, err := fs.ReadFile(packs, path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}
		rel := strings.TrimPrefix(path, skillsRootDir+"/")
		files = append(files, packFile{relPath: rel, content: body})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read skill files: %w", err)
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].relPath < files[j].relPath
	})
	return files, nil
}

// findOwnedConflicts returns the list of owned files (by relPath) that
// already exist at destAbs with different content from what we ship.
// Files that match byte-for-byte are NOT conflicts (re-install is a
// no-op). Files that do not exist yet are NOT conflicts.
//
// Returns paths in pack-relative form, sorted, so callers can render
// stable error messages.
func findOwnedConflicts(destAbs string, files []packFile) ([]string, error) {
	var conflicts []string
	for _, f := range files {
		target := filepath.Join(destAbs, filepath.FromSlash(f.relPath))
		info, err := os.Lstat(target)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect %s: %w", f.relPath, err)
		}
		// Reject directory / symlink occupying an owned file path:
		// even with --force we will not delete those.
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf(
				"destination %s is occupied by a %s; remove it manually before installing",
				f.relPath, fileKind(info))
		}
		existing, err := os.ReadFile(target)
		if err != nil {
			return nil, fmt.Errorf("read existing %s: %w", f.relPath, err)
		}
		if !contentEqual(existing, f.content) {
			conflicts = append(conflicts, f.relPath)
		}
	}
	sort.Strings(conflicts)
	return conflicts, nil
}

// writeOwnedFile creates parent dirs as needed and writes content
// atomically (write to .tmp + rename). Mode is 0644.
func writeOwnedFile(destAbs string, f packFile) error {
	target := filepath.Join(destAbs, filepath.FromSlash(f.relPath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create parent dir for %s: %w", f.relPath, err)
	}

	tmp := target + ".tmp"
	//nolint:gosec // skills files should remain readable by project tooling
	if err := os.WriteFile(tmp, f.content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", f.relPath, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename into place %s: %w", f.relPath, err)
	}
	return nil
}

// contentEqual returns true when two byte slices contain identical
// data. Length check first to short-circuit cheaply on the common case
// where a file was added or truncated.
func contentEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	return bytes.Equal(a, b)
}

// fileKind returns "directory", "symlink", or "file" for use in error
// messages.
func fileKind(info os.FileInfo) string {
	switch {
	case info.IsDir():
		return "directory"
	case info.Mode()&os.ModeSymlink != 0:
		return "symlink"
	default:
		return "file"
	}
}

// renderResult writes the success output in the format selected by
// --output. Text is a small human-readable summary; JSON is the
// machine-parseable wire shape consumed by callers like the agents
// init pre-flow.
func (a *SkillInstallAction) renderResult(destRel string, files []string) error {
	if isJSONOutput(a.flags.output) {
		return writeJSON(a.out, skillInstallResult{
			Status: "installed",
			Target: a.flags.target,
			Path:   filepath.ToSlash(destRel),
			Files:  files,
		})
	}

	fmt.Fprintf(a.out, "Installed %d file(s) into %s\n", len(files), destRel)
	for _, f := range files {
		fmt.Fprintf(a.out, "  %s\n", f)
	}
	return nil
}

// isJSONOutput reports whether the resolved --output value selects JSON.
// Defensively treats the SDK pre-parse sentinel "default" as the
// command's declared default ("text").
func isJSONOutput(format string) bool {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		return true
	default:
		return false
	}
}

// writeJSON marshals v with two-space indent and writes it to w followed
// by a trailing newline so terminal users get a clean prompt back.
func writeJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err = w.Write([]byte{'\n'})
	return err
}

// targetNames returns the built-in target names. Used by tests to assert
// the canonical list above stays in sync with consumers (the agents
// extension pre-flow's Select choices).
func targetNames() []string {
	out := make([]string, 0, len(knownTargets))
	for _, t := range knownTargets {
		out = append(out, t.name)
	}
	return out
}
