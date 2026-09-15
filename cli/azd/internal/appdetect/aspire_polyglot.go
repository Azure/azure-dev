// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package appdetect

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// aspirePolyglotConfigFile is the unified Aspire CLI configuration file. For polyglot (non-C#)
// AppHosts it declares the AppHost entry-point path and language under the "appHost" object.
const aspirePolyglotConfigFile = "aspire.config.json"

// aspireConfig models the subset of aspire.config.json that azd inspects.
type aspireConfig struct {
	AppHost struct {
		Path     string `json:"path"`
		Language string `json:"language"`
	} `json:"appHost"`
}

// polyglotAppHostFileLanguage maps well-known polyglot Aspire AppHost entry-point file names
// (compared case-insensitively) to the normalized language azd reports. These mirror the
// detection patterns used by the Aspire CLI's language discovery
// (microsoft/aspire: src/Aspire.Cli/Projects/DefaultLanguageDiscovery.cs).
var polyglotAppHostFileLanguage = map[string]string{
	"apphost.mts":  "typescript",
	"apphost.ts":   "typescript",
	"apphost.py":   "python",
	"apphost.go":   "go",
	"apphost.rs":   "rust",
	"apphost.java": "java",
}

// pythonAppHostCompanions are additional files that corroborate a Python Aspire AppHost. Because
// "apphost.py" is a fairly generic file name, azd only treats it as an Aspire AppHost (via the
// file-name fallback) when one of these companion markers is also present, to avoid false
// positives. A Python AppHost declared explicitly in aspire.config.json is resolved earlier and
// does not rely on these companions.
var pythonAppHostCompanions = []string{"pylock.apphost.toml", "apphost_requirements.txt"}

// normalizeAspireLanguage maps an aspire.config.json "appHost.language" value to a normalized
// language identifier. It returns an empty string for C# (or unknown/empty values), since C#
// AppHosts are detected and supported through the regular .NET AppHost path.
func normalizeAspireLanguage(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "typescript/nodejs", "typescript", "ts", "javascript/nodejs", "javascript", "js":
		return "typescript"
	case "python", "py":
		return "python"
	case "go", "golang":
		return "go"
	case "java":
		return "java"
	case "rust", "rs":
		return "rust"
	default:
		// csharp, c#, dotnet, empty, or anything azd doesn't recognize as polyglot.
		return ""
	}
}

// detectAspirePolyglotAppHost inspects a directory for an Aspire polyglot (non-C#) AppHost.
// It returns the normalized language (e.g. "typescript", "python") and the AppHost file path
// when detected. azd does not yet support these AppHosts; see
// https://github.com/Azure/azure-dev/issues/7138.
//
// Detection prefers the explicit signal from aspire.config.json ("appHost.language" or a
// polyglot "appHost.path"), and otherwise falls back to well-known AppHost file names.
func detectAspirePolyglotAppHost(dir string, entries []fs.DirEntry) (language string, appHostFile string, ok bool) {
	present := make(map[string]string, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			present[strings.ToLower(entry.Name())] = entry.Name()
		}
	}

	// Strongest signal: aspire.config.json explicitly declares the AppHost language/path.
	if configName, has := present[aspirePolyglotConfigFile]; has {
		lang, file, declared := languageFromAspireConfig(filepath.Join(dir, configName), present)
		if lang != "" {
			return lang, filepath.Join(dir, file), true
		}
		if declared {
			// The config authoritatively declares an AppHost that resolves to C# (or an
			// otherwise non-polyglot target). Trust it and do NOT run the filename fallback:
			// a sibling apphost.ts/apphost.py in a C# Aspire layout must not be misreported as
			// polyglot, which would hard-fail `azd init`/`up` on a supported project.
			return "", "", false
		}
	}

	// Fallback: detect by well-known AppHost file names when there is no explicit config signal.
	// Only TypeScript file names are trusted on their own; "apphost.py" requires a companion marker,
	// and the remaining (experimental) languages are only detected via aspire.config.json above, to
	// avoid false positives on generically named files.
	for fileName, original := range present {
		lang, isAppHostFile := polyglotAppHostFileLanguage[fileName]
		if !isAppHostFile {
			continue
		}

		switch lang {
		case "typescript":
			return lang, filepath.Join(dir, original), true
		case "python":
			if hasPythonAppHostCompanion(present) {
				return lang, filepath.Join(dir, original), true
			}
		}
	}

	return "", "", false
}

// languageFromAspireConfig resolves the polyglot language declared in aspire.config.json. It
// returns:
//   - language: the normalized polyglot language, or "" for a C# AppHost (handled by the regular
//     .NET path) or when no language could be resolved.
//   - appHostFile: the relative AppHost path declared in the config (may include subdirectories,
//     e.g. "src/apphost.mts").
//   - declared: whether the config authoritatively declares an AppHost (a readable, parsable
//     config with a non-empty "appHost.path"). When true but language is "", the config declares a
//     C#/non-polyglot AppHost; callers should treat that as authoritative and skip filename-based
//     fallback detection. When false, the config was missing/unreadable/malformed and callers may
//     fall back to file-name heuristics.
func languageFromAspireConfig(
	configPath string,
	present map[string]string,
) (language string, appHostFile string, declared bool) {
	//nolint:gosec // G304: configPath is derived from a directory listing during app detection.
	contents, err := os.ReadFile(configPath)
	if err != nil {
		return "", "", false
	}

	var config aspireConfig
	if err := json.Unmarshal(contents, &config); err != nil {
		return "", "", false
	}

	if strings.TrimSpace(config.AppHost.Path) == "" {
		return "", "", false
	}

	// Preserve the full relative path (may contain subdirectories). Only case-resolve the path
	// against the directory listing when it refers to an immediate child, since `present` only
	// contains the top-level entries of the scanned directory.
	appHostFile = filepath.Clean(filepath.FromSlash(config.AppHost.Path))
	if !strings.ContainsRune(appHostFile, filepath.Separator) {
		if resolved, has := present[strings.ToLower(appHostFile)]; has {
			appHostFile = resolved
		}
	}

	fileName := strings.ToLower(filepath.Base(appHostFile))

	// Prefer the explicit language declaration.
	if lang := normalizeAspireLanguage(config.AppHost.Language); lang != "" {
		return lang, appHostFile, true
	}

	// No explicit (polyglot) language: infer from the AppHost path's file name. This treats a
	// ".csproj"/"apphost.cs" path as C# (empty language), so it is not misreported as polyglot.
	if lang, isAppHostFile := polyglotAppHostFileLanguage[fileName]; isAppHostFile {
		return lang, appHostFile, true
	}

	return "", appHostFile, true
}

// hasPythonAppHostCompanion reports whether a Python Aspire AppHost companion marker is present.
func hasPythonAppHostCompanion(present map[string]string) bool {
	for _, companion := range pythonAppHostCompanions {
		if _, has := present[companion]; has {
			return true
		}
	}
	return false
}
