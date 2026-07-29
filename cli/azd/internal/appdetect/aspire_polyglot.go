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
// "apphost.py" is a fairly generic file name, azd only treats it as an Aspire AppHost when one of
// these companion markers (or aspire.config.json) is also present, to avoid false positives.
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
		if lang, file := languageFromAspireConfig(filepath.Join(dir, configName), present); lang != "" {
			return lang, filepath.Join(dir, file), true
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
// returns an empty language for C# AppHosts (which are handled by the regular .NET path) or when
// the config cannot be read/parsed.
func languageFromAspireConfig(configPath string, present map[string]string) (language string, appHostFile string) {
	//nolint:gosec // G304: configPath is derived from a directory listing during app detection.
	contents, err := os.ReadFile(configPath)
	if err != nil {
		return "", ""
	}

	var config aspireConfig
	if err := json.Unmarshal(contents, &config); err != nil {
		return "", ""
	}

	appHostFile = filepath.Base(config.AppHost.Path)
	if resolved, has := present[strings.ToLower(appHostFile)]; has {
		appHostFile = resolved
	}

	// Prefer the explicit language declaration.
	if lang := normalizeAspireLanguage(config.AppHost.Language); lang != "" {
		return lang, appHostFile
	}

	// No explicit (polyglot) language: infer from the AppHost path's file name. This treats a
	// ".csproj"/"apphost.cs" path as C# (empty language), so it is not misreported as polyglot.
	pathFileName := strings.ToLower(filepath.Base(config.AppHost.Path))
	if lang, isAppHostFile := polyglotAppHostFileLanguage[pathFileName]; isAppHostFile {
		return lang, appHostFile
	}

	return "", ""
}

// hasPythonAppHostCompanion reports whether a Python Aspire AppHost companion marker is present.
func hasPythonAppHostCompanion(present map[string]string) bool {
	if _, has := present[aspirePolyglotConfigFile]; has {
		return true
	}
	for _, companion := range pythonAppHostCompanions {
		if _, has := present[companion]; has {
			return true
		}
	}
	return false
}
