// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package analysis

import (
	"context"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"azure.ai.content-understanding/internal/cuclient"
)

type InputOptions struct {
	Positional, Files, Sources, URLs []string
	Pattern                          *string
	Recursive                        bool
}

type Input struct {
	Reference string
	Path      string
	URL       string
	Relative  string
	identity  fs.FileInfo
}

// DiscoverFile resolves one regular local file without directory expansion.
func DiscoverFile(ctx context.Context, value string) (Input, error) {
	if err := ctx.Err(); err != nil {
		return Input{}, err
	}
	if value == "" || isURL(value) {
		return Input{}, invalid("invalid_input_file", "Specify a readable regular local file.")
	}
	path, err := filepath.Abs(value)
	if err != nil {
		return Input{}, invalid("invalid_input_path", "An input path could not be resolved.")
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return Input{}, invalid("invalid_input_path", "An input path does not exist or could not be resolved.")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Input{}, invalid("invalid_input_file", "An input must be a readable regular file.")
	}
	return Input{Reference: path, Path: path, Relative: filepath.Base(path), identity: info}, nil
}

// Open verifies discovery identity before returning a caller-owned local reader.
func (input Input) Open() (*os.File, error) {
	file, err := os.Open(input.Path)
	if err != nil {
		return nil, invalid("input_read_failed", "An input file could not be opened.")
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || input.identity == nil || !os.SameFile(input.identity, info) {
		return nil, errors.Join(invalid("input_changed", "An input file changed after discovery."), file.Close())
	}
	return file, nil
}

func invalid(code, message string) error {
	return &cuclient.Error{
		Kind: cuclient.ErrorValidation, Code: code, Message: message,
		Suggestion: "Check analyze --help and correct the input or output options.",
	}
}

func Discover(ctx context.Context, options InputOptions) ([]Input, error) {
	modes := [][]string{options.Positional, options.Files, options.Sources, options.URLs}
	var selected []string
	count := 0
	for _, mode := range modes {
		if len(mode) != 0 {
			count++
			selected = mode
		}
	}
	if count != 1 {
		return nil, invalid("invalid_input_mode",
			"Select inputs using positionals, --file, --source, or --url, not a mixture.")
	}
	if options.Pattern != nil && (len(options.Sources) == 0 || *options.Pattern == "") {
		return nil, invalid("invalid_pattern", "A nonempty --pattern requires --source.")
	}
	pattern := "*"
	if options.Pattern != nil {
		pattern = *options.Pattern
	}
	match, err := globPattern(pattern)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var result []Input
	directory := false
	addFile := func(path, relative string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		input, err := DiscoverFile(ctx, path)
		if err != nil {
			return err
		}
		for _, previous := range result {
			if previous.Path == input.Path || (previous.identity != nil && os.SameFile(previous.identity, input.identity)) {
				return nil
			}
		}
		input.Relative = relative
		result = append(result, input)
		return nil
	}
	for _, value := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if value == "" {
			return nil, invalid("empty_input", "Input paths and URLs must not be empty.")
		}
		if seen[value] {
			return nil, invalid("duplicate_input", "The same direct input was specified more than once.")
		}
		seen[value] = true
		remote := len(options.URLs) != 0 || isURL(value)
		if remote {
			if len(options.Files) != 0 || len(options.Sources) != 0 {
				return nil, invalid("invalid_input_mode", "Use --url or a positional argument for HTTPS inputs.")
			}
			input, err := remoteInput(value)
			if err != nil {
				return nil, err
			}
			result = append(result, input)
			continue
		}
		if strings.ContainsAny(value, "*?[") {
			return nil, invalid("input_wildcard", "Use --source with --pattern instead of a wildcard input path.")
		}
		path, err := filepath.Abs(value)
		if err != nil {
			return nil, invalid("invalid_input_path", "An input path could not be resolved.")
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return nil, invalid("invalid_input_path", "An input path does not exist or could not be resolved.")
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, invalid("invalid_input_path", "An input path could not be inspected.")
		}
		if !info.IsDir() {
			if len(options.Sources) != 0 {
				return nil, invalid("invalid_source", "--source must name a directory.")
			}
			if err := addFile(path, filepath.Base(path)); err != nil {
				return nil, err
			}
			continue
		}
		if len(options.Files) != 0 {
			return nil, invalid("invalid_input_file", "--file must name a regular file, not a directory.")
		}
		directory = true
		type selectedFile struct{ path, relative string }
		var files []selectedFile
		err = filepath.WalkDir(path, func(entryPath string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return invalid("input_discovery_failed", "An input directory could not be read.")
			}
			if entryPath == path {
				return nil
			}
			if entry.IsDir() {
				if !options.Recursive || strings.HasPrefix(entry.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasPrefix(entry.Name(), ".") || strings.HasSuffix(entry.Name(), ".result.json") ||
				strings.HasSuffix(entry.Name(), ".result.md") {
				return nil
			}
			relative, err := filepath.Rel(path, entryPath)
			if err != nil {
				return err
			}
			if !match.MatchString(filepath.ToSlash(relative)) {
				return nil
			}
			fileInfo, err := os.Stat(entryPath)
			if err != nil {
				return invalid("input_discovery_failed", "A selected directory entry could not be inspected.")
			}
			if !fileInfo.Mode().IsRegular() {
				return nil
			}
			files = append(files, selectedFile{entryPath, relative})
			return nil
		})
		if err != nil {
			return nil, err
		}
		slices.SortFunc(files, func(left, right selectedFile) int {
			return strings.Compare(filepath.ToSlash(left.relative), filepath.ToSlash(right.relative))
		})
		for _, file := range files {
			if err := addFile(file.path, file.relative); err != nil {
				return nil, err
			}
		}
	}
	if options.Recursive && !directory {
		return nil, invalid("invalid_recursion", "--recursive requires at least one directory input.")
	}
	if len(result) == 0 {
		return nil, invalid("empty_inputs", "No input files matched the selection.")
	}
	return result, nil
}

func isURL(value string) bool {
	if len(value) >= 3 && value[1] == ':' && (value[2] == '/' || value[2] == '\\') {
		return false
	}
	if strings.Contains(value, "://") {
		return true
	}
	lower := strings.ToLower(value)
	if !strings.HasPrefix(lower, "http:") && !strings.HasPrefix(lower, "https:") {
		return false
	}
	_, err := os.Stat(value)
	return err != nil
}

func remoteInput(value string) (Input, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil ||
		strings.IndexFunc(value, unicode.IsSpace) >= 0 || utf8.RuneCountInString(value) > 8192 {
		return Input{}, invalid("invalid_input_url",
			"Remote inputs require an absolute HTTPS URL without credentials or whitespace.")
	}
	request := cuclient.AnalyzeRequest{
		AnalyzerID: "validation", Input: cuclient.Input{URL: value}, Mode: cuclient.AnalysisModeAsync,
	}
	if err := request.Validate(); err != nil {
		return Input{}, invalid("invalid_input_url", "The remote input URL is invalid.")
	}
	authority := value[len(parsed.Scheme)+3:]
	rawPath := ""
	if start := strings.IndexAny(authority, "/?#"); start >= 0 && authority[start] == '/' {
		rawPath = authority[start:]
		if end := strings.IndexAny(rawPath, "?#"); end >= 0 {
			rawPath = rawPath[:end]
		}
	}
	name := filepath.Base(strings.TrimRight(rawPath, "/"))
	if name == "." || name == "/" || name == "" {
		name = "remote-input"
	}
	name = strings.Map(func(character rune) rune {
		if character < 32 || strings.ContainsRune(`<>:"/\|?*`, character) {
			return '_'
		}
		return character
	}, name)
	name = strings.TrimRight(name, " .")
	if name == "" {
		name = "remote-input"
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" ||
		regexp.MustCompile(`^(COM|LPT)[1-9]$`).MatchString(base) {
		name = "_" + name
	}
	return Input{Reference: RedactURL(value), URL: value, Relative: name}, nil
}

func RedactURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return "[invalid URL]"
	}
	reference := value
	if end := strings.IndexAny(reference, "?#"); end >= 0 {
		reference = reference[:end]
	}
	if parsed.User != nil {
		start := strings.Index(reference, "://") + 3
		authority := reference[start:]
		if end := strings.IndexByte(authority, '/'); end >= 0 {
			authority = authority[:end]
		}
		reference = reference[:start] + reference[start+strings.LastIndexByte(authority, '@')+1:]
	}
	if parsed.RawQuery != "" {
		reference += "?REDACTED"
	}
	return reference
}

// fnmatch-style stars match separators too; filepath.Match does not.
func globPattern(pattern string) (*regexp.Regexp, error) {
	if runtime.GOOS == "windows" {
		pattern = filepath.ToSlash(pattern)
	}
	runes := []rune(pattern)
	var expression strings.Builder
	if runtime.GOOS == "windows" {
		expression.WriteString("(?i)")
	}
	expression.WriteString("(?s)^")
	for index := 0; index < len(runes); index++ {
		switch runes[index] {
		case '*':
			expression.WriteString(".*")
		case '?':
			expression.WriteByte('.')
		case '[':
			end := index + 1
			if end < len(runes) && runes[end] == '!' {
				end++
			}
			if end < len(runes) && runes[end] == ']' {
				end++
			}
			for end < len(runes) && runes[end] != ']' {
				end++
			}
			if end == len(runes) {
				expression.WriteString(`\[`)
				continue
			}
			class := strings.ReplaceAll(string(runes[index+1:end]), `\`, `\\`)
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			} else if strings.HasPrefix(class, "^") {
				class = `\` + class
			}
			expression.WriteString("[" + class + "]")
			index = end
		default:
			expression.WriteString(regexp.QuoteMeta(string(runes[index])))
		}
	}
	expression.WriteByte('$')
	compiled, err := regexp.Compile(expression.String())
	if err != nil {
		return nil, invalid("invalid_pattern", "The source pattern is invalid.")
	}
	return compiled, nil
}
