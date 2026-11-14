// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

var unsupportedSpringBootstrap = []*regexp.Regexp{
	regexp.MustCompile(`@(?:[\w.]+\.)?(?:PropertySource|PropertySources|ImportResource)\b`),
	regexp.MustCompile(`\b(?:implements|extends)\s+[\w.,\s]*\b(?:EnvironmentPostProcessor|ApplicationContextInitializer)\b`),
	regexp.MustCompile(`\.\s*(?:setDefaultProperties|addInitializers|setInitializers)\s*\(`),
	regexp.MustCompile(`getPropertySources\s*\(\s*\)\s*\.\s*(?:add\w*|replace|remove)\s*\(`),
	regexp.MustCompile(`\bnew\s+[\w.]*DataSource\s*\(`),
	regexp.MustCompile(
		`@(?:[\w.]+\.)?Bean\b[^;{}]*\b[\w.]*DataSource\s+\w+\s*\(`,
	),
}

// This is a bounded rejection check for recognizable custom bootstrap mechanisms,
// not a proof of arbitrary Java behavior or generated/transitive application code.
func validateStandardSpringBootstrap(ctx context.Context, root string) error {
	sourceRoot := filepath.Join(root, "src", "main")
	if _, err := os.Lstat(sourceRoot); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	loader := &springConfigLoader{input: springConfigInput{root: root}}
	count, size := 0, 0
	return filepath.WalkDir(sourceRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > 4096 || entry.Type()&os.ModeSymlink != 0 {
			return configurationError("application bootstrap discovery requires bounded, nonsymlink source files")
		}
		if entry.IsDir() {
			return nil
		}
		if extension := filepath.Ext(path); extension == ".kt" || extension == ".groovy" {
			return configurationError("custom non-Java application bootstrap is not supported by datasource discovery")
		} else if extension != ".java" && filepath.Base(path) != "spring.factories" {
			return nil
		}
		data, err := loader.readFile(path)
		if err != nil {
			return err
		}
		size += len(data)
		if size > 8*1024*1024 {
			return configurationError("application bootstrap source exceeds the discovery size limit")
		}
		if filepath.Base(path) == "spring.factories" {
			if bytes.Contains(data, []byte("EnvironmentPostProcessor")) ||
				bytes.Contains(data, []byte("ApplicationContextInitializer")) {
				return configurationError("custom Spring bootstrap registrations require runtime configuration evidence")
			}
			return nil
		}
		code, err := javaBootstrapCode(data)
		if err != nil {
			return err
		}
		for _, pattern := range unsupportedSpringBootstrap {
			if pattern.Match(code) {
				return configurationError(
					"custom Spring property sources or datasource construction require runtime configuration evidence",
				)
			}
		}
		return nil
	})
}

func javaBootstrapCode(source []byte) ([]byte, error) {
	code := bytes.Clone(source)
	for index := 0; index < len(source); {
		start := index
		remaining := source[index:]
		if bytes.HasPrefix(remaining, []byte("//")) {
			index += 2
			for index < len(source) && source[index] != '\n' {
				index++
			}
		} else if bytes.HasPrefix(remaining, []byte("/*")) {
			end := bytes.Index(remaining[2:], []byte("*/"))
			if end < 0 {
				return nil, configurationError("application bootstrap has an unterminated comment")
			}
			index += end + 4
		} else if source[index] == '"' || source[index] == '\'' {
			delimiter := source[index : index+1]
			if bytes.HasPrefix(remaining, []byte(`"""`)) {
				delimiter = []byte(`"""`)
			}
			index += len(delimiter)
			closed := false
			for index < len(source) {
				if source[index] == '\\' {
					index = min(index+2, len(source))
				} else if bytes.HasPrefix(source[index:], delimiter) {
					index += len(delimiter)
					closed = true
					break
				} else {
					index++
				}
			}
			if !closed {
				return nil, configurationError("application bootstrap has an unterminated string or character literal")
			}
		} else {
			index++
			continue
		}
		for offset := start; offset < index; offset++ {
			if code[offset] != '\n' {
				code[offset] = ' '
			}
		}
	}
	return code, nil
}
