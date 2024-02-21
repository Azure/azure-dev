// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

//go:embed all:testdata/samples/*
var samples embed.FS

func samplePath(paths ...string) string {
	elem := append([]string{"testdata", "samples"}, paths...)
	return path.Join(elem...)
}

// copySample copies the given sample to targetRoot.
func copySample(targetRoot string, sampleName string) error {
	sampleRoot := samplePath(sampleName)

	return fs.WalkDir(samples, sampleRoot, func(name string, d fs.DirEntry, err error) error {
		// If there was some error that was preventing is from walking into the directory, just fail now,
		// not much we can do to recover.
		if err != nil {
			return err
		}
		targetPath := filepath.Join(targetRoot, name[len(sampleRoot):])

		if d.IsDir() {
			return os.MkdirAll(targetPath, osutil.PermissionDirectory)
		}

		contents, err := fs.ReadFile(samples, name)
		if err != nil {
			return fmt.Errorf("reading sample file: %w", err)
		}
		return os.WriteFile(targetPath, contents, osutil.PermissionFile)
	})
}
