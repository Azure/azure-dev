// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package containerregistry

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

// PackRemoteBuildSource creates a tarball of the specified context directory into a temporary file and returns the path to
// it. It ensures that the dockerfile is present in the tarball and returns the relative path of it in the archive. If the
// dockerfile is located outside of the context, it is added to the root of the archive with a unique prefix.
//
// This tarball may be used as source input for the ACR Remote Build feature.
//
// On error, if the archive had been created, the path is returned. The caller should ensure it is removed.
//
// A `.dockerignore` file may be used to control what is included in the build context. If a file with the same path as
// `dockerfile` exists but with an additional `.dockerignore` suffix, it is used as the ignore file. Otherwise, if a
// `.dockerignore` file exists in the root of the context, it is used.
//
// Any folders named `.git` is excluded from the produced archive.
func PackRemoteBuildSource(ctx context.Context, root string, dockerfile string) (string, string, error) {
	var ignores []string

	// Like docker, we allow the use of a .dockerignore file to control what is included in the build context.
	candidates := []string{dockerfile + ".dockerignore", filepath.Join(root, ".dockerignore")}

	for _, candidate := range candidates {
		f, err := os.Open(candidate)
		if err == nil {
			defer f.Close()
			i, err := ignorefile.ReadAll(f)
			if err != nil {
				return "", "", err
			}
			ignores = i
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}
	}

	contextArchive, err := os.CreateTemp("", "azd-docker-context*.tar.gz")
	if err != nil {
		return "", "", err
	}
	defer contextArchive.Close()

	gw := gzip.NewWriter(contextArchive)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	var dockerfileArchivePath string

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if filepath.Base(path) == ".git" {
				return filepath.SkipDir
			}

			return nil
		}

		archivePath := filepath.ToSlash(path[len(root)+1:])

		ignore, err := patternmatcher.MatchesOrParentMatches(archivePath, ignores)
		if err != nil {
			return err
		}

		if !ignore {
			info, err := d.Info()
			if err != nil {
				return err
			}

			hdr, err := tar.FileInfoHeader(info, info.Name())
			if err != nil {
				return err
			}

			hdr.Name = archivePath
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}

			err = func() error {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				defer f.Close()

				_, err = io.Copy(tw, f)
				if err != nil {
					return err
				}

				return nil
			}()
			if err != nil {
				return err
			}

			if path == dockerfile {
				dockerfileArchivePath = archivePath
			}
		}

		return nil
	})

	if err != nil {
		return contextArchive.Name(), dockerfileArchivePath, err
	}

	// If we didn't see the dockerfile in the context, add it to the archive at the root with a unique name.
	if dockerfileArchivePath == "" {
		f, err := os.Open(dockerfile)
		if err != nil {
			return contextArchive.Name(), dockerfileArchivePath, err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return contextArchive.Name(), dockerfileArchivePath, err
		}

		hdr, err := tar.FileInfoHeader(info, info.Name())
		if err != nil {
			return contextArchive.Name(), dockerfileArchivePath, err
		}

		uniqueName := uuid.NewString() + "_" + filepath.Base(dockerfile)

		hdr.Name = uniqueName
		if err := tw.WriteHeader(hdr); err != nil {
			return contextArchive.Name(), dockerfileArchivePath, err
		}

		_, err = io.Copy(tw, f)
		if err != nil {
			return contextArchive.Name(), dockerfileArchivePath, err
		}

		dockerfileArchivePath = uniqueName
	}

	return contextArchive.Name(), dockerfileArchivePath, err
}
