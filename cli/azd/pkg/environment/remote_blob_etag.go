// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// remoteBlobETagFileName is the local file that records blob ETags for the
// environment snapshot stored in this directory. It is not uploaded.
const remoteBlobETagFileName = "remote-etags.json"

type remoteBlobETagFile struct {
	Blobs map[string]string `json:"blobs"`
}

// loadRemoteBlobETags loads concurrency tokens persisted with the local snapshot.
// A missing file leaves any tokens already on env unchanged.
func loadRemoteBlobETags(azdCtx *azdcontext.AzdContext, env *Environment) error {
	path, err := remoteBlobETagPath(azdCtx, env)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("reading remote environment versions: %w", err)
	}

	var file remoteBlobETagFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("reading remote environment versions: %w", err)
	}

	dotenvPath, configPath, err := remoteStateBlobPaths(env)
	if err != nil {
		return err
	}

	matches := map[string]string{}
	for _, blobPath := range []string{dotenvPath, configPath} {
		if match := file.Blobs[blobPath]; match != "" {
			matches[blobPath] = match
		}
	}
	env.replaceRemoteBlobMatches(matches)
	return nil
}

// writeRemoteBlobETags persists the environment's blob ETags next to the local
// snapshot. An environment with no tokens does not replace an existing file.
func writeRemoteBlobETags(ctx context.Context, azdCtx *azdcontext.AzdContext, env *Environment) error {
	dotenvPath, configPath, err := remoteStateBlobPaths(env)
	if err != nil {
		return err
	}

	current := env.cloneRemoteBlobMatches()
	blobs := map[string]string{}
	for _, blobPath := range []string{dotenvPath, configPath} {
		if match := current[blobPath]; match != "" {
			blobs[blobPath] = match
		}
	}
	if len(blobs) == 0 {
		return nil
	}

	path, err := remoteBlobETagPath(azdCtx, env)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(remoteBlobETagFile{Blobs: blobs})
	if err != nil {
		return fmt.Errorf("recording remote environment versions: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("recording remote environment versions: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), remoteBlobETagFileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("recording remote environment versions: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	if _, err := tmp.Write(append(payload, '\n')); err != nil {
		cleanup()
		return fmt.Errorf("recording remote environment versions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("recording remote environment versions: %w", err)
	}
	if err := osutil.Rename(ctx, tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("recording remote environment versions: %w", err)
	}
	return nil
}

func remoteBlobETagPath(azdCtx *azdcontext.AzdContext, env *Environment) (string, error) {
	path, err := azdCtx.EnvironmentFilePath(env.Name(), remoteBlobETagFileName)
	if err != nil {
		return "", fmt.Errorf("resolving remote environment versions: %w", err)
	}
	return path, nil
}

func remoteStateBlobPaths(env *Environment) (dotenvPath, configPath string, err error) {
	if !IsValidEnvironmentName(env.Name()) {
		return "", "", InvalidEnvironmentNameError(env.Name())
	}
	return fmt.Sprintf("%s/%s", env.Name(), DotEnvFileName),
		fmt.Sprintf("%s/%s", env.Name(), ConfigFileName),
		nil
}
