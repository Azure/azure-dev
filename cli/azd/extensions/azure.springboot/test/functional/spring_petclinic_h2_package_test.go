// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_CLI_SpringPetclinic_H2NativePackage(t *testing.T) {
	if os.Getenv("AZD_TEST_SPRING_PETCLINIC_PACKAGE") != "1" {
		t.Skip("set AZD_TEST_SPRING_PETCLINIC_PACKAGE=1 for the separate H2 native gate")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
	defer cancel()
	cli := newSpringCLI(t)
	configDir := tempDirWithDiagnostics(t)
	installSpringExtensionFixture(t, ctx, configDir)
	source := filepath.Join(azdSourcePath(t), "extensions", "azure.springboot", "test", "functional",
		"testdata", "spring-petclinic-managedidentity")
	projectDir := tempDirWithDiagnostics(t)
	require.NoError(t, os.CopyFS(projectDir, os.DirFS(source)))
	const overlay = `name: spring-petclinic
requiredVersions:
  azd: ">=1.37.2"
  extensions:
    azure.springboot: ">=0.1.0"
infra:
  provider: bicep
  path: infra
  module: main
services:
  petclinic:
    project: .
    language: java
    host: springbootapp
    module: petclinic
`
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "azure.yaml"), []byte(overlay), 0o600))
	envName := "h2-native-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	referencePrefix := "spring-petclinic/petclinic-" + envName + ":"
	require.Empty(t, nativeImageTags(t, ctx, referencePrefix))
	registerNativeImageCleanup(t, referencePrefix)
	cli.WorkingDirectory = projectDir
	cli.Env = environmentWithout(cli.Env, "AZD_CONFIG_DIR", "SPRING_DATABASE_TYPE", "SPRING_DATABASE_PASSWORD")
	cli.Env = append(cli.Env, "AZD_CONFIG_DIR="+configDir)
	result, err := cli.RunCommand(ctx, "init", "-e", envName, "--no-prompt")
	require.NoError(t, err, result.Stdout+result.Stderr)
	result, err = cli.RunCommand(ctx, "package", "--no-prompt")
	require.NoError(t, err, result.Stdout+result.Stderr)
	images := nativeImageTags(t, ctx, referencePrefix)
	require.Len(t, images, 1)
	require.NoError(t, fs.WalkDir(os.DirFS(source), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || path == "azure.yaml" {
			return nil
		}
		expected, err := fs.ReadFile(os.DirFS(source), path)
		if err != nil {
			return err
		}
		actual, err := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(path)))
		if err != nil {
			return err
		}
		require.Equal(t, expected, actual, path)
		return nil
	}))
	verifyH2ContainerStartup(t, ctx, images[0])
	t.Log("separate runtime-H2 MVC fixture: native packaging, unchanged source and actual H2 database health")
}
