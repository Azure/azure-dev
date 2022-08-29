package docker

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/stretchr/testify/require"
)

func Test_DockerBuild(t *testing.T) {
	docker := NewDocker(DockerArgs{})

	cwd := "."
	dockerFile := "./Dockerfile"
	dockerContext := "../"
	platform := "amd64"

	t.Run("NoError", func(t *testing.T) {
		ran := false

		docker.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, "docker", args.Cmd)
			require.Equal(t, cwd, args.Cwd)
			require.Equal(t, []string{
				"build",
				"-q",
				"-f", dockerFile,
				"--platform", platform,
				dockerContext,
			}, args.Args)

			return executil.RunResult{
				Stdout:   "Docker build output",
				Stderr:   "",
				ExitCode: 0,
			}, nil
		}

		result, err := docker.Build(context.Background(), cwd, dockerFile, platform, dockerContext)

		require.Equal(t, true, ran)
		require.Nil(t, err)
		require.Equal(t, "Docker build output", result)
	})

	t.Run("WithError", func(t *testing.T) {
		ran := false
		stdErr := "Error tagging DockerFile"
		customErrorMessage := "example error message"

		docker.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, "docker", args.Cmd)
			require.Equal(t, cwd, args.Cwd)
			require.Equal(t, []string{
				"build",
				"-q",
				"-f", dockerFile,
				"--platform", platform,
				dockerContext,
			}, args.Args)

			return executil.RunResult{
				Stdout:   "",
				Stderr:   stdErr,
				ExitCode: 1,
			}, errors.New(customErrorMessage)
		}

		result, err := docker.Build(context.Background(), cwd, dockerFile, platform, dockerContext)

		require.Equal(t, true, ran)
		require.NotNil(t, err)
		require.Equal(t, fmt.Sprintf("building image: exit code: 1, stdout: , stderr: %s: %s", stdErr, customErrorMessage), err.Error())
		require.Equal(t, "", result)
	})
}

func Test_DockerBuildEmptyPlatform(t *testing.T) {
	docker := NewDocker(DockerArgs{})

	ran := false
	cwd := "."
	dockerFile := "./Dockerfile"
	dockerContext := "../"
	platform := "amd64"

	docker.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
		ran = true

		require.Equal(t, "docker", args.Cmd)
		require.Equal(t, cwd, args.Cwd)
		require.Equal(t, []string{
			"build",
			"-q",
			"-f", dockerFile,
			"--platform", platform,
			dockerContext,
		}, args.Args)

		return executil.RunResult{
			Stdout:   "Docker build output",
			Stderr:   "",
			ExitCode: 0,
		}, nil
	}

	result, err := docker.Build(context.Background(), cwd, dockerFile, "", dockerContext)

	require.Equal(t, true, ran)
	require.Nil(t, err)
	require.Equal(t, "Docker build output", result)
}

func Test_DockerTag(t *testing.T) {
	docker := NewDocker(DockerArgs{})

	cwd := "."
	imageName := "image-name"
	tag := "customTag"

	t.Run("NoError", func(t *testing.T) {
		ran := false

		docker.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, "docker", args.Cmd)
			require.Equal(t, cwd, args.Cwd)
			require.Equal(t, []string{
				"tag",
				imageName,
				tag,
			}, args.Args)

			return executil.RunResult{
				Stdout:   "Docker build output",
				Stderr:   "",
				ExitCode: 0,
			}, nil
		}

		err := docker.Tag(context.Background(), cwd, imageName, tag)

		require.Equal(t, true, ran)
		require.Nil(t, err)
	})

	t.Run("WithError", func(t *testing.T) {
		ran := false
		stdErr := "Error tagging DockerFile"
		customErrorMessage := "example error message"

		docker.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, "docker", args.Cmd)
			require.Equal(t, cwd, args.Cwd)
			require.Equal(t, []string{
				"tag",
				imageName,
				tag,
			}, args.Args)

			return executil.RunResult{
				Stdout:   "",
				Stderr:   stdErr,
				ExitCode: 1,
			}, errors.New(customErrorMessage)
		}

		err := docker.Tag(context.Background(), cwd, imageName, tag)

		require.Equal(t, true, ran)
		require.NotNil(t, err)
		require.Equal(t, fmt.Sprintf("tagging image: exit code: 1, stdout: , stderr: %s: %s", stdErr, customErrorMessage), err.Error())
	})
}

func Test_DockerPush(t *testing.T) {
	docker := NewDocker(DockerArgs{})

	cwd := "."
	tag := "customTag"

	t.Run("NoError", func(t *testing.T) {
		ran := false

		docker.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, "docker", args.Cmd)
			require.Equal(t, cwd, args.Cwd)
			require.Equal(t, []string{
				"push",
				tag,
			}, args.Args)

			return executil.RunResult{
				Stdout:   "Docker build output",
				Stderr:   "",
				ExitCode: 0,
			}, nil
		}

		err := docker.Push(context.Background(), cwd, tag)

		require.Equal(t, true, ran)
		require.Nil(t, err)
	})

	t.Run("WithError", func(t *testing.T) {
		ran := false
		stdErr := "Error pushing DockerFile"
		customErrorMessage := "example error message"

		docker.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, "docker", args.Cmd)
			require.Equal(t, cwd, args.Cwd)
			require.Equal(t, []string{
				"push",
				tag,
			}, args.Args)

			return executil.RunResult{
				Stdout:   "",
				Stderr:   stdErr,
				ExitCode: 1,
			}, errors.New(customErrorMessage)
		}

		err := docker.Push(context.Background(), cwd, tag)

		require.Equal(t, true, ran)
		require.NotNil(t, err)
		require.Equal(t, fmt.Sprintf("pushing image: exit code: 1, stdout: , stderr: %s: %s", stdErr, customErrorMessage), err.Error())
	})
}
