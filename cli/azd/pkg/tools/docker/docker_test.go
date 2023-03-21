package docker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_DockerBuild(t *testing.T) {

	cwd := "."
	dockerFile := "./Dockerfile"
	dockerContext := "../"
	platform := "amd64"

	t.Run("NoError", func(t *testing.T) {
		ran := false

		mockContext := mocks.NewMockContext(context.Background())
		docker := NewDocker(mockContext.CommandRunner)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "docker build")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
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

			return exec.RunResult{
				Stdout:   "Docker build output",
				Stderr:   "",
				ExitCode: 0,
			}, nil
		})

		result, err := docker.Build(context.Background(), cwd, dockerFile, platform, dockerContext)

		require.Equal(t, true, ran)
		require.Nil(t, err)
		require.Equal(t, "Docker build output", result)
	})

	t.Run("WithError", func(t *testing.T) {
		ran := false
		stdErr := "Error tagging DockerFile"
		customErrorMessage := "example error message"

		mockContext := mocks.NewMockContext(context.Background())
		docker := NewDocker(mockContext.CommandRunner)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "docker build")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
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

			return exec.RunResult{
				Stdout:   "",
				Stderr:   stdErr,
				ExitCode: 1,
			}, errors.New(customErrorMessage)
		})

		result, err := docker.Build(context.Background(), cwd, dockerFile, platform, dockerContext)

		require.Equal(t, true, ran)
		require.NotNil(t, err)
		require.Equal(
			t,
			fmt.Sprintf("building image: exit code: 1, stdout: , stderr: %s: %s", stdErr, customErrorMessage),
			err.Error(),
		)
		require.Equal(t, "", result)
	})
}

func Test_DockerBuildEmptyPlatform(t *testing.T) {
	ran := false
	cwd := "."
	dockerFile := "./Dockerfile"
	dockerContext := "../"
	platform := "amd64"

	mockContext := mocks.NewMockContext(context.Background())
	docker := NewDocker(mockContext.CommandRunner)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker build")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
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

		return exec.RunResult{
			Stdout:   "Docker build output",
			Stderr:   "",
			ExitCode: 0,
		}, nil
	})

	result, err := docker.Build(context.Background(), cwd, dockerFile, "", dockerContext)

	require.Equal(t, true, ran)
	require.Nil(t, err)
	require.Equal(t, "Docker build output", result)
}

func Test_DockerTag(t *testing.T) {
	cwd := "."
	imageName := "image-name"
	tag := "customTag"

	t.Run("NoError", func(t *testing.T) {
		ran := false

		mockContext := mocks.NewMockContext(context.Background())
		docker := NewDocker(mockContext.CommandRunner)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "docker tag")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, "docker", args.Cmd)
			require.Equal(t, cwd, args.Cwd)
			require.Equal(t, []string{
				"tag",
				imageName,
				tag,
			}, args.Args)

			return exec.RunResult{
				Stdout:   "Docker build output",
				Stderr:   "",
				ExitCode: 0,
			}, nil
		})

		err := docker.Tag(context.Background(), cwd, imageName, tag)

		require.Equal(t, true, ran)
		require.Nil(t, err)
	})

	t.Run("WithError", func(t *testing.T) {
		ran := false
		stdErr := "Error tagging DockerFile"
		customErrorMessage := "example error message"

		mockContext := mocks.NewMockContext(context.Background())
		docker := NewDocker(mockContext.CommandRunner)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "docker tag")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, "docker", args.Cmd)
			require.Equal(t, cwd, args.Cwd)
			require.Equal(t, []string{
				"tag",
				imageName,
				tag,
			}, args.Args)

			return exec.RunResult{
				Stdout:   "",
				Stderr:   stdErr,
				ExitCode: 1,
			}, errors.New(customErrorMessage)
		})

		err := docker.Tag(context.Background(), cwd, imageName, tag)

		require.Equal(t, true, ran)
		require.NotNil(t, err)
		require.Equal(
			t,
			fmt.Sprintf("tagging image: exit code: 1, stdout: , stderr: %s: %s", stdErr, customErrorMessage),
			err.Error(),
		)
	})
}

func Test_DockerPush(t *testing.T) {
	cwd := "."
	tag := "customTag"

	t.Run("NoError", func(t *testing.T) {
		ran := false

		mockContext := mocks.NewMockContext(context.Background())
		docker := NewDocker(mockContext.CommandRunner)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "docker push")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, "docker", args.Cmd)
			require.Equal(t, cwd, args.Cwd)
			require.Equal(t, []string{
				"push",
				tag,
			}, args.Args)

			return exec.RunResult{
				Stdout:   "Docker build output",
				Stderr:   "",
				ExitCode: 0,
			}, nil
		})

		err := docker.Push(context.Background(), cwd, tag)

		require.Equal(t, true, ran)
		require.Nil(t, err)
	})

	t.Run("WithError", func(t *testing.T) {
		ran := false
		stdErr := "Error pushing DockerFile"
		customErrorMessage := "example error message"

		mockContext := mocks.NewMockContext(context.Background())
		docker := NewDocker(mockContext.CommandRunner)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "docker push")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, "docker", args.Cmd)
			require.Equal(t, cwd, args.Cwd)
			require.Equal(t, []string{
				"push",
				tag,
			}, args.Args)

			return exec.RunResult{
				Stdout:   "",
				Stderr:   stdErr,
				ExitCode: 1,
			}, errors.New(customErrorMessage)
		})

		err := docker.Push(context.Background(), cwd, tag)

		require.Equal(t, true, ran)
		require.NotNil(t, err)
		require.Equal(
			t,
			fmt.Sprintf("pushing image: exit code: 1, stdout: , stderr: %s: %s", stdErr, customErrorMessage),
			err.Error(),
		)
	})
}

func Test_DockerLogin(t *testing.T) {
	cwd := "."

	t.Run("NoError", func(t *testing.T) {
		ran := false

		mockContext := mocks.NewMockContext(context.Background())
		docker := NewDocker(mockContext.CommandRunner)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "docker login")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, "docker", args.Cmd)
			require.Equal(t, cwd, args.Cwd)
			require.Equal(t, []string{
				"login",
				"--username", "USERNAME",
				"--password", "PASSWORD",
				"LOGIN_SERVER",
			}, args.Args)

			return exec.RunResult{
				Stdout:   "Docker build output",
				Stderr:   "",
				ExitCode: 0,
			}, nil
		})

		err := docker.Login(context.Background(), "LOGIN_SERVER", "USERNAME", "PASSWORD")

		require.Equal(t, true, ran)
		require.Nil(t, err)
	})

	t.Run("WithError", func(t *testing.T) {
		ran := false
		stdErr := "failed logging into docker"
		customErrorMessage := "example error message"

		mockContext := mocks.NewMockContext(context.Background())
		docker := NewDocker(mockContext.CommandRunner)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "docker login")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, "docker", args.Cmd)
			require.Equal(t, cwd, args.Cwd)
			require.Equal(t, []string{
				"login",
				"--username", "USERNAME",
				"--password", "PASSWORD",
				"LOGIN_SERVER",
			}, args.Args)

			return exec.RunResult{
				Stdout:   "",
				Stderr:   stdErr,
				ExitCode: 1,
			}, errors.New(customErrorMessage)
		})

		err := docker.Login(context.Background(), "LOGIN_SERVER", "USERNAME", "PASSWORD")

		require.Equal(t, true, ran)
		require.NotNil(t, err)
		require.Equal(t, fmt.Sprintf("%s: %s", stdErr, customErrorMessage), err.Error())
	})
}

func Test_IsSupportedDockerVersion(t *testing.T) {
	cases := []struct {
		name        string
		version     string
		supported   bool
		expectError bool
	}{
		{
			name:        "CI_Linux",
			version:     "Docker version 20.10.17+azure-1, build 100c70180fde3601def79a59cc3e996aa553c9b9",
			supported:   true,
			expectError: false,
		},
		{
			name:        "CI_Mac",
			version:     "Docker version 17.09.0-ce, build afdb6d4",
			supported:   true,
			expectError: false,
		},
		{
			name:        "CI_Windows",
			version:     "Docker version master-dockerproject-2022-03-26, build dd7397342a",
			supported:   true,
			expectError: false,
		},
		{
			name:        "DockerDesktop_Windows",
			version:     "Docker version 20.10.17, build 100c701",
			supported:   true,
			expectError: false,
		},
		{
			name:        "NotNewEnough",
			version:     "Docker version 17.06.0-ce, build badf00d",
			supported:   false,
			expectError: false,
		},
		{
			name:        "UnknownScheme",
			version:     "Docker version some-new-scheme-we-don-t-know-about-2021-01-01, build badf00d",
			supported:   false,
			expectError: true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			supported, err := isSupportedDockerVersion(testCase.version)
			require.Equal(t, testCase.supported, supported)
			if testCase.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
