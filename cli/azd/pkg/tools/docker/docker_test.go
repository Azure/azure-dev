// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

const mockedDockerImgId = "fake-docker-image-id"

func Test_DockerBuild(t *testing.T) {
	cwd := "."
	dockerFile := "./Dockerfile"
	dockerContext := "../"
	platform := DefaultPlatform
	imageName := "IMAGE_NAME"
	buildArgs := []string{"foo=bar"}

	t.Run("NoError", func(t *testing.T) {
		ran := false

		mockContext := mocks.NewMockContext(context.Background())
		docker := NewCli(mockContext.CommandRunner)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "docker build")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			// extract img id file arg. "--iidfile" and path args are expected always at the end
			argsNoFile, value := args.Args[:len(args.Args)-2], args.Args[len(args.Args)-1]

			require.Equal(t, "docker", args.Cmd)
			require.Equal(t, cwd, args.Cwd)
			require.Equal(t, []string{
				"build",
				"-f", dockerFile,
				"--platform", platform,
				"-t", imageName,
				"--build-arg", buildArgs[0],
				dockerContext,
			}, argsNoFile)

			// create the file as expected
			err := os.WriteFile(value, []byte(mockedDockerImgId), 0600)
			require.NoError(t, err)

			return exec.RunResult{
				Stdout:   mockedDockerImgId,
				Stderr:   "",
				ExitCode: 0,
			}, nil
		})

		result, err := docker.Build(
			context.Background(),
			cwd,
			dockerFile,
			platform,
			"",
			dockerContext,
			imageName,
			buildArgs,
			nil,
			nil,
			nil,
		)

		require.Equal(t, true, ran)
		require.Nil(t, err)
		require.Equal(t, mockedDockerImgId, result)
	})

	t.Run("WithError", func(t *testing.T) {
		ran := false
		stdErr := "Error tagging DockerFile"
		customErrorMessage := "example error message"
		imageName := "IMAGE_NAME"
		buildArgs := []string{"foo=bar"}

		mockContext := mocks.NewMockContext(context.Background())
		docker := NewCli(mockContext.CommandRunner)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "docker build")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			// extract img id file arg. "--iidfile" and path args are expected always at the end
			argsNoFile, value := args.Args[:len(args.Args)-2], args.Args[len(args.Args)-1]

			require.Equal(t, "docker", args.Cmd)
			require.Equal(t, cwd, args.Cwd)
			require.Equal(t, []string{
				"build",
				"-f", dockerFile,
				"--platform", platform,
				"-t", imageName,
				"--build-arg", buildArgs[0],
				dockerContext,
			}, argsNoFile)

			// create the file as expected
			err := os.WriteFile(value, []byte(""), 0600)
			require.NoError(t, err)

			return exec.RunResult{
				Stdout:   "",
				Stderr:   stdErr,
				ExitCode: 1,
			}, errors.New(customErrorMessage)
		})

		result, err := docker.Build(
			context.Background(),
			cwd,
			dockerFile,
			platform,
			"",
			dockerContext,
			imageName,
			buildArgs,
			nil,
			nil,
			nil,
		)

		require.Equal(t, true, ran)
		require.NotNil(t, err)
		require.Equal(
			t,
			fmt.Sprintf("building image: %s", customErrorMessage),
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
	platform := DefaultPlatform
	imageName := "IMAGE_NAME"
	buildArgs := []string{"foo=bar"}

	mockContext := mocks.NewMockContext(context.Background())
	docker := NewCli(mockContext.CommandRunner)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker build")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		ran = true

		// extract img id file arg. "--iidfile" and path args are expected always at the end
		argsNoFile, value := args.Args[:len(args.Args)-2], args.Args[len(args.Args)-1]

		require.Equal(t, "docker", args.Cmd)
		require.Equal(t, cwd, args.Cwd)
		require.Equal(t, []string{
			"build",
			"-f", dockerFile,
			"--platform", platform,
			"-t", imageName,
			"--build-arg", buildArgs[0],
			dockerContext,
		}, argsNoFile)

		// create the file as expected
		err := os.WriteFile(value, []byte(mockedDockerImgId), 0600)
		require.NoError(t, err)

		return exec.RunResult{
			Stdout:   mockedDockerImgId,
			Stderr:   "",
			ExitCode: 0,
		}, nil
	})

	result, err := docker.Build(
		context.Background(), cwd, dockerFile, "", "", dockerContext, imageName, buildArgs, nil, nil, nil)

	require.Equal(t, true, ran)
	require.Nil(t, err)
	require.Equal(t, mockedDockerImgId, result)
}

func Test_DockerBuildArgsEmpty(t *testing.T) {
	ran := false
	cwd := "."
	dockerFile := "./Dockerfile"
	dockerContext := "../"
	platform := DefaultPlatform
	imageName := "IMAGE_NAME"
	buildArgs := []string{}

	mockContext := mocks.NewMockContext(context.Background())
	docker := NewCli(mockContext.CommandRunner)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker build")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		ran = true

		// extract img id file arg. "--iidfile" and path args are expected always at the end
		argsNoFile, value := args.Args[:len(args.Args)-2], args.Args[len(args.Args)-1]

		require.Equal(t, "docker", args.Cmd)
		require.Equal(t, cwd, args.Cwd)
		require.Equal(t, []string{
			"build",
			"-f", dockerFile,
			"--platform", platform,
			"-t", imageName,
			dockerContext,
		}, argsNoFile)

		// create the file as expected
		err := os.WriteFile(value, []byte(mockedDockerImgId), 0600)
		require.NoError(t, err)

		return exec.RunResult{
			Stdout:   mockedDockerImgId,
			Stderr:   "",
			ExitCode: 0,
		}, nil
	})

	result, err := docker.Build(
		context.Background(), cwd, dockerFile, "", "", dockerContext, imageName, buildArgs, nil, nil, nil)

	require.Equal(t, true, ran)
	require.Nil(t, err)
	require.Equal(t, mockedDockerImgId, result)
}

func Test_DockerBuildArgsMultiple(t *testing.T) {
	ran := false
	cwd := "."
	dockerFile := "./Dockerfile"
	dockerContext := "../"
	platform := DefaultPlatform
	imageName := "IMAGE_NAME"
	buildArgs := []string{"foo=bar", "bar=baz"}

	mockContext := mocks.NewMockContext(context.Background())
	docker := NewCli(mockContext.CommandRunner)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker build")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		ran = true

		// extract img id file arg. "--iidfile" and path args are expected always at the end
		argsNoFile, value := args.Args[:len(args.Args)-2], args.Args[len(args.Args)-1]

		require.Equal(t, "docker", args.Cmd)
		require.Equal(t, cwd, args.Cwd)
		require.Equal(t, []string{
			"build",
			"-f", dockerFile,
			"--platform", platform,
			"-t", imageName,
			"--build-arg", buildArgs[0],
			"--build-arg", buildArgs[1],
			dockerContext,
		}, argsNoFile)

		// create the file as expected
		err := os.WriteFile(value, []byte(mockedDockerImgId), 0600)
		require.NoError(t, err)

		return exec.RunResult{
			Stdout:   mockedDockerImgId,
			Stderr:   "",
			ExitCode: 0,
		}, nil
	})

	result, err := docker.Build(
		context.Background(), cwd, dockerFile, "", "", dockerContext, imageName, buildArgs, nil, nil, nil)

	require.Equal(t, true, ran)
	require.Nil(t, err)
	require.Equal(t, mockedDockerImgId, result)
}

func Test_DockerTag(t *testing.T) {
	cwd := "."
	imageName := "image-name"
	tag := "customTag"

	t.Run("NoError", func(t *testing.T) {
		ran := false

		mockContext := mocks.NewMockContext(context.Background())
		docker := NewCli(mockContext.CommandRunner)

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
				Stdout:   mockedDockerImgId,
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
		docker := NewCli(mockContext.CommandRunner)

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
			fmt.Sprintf("tagging image: %s", customErrorMessage),
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
		docker := NewCli(mockContext.CommandRunner)

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
				Stdout:   mockedDockerImgId,
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
		docker := NewCli(mockContext.CommandRunner)

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
			fmt.Sprintf("pushing image: %s", customErrorMessage),
			err.Error(),
		)
	})
}

func Test_DockerLogin(t *testing.T) {
	t.Run("NoError", func(t *testing.T) {
		ran := false

		mockContext := mocks.NewMockContext(context.Background())
		docker := NewCli(mockContext.CommandRunner)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "docker login")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, "docker", args.Cmd)
			require.Equal(t, []string{
				"login",
				"--username", "USERNAME",
				"--password-stdin",
				"LOGIN_SERVER",
			}, args.Args)

			return exec.RunResult{
				Stdout:   mockedDockerImgId,
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
		stdErr := "Error logging into docker"
		customErrorMessage := "example error message"

		mockContext := mocks.NewMockContext(context.Background())
		docker := NewCli(mockContext.CommandRunner)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "docker login")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, "docker", args.Cmd)
			require.Equal(t, []string{
				"login",
				"--username", "USERNAME",
				"--password-stdin",
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
		require.Equal(t, fmt.Sprintf("failed logging into %s: %s", "Docker", customErrorMessage), err.Error())
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

func Test_IsSupportedPodmanVersion(t *testing.T) {
	cases := []struct {
		name        string
		version     string
		supported   bool
		expectError bool
	}{
		{
			name:        "Podman_4_3_1",
			version:     "podman version 4.3.1",
			supported:   true,
			expectError: false,
		},
		{
			name:        "Podman_3_0_0",
			version:     "podman version 3.0.0",
			supported:   true,
			expectError: false,
		},
		{
			name:        "Podman_3_4_4",
			version:     "podman version 3.4.4",
			supported:   true,
			expectError: false,
		},
		{
			name:        "Podman_5_0_0",
			version:     "podman version 5.0.0",
			supported:   true,
			expectError: false,
		},
		{
			name:        "Podman_TooOld",
			version:     "podman version 2.9.0",
			supported:   false,
			expectError: false,
		},
		{
			name:        "InvalidFormat",
			version:     "podman ver 4.3.1",
			supported:   false,
			expectError: true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			supported, err := isSupportedPodmanVersion(testCase.version)
			require.Equal(t, testCase.supported, supported)
			if testCase.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func Test_CheckInstalled_Docker(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	docker := NewCli(mockContext.CommandRunner)

	// Mock ToolInPath for docker
	mockContext.CommandRunner.MockToolInPath("docker", nil)

	// Mock docker --version
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker --version")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{
			Stdout:   "Docker version 20.10.17, build 100c701",
			ExitCode: 0,
		}, nil
	})

	// Mock docker ps
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker ps")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{
			Stdout:   "CONTAINER ID   IMAGE     COMMAND   CREATED   STATUS    PORTS     NAMES",
			ExitCode: 0,
		}, nil
	})

	err := docker.CheckInstalled(context.Background())
	require.NoError(t, err)
	require.Equal(t, "docker", docker.containerEngine)
	require.Equal(t, "Docker", docker.Name())
}

func Test_CheckInstalled_Podman(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	docker := NewCli(mockContext.CommandRunner)

	// Mock ToolInPath - docker fails, podman succeeds
	mockContext.CommandRunner.MockToolInPath("docker", errors.New("docker not found"))
	mockContext.CommandRunner.MockToolInPath("podman", nil)

	// Mock podman --version
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "podman --version")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{
			Stdout:   "podman version 4.3.1",
			ExitCode: 0,
		}, nil
	})

	// Mock podman ps
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "podman ps")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{
			Stdout:   "CONTAINER ID  IMAGE  COMMAND  CREATED  STATUS  PORTS  NAMES",
			ExitCode: 0,
		}, nil
	})

	err := docker.CheckInstalled(context.Background())
	require.NoError(t, err)
	require.Equal(t, "podman", docker.containerEngine)
	require.Equal(t, "Podman", docker.Name())
}

func Test_CheckInstalled_EnvVarOverride(t *testing.T) {
	t.Run("DockerOverride", func(t *testing.T) {
		os.Setenv("AZD_CONTAINER_RUNTIME", "docker")
		defer os.Unsetenv("AZD_CONTAINER_RUNTIME")

		mockContext := mocks.NewMockContext(context.Background())
		docker := NewCli(mockContext.CommandRunner)

		mockContext.CommandRunner.MockToolInPath("docker", nil)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "docker --version")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{
				Stdout:   "Docker version 20.10.17, build 100c701",
				ExitCode: 0,
			}, nil
		})

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "docker ps")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{
				Stdout:   "CONTAINER ID   IMAGE     COMMAND   CREATED   STATUS    PORTS     NAMES",
				ExitCode: 0,
			}, nil
		})

		err := docker.CheckInstalled(context.Background())
		require.NoError(t, err)
		require.Equal(t, "docker", docker.containerEngine)
	})

	t.Run("PodmanOverride", func(t *testing.T) {
		os.Setenv("AZD_CONTAINER_RUNTIME", "podman")
		defer os.Unsetenv("AZD_CONTAINER_RUNTIME")

		mockContext := mocks.NewMockContext(context.Background())
		docker := NewCli(mockContext.CommandRunner)

		mockContext.CommandRunner.MockToolInPath("podman", nil)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "podman --version")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{
				Stdout:   "podman version 4.3.1",
				ExitCode: 0,
			}, nil
		})

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "podman ps")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{
				Stdout:   "CONTAINER ID  IMAGE  COMMAND  CREATED  STATUS  PORTS  NAMES",
				ExitCode: 0,
			}, nil
		})

		err := docker.CheckInstalled(context.Background())
		require.NoError(t, err)
		require.Equal(t, "podman", docker.containerEngine)
	})

	t.Run("InvalidOverride", func(t *testing.T) {
		os.Setenv("AZD_CONTAINER_RUNTIME", "invalid")
		defer os.Unsetenv("AZD_CONTAINER_RUNTIME")

		mockContext := mocks.NewMockContext(context.Background())
		docker := NewCli(mockContext.CommandRunner)

		err := docker.CheckInstalled(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported container runtime")
	})
}

func TestSplitDockerImage(t *testing.T) {
	tests := []struct {
		name      string
		fullImg   string
		wantImage string
		wantTag   string
	}{
		{"local image", "local-img", "local-img", ""},
		{"local image with tag", "local-img:tag", "local-img", "tag"},
		{"remote image", "docker.io/remote-img", "docker.io/remote-img", ""},
		{"remote image with tag", "docker.io/remote-img:tag", "docker.io/remote-img", "tag"},
		{"remote image with port and tag", "docker.io:8080/remote-img:tag", "docker.io:8080/remote-img", "tag"},
		{"invalid remote image", "docker.io:8080/remote-img:", "docker.io:8080/remote-img:", ""},
		{"invalid local image", "local-img:", "local-img:", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			image, tag := SplitDockerImage(tt.fullImg)
			require.Equal(t, tt.wantImage, image)
			require.Equal(t, tt.wantTag, tag)
		})
	}
}

func TestIsContainerdEnabled(t *testing.T) {
	tests := []struct {
		name            string
		systemInfoOut   string
		systemInfoErr   error
		expectedEnabled bool
		expectError     bool
	}{
		{
			name: "containerd enabled",
			systemInfoOut: ` [
				["driver", "overlay2"],
				["snapshotter", "io.containerd.snapshotter.v1.overlayfs"]
			]`,
			expectedEnabled: true,
			expectError:     false,
		},
		{
			name: "containerd not enabled",
			systemInfoOut: ` [
				["driver", "overlay2"],
				["backing filesystem", "extfs"]
			]`,
			expectedEnabled: false,
			expectError:     false,
		},
		{
			name:            "empty driver status",
			systemInfoOut:   "",
			expectedEnabled: false,
			expectError:     false,
		},
		{
			name:            "whitespace only output",
			systemInfoOut:   "   \n\t  ",
			expectedEnabled: false,
			expectError:     false,
		},
		{
			name: "containerd string present in other context",
			systemInfoOut: ` [
				["some unrelated field", "io.containerd.snapshotter.v1.something"],
				["driver", "overlay2"]
			]`,
			expectedEnabled: true,
			expectError:     false,
		},
		{
			name:            "command execution error",
			systemInfoErr:   errors.New("docker daemon not running"),
			expectedEnabled: false,
			expectError:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			docker := NewCli(mockContext.CommandRunner)

			mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "docker system info")
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				require.Equal(t, "docker", args.Cmd)
				require.Equal(t, []string{"system", "info", "--format", "{{.DriverStatus}}"}, args.Args)

				if tt.systemInfoErr != nil {
					return exec.RunResult{}, tt.systemInfoErr
				}

				return exec.RunResult{
					Stdout:   tt.systemInfoOut,
					Stderr:   "",
					ExitCode: 0,
				}, nil
			})

			enabled, err := docker.IsContainerdEnabled(context.Background())

			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), "checking docker driver status")
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tt.expectedEnabled, enabled)
		})
	}
}
