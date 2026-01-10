// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"io"
	"strings"
)

// DockerfileBuilder provides a fluent API for building Dockerfiles programmatically.
type DockerfileBuilder struct {
	stages     []*DockerfileStage
	globalArgs []dockerfileArg
}

// dockerfileArg represents a build argument
type dockerfileArg struct {
	name         string
	defaultValue string
	hasDefault   bool
}

// NewDockerfileBuilder creates a new DockerfileBuilder instance.
func NewDockerfileBuilder() *DockerfileBuilder {
	return &DockerfileBuilder{
		stages:     make([]*DockerfileStage, 0),
		globalArgs: make([]dockerfileArg, 0),
	}
}

// Arg adds a global ARG statement to define a build-time variable before any stages.
// Global ARG statements appear before the first FROM statement and can be used
// to parameterize the base image selection.
func (b *DockerfileBuilder) Arg(name string, defaultValue ...string) *DockerfileBuilder {
	if name == "" {
		panic("arg name cannot be empty")
	}

	arg := dockerfileArg{name: name}
	if len(defaultValue) > 0 && defaultValue[0] != "" {
		arg.defaultValue = defaultValue[0]
		arg.hasDefault = true
	}

	b.globalArgs = append(b.globalArgs, arg)
	return b
}

// From adds a FROM statement to start a new stage.
// If stageName is provided, it creates a named stage for multi-stage builds.
func (b *DockerfileBuilder) From(image string, stageName ...string) *DockerfileStage {
	if image == "" {
		panic("image cannot be empty")
	}

	var name string
	if len(stageName) > 0 {
		name = stageName[0]
	}

	stage := &DockerfileStage{
		name:       name,
		image:      image,
		statements: make([]dockerfileStatement, 0),
	}

	b.stages = append(b.stages, stage)
	return stage
}

// Build writes the Dockerfile content to the specified writer.
func (b *DockerfileBuilder) Build(w io.Writer) error {
	// Write global ARG statements first
	if len(b.globalArgs) > 0 {
		for _, arg := range b.globalArgs {
			if err := arg.write(w); err != nil {
				return err
			}
		}
		// Add a blank line after global args
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	// Write each stage
	for i, stage := range b.stages {
		if err := stage.writeStage(w); err != nil {
			return err
		}

		// Add a blank line between stages
		if i < len(b.stages)-1 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
	}

	return nil
}

// DockerfileStage represents a stage within a multi-stage Dockerfile.
type DockerfileStage struct {
	name       string
	image      string
	statements []dockerfileStatement
}

// dockerfileStatement is an interface for all Dockerfile statements
type dockerfileStatement interface {
	write(w io.Writer) error
}

// Arg adds an ARG statement to the stage to define a build-time variable.
func (s *DockerfileStage) Arg(name string, defaultValue ...string) *DockerfileStage {
	if name == "" {
		panic("arg name cannot be empty")
	}

	arg := dockerfileArg{name: name}
	if len(defaultValue) > 0 && defaultValue[0] != "" {
		arg.defaultValue = defaultValue[0]
		arg.hasDefault = true
	}

	s.statements = append(s.statements, arg)
	return s
}

// WorkDir adds a WORKDIR statement to set the working directory.
func (s *DockerfileStage) WorkDir(path string) *DockerfileStage {
	if path == "" {
		panic("workdir path cannot be empty")
	}
	s.statements = append(s.statements, dockerfileWorkDir{path: path})
	return s
}

// Run adds a RUN statement to execute a command.
func (s *DockerfileStage) Run(command string) *DockerfileStage {
	if command == "" {
		panic("run command cannot be empty")
	}
	s.statements = append(s.statements, dockerfileRun{command: command})
	return s
}

// Copy adds a COPY statement to copy files from the build context.
func (s *DockerfileStage) Copy(source, destination string, chown ...string) *DockerfileStage {
	if source == "" || destination == "" {
		panic("copy source and destination cannot be empty")
	}

	stmt := dockerfileCopy{
		source:      source,
		destination: destination,
	}
	if len(chown) > 0 && chown[0] != "" {
		stmt.chown = chown[0]
	}

	s.statements = append(s.statements, stmt)
	return s
}

// CopyFrom adds a COPY statement to copy files from another stage.
func (s *DockerfileStage) CopyFrom(from, source, destination string, chown ...string) *DockerfileStage {
	if from == "" || source == "" || destination == "" {
		panic("copyFrom from, source, and destination cannot be empty")
	}

	stmt := dockerfileCopyFrom{
		from:        from,
		source:      source,
		destination: destination,
	}
	if len(chown) > 0 && chown[0] != "" {
		stmt.chown = chown[0]
	}

	s.statements = append(s.statements, stmt)
	return s
}

// Env adds an ENV statement to set an environment variable.
func (s *DockerfileStage) Env(name, value string) *DockerfileStage {
	if name == "" {
		panic("env name cannot be empty")
	}
	s.statements = append(s.statements, dockerfileEnv{name: name, value: value})
	return s
}

// Expose adds an EXPOSE statement to expose a port.
func (s *DockerfileStage) Expose(port int) *DockerfileStage {
	if port <= 0 {
		panic("port must be positive")
	}
	s.statements = append(s.statements, dockerfileExpose{port: port})
	return s
}

// Cmd adds a CMD statement to set the default command.
func (s *DockerfileStage) Cmd(command ...string) *DockerfileStage {
	if len(command) == 0 {
		panic("cmd command cannot be empty")
	}
	s.statements = append(s.statements, dockerfileCmd{command: command})
	return s
}

// Entrypoint adds an ENTRYPOINT statement to set the container entrypoint.
func (s *DockerfileStage) Entrypoint(command ...string) *DockerfileStage {
	if len(command) == 0 {
		panic("entrypoint command cannot be empty")
	}
	s.statements = append(s.statements, dockerfileEntrypoint{command: command})
	return s
}

// User adds a USER statement to set the user for subsequent commands.
func (s *DockerfileStage) User(user string) *DockerfileStage {
	if user == "" {
		panic("user cannot be empty")
	}
	s.statements = append(s.statements, dockerfileUser{user: user})
	return s
}

// RunWithMounts adds a RUN statement with mount options for BuildKit.
func (s *DockerfileStage) RunWithMounts(command string, mounts ...string) *DockerfileStage {
	if command == "" {
		panic("run command cannot be empty")
	}
	s.statements = append(s.statements, dockerfileRunWithMounts{
		command: command,
		mounts:  mounts,
	})
	return s
}

// EmptyLine adds an empty line to the Dockerfile for better readability.
func (s *DockerfileStage) EmptyLine() *DockerfileStage {
	s.statements = append(s.statements, dockerfileEmptyLine{})
	return s
}

// Comment adds a comment to the Dockerfile. Multi-line comments are supported.
func (s *DockerfileStage) Comment(comment string) *DockerfileStage {
	s.statements = append(s.statements, dockerfileComment{comment: comment})
	return s
}

// writeStage writes the stage content
func (s *DockerfileStage) writeStage(w io.Writer) error {
	// Write FROM statement
	var fromStmt string
	if s.name != "" {
		fromStmt = fmt.Sprintf("FROM %s AS %s\n", s.image, s.name)
	} else {
		fromStmt = fmt.Sprintf("FROM %s\n", s.image)
	}

	if _, err := fmt.Fprint(w, fromStmt); err != nil {
		return err
	}

	// Write all statements
	for _, stmt := range s.statements {
		if err := stmt.write(w); err != nil {
			return err
		}
	}

	return nil
}

// Statement implementations

type dockerfileWorkDir struct {
	path string
}

func (s dockerfileWorkDir) write(w io.Writer) error {
	_, err := fmt.Fprintf(w, "WORKDIR %s\n", s.path)
	return err
}

type dockerfileRun struct {
	command string
}

func (s dockerfileRun) write(w io.Writer) error {
	_, err := fmt.Fprintf(w, "RUN %s\n", s.command)
	return err
}

type dockerfileCopy struct {
	source      string
	destination string
	chown       string
}

func (s dockerfileCopy) write(w io.Writer) error {
	if s.chown != "" {
		_, err := fmt.Fprintf(w, "COPY --chown=%s %s %s\n", s.chown, s.source, s.destination)
		return err
	}
	_, err := fmt.Fprintf(w, "COPY %s %s\n", s.source, s.destination)
	return err
}

type dockerfileCopyFrom struct {
	from        string
	source      string
	destination string
	chown       string
}

func (s dockerfileCopyFrom) write(w io.Writer) error {
	if s.chown != "" {
		_, err := fmt.Fprintf(w, "COPY --from=%s --chown=%s %s %s\n", s.from, s.chown, s.source, s.destination)
		return err
	}
	_, err := fmt.Fprintf(w, "COPY --from=%s %s %s\n", s.from, s.source, s.destination)
	return err
}

type dockerfileEnv struct {
	name  string
	value string
}

func (s dockerfileEnv) write(w io.Writer) error {
	// Escape the value if it contains spaces or special characters
	value := s.value
	if strings.ContainsAny(value, " \t\n\"'") {
		value = fmt.Sprintf("\"%s\"", strings.ReplaceAll(value, "\"", "\\\""))
	}
	_, err := fmt.Fprintf(w, "ENV %s=%s\n", s.name, value)
	return err
}

type dockerfileExpose struct {
	port int
}

func (s dockerfileExpose) write(w io.Writer) error {
	_, err := fmt.Fprintf(w, "EXPOSE %d\n", s.port)
	return err
}

type dockerfileCmd struct {
	command []string
}

func (s dockerfileCmd) write(w io.Writer) error {
	// Format as JSON array
	parts := make([]string, len(s.command))
	for i, arg := range s.command {
		parts[i] = fmt.Sprintf("\"%s\"", strings.ReplaceAll(arg, "\"", "\\\""))
	}
	_, err := fmt.Fprintf(w, "CMD [%s]\n", strings.Join(parts, ", "))
	return err
}

type dockerfileEntrypoint struct {
	command []string
}

func (s dockerfileEntrypoint) write(w io.Writer) error {
	// Format as JSON array
	parts := make([]string, len(s.command))
	for i, arg := range s.command {
		parts[i] = fmt.Sprintf("\"%s\"", strings.ReplaceAll(arg, "\"", "\\\""))
	}
	_, err := fmt.Fprintf(w, "ENTRYPOINT [%s]\n", strings.Join(parts, ", "))
	return err
}

type dockerfileUser struct {
	user string
}

func (s dockerfileUser) write(w io.Writer) error {
	_, err := fmt.Fprintf(w, "USER %s\n", s.user)
	return err
}

type dockerfileRunWithMounts struct {
	command string
	mounts  []string
}

func (s dockerfileRunWithMounts) write(w io.Writer) error {
	var mountsStr string
	for _, mount := range s.mounts {
		mountsStr += fmt.Sprintf(" --mount=%s", mount)
	}
	_, err := fmt.Fprintf(w, "RUN%s %s\n", mountsStr, s.command)
	return err
}

type dockerfileEmptyLine struct{}

func (s dockerfileEmptyLine) write(w io.Writer) error {
	_, err := fmt.Fprintln(w)
	return err
}

type dockerfileComment struct {
	comment string
}

func (s dockerfileComment) write(w io.Writer) error {
	// Handle multi-line comments
	lines := strings.Split(s.comment, "\n")
	for _, line := range lines {
		if _, err := fmt.Fprintf(w, "# %s\n", line); err != nil {
			return err
		}
	}
	return nil
}

func (a dockerfileArg) write(w io.Writer) error {
	if a.hasDefault {
		_, err := fmt.Fprintf(w, "ARG %s=%s\n", a.name, a.defaultValue)
		return err
	}
	_, err := fmt.Fprintf(w, "ARG %s\n", a.name)
	return err
}
