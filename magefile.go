//go:build mage
// +build mage

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/magefile/mage/mg"
)

type AZD mg.Namespace

func (a AZD) Build(ctx context.Context) error {
	cmdStr, cmd := runIn(
		".",
		"go",
		"build",
		"-o",
		"./bin/azd",
		"./cli/azd",
	)
	fmt.Println(cmdStr)
	return cmd()
}

func (a AZD) Test(ctx context.Context) error {
	cmdStr, cmd := runIn(
		".",
		"go",
		"test",
		"./...",
	)
	fmt.Println(cmdStr)
	return cmd()
}

func runIn(cwd string, cmd string, args ...string) (string, func() error) {
	c := exec.Command(cmd, args...)
	c.Dir = cwd
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.String(), func() error {
		return c.Run()
	}
}
