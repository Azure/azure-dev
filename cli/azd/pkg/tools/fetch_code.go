// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"errors"
	"fmt"
	"strings"
)

var FetchCodeNotFoundError = errors.New("Repository was not found.")

type gitRepo struct {
	host string
	slug string
}

func parseAsGit(url string) (gitRepo, error) {
	hostAndSlug := strings.Split(strings.Split(url, "@")[1], ":")

	return gitRepo{
		host: hostAndSlug[0],
		slug: strings.Split(hostAndSlug[1], ".git")[0],
	}, nil
}

func parseAsHttp(url string) (gitRepo, error) {
	hostAndSlug := strings.Split(strings.Split(url, "://")[1], "/")

	return gitRepo{
		host: hostAndSlug[0],
		slug: strings.Split(strings.Join(hostAndSlug[1:], "/"), ".git")[0],
	}, nil
}

func parseRepoUrl(url string) (gitRepo, error) {
	var result gitRepo
	var err error

	if strings.HasPrefix(url, "git") {
		result, err = parseAsGit(url)
	} else if strings.HasPrefix(url, "http") {
		result, err = parseAsHttp(url)
	}

	if err != nil {
		return result, fmt.Errorf("parsing repo url: %w", err)
	}

	return result, nil
}
