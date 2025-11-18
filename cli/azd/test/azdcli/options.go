// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdcli

import "github.com/azure/azure-dev/cli/azd/test/recording"

type option struct {
	Session *recording.Session
}

type Options interface {
	Apply(o *option)
}

type sessionOption struct {
	session *recording.Session
}

func (s *sessionOption) Apply(o *option) {
	o.Session = s.session
}

// WithSession sets a recording session to use for the test.
func WithSession(session *recording.Session) Options {
	return &sessionOption{session: session}
}
