// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import "errors"

// InvocationMetadataProvider exposes the extension invocation context carried
// by an error without changing its classification or user-facing message.
type InvocationMetadataProvider interface {
	error
	InvocationExtensionId() string
	InvocationExtensionVersion() string
	InvocationEvent() string
}

// InvocationError adds extension invocation metadata while preserving the
// original error chain and message.
type InvocationError struct {
	Err              error
	ExtensionId      string
	ExtensionVersion string
	Event            string
}

// WrapInvocationError adds invocation metadata without changing the error
// chain or user-facing message.
func WrapInvocationError(err error, extensionId, extensionVersion, event string) error {
	if err == nil {
		return nil
	}

	if metadata, ok := errors.AsType[InvocationMetadataProvider](err); ok {
		if extensionId == "" {
			extensionId = metadata.InvocationExtensionId()
		}
		if extensionVersion == "" {
			extensionVersion = metadata.InvocationExtensionVersion()
		}
		if event == "" {
			event = metadata.InvocationEvent()
		}
	}

	return &InvocationError{
		Err:              err,
		ExtensionId:      extensionId,
		ExtensionVersion: extensionVersion,
		Event:            event,
	}
}

func (e *InvocationError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}

	return e.Err.Error()
}

func (e *InvocationError) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.Err
}

func (e *InvocationError) InvocationExtensionId() string {
	if e == nil {
		return ""
	}

	return e.ExtensionId
}

func (e *InvocationError) InvocationExtensionVersion() string {
	if e == nil {
		return ""
	}

	return e.ExtensionVersion
}

func (e *InvocationError) InvocationEvent() string {
	if e == nil {
		return ""
	}

	return e.Event
}
