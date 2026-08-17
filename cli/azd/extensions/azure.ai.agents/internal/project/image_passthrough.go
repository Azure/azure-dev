// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

// cSpell:ignore protowire

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/protobuf/encoding/protowire"
)

const imagePassthroughFieldNumber = 11

// EnableDockerImagePassthrough enables the core azd image passthrough option.
//
// The extension currently builds against an azd SDK version that predates this
// protobuf field. Encoding it as an unknown field keeps the extension compatible
// with that SDK while allowing newer azd hosts to consume the option.
func EnableDockerImagePassthrough(options *azdext.DockerProjectOptions) {
	unknown := options.ProtoReflect().GetUnknown()
	unknown = protowire.AppendTag(unknown, imagePassthroughFieldNumber, protowire.VarintType)
	unknown = protowire.AppendVarint(unknown, 1)
	options.ProtoReflect().SetUnknown(unknown)
}

// DockerImagePassthrough reports whether the core azd image passthrough option is enabled.
func DockerImagePassthrough(options *azdext.DockerProjectOptions) bool {
	if options == nil {
		return false
	}

	unknown := options.ProtoReflect().GetUnknown()
	for len(unknown) > 0 {
		number, wireType, tagLength := protowire.ConsumeTag(unknown)
		if tagLength < 0 {
			return false
		}
		unknown = unknown[tagLength:]

		if number == imagePassthroughFieldNumber && wireType == protowire.VarintType {
			value, valueLength := protowire.ConsumeVarint(unknown)
			return valueLength >= 0 && value != 0
		}

		fieldLength := protowire.ConsumeFieldValue(number, wireType, unknown)
		if fieldLength < 0 {
			return false
		}
		unknown = unknown[fieldLength:]
	}

	return false
}
