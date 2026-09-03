// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package contracts

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	v1 "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
)

func TestStableContractIsSubsetOfBeta(t *testing.T) {
	t.Parallel()

	stable := contractFiles(t, "azd.extensions.v1")
	beta := contractFiles(t, "azd.extensions.v1beta")

	require.Len(t, stable, 17)
	require.GreaterOrEqual(t, len(beta), len(stable))
	require.NoError(t, validateStableSubset(stable, beta))
}

func TestPreviewOnlyServicesAreExcludedFromStable(t *testing.T) {
	t.Parallel()

	stable := contractFiles(t, "azd.extensions.v1")
	beta := contractFiles(t, "azd.extensions.v1beta")

	for _, fileName := range []string{"compose.proto", "copilot.proto", "telemetry.proto"} {
		require.NotContains(t, stable, fileName)
		require.Contains(t, beta, fileName)
		require.NotEmpty(t, beta[fileName].Services())
	}
}

func TestStableSubsetAllowsAdditiveBetaFieldsAndMethods(t *testing.T) {
	t.Parallel()

	stable := contractFiles(t, "azd.extensions.v1")
	beta := contractFiles(t, "azd.extensions.v1beta")
	account := proto.Clone(
		protodesc.ToFileDescriptorProto(beta["account.proto"]),
	).(*descriptorpb.FileDescriptorProto)

	lookupRequest := findMessageProto(t, account, "LookupTenantRequest")
	lookupRequest.Field = append(lookupRequest.Field, &descriptorpb.FieldDescriptorProto{
		Name:     new("preview_label"),
		JsonName: new("previewLabel"),
		Number:   proto.Int32(100),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
	})
	account.Service[0].Method = append(account.Service[0].Method, &descriptorpb.MethodDescriptorProto{
		Name:       new("PreviewLookup"),
		InputType:  new(".azd.extensions.v1beta.LookupTenantRequest"),
		OutputType: new(".azd.extensions.v1beta.LookupTenantResponse"),
	})

	additiveAccount, err := protodesc.NewFile(account, protoregistry.GlobalFiles)
	require.NoError(t, err)
	additiveBeta := cloneContractFiles(beta)
	additiveBeta["account.proto"] = additiveAccount
	require.NoError(t, validateStableSubset(stable, additiveBeta))
}

func TestStableSubsetRejectsSharedFieldTypeChange(t *testing.T) {
	t.Parallel()

	stable := contractFiles(t, "azd.extensions.v1")
	beta := contractFiles(t, "azd.extensions.v1beta")
	account := proto.Clone(
		protodesc.ToFileDescriptorProto(beta["account.proto"]),
	).(*descriptorpb.FileDescriptorProto)

	lookupRequest := findMessageProto(t, account, "LookupTenantRequest")
	lookupRequest.Field[0].Type = descriptorpb.FieldDescriptorProto_TYPE_BYTES.Enum()

	incompatibleAccount, err := protodesc.NewFile(account, protoregistry.GlobalFiles)
	require.NoError(t, err)
	incompatibleBeta := cloneContractFiles(beta)
	incompatibleBeta["account.proto"] = incompatibleAccount

	err = validateStableSubset(stable, incompatibleBeta)
	require.Error(t, err)
	require.Contains(t, err.Error(), "LookupTenantRequest.subscription_id")
	require.Contains(t, err.Error(), "kind differs")
}

func TestVersionedFullMethodNames(t *testing.T) {
	tests := []struct {
		name   string
		stable string
		beta   string
	}{
		{
			name:   "unary",
			stable: v1.AccountService_ListSubscriptions_FullMethodName,
			beta:   v1beta.AccountService_ListSubscriptions_FullMethodName,
		},
		{
			name:   "stream",
			stable: v1.ValidationService_Stream_FullMethodName,
			beta:   v1beta.ValidationService_Stream_FullMethodName,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			suffix := strings.TrimPrefix(test.stable, "/azd.extensions.v1")
			require.NotEqual(t, test.stable, suffix)
			require.Equal(t, "/azd.extensions.v1beta"+suffix, test.beta)
		})
	}
}

func TestBetaIncludesCurrentStableAdditions(t *testing.T) {
	imagePassthrough := (&v1beta.DockerProjectOptions{}).
		ProtoReflect().
		Descriptor().
		Fields().
		ByName("image_passthrough")
	require.NotNil(t, imagePassthrough)
	require.Equal(t, protoreflect.FieldNumber(11), imagePassthrough.Number())

	allowEmptySelection := (&v1beta.MultiSelectOptions{}).
		ProtoReflect().
		Descriptor().
		Fields().
		ByName("allow_empty_selection")
	require.NotNil(t, allowEmptySelection)
	require.Equal(t, protoreflect.FieldNumber(8), allowEmptySelection.Number())

	telemetry := v1beta.File_azd_extensions_v1beta_telemetry_proto.
		Services().
		ByName("TelemetryService")
	require.NotNil(t, telemetry)
	require.NotNil(t, telemetry.Methods().ByName("ReportUsage"))
}

func validateStableSubset(
	stableFiles map[string]protoreflect.FileDescriptor,
	betaFiles map[string]protoreflect.FileDescriptor,
) error {
	stableMessages, stableEnums := collectTypes(stableFiles)
	betaMessages, betaEnums := collectTypes(betaFiles)

	messageNames := sortedDescriptorNames(stableMessages)
	for _, stableName := range messageNames {
		stableMessage := stableMessages[stableName]
		betaName := betaContractName(stableName)
		betaMessage, ok := betaMessages[betaName]
		if !ok {
			return fmt.Errorf("stable message %s is missing from beta", stableName)
		}
		if err := validateMessageSubset(stableMessage, betaMessage); err != nil {
			return err
		}
	}

	enumNames := sortedDescriptorNames(stableEnums)
	for _, stableName := range enumNames {
		stableEnum := stableEnums[stableName]
		betaName := betaContractName(stableName)
		betaEnum, ok := betaEnums[betaName]
		if !ok {
			return fmt.Errorf("stable enum %s is missing from beta", stableName)
		}
		if err := validateEnumSubset(stableEnum, betaEnum); err != nil {
			return err
		}
	}

	fileNames := make([]string, 0, len(stableFiles))
	for name := range stableFiles {
		fileNames = append(fileNames, name)
	}
	slices.Sort(fileNames)
	for _, name := range fileNames {
		stableFile := stableFiles[name]
		betaFile, ok := betaFiles[name]
		if !ok {
			return fmt.Errorf("stable contract file %s is missing from beta", name)
		}
		if err := validateServicesSubset(stableFile.Services(), betaFile.Services()); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}

	return nil
}

func validateMessageSubset(stable, beta protoreflect.MessageDescriptor) error {
	if stable.IsMapEntry() != beta.IsMapEntry() {
		return fmt.Errorf("%s map-entry shape differs between stable and beta", stable.FullName())
	}

	for index := range stable.Oneofs().Len() {
		stableOneof := stable.Oneofs().Get(index)
		betaOneof := beta.Oneofs().ByName(stableOneof.Name())
		if betaOneof == nil {
			return fmt.Errorf("%s oneof %s is missing from beta", stable.FullName(), stableOneof.Name())
		}
		if stableOneof.IsSynthetic() != betaOneof.IsSynthetic() {
			return fmt.Errorf("%s oneof %s optional shape differs", stable.FullName(), stableOneof.Name())
		}
	}

	for index := range stable.Fields().Len() {
		stableField := stable.Fields().Get(index)
		betaField := beta.Fields().ByNumber(stableField.Number())
		fieldName := stableField.FullName()
		if betaField == nil {
			return fmt.Errorf("%s field number %d is missing from beta", fieldName, stableField.Number())
		}
		if stableField.Name() != betaField.Name() {
			return fmt.Errorf(
				"%s field number %d was renamed to %s in beta",
				fieldName,
				stableField.Number(),
				betaField.Name(),
			)
		}
		if stableField.Kind() != betaField.Kind() {
			return fmt.Errorf(
				"%s kind differs: stable %s, beta %s",
				fieldName,
				stableField.Kind(),
				betaField.Kind(),
			)
		}
		if stableField.Cardinality() != betaField.Cardinality() ||
			stableField.IsList() != betaField.IsList() ||
			stableField.IsMap() != betaField.IsMap() {
			return fmt.Errorf("%s cardinality differs between stable and beta", fieldName)
		}
		if stableField.HasPresence() != betaField.HasPresence() ||
			stableField.HasOptionalKeyword() != betaField.HasOptionalKeyword() {
			return fmt.Errorf("%s presence differs between stable and beta", fieldName)
		}
		if oneofName(stableField) != oneofName(betaField) {
			return fmt.Errorf("%s oneof membership differs between stable and beta", fieldName)
		}

		switch stableField.Kind() {
		case protoreflect.MessageKind, protoreflect.GroupKind:
			if betaField.Message().FullName() != betaContractName(stableField.Message().FullName()) {
				return fmt.Errorf("%s message type differs between stable and beta", fieldName)
			}
		case protoreflect.EnumKind:
			if betaField.Enum().FullName() != betaContractName(stableField.Enum().FullName()) {
				return fmt.Errorf("%s enum type differs between stable and beta", fieldName)
			}
		}
	}

	return nil
}

func validateEnumSubset(stable, beta protoreflect.EnumDescriptor) error {
	for index := range stable.Values().Len() {
		stableValue := stable.Values().Get(index)
		betaValue := beta.Values().ByName(stableValue.Name())
		if betaValue == nil {
			return fmt.Errorf("%s value %s is missing from beta", stable.FullName(), stableValue.Name())
		}
		if stableValue.Number() != betaValue.Number() {
			return fmt.Errorf("%s value %s number differs", stable.FullName(), stableValue.Name())
		}
	}
	return nil
}

func validateServicesSubset(
	stableServices protoreflect.ServiceDescriptors,
	betaServices protoreflect.ServiceDescriptors,
) error {
	for serviceIndex := range stableServices.Len() {
		stableService := stableServices.Get(serviceIndex)
		betaService := betaServices.ByName(stableService.Name())
		if betaService == nil {
			return fmt.Errorf("stable service %s is missing from beta", stableService.FullName())
		}

		for methodIndex := range stableService.Methods().Len() {
			stableMethod := stableService.Methods().Get(methodIndex)
			betaMethod := betaService.Methods().ByName(stableMethod.Name())
			methodName := stableMethod.FullName()
			if betaMethod == nil {
				return fmt.Errorf("stable method %s is missing from beta", methodName)
			}
			if stableMethod.IsStreamingClient() != betaMethod.IsStreamingClient() ||
				stableMethod.IsStreamingServer() != betaMethod.IsStreamingServer() {
				return fmt.Errorf("%s stream shape differs between stable and beta", methodName)
			}
			if betaMethod.Input().FullName() != betaContractName(stableMethod.Input().FullName()) {
				return fmt.Errorf("%s request type differs between stable and beta", methodName)
			}
			if betaMethod.Output().FullName() != betaContractName(stableMethod.Output().FullName()) {
				return fmt.Errorf("%s response type differs between stable and beta", methodName)
			}
		}
	}
	return nil
}

func collectTypes(
	files map[string]protoreflect.FileDescriptor,
) (map[protoreflect.FullName]protoreflect.MessageDescriptor, map[protoreflect.FullName]protoreflect.EnumDescriptor) {
	messages := map[protoreflect.FullName]protoreflect.MessageDescriptor{}
	enums := map[protoreflect.FullName]protoreflect.EnumDescriptor{}
	for _, file := range files {
		collectMessages(file.Messages(), messages, enums)
		for index := range file.Enums().Len() {
			enum := file.Enums().Get(index)
			enums[enum.FullName()] = enum
		}
	}
	return messages, enums
}

func collectMessages(
	descriptors protoreflect.MessageDescriptors,
	messages map[protoreflect.FullName]protoreflect.MessageDescriptor,
	enums map[protoreflect.FullName]protoreflect.EnumDescriptor,
) {
	for index := range descriptors.Len() {
		message := descriptors.Get(index)
		messages[message.FullName()] = message
		collectMessages(message.Messages(), messages, enums)
		for enumIndex := range message.Enums().Len() {
			enum := message.Enums().Get(enumIndex)
			enums[enum.FullName()] = enum
		}
	}
}

func sortedDescriptorNames[T any](descriptors map[protoreflect.FullName]T) []protoreflect.FullName {
	names := make([]protoreflect.FullName, 0, len(descriptors))
	for name := range descriptors {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func betaContractName(stableName protoreflect.FullName) protoreflect.FullName {
	return protoreflect.FullName(strings.Replace(string(stableName), "azd.extensions.v1.", "azd.extensions.v1beta.", 1))
}

func oneofName(field protoreflect.FieldDescriptor) protoreflect.Name {
	if field.ContainingOneof() == nil {
		return ""
	}
	return field.ContainingOneof().Name()
}

func cloneContractFiles(
	files map[string]protoreflect.FileDescriptor,
) map[string]protoreflect.FileDescriptor {
	cloned := make(map[string]protoreflect.FileDescriptor, len(files))
	maps.Copy(cloned, files)
	return cloned
}

func findMessageProto(
	t *testing.T,
	file *descriptorpb.FileDescriptorProto,
	name string,
) *descriptorpb.DescriptorProto {
	t.Helper()
	for _, message := range file.GetMessageType() {
		if message.GetName() == name {
			return message
		}
	}
	require.FailNow(t, "message not found", name)
	return nil
}

func contractFiles(t *testing.T, packageName protoreflect.FullName) map[string]protoreflect.FileDescriptor {
	t.Helper()

	files := map[string]protoreflect.FileDescriptor{}
	protoregistry.GlobalFiles.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		if file.Package() != packageName {
			return true
		}
		name := file.Path()[strings.LastIndex(file.Path(), "/")+1:]
		require.NotContains(t, files, name)
		files[name] = file
		return true
	})
	return files
}
