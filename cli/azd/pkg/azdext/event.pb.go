// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.35.2
// 	protoc        v5.29.1
// source: event.proto

package azdext

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// Represents different types of messages sent over the stream
type EventMessage struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Types that are assignable to MessageType:
	//
	//	*EventMessage_SubscribeProjectEvent
	//	*EventMessage_InvokeProjectHandler
	//	*EventMessage_ProjectHandlerStatus
	//	*EventMessage_SubscribeServiceEvent
	//	*EventMessage_InvokeServiceHandler
	//	*EventMessage_ServiceHandlerStatus
	//	*EventMessage_ExtensionReadyEvent
	MessageType isEventMessage_MessageType `protobuf_oneof:"message_type"`
}

func (x *EventMessage) Reset() {
	*x = EventMessage{}
	mi := &file_event_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *EventMessage) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*EventMessage) ProtoMessage() {}

func (x *EventMessage) ProtoReflect() protoreflect.Message {
	mi := &file_event_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use EventMessage.ProtoReflect.Descriptor instead.
func (*EventMessage) Descriptor() ([]byte, []int) {
	return file_event_proto_rawDescGZIP(), []int{0}
}

func (m *EventMessage) GetMessageType() isEventMessage_MessageType {
	if m != nil {
		return m.MessageType
	}
	return nil
}

func (x *EventMessage) GetSubscribeProjectEvent() *SubscribeProjectEvent {
	if x, ok := x.GetMessageType().(*EventMessage_SubscribeProjectEvent); ok {
		return x.SubscribeProjectEvent
	}
	return nil
}

func (x *EventMessage) GetInvokeProjectHandler() *InvokeProjectHandler {
	if x, ok := x.GetMessageType().(*EventMessage_InvokeProjectHandler); ok {
		return x.InvokeProjectHandler
	}
	return nil
}

func (x *EventMessage) GetProjectHandlerStatus() *ProjectHandlerStatus {
	if x, ok := x.GetMessageType().(*EventMessage_ProjectHandlerStatus); ok {
		return x.ProjectHandlerStatus
	}
	return nil
}

func (x *EventMessage) GetSubscribeServiceEvent() *SubscribeServiceEvent {
	if x, ok := x.GetMessageType().(*EventMessage_SubscribeServiceEvent); ok {
		return x.SubscribeServiceEvent
	}
	return nil
}

func (x *EventMessage) GetInvokeServiceHandler() *InvokeServiceHandler {
	if x, ok := x.GetMessageType().(*EventMessage_InvokeServiceHandler); ok {
		return x.InvokeServiceHandler
	}
	return nil
}

func (x *EventMessage) GetServiceHandlerStatus() *ServiceHandlerStatus {
	if x, ok := x.GetMessageType().(*EventMessage_ServiceHandlerStatus); ok {
		return x.ServiceHandlerStatus
	}
	return nil
}

func (x *EventMessage) GetExtensionReadyEvent() *ExtensionReadyEvent {
	if x, ok := x.GetMessageType().(*EventMessage_ExtensionReadyEvent); ok {
		return x.ExtensionReadyEvent
	}
	return nil
}

type isEventMessage_MessageType interface {
	isEventMessage_MessageType()
}

type EventMessage_SubscribeProjectEvent struct {
	SubscribeProjectEvent *SubscribeProjectEvent `protobuf:"bytes,1,opt,name=subscribe_project_event,json=subscribeProjectEvent,proto3,oneof"`
}

type EventMessage_InvokeProjectHandler struct {
	InvokeProjectHandler *InvokeProjectHandler `protobuf:"bytes,2,opt,name=invoke_project_handler,json=invokeProjectHandler,proto3,oneof"`
}

type EventMessage_ProjectHandlerStatus struct {
	ProjectHandlerStatus *ProjectHandlerStatus `protobuf:"bytes,3,opt,name=project_handler_status,json=projectHandlerStatus,proto3,oneof"`
}

type EventMessage_SubscribeServiceEvent struct {
	SubscribeServiceEvent *SubscribeServiceEvent `protobuf:"bytes,4,opt,name=subscribe_service_event,json=subscribeServiceEvent,proto3,oneof"`
}

type EventMessage_InvokeServiceHandler struct {
	InvokeServiceHandler *InvokeServiceHandler `protobuf:"bytes,5,opt,name=invoke_service_handler,json=invokeServiceHandler,proto3,oneof"`
}

type EventMessage_ServiceHandlerStatus struct {
	ServiceHandlerStatus *ServiceHandlerStatus `protobuf:"bytes,6,opt,name=service_handler_status,json=serviceHandlerStatus,proto3,oneof"`
}

type EventMessage_ExtensionReadyEvent struct {
	ExtensionReadyEvent *ExtensionReadyEvent `protobuf:"bytes,7,opt,name=extension_ready_event,json=extensionReadyEvent,proto3,oneof"`
}

func (*EventMessage_SubscribeProjectEvent) isEventMessage_MessageType() {}

func (*EventMessage_InvokeProjectHandler) isEventMessage_MessageType() {}

func (*EventMessage_ProjectHandlerStatus) isEventMessage_MessageType() {}

func (*EventMessage_SubscribeServiceEvent) isEventMessage_MessageType() {}

func (*EventMessage_InvokeServiceHandler) isEventMessage_MessageType() {}

func (*EventMessage_ServiceHandlerStatus) isEventMessage_MessageType() {}

func (*EventMessage_ExtensionReadyEvent) isEventMessage_MessageType() {}

type ExtensionReadyEvent struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Status indicates the readiness state of the extension.
	Status string `protobuf:"bytes,1,opt,name=status,proto3" json:"status,omitempty"`
	// Message provides additional details.
	Message string `protobuf:"bytes,2,opt,name=message,proto3" json:"message,omitempty"`
}

func (x *ExtensionReadyEvent) Reset() {
	*x = ExtensionReadyEvent{}
	mi := &file_event_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ExtensionReadyEvent) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ExtensionReadyEvent) ProtoMessage() {}

func (x *ExtensionReadyEvent) ProtoReflect() protoreflect.Message {
	mi := &file_event_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ExtensionReadyEvent.ProtoReflect.Descriptor instead.
func (*ExtensionReadyEvent) Descriptor() ([]byte, []int) {
	return file_event_proto_rawDescGZIP(), []int{1}
}

func (x *ExtensionReadyEvent) GetStatus() string {
	if x != nil {
		return x.Status
	}
	return ""
}

func (x *ExtensionReadyEvent) GetMessage() string {
	if x != nil {
		return x.Message
	}
	return ""
}

// Client subscribes to project-related events
type SubscribeProjectEvent struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// List of event names to subscribe to.
	EventNames []string `protobuf:"bytes,1,rep,name=event_names,json=eventNames,proto3" json:"event_names,omitempty"`
}

func (x *SubscribeProjectEvent) Reset() {
	*x = SubscribeProjectEvent{}
	mi := &file_event_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SubscribeProjectEvent) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SubscribeProjectEvent) ProtoMessage() {}

func (x *SubscribeProjectEvent) ProtoReflect() protoreflect.Message {
	mi := &file_event_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SubscribeProjectEvent.ProtoReflect.Descriptor instead.
func (*SubscribeProjectEvent) Descriptor() ([]byte, []int) {
	return file_event_proto_rawDescGZIP(), []int{2}
}

func (x *SubscribeProjectEvent) GetEventNames() []string {
	if x != nil {
		return x.EventNames
	}
	return nil
}

// Client subscribes to service-related events
type SubscribeServiceEvent struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// List of event names to subscribe to.
	EventNames []string `protobuf:"bytes,1,rep,name=event_names,json=eventNames,proto3" json:"event_names,omitempty"`
	Language   string   `protobuf:"bytes,2,opt,name=language,proto3" json:"language,omitempty"`
	Host       string   `protobuf:"bytes,3,opt,name=host,proto3" json:"host,omitempty"`
}

func (x *SubscribeServiceEvent) Reset() {
	*x = SubscribeServiceEvent{}
	mi := &file_event_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SubscribeServiceEvent) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SubscribeServiceEvent) ProtoMessage() {}

func (x *SubscribeServiceEvent) ProtoReflect() protoreflect.Message {
	mi := &file_event_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SubscribeServiceEvent.ProtoReflect.Descriptor instead.
func (*SubscribeServiceEvent) Descriptor() ([]byte, []int) {
	return file_event_proto_rawDescGZIP(), []int{3}
}

func (x *SubscribeServiceEvent) GetEventNames() []string {
	if x != nil {
		return x.EventNames
	}
	return nil
}

func (x *SubscribeServiceEvent) GetLanguage() string {
	if x != nil {
		return x.Language
	}
	return ""
}

func (x *SubscribeServiceEvent) GetHost() string {
	if x != nil {
		return x.Host
	}
	return ""
}

// Server invokes the project event handler
type InvokeProjectHandler struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Name of the event being invoked.
	EventName string `protobuf:"bytes,1,opt,name=event_name,json=eventName,proto3" json:"event_name,omitempty"`
	// Current project configuration.
	Project *ProjectConfig `protobuf:"bytes,2,opt,name=project,proto3" json:"project,omitempty"`
}

func (x *InvokeProjectHandler) Reset() {
	*x = InvokeProjectHandler{}
	mi := &file_event_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *InvokeProjectHandler) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*InvokeProjectHandler) ProtoMessage() {}

func (x *InvokeProjectHandler) ProtoReflect() protoreflect.Message {
	mi := &file_event_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use InvokeProjectHandler.ProtoReflect.Descriptor instead.
func (*InvokeProjectHandler) Descriptor() ([]byte, []int) {
	return file_event_proto_rawDescGZIP(), []int{4}
}

func (x *InvokeProjectHandler) GetEventName() string {
	if x != nil {
		return x.EventName
	}
	return ""
}

func (x *InvokeProjectHandler) GetProject() *ProjectConfig {
	if x != nil {
		return x.Project
	}
	return nil
}

// Server invokes the service event handler
type InvokeServiceHandler struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Name of the event being invoked.
	EventName string `protobuf:"bytes,1,opt,name=event_name,json=eventName,proto3" json:"event_name,omitempty"`
	// Current project configuration.
	Project *ProjectConfig `protobuf:"bytes,2,opt,name=project,proto3" json:"project,omitempty"`
	// Specific service configuration.
	Service *ServiceConfig `protobuf:"bytes,3,opt,name=service,proto3" json:"service,omitempty"`
}

func (x *InvokeServiceHandler) Reset() {
	*x = InvokeServiceHandler{}
	mi := &file_event_proto_msgTypes[5]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *InvokeServiceHandler) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*InvokeServiceHandler) ProtoMessage() {}

func (x *InvokeServiceHandler) ProtoReflect() protoreflect.Message {
	mi := &file_event_proto_msgTypes[5]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use InvokeServiceHandler.ProtoReflect.Descriptor instead.
func (*InvokeServiceHandler) Descriptor() ([]byte, []int) {
	return file_event_proto_rawDescGZIP(), []int{5}
}

func (x *InvokeServiceHandler) GetEventName() string {
	if x != nil {
		return x.EventName
	}
	return ""
}

func (x *InvokeServiceHandler) GetProject() *ProjectConfig {
	if x != nil {
		return x.Project
	}
	return nil
}

func (x *InvokeServiceHandler) GetService() *ServiceConfig {
	if x != nil {
		return x.Service
	}
	return nil
}

// Client sends status updates for project events
type ProjectHandlerStatus struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Name of the event this status update is for.
	EventName string `protobuf:"bytes,1,opt,name=event_name,json=eventName,proto3" json:"event_name,omitempty"`
	// Status such as "running", "completed", "failed", etc.
	Status string `protobuf:"bytes,2,opt,name=status,proto3" json:"status,omitempty"`
	// Optional message providing further details.
	Message string `protobuf:"bytes,3,opt,name=message,proto3" json:"message,omitempty"`
}

func (x *ProjectHandlerStatus) Reset() {
	*x = ProjectHandlerStatus{}
	mi := &file_event_proto_msgTypes[6]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ProjectHandlerStatus) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ProjectHandlerStatus) ProtoMessage() {}

func (x *ProjectHandlerStatus) ProtoReflect() protoreflect.Message {
	mi := &file_event_proto_msgTypes[6]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ProjectHandlerStatus.ProtoReflect.Descriptor instead.
func (*ProjectHandlerStatus) Descriptor() ([]byte, []int) {
	return file_event_proto_rawDescGZIP(), []int{6}
}

func (x *ProjectHandlerStatus) GetEventName() string {
	if x != nil {
		return x.EventName
	}
	return ""
}

func (x *ProjectHandlerStatus) GetStatus() string {
	if x != nil {
		return x.Status
	}
	return ""
}

func (x *ProjectHandlerStatus) GetMessage() string {
	if x != nil {
		return x.Message
	}
	return ""
}

// Client sends status updates for service events
type ServiceHandlerStatus struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Name of the event this status update is for.
	EventName string `protobuf:"bytes,1,opt,name=event_name,json=eventName,proto3" json:"event_name,omitempty"`
	// Name of the service related to the update.
	ServiceName string `protobuf:"bytes,2,opt,name=service_name,json=serviceName,proto3" json:"service_name,omitempty"`
	// Status such as "running", "completed", "failed", etc.
	Status string `protobuf:"bytes,3,opt,name=status,proto3" json:"status,omitempty"`
	// Optional message providing further details.
	Message string `protobuf:"bytes,4,opt,name=message,proto3" json:"message,omitempty"`
}

func (x *ServiceHandlerStatus) Reset() {
	*x = ServiceHandlerStatus{}
	mi := &file_event_proto_msgTypes[7]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ServiceHandlerStatus) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ServiceHandlerStatus) ProtoMessage() {}

func (x *ServiceHandlerStatus) ProtoReflect() protoreflect.Message {
	mi := &file_event_proto_msgTypes[7]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ServiceHandlerStatus.ProtoReflect.Descriptor instead.
func (*ServiceHandlerStatus) Descriptor() ([]byte, []int) {
	return file_event_proto_rawDescGZIP(), []int{7}
}

func (x *ServiceHandlerStatus) GetEventName() string {
	if x != nil {
		return x.EventName
	}
	return ""
}

func (x *ServiceHandlerStatus) GetServiceName() string {
	if x != nil {
		return x.ServiceName
	}
	return ""
}

func (x *ServiceHandlerStatus) GetStatus() string {
	if x != nil {
		return x.Status
	}
	return ""
}

func (x *ServiceHandlerStatus) GetMessage() string {
	if x != nil {
		return x.Message
	}
	return ""
}

var File_event_proto protoreflect.FileDescriptor

var file_event_proto_rawDesc = []byte{
	0x0a, 0x0b, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x06, 0x61,
	0x7a, 0x64, 0x65, 0x78, 0x74, 0x1a, 0x0c, 0x6d, 0x6f, 0x64, 0x65, 0x6c, 0x73, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x22, 0xfb, 0x04, 0x0a, 0x0c, 0x45, 0x76, 0x65, 0x6e, 0x74, 0x4d, 0x65, 0x73,
	0x73, 0x61, 0x67, 0x65, 0x12, 0x57, 0x0a, 0x17, 0x73, 0x75, 0x62, 0x73, 0x63, 0x72, 0x69, 0x62,
	0x65, 0x5f, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x5f, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1d, 0x2e, 0x61, 0x7a, 0x64, 0x65, 0x78, 0x74, 0x2e, 0x53,
	0x75, 0x62, 0x73, 0x63, 0x72, 0x69, 0x62, 0x65, 0x50, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x45,
	0x76, 0x65, 0x6e, 0x74, 0x48, 0x00, 0x52, 0x15, 0x73, 0x75, 0x62, 0x73, 0x63, 0x72, 0x69, 0x62,
	0x65, 0x50, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x45, 0x76, 0x65, 0x6e, 0x74, 0x12, 0x54, 0x0a,
	0x16, 0x69, 0x6e, 0x76, 0x6f, 0x6b, 0x65, 0x5f, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x5f,
	0x68, 0x61, 0x6e, 0x64, 0x6c, 0x65, 0x72, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1c, 0x2e,
	0x61, 0x7a, 0x64, 0x65, 0x78, 0x74, 0x2e, 0x49, 0x6e, 0x76, 0x6f, 0x6b, 0x65, 0x50, 0x72, 0x6f,
	0x6a, 0x65, 0x63, 0x74, 0x48, 0x61, 0x6e, 0x64, 0x6c, 0x65, 0x72, 0x48, 0x00, 0x52, 0x14, 0x69,
	0x6e, 0x76, 0x6f, 0x6b, 0x65, 0x50, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x48, 0x61, 0x6e, 0x64,
	0x6c, 0x65, 0x72, 0x12, 0x54, 0x0a, 0x16, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x5f, 0x68,
	0x61, 0x6e, 0x64, 0x6c, 0x65, 0x72, 0x5f, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x18, 0x03, 0x20,
	0x01, 0x28, 0x0b, 0x32, 0x1c, 0x2e, 0x61, 0x7a, 0x64, 0x65, 0x78, 0x74, 0x2e, 0x50, 0x72, 0x6f,
	0x6a, 0x65, 0x63, 0x74, 0x48, 0x61, 0x6e, 0x64, 0x6c, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x75,
	0x73, 0x48, 0x00, 0x52, 0x14, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x48, 0x61, 0x6e, 0x64,
	0x6c, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x12, 0x57, 0x0a, 0x17, 0x73, 0x75, 0x62,
	0x73, 0x63, 0x72, 0x69, 0x62, 0x65, 0x5f, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x5f, 0x65,
	0x76, 0x65, 0x6e, 0x74, 0x18, 0x04, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1d, 0x2e, 0x61, 0x7a, 0x64,
	0x65, 0x78, 0x74, 0x2e, 0x53, 0x75, 0x62, 0x73, 0x63, 0x72, 0x69, 0x62, 0x65, 0x53, 0x65, 0x72,
	0x76, 0x69, 0x63, 0x65, 0x45, 0x76, 0x65, 0x6e, 0x74, 0x48, 0x00, 0x52, 0x15, 0x73, 0x75, 0x62,
	0x73, 0x63, 0x72, 0x69, 0x62, 0x65, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x45, 0x76, 0x65,
	0x6e, 0x74, 0x12, 0x54, 0x0a, 0x16, 0x69, 0x6e, 0x76, 0x6f, 0x6b, 0x65, 0x5f, 0x73, 0x65, 0x72,
	0x76, 0x69, 0x63, 0x65, 0x5f, 0x68, 0x61, 0x6e, 0x64, 0x6c, 0x65, 0x72, 0x18, 0x05, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x1c, 0x2e, 0x61, 0x7a, 0x64, 0x65, 0x78, 0x74, 0x2e, 0x49, 0x6e, 0x76, 0x6f,
	0x6b, 0x65, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x48, 0x61, 0x6e, 0x64, 0x6c, 0x65, 0x72,
	0x48, 0x00, 0x52, 0x14, 0x69, 0x6e, 0x76, 0x6f, 0x6b, 0x65, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63,
	0x65, 0x48, 0x61, 0x6e, 0x64, 0x6c, 0x65, 0x72, 0x12, 0x54, 0x0a, 0x16, 0x73, 0x65, 0x72, 0x76,
	0x69, 0x63, 0x65, 0x5f, 0x68, 0x61, 0x6e, 0x64, 0x6c, 0x65, 0x72, 0x5f, 0x73, 0x74, 0x61, 0x74,
	0x75, 0x73, 0x18, 0x06, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1c, 0x2e, 0x61, 0x7a, 0x64, 0x65, 0x78,
	0x74, 0x2e, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x48, 0x61, 0x6e, 0x64, 0x6c, 0x65, 0x72,
	0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x48, 0x00, 0x52, 0x14, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63,
	0x65, 0x48, 0x61, 0x6e, 0x64, 0x6c, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x12, 0x51,
	0x0a, 0x15, 0x65, 0x78, 0x74, 0x65, 0x6e, 0x73, 0x69, 0x6f, 0x6e, 0x5f, 0x72, 0x65, 0x61, 0x64,
	0x79, 0x5f, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x18, 0x07, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1b, 0x2e,
	0x61, 0x7a, 0x64, 0x65, 0x78, 0x74, 0x2e, 0x45, 0x78, 0x74, 0x65, 0x6e, 0x73, 0x69, 0x6f, 0x6e,
	0x52, 0x65, 0x61, 0x64, 0x79, 0x45, 0x76, 0x65, 0x6e, 0x74, 0x48, 0x00, 0x52, 0x13, 0x65, 0x78,
	0x74, 0x65, 0x6e, 0x73, 0x69, 0x6f, 0x6e, 0x52, 0x65, 0x61, 0x64, 0x79, 0x45, 0x76, 0x65, 0x6e,
	0x74, 0x42, 0x0e, 0x0a, 0x0c, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x5f, 0x74, 0x79, 0x70,
	0x65, 0x22, 0x47, 0x0a, 0x13, 0x45, 0x78, 0x74, 0x65, 0x6e, 0x73, 0x69, 0x6f, 0x6e, 0x52, 0x65,
	0x61, 0x64, 0x79, 0x45, 0x76, 0x65, 0x6e, 0x74, 0x12, 0x16, 0x0a, 0x06, 0x73, 0x74, 0x61, 0x74,
	0x75, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73,
	0x12, 0x18, 0x0a, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x22, 0x38, 0x0a, 0x15, 0x53, 0x75,
	0x62, 0x73, 0x63, 0x72, 0x69, 0x62, 0x65, 0x50, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x45, 0x76,
	0x65, 0x6e, 0x74, 0x12, 0x1f, 0x0a, 0x0b, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x5f, 0x6e, 0x61, 0x6d,
	0x65, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x09, 0x52, 0x0a, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x4e,
	0x61, 0x6d, 0x65, 0x73, 0x22, 0x68, 0x0a, 0x15, 0x53, 0x75, 0x62, 0x73, 0x63, 0x72, 0x69, 0x62,
	0x65, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x45, 0x76, 0x65, 0x6e, 0x74, 0x12, 0x1f, 0x0a,
	0x0b, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x5f, 0x6e, 0x61, 0x6d, 0x65, 0x73, 0x18, 0x01, 0x20, 0x03,
	0x28, 0x09, 0x52, 0x0a, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x4e, 0x61, 0x6d, 0x65, 0x73, 0x12, 0x1a,
	0x0a, 0x08, 0x6c, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x67, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x08, 0x6c, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x67, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x68, 0x6f,
	0x73, 0x74, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x68, 0x6f, 0x73, 0x74, 0x22, 0x66,
	0x0a, 0x14, 0x49, 0x6e, 0x76, 0x6f, 0x6b, 0x65, 0x50, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x48,
	0x61, 0x6e, 0x64, 0x6c, 0x65, 0x72, 0x12, 0x1d, 0x0a, 0x0a, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x5f,
	0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x09, 0x65, 0x76, 0x65, 0x6e,
	0x74, 0x4e, 0x61, 0x6d, 0x65, 0x12, 0x2f, 0x0a, 0x07, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74,
	0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x15, 0x2e, 0x61, 0x7a, 0x64, 0x65, 0x78, 0x74, 0x2e,
	0x50, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x52, 0x07, 0x70,
	0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x22, 0x97, 0x01, 0x0a, 0x14, 0x49, 0x6e, 0x76, 0x6f, 0x6b,
	0x65, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x48, 0x61, 0x6e, 0x64, 0x6c, 0x65, 0x72, 0x12,
	0x1d, 0x0a, 0x0a, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x5f, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x09, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x4e, 0x61, 0x6d, 0x65, 0x12, 0x2f,
	0x0a, 0x07, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32,
	0x15, 0x2e, 0x61, 0x7a, 0x64, 0x65, 0x78, 0x74, 0x2e, 0x50, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74,
	0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x52, 0x07, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x12,
	0x2f, 0x0a, 0x07, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x15, 0x2e, 0x61, 0x7a, 0x64, 0x65, 0x78, 0x74, 0x2e, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63,
	0x65, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x52, 0x07, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65,
	0x22, 0x67, 0x0a, 0x14, 0x50, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x48, 0x61, 0x6e, 0x64, 0x6c,
	0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x12, 0x1d, 0x0a, 0x0a, 0x65, 0x76, 0x65, 0x6e,
	0x74, 0x5f, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x09, 0x65, 0x76,
	0x65, 0x6e, 0x74, 0x4e, 0x61, 0x6d, 0x65, 0x12, 0x16, 0x0a, 0x06, 0x73, 0x74, 0x61, 0x74, 0x75,
	0x73, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x12,
	0x18, 0x0a, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x22, 0x8a, 0x01, 0x0a, 0x14, 0x53, 0x65,
	0x72, 0x76, 0x69, 0x63, 0x65, 0x48, 0x61, 0x6e, 0x64, 0x6c, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74,
	0x75, 0x73, 0x12, 0x1d, 0x0a, 0x0a, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x5f, 0x6e, 0x61, 0x6d, 0x65,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x09, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x4e, 0x61, 0x6d,
	0x65, 0x12, 0x21, 0x0a, 0x0c, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x5f, 0x6e, 0x61, 0x6d,
	0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65,
	0x4e, 0x61, 0x6d, 0x65, 0x12, 0x16, 0x0a, 0x06, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x18, 0x03,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x12, 0x18, 0x0a, 0x07,
	0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x04, 0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x6d,
	0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x32, 0x4d, 0x0a, 0x0c, 0x45, 0x76, 0x65, 0x6e, 0x74, 0x53,
	0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x3d, 0x0a, 0x0b, 0x45, 0x76, 0x65, 0x6e, 0x74, 0x53,
	0x74, 0x72, 0x65, 0x61, 0x6d, 0x12, 0x14, 0x2e, 0x61, 0x7a, 0x64, 0x65, 0x78, 0x74, 0x2e, 0x45,
	0x76, 0x65, 0x6e, 0x74, 0x4d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x1a, 0x14, 0x2e, 0x61, 0x7a,
	0x64, 0x65, 0x78, 0x74, 0x2e, 0x45, 0x76, 0x65, 0x6e, 0x74, 0x4d, 0x65, 0x73, 0x73, 0x61, 0x67,
	0x65, 0x28, 0x01, 0x30, 0x01, 0x42, 0x2f, 0x5a, 0x2d, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e,
	0x63, 0x6f, 0x6d, 0x2f, 0x61, 0x7a, 0x75, 0x72, 0x65, 0x2f, 0x61, 0x7a, 0x75, 0x72, 0x65, 0x2d,
	0x64, 0x65, 0x76, 0x2f, 0x63, 0x6c, 0x69, 0x2f, 0x61, 0x7a, 0x64, 0x2f, 0x70, 0x6b, 0x67, 0x2f,
	0x61, 0x7a, 0x64, 0x65, 0x78, 0x74, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_event_proto_rawDescOnce sync.Once
	file_event_proto_rawDescData = file_event_proto_rawDesc
)

func file_event_proto_rawDescGZIP() []byte {
	file_event_proto_rawDescOnce.Do(func() {
		file_event_proto_rawDescData = protoimpl.X.CompressGZIP(file_event_proto_rawDescData)
	})
	return file_event_proto_rawDescData
}

var file_event_proto_msgTypes = make([]protoimpl.MessageInfo, 8)
var file_event_proto_goTypes = []any{
	(*EventMessage)(nil),          // 0: azdext.EventMessage
	(*ExtensionReadyEvent)(nil),   // 1: azdext.ExtensionReadyEvent
	(*SubscribeProjectEvent)(nil), // 2: azdext.SubscribeProjectEvent
	(*SubscribeServiceEvent)(nil), // 3: azdext.SubscribeServiceEvent
	(*InvokeProjectHandler)(nil),  // 4: azdext.InvokeProjectHandler
	(*InvokeServiceHandler)(nil),  // 5: azdext.InvokeServiceHandler
	(*ProjectHandlerStatus)(nil),  // 6: azdext.ProjectHandlerStatus
	(*ServiceHandlerStatus)(nil),  // 7: azdext.ServiceHandlerStatus
	(*ProjectConfig)(nil),         // 8: azdext.ProjectConfig
	(*ServiceConfig)(nil),         // 9: azdext.ServiceConfig
}
var file_event_proto_depIdxs = []int32{
	2,  // 0: azdext.EventMessage.subscribe_project_event:type_name -> azdext.SubscribeProjectEvent
	4,  // 1: azdext.EventMessage.invoke_project_handler:type_name -> azdext.InvokeProjectHandler
	6,  // 2: azdext.EventMessage.project_handler_status:type_name -> azdext.ProjectHandlerStatus
	3,  // 3: azdext.EventMessage.subscribe_service_event:type_name -> azdext.SubscribeServiceEvent
	5,  // 4: azdext.EventMessage.invoke_service_handler:type_name -> azdext.InvokeServiceHandler
	7,  // 5: azdext.EventMessage.service_handler_status:type_name -> azdext.ServiceHandlerStatus
	1,  // 6: azdext.EventMessage.extension_ready_event:type_name -> azdext.ExtensionReadyEvent
	8,  // 7: azdext.InvokeProjectHandler.project:type_name -> azdext.ProjectConfig
	8,  // 8: azdext.InvokeServiceHandler.project:type_name -> azdext.ProjectConfig
	9,  // 9: azdext.InvokeServiceHandler.service:type_name -> azdext.ServiceConfig
	0,  // 10: azdext.EventService.EventStream:input_type -> azdext.EventMessage
	0,  // 11: azdext.EventService.EventStream:output_type -> azdext.EventMessage
	11, // [11:12] is the sub-list for method output_type
	10, // [10:11] is the sub-list for method input_type
	10, // [10:10] is the sub-list for extension type_name
	10, // [10:10] is the sub-list for extension extendee
	0,  // [0:10] is the sub-list for field type_name
}

func init() { file_event_proto_init() }
func file_event_proto_init() {
	if File_event_proto != nil {
		return
	}
	file_models_proto_init()
	file_event_proto_msgTypes[0].OneofWrappers = []any{
		(*EventMessage_SubscribeProjectEvent)(nil),
		(*EventMessage_InvokeProjectHandler)(nil),
		(*EventMessage_ProjectHandlerStatus)(nil),
		(*EventMessage_SubscribeServiceEvent)(nil),
		(*EventMessage_InvokeServiceHandler)(nil),
		(*EventMessage_ServiceHandlerStatus)(nil),
		(*EventMessage_ExtensionReadyEvent)(nil),
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_event_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   8,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_event_proto_goTypes,
		DependencyIndexes: file_event_proto_depIdxs,
		MessageInfos:      file_event_proto_msgTypes,
	}.Build()
	File_event_proto = out.File
	file_event_proto_rawDesc = nil
	file_event_proto_goTypes = nil
	file_event_proto_depIdxs = nil
}
