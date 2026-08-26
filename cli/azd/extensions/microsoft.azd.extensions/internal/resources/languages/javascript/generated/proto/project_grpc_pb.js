// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var project_pb = require('./project_pb.js');
var models_pb = require('./models_pb.js');
var google_protobuf_struct_pb = require('google-protobuf/google/protobuf/struct_pb.js');

function serialize_azdext_AddServiceRequest(arg) {
  if (!(arg instanceof project_pb.AddServiceRequest)) {
    throw new Error('Expected argument of type azdext.AddServiceRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_AddServiceRequest(buffer_arg) {
  return project_pb.AddServiceRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_EmptyRequest(arg) {
  if (!(arg instanceof models_pb.EmptyRequest)) {
    throw new Error('Expected argument of type azdext.EmptyRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_EmptyRequest(buffer_arg) {
  return models_pb.EmptyRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_EmptyResponse(arg) {
  if (!(arg instanceof models_pb.EmptyResponse)) {
    throw new Error('Expected argument of type azdext.EmptyResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_EmptyResponse(buffer_arg) {
  return models_pb.EmptyResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_GetProjectResponse(arg) {
  if (!(arg instanceof project_pb.GetProjectResponse)) {
    throw new Error('Expected argument of type azdext.GetProjectResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetProjectResponse(buffer_arg) {
  return project_pb.GetProjectResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_PatchServiceConfigRequest(arg) {
  if (!(arg instanceof project_pb.PatchServiceConfigRequest)) {
    throw new Error('Expected argument of type azdext.PatchServiceConfigRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_PatchServiceConfigRequest(buffer_arg) {
  return project_pb.PatchServiceConfigRequest.deserializeBinary(new Uint8Array(buffer_arg));
}


// ProjectService defines methods for managing projects and their configurations.
var ProjectServiceService = exports.ProjectServiceService = {
  // Gets the current project.
get: {
    path: '/azdext.ProjectService/Get',
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: project_pb.GetProjectResponse,
    requestSerialize: serialize_azdext_EmptyRequest,
    requestDeserialize: deserialize_azdext_EmptyRequest,
    responseSerialize: serialize_azdext_GetProjectResponse,
    responseDeserialize: deserialize_azdext_GetProjectResponse,
  },
  // AddService adds a new service to the project.
addService: {
    path: '/azdext.ProjectService/AddService',
    requestStream: false,
    responseStream: false,
    requestType: project_pb.AddServiceRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azdext_AddServiceRequest,
    requestDeserialize: deserialize_azdext_AddServiceRequest,
    responseSerialize: serialize_azdext_EmptyResponse,
    responseDeserialize: deserialize_azdext_EmptyResponse,
  },
  // Creates or patches a service under one process-local mutation lock.
patchServiceConfig: {
    path: '/azdext.ProjectService/PatchServiceConfig',
    requestStream: false,
    responseStream: false,
    requestType: project_pb.PatchServiceConfigRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azdext_PatchServiceConfigRequest,
    requestDeserialize: deserialize_azdext_PatchServiceConfigRequest,
    responseSerialize: serialize_azdext_EmptyResponse,
    responseDeserialize: deserialize_azdext_EmptyResponse,
  },
};

exports.ProjectServiceClient = grpc.makeGenericClientConstructor(ProjectServiceService, 'ProjectService');
