// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var project_pb = require('./project_pb.js');
var models_pb = require('./models_pb.js');

function serialize_azd_extensions_v1beta_AddServiceRequest(arg) {
  if (!(arg instanceof project_pb.AddServiceRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.AddServiceRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_AddServiceRequest(buffer_arg) {
  return project_pb.AddServiceRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_EmptyRequest(arg) {
  if (!(arg instanceof models_pb.EmptyRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.EmptyRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_EmptyRequest(buffer_arg) {
  return models_pb.EmptyRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_EmptyResponse(arg) {
  if (!(arg instanceof models_pb.EmptyResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.EmptyResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_EmptyResponse(buffer_arg) {
  return models_pb.EmptyResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_GetProjectResponse(arg) {
  if (!(arg instanceof project_pb.GetProjectResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.GetProjectResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_GetProjectResponse(buffer_arg) {
  return project_pb.GetProjectResponse.deserializeBinary(new Uint8Array(buffer_arg));
}


// ProjectService defines methods for managing projects and their configurations.
var ProjectServiceService = exports.ProjectServiceService = {
  // Gets the current project.
get: {
    path: '/azd.extensions.v1beta.ProjectService/Get',
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: project_pb.GetProjectResponse,
    requestSerialize: serialize_azd_extensions_v1beta_EmptyRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_EmptyRequest,
    responseSerialize: serialize_azd_extensions_v1beta_GetProjectResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_GetProjectResponse,
  },
  // AddService adds a new service to the project.
addService: {
    path: '/azd.extensions.v1beta.ProjectService/AddService',
    requestStream: false,
    responseStream: false,
    requestType: project_pb.AddServiceRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azd_extensions_v1beta_AddServiceRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_AddServiceRequest,
    responseSerialize: serialize_azd_extensions_v1beta_EmptyResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_EmptyResponse,
  },
};

exports.ProjectServiceClient = grpc.makeGenericClientConstructor(ProjectServiceService, 'ProjectService');
