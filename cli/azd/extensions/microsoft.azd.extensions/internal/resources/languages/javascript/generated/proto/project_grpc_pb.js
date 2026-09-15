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

function serialize_azd_extensions_v1beta_GetLayerRequest(arg) {
  if (!(arg instanceof project_pb.GetLayerRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.GetLayerRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_GetLayerRequest(buffer_arg) {
  return project_pb.GetLayerRequest.deserializeBinary(new Uint8Array(buffer_arg));
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

function serialize_azd_extensions_v1beta_LayerResponse(arg) {
  if (!(arg instanceof project_pb.LayerResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.LayerResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_LayerResponse(buffer_arg) {
  return project_pb.LayerResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_ListLayersResponse(arg) {
  if (!(arg instanceof project_pb.ListLayersResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.ListLayersResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_ListLayersResponse(buffer_arg) {
  return project_pb.ListLayersResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_RemoveLayerRequest(arg) {
  if (!(arg instanceof project_pb.RemoveLayerRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.RemoveLayerRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_RemoveLayerRequest(buffer_arg) {
  return project_pb.RemoveLayerRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_RemoveLayerResponse(arg) {
  if (!(arg instanceof project_pb.RemoveLayerResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.RemoveLayerResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_RemoveLayerResponse(buffer_arg) {
  return project_pb.RemoveLayerResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_SetLayerRequest(arg) {
  if (!(arg instanceof project_pb.SetLayerRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.SetLayerRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_SetLayerRequest(buffer_arg) {
  return project_pb.SetLayerRequest.deserializeBinary(new Uint8Array(buffer_arg));
}


// ProjectService defines methods for managing projects and their configurations.
var ProjectServiceService = exports.ProjectServiceService = {
  // Gets the current flat or infra.layers project.
// Top-level layers projects must use ListLayers or GetLayer.
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
  // SetLayer creates or fully replaces a persisted top-level project layer.
setLayer: {
    path: '/azd.extensions.v1beta.ProjectService/SetLayer',
    requestStream: false,
    responseStream: false,
    requestType: project_pb.SetLayerRequest,
    responseType: project_pb.LayerResponse,
    requestSerialize: serialize_azd_extensions_v1beta_SetLayerRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_SetLayerRequest,
    responseSerialize: serialize_azd_extensions_v1beta_LayerResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_LayerResponse,
  },
  // GetLayer gets a persisted top-level project layer by name.
getLayer: {
    path: '/azd.extensions.v1beta.ProjectService/GetLayer',
    requestStream: false,
    responseStream: false,
    requestType: project_pb.GetLayerRequest,
    responseType: project_pb.LayerResponse,
    requestSerialize: serialize_azd_extensions_v1beta_GetLayerRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_GetLayerRequest,
    responseSerialize: serialize_azd_extensions_v1beta_LayerResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_LayerResponse,
  },
  // ListLayers lists all persisted top-level project layers.
listLayers: {
    path: '/azd.extensions.v1beta.ProjectService/ListLayers',
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: project_pb.ListLayersResponse,
    requestSerialize: serialize_azd_extensions_v1beta_EmptyRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_EmptyRequest,
    responseSerialize: serialize_azd_extensions_v1beta_ListLayersResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_ListLayersResponse,
  },
  // RemoveLayer removes a project layer and its contents.
removeLayer: {
    path: '/azd.extensions.v1beta.ProjectService/RemoveLayer',
    requestStream: false,
    responseStream: false,
    requestType: project_pb.RemoveLayerRequest,
    responseType: project_pb.RemoveLayerResponse,
    requestSerialize: serialize_azd_extensions_v1beta_RemoveLayerRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_RemoveLayerRequest,
    responseSerialize: serialize_azd_extensions_v1beta_RemoveLayerResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_RemoveLayerResponse,
  },
};

exports.ProjectServiceClient = grpc.makeGenericClientConstructor(ProjectServiceService, 'ProjectService');
