// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var project_pb = require('./project_pb.js');
var models_pb = require('./models_pb.js');

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

function serialize_azdext_GetLayerRequest(arg) {
  if (!(arg instanceof project_pb.GetLayerRequest)) {
    throw new Error('Expected argument of type azdext.GetLayerRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetLayerRequest(buffer_arg) {
  return project_pb.GetLayerRequest.deserializeBinary(new Uint8Array(buffer_arg));
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

function serialize_azdext_LayerResponse(arg) {
  if (!(arg instanceof project_pb.LayerResponse)) {
    throw new Error('Expected argument of type azdext.LayerResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_LayerResponse(buffer_arg) {
  return project_pb.LayerResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_ListLayersResponse(arg) {
  if (!(arg instanceof project_pb.ListLayersResponse)) {
    throw new Error('Expected argument of type azdext.ListLayersResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_ListLayersResponse(buffer_arg) {
  return project_pb.ListLayersResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_RemoveLayerRequest(arg) {
  if (!(arg instanceof project_pb.RemoveLayerRequest)) {
    throw new Error('Expected argument of type azdext.RemoveLayerRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_RemoveLayerRequest(buffer_arg) {
  return project_pb.RemoveLayerRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_RemoveLayerResponse(arg) {
  if (!(arg instanceof project_pb.RemoveLayerResponse)) {
    throw new Error('Expected argument of type azdext.RemoveLayerResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_RemoveLayerResponse(buffer_arg) {
  return project_pb.RemoveLayerResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_SetLayerRequest(arg) {
  if (!(arg instanceof project_pb.SetLayerRequest)) {
    throw new Error('Expected argument of type azdext.SetLayerRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_SetLayerRequest(buffer_arg) {
  return project_pb.SetLayerRequest.deserializeBinary(new Uint8Array(buffer_arg));
}


// ProjectService defines methods for managing projects and their configurations.
var ProjectServiceService = exports.ProjectServiceService = {
  // Gets the current flat or infra.layers project.
// Top-level layers projects must use ListLayers or GetLayer.
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
  // SetLayer creates or fully replaces a persisted top-level project layer.
setLayer: {
    path: '/azdext.ProjectService/SetLayer',
    requestStream: false,
    responseStream: false,
    requestType: project_pb.SetLayerRequest,
    responseType: project_pb.LayerResponse,
    requestSerialize: serialize_azdext_SetLayerRequest,
    requestDeserialize: deserialize_azdext_SetLayerRequest,
    responseSerialize: serialize_azdext_LayerResponse,
    responseDeserialize: deserialize_azdext_LayerResponse,
  },
  // GetLayer gets a persisted top-level project layer by name.
getLayer: {
    path: '/azdext.ProjectService/GetLayer',
    requestStream: false,
    responseStream: false,
    requestType: project_pb.GetLayerRequest,
    responseType: project_pb.LayerResponse,
    requestSerialize: serialize_azdext_GetLayerRequest,
    requestDeserialize: deserialize_azdext_GetLayerRequest,
    responseSerialize: serialize_azdext_LayerResponse,
    responseDeserialize: deserialize_azdext_LayerResponse,
  },
  // ListLayers lists all persisted top-level project layers.
listLayers: {
    path: '/azdext.ProjectService/ListLayers',
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: project_pb.ListLayersResponse,
    requestSerialize: serialize_azdext_EmptyRequest,
    requestDeserialize: deserialize_azdext_EmptyRequest,
    responseSerialize: serialize_azdext_ListLayersResponse,
    responseDeserialize: deserialize_azdext_ListLayersResponse,
  },
  // RemoveLayer removes a project layer and its contents.
removeLayer: {
    path: '/azdext.ProjectService/RemoveLayer',
    requestStream: false,
    responseStream: false,
    requestType: project_pb.RemoveLayerRequest,
    responseType: project_pb.RemoveLayerResponse,
    requestSerialize: serialize_azdext_RemoveLayerRequest,
    requestDeserialize: deserialize_azdext_RemoveLayerRequest,
    responseSerialize: serialize_azdext_RemoveLayerResponse,
    responseDeserialize: deserialize_azdext_RemoveLayerResponse,
  },
};

exports.ProjectServiceClient = grpc.makeGenericClientConstructor(ProjectServiceService, 'ProjectService');
