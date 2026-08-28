// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var compose_pb = require('./compose_pb.js');
var models_pb = require('./models_pb.js');

function serialize_azd_extensions_v1_AddResourceRequest(arg) {
  if (!(arg instanceof compose_pb.AddResourceRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.AddResourceRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_AddResourceRequest(buffer_arg) {
  return compose_pb.AddResourceRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_AddResourceResponse(arg) {
  if (!(arg instanceof compose_pb.AddResourceResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.AddResourceResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_AddResourceResponse(buffer_arg) {
  return compose_pb.AddResourceResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_EmptyRequest(arg) {
  if (!(arg instanceof models_pb.EmptyRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.EmptyRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_EmptyRequest(buffer_arg) {
  return models_pb.EmptyRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_GetResourceRequest(arg) {
  if (!(arg instanceof compose_pb.GetResourceRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.GetResourceRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_GetResourceRequest(buffer_arg) {
  return compose_pb.GetResourceRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_GetResourceResponse(arg) {
  if (!(arg instanceof compose_pb.GetResourceResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.GetResourceResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_GetResourceResponse(buffer_arg) {
  return compose_pb.GetResourceResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_GetResourceTypeRequest(arg) {
  if (!(arg instanceof compose_pb.GetResourceTypeRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.GetResourceTypeRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_GetResourceTypeRequest(buffer_arg) {
  return compose_pb.GetResourceTypeRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_GetResourceTypeResponse(arg) {
  if (!(arg instanceof compose_pb.GetResourceTypeResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.GetResourceTypeResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_GetResourceTypeResponse(buffer_arg) {
  return compose_pb.GetResourceTypeResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_ListResourceTypesResponse(arg) {
  if (!(arg instanceof compose_pb.ListResourceTypesResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.ListResourceTypesResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_ListResourceTypesResponse(buffer_arg) {
  return compose_pb.ListResourceTypesResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_ListResourcesResponse(arg) {
  if (!(arg instanceof compose_pb.ListResourcesResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.ListResourcesResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_ListResourcesResponse(buffer_arg) {
  return compose_pb.ListResourcesResponse.deserializeBinary(new Uint8Array(buffer_arg));
}


var ComposeServiceService = exports.ComposeServiceService = {
  // ListResources retrieves all configured composability resources in the current project.
listResources: {
    path: '/azd.extensions.v1.ComposeService/ListResources',
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: compose_pb.ListResourcesResponse,
    requestSerialize: serialize_azd_extensions_v1_EmptyRequest,
    requestDeserialize: deserialize_azd_extensions_v1_EmptyRequest,
    responseSerialize: serialize_azd_extensions_v1_ListResourcesResponse,
    responseDeserialize: deserialize_azd_extensions_v1_ListResourcesResponse,
  },
  // GetResource retrieves the configuration of a specific named composability resource.
getResource: {
    path: '/azd.extensions.v1.ComposeService/GetResource',
    requestStream: false,
    responseStream: false,
    requestType: compose_pb.GetResourceRequest,
    responseType: compose_pb.GetResourceResponse,
    requestSerialize: serialize_azd_extensions_v1_GetResourceRequest,
    requestDeserialize: deserialize_azd_extensions_v1_GetResourceRequest,
    responseSerialize: serialize_azd_extensions_v1_GetResourceResponse,
    responseDeserialize: deserialize_azd_extensions_v1_GetResourceResponse,
  },
  // ListResourceTypes retrieves all supported composability resource types.
listResourceTypes: {
    path: '/azd.extensions.v1.ComposeService/ListResourceTypes',
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: compose_pb.ListResourceTypesResponse,
    requestSerialize: serialize_azd_extensions_v1_EmptyRequest,
    requestDeserialize: deserialize_azd_extensions_v1_EmptyRequest,
    responseSerialize: serialize_azd_extensions_v1_ListResourceTypesResponse,
    responseDeserialize: deserialize_azd_extensions_v1_ListResourceTypesResponse,
  },
  // GetResourceType retrieves the schema of a specific named composability resource type.
getResourceType: {
    path: '/azd.extensions.v1.ComposeService/GetResourceType',
    requestStream: false,
    responseStream: false,
    requestType: compose_pb.GetResourceTypeRequest,
    responseType: compose_pb.GetResourceTypeResponse,
    requestSerialize: serialize_azd_extensions_v1_GetResourceTypeRequest,
    requestDeserialize: deserialize_azd_extensions_v1_GetResourceTypeRequest,
    responseSerialize: serialize_azd_extensions_v1_GetResourceTypeResponse,
    responseDeserialize: deserialize_azd_extensions_v1_GetResourceTypeResponse,
  },
  // AddResource adds a new composability resource to the current project.
addResource: {
    path: '/azd.extensions.v1.ComposeService/AddResource',
    requestStream: false,
    responseStream: false,
    requestType: compose_pb.AddResourceRequest,
    responseType: compose_pb.AddResourceResponse,
    requestSerialize: serialize_azd_extensions_v1_AddResourceRequest,
    requestDeserialize: deserialize_azd_extensions_v1_AddResourceRequest,
    responseSerialize: serialize_azd_extensions_v1_AddResourceResponse,
    responseDeserialize: deserialize_azd_extensions_v1_AddResourceResponse,
  },
};

exports.ComposeServiceClient = grpc.makeGenericClientConstructor(ComposeServiceService, 'ComposeService');
