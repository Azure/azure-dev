// source: compose.proto
// GENERATED CODE -- DO NOT EDIT!
/* eslint-disable */
// @ts-nocheck
"use strict";

const grpc = require("@grpc/grpc-js");
const compose_pb = require("./compose_pb.js");
const models_pb = require("./models_pb.js");

function serialize_azdext_EmptyRequest(arg) {
  if (!arg instanceof models_pb.EmptyRequest) {
    throw new Error("Expected argument of type azdext.EmptyRequest");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_EmptyRequest(arg) {
  return models_pb.EmptyRequest.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_ListResourcesResponse(arg) {
  if (!arg instanceof compose_pb.ListResourcesResponse) {
    throw new Error("Expected argument of type azdext.ListResourcesResponse");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_ListResourcesResponse(arg) {
  return compose_pb.ListResourcesResponse.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_GetResourceRequest(arg) {
  if (!arg instanceof compose_pb.GetResourceRequest) {
    throw new Error("Expected argument of type azdext.GetResourceRequest");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetResourceRequest(arg) {
  return compose_pb.GetResourceRequest.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_GetResourceResponse(arg) {
  if (!arg instanceof compose_pb.GetResourceResponse) {
    throw new Error("Expected argument of type azdext.GetResourceResponse");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetResourceResponse(arg) {
  return compose_pb.GetResourceResponse.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_ListResourceTypesResponse(arg) {
  if (!arg instanceof compose_pb.ListResourceTypesResponse) {
    throw new Error("Expected argument of type azdext.ListResourceTypesResponse");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_ListResourceTypesResponse(arg) {
  return compose_pb.ListResourceTypesResponse.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_GetResourceTypeRequest(arg) {
  if (!arg instanceof compose_pb.GetResourceTypeRequest) {
    throw new Error("Expected argument of type azdext.GetResourceTypeRequest");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetResourceTypeRequest(arg) {
  return compose_pb.GetResourceTypeRequest.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_GetResourceTypeResponse(arg) {
  if (!arg instanceof compose_pb.GetResourceTypeResponse) {
    throw new Error("Expected argument of type azdext.GetResourceTypeResponse");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetResourceTypeResponse(arg) {
  return compose_pb.GetResourceTypeResponse.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_AddResourceRequest(arg) {
  if (!arg instanceof compose_pb.AddResourceRequest) {
    throw new Error("Expected argument of type azdext.AddResourceRequest");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_AddResourceRequest(arg) {
  return compose_pb.AddResourceRequest.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_AddResourceResponse(arg) {
  if (!arg instanceof compose_pb.AddResourceResponse) {
    throw new Error("Expected argument of type azdext.AddResourceResponse");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_AddResourceResponse(arg) {
  return compose_pb.AddResourceResponse.deserializeBinary(new Uint8Array(arg));
}

const ComposeServiceService = exports.ComposeServiceService = {
  listResources: {
    path: "/azdext.ComposeService/ListResources",
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: compose_pb.ListResourcesResponse,
    requestSerialize: serialize_azdext_EmptyRequest,
    requestDeserialize: deserialize_azdext_EmptyRequest,
    responseSerialize: serialize_azdext_ListResourcesResponse,
    responseDeserialize: deserialize_azdext_ListResourcesResponse
  },
  getResource: {
    path: "/azdext.ComposeService/GetResource",
    requestStream: false,
    responseStream: false,
    requestType: compose_pb.GetResourceRequest,
    responseType: compose_pb.GetResourceResponse,
    requestSerialize: serialize_azdext_GetResourceRequest,
    requestDeserialize: deserialize_azdext_GetResourceRequest,
    responseSerialize: serialize_azdext_GetResourceResponse,
    responseDeserialize: deserialize_azdext_GetResourceResponse
  },
  listResourceTypes: {
    path: "/azdext.ComposeService/ListResourceTypes",
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: compose_pb.ListResourceTypesResponse,
    requestSerialize: serialize_azdext_EmptyRequest,
    requestDeserialize: deserialize_azdext_EmptyRequest,
    responseSerialize: serialize_azdext_ListResourceTypesResponse,
    responseDeserialize: deserialize_azdext_ListResourceTypesResponse
  },
  getResourceType: {
    path: "/azdext.ComposeService/GetResourceType",
    requestStream: false,
    responseStream: false,
    requestType: compose_pb.GetResourceTypeRequest,
    responseType: compose_pb.GetResourceTypeResponse,
    requestSerialize: serialize_azdext_GetResourceTypeRequest,
    requestDeserialize: deserialize_azdext_GetResourceTypeRequest,
    responseSerialize: serialize_azdext_GetResourceTypeResponse,
    responseDeserialize: deserialize_azdext_GetResourceTypeResponse
  },
  addResource: {
    path: "/azdext.ComposeService/AddResource",
    requestStream: false,
    responseStream: false,
    requestType: compose_pb.AddResourceRequest,
    responseType: compose_pb.AddResourceResponse,
    requestSerialize: serialize_azdext_AddResourceRequest,
    requestDeserialize: deserialize_azdext_AddResourceRequest,
    responseSerialize: serialize_azdext_AddResourceResponse,
    responseDeserialize: deserialize_azdext_AddResourceResponse
  },
};

exports.ComposeServiceClient = grpc.makeGenericClientConstructor(ComposeServiceService);