// source: project.proto
// GENERATED CODE -- DO NOT EDIT!
/* eslint-disable */
// @ts-nocheck
"use strict";

const grpc = require("@grpc/grpc-js");
const project_pb = require("./project_pb.js");
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

function serialize_azdext_GetProjectResponse(arg) {
  if (!arg instanceof project_pb.GetProjectResponse) {
    throw new Error("Expected argument of type azdext.GetProjectResponse");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetProjectResponse(arg) {
  return project_pb.GetProjectResponse.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_AddServiceRequest(arg) {
  if (!arg instanceof project_pb.AddServiceRequest) {
    throw new Error("Expected argument of type azdext.AddServiceRequest");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_AddServiceRequest(arg) {
  return project_pb.AddServiceRequest.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_EmptyResponse(arg) {
  if (!arg instanceof models_pb.EmptyResponse) {
    throw new Error("Expected argument of type azdext.EmptyResponse");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_EmptyResponse(arg) {
  return models_pb.EmptyResponse.deserializeBinary(new Uint8Array(arg));
}

const ProjectServiceService = exports.ProjectServiceService = {
  get: {
    path: "/azdext.ProjectService/Get",
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: project_pb.GetProjectResponse,
    requestSerialize: serialize_azdext_EmptyRequest,
    requestDeserialize: deserialize_azdext_EmptyRequest,
    responseSerialize: serialize_azdext_GetProjectResponse,
    responseDeserialize: deserialize_azdext_GetProjectResponse
  },
  addService: {
    path: "/azdext.ProjectService/AddService",
    requestStream: false,
    responseStream: false,
    requestType: project_pb.AddServiceRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azdext_AddServiceRequest,
    requestDeserialize: deserialize_azdext_AddServiceRequest,
    responseSerialize: serialize_azdext_EmptyResponse,
    responseDeserialize: deserialize_azdext_EmptyResponse
  },
};

exports.ProjectServiceClient = grpc.makeGenericClientConstructor(ProjectServiceService);