// source: deployment.proto
// GENERATED CODE -- DO NOT EDIT!
/* eslint-disable */
// @ts-nocheck
"use strict";

const grpc = require("@grpc/grpc-js");
const deployment_pb = require("./deployment_pb.js");
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

function serialize_azdext_GetDeploymentResponse(arg) {
  if (!arg instanceof deployment_pb.GetDeploymentResponse) {
    throw new Error("Expected argument of type azdext.GetDeploymentResponse");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetDeploymentResponse(arg) {
  return deployment_pb.GetDeploymentResponse.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_GetDeploymentContextResponse(arg) {
  if (!arg instanceof deployment_pb.GetDeploymentContextResponse) {
    throw new Error("Expected argument of type azdext.GetDeploymentContextResponse");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetDeploymentContextResponse(arg) {
  return deployment_pb.GetDeploymentContextResponse.deserializeBinary(new Uint8Array(arg));
}

const DeploymentServiceService = exports.DeploymentServiceService = {
  getDeployment: {
    path: "/azdext.DeploymentService/GetDeployment",
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: deployment_pb.GetDeploymentResponse,
    requestSerialize: serialize_azdext_EmptyRequest,
    requestDeserialize: deserialize_azdext_EmptyRequest,
    responseSerialize: serialize_azdext_GetDeploymentResponse,
    responseDeserialize: deserialize_azdext_GetDeploymentResponse
  },
  getDeploymentContext: {
    path: "/azdext.DeploymentService/GetDeploymentContext",
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: deployment_pb.GetDeploymentContextResponse,
    requestSerialize: serialize_azdext_EmptyRequest,
    requestDeserialize: deserialize_azdext_EmptyRequest,
    responseSerialize: serialize_azdext_GetDeploymentContextResponse,
    responseDeserialize: deserialize_azdext_GetDeploymentContextResponse
  },
};

exports.DeploymentServiceClient = grpc.makeGenericClientConstructor(DeploymentServiceService);