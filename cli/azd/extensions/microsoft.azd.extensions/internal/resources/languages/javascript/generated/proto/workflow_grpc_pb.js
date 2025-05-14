// source: workflow.proto
// GENERATED CODE -- DO NOT EDIT!
/* eslint-disable */
// @ts-nocheck
"use strict";

const grpc = require("@grpc/grpc-js");
const workflow_pb = require("./workflow_pb.js");
const models_pb = require("./models_pb.js");

function serialize_azdext_RunWorkflowRequest(arg) {
  if (!arg instanceof workflow_pb.RunWorkflowRequest) {
    throw new Error("Expected argument of type azdext.RunWorkflowRequest");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_RunWorkflowRequest(arg) {
  return workflow_pb.RunWorkflowRequest.deserializeBinary(new Uint8Array(arg));
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

const WorkflowServiceService = exports.WorkflowServiceService = {
  run: {
    path: "/azdext.WorkflowService/Run",
    requestStream: false,
    responseStream: false,
    requestType: workflow_pb.RunWorkflowRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azdext_RunWorkflowRequest,
    requestDeserialize: deserialize_azdext_RunWorkflowRequest,
    responseSerialize: serialize_azdext_EmptyResponse,
    responseDeserialize: deserialize_azdext_EmptyResponse
  },
};

exports.WorkflowServiceClient = grpc.makeGenericClientConstructor(WorkflowServiceService);