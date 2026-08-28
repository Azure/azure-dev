// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var workflow_pb = require('./workflow_pb.js');
var models_pb = require('./models_pb.js');

function serialize_azd_extensions_v1_EmptyResponse(arg) {
  if (!(arg instanceof models_pb.EmptyResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.EmptyResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_EmptyResponse(buffer_arg) {
  return models_pb.EmptyResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_RunWorkflowRequest(arg) {
  if (!(arg instanceof workflow_pb.RunWorkflowRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.RunWorkflowRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_RunWorkflowRequest(buffer_arg) {
  return workflow_pb.RunWorkflowRequest.deserializeBinary(new Uint8Array(buffer_arg));
}


var WorkflowServiceService = exports.WorkflowServiceService = {
  // Run executes a workflow.
run: {
    path: '/azd.extensions.v1.WorkflowService/Run',
    requestStream: false,
    responseStream: false,
    requestType: workflow_pb.RunWorkflowRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azd_extensions_v1_RunWorkflowRequest,
    requestDeserialize: deserialize_azd_extensions_v1_RunWorkflowRequest,
    responseSerialize: serialize_azd_extensions_v1_EmptyResponse,
    responseDeserialize: deserialize_azd_extensions_v1_EmptyResponse,
  },
};

exports.WorkflowServiceClient = grpc.makeGenericClientConstructor(WorkflowServiceService, 'WorkflowService');
