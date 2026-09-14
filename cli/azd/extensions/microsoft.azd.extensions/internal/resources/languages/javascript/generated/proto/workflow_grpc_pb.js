// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var workflow_pb = require('./workflow_pb.js');
var models_pb = require('./models_pb.js');

function serialize_azdext_EmptyResponse(arg) {
  if (!(arg instanceof models_pb.EmptyResponse)) {
    throw new Error('Expected argument of type azdext.EmptyResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_EmptyResponse(buffer_arg) {
  return models_pb.EmptyResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_RunWorkflowRequest(arg) {
  if (!(arg instanceof workflow_pb.RunWorkflowRequest)) {
    throw new Error('Expected argument of type azdext.RunWorkflowRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_RunWorkflowRequest(buffer_arg) {
  return workflow_pb.RunWorkflowRequest.deserializeBinary(new Uint8Array(buffer_arg));
}


var WorkflowServiceService = exports.WorkflowServiceService = {
  // ListResources retrieves all configured composability resources in the current project.
run: {
    path: '/azdext.WorkflowService/Run',
    requestStream: false,
    responseStream: false,
    requestType: workflow_pb.RunWorkflowRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azdext_RunWorkflowRequest,
    requestDeserialize: deserialize_azdext_RunWorkflowRequest,
    responseSerialize: serialize_azdext_EmptyResponse,
    responseDeserialize: deserialize_azdext_EmptyResponse,
  },
};

exports.WorkflowServiceClient = grpc.makeGenericClientConstructor(WorkflowServiceService, 'WorkflowService');
