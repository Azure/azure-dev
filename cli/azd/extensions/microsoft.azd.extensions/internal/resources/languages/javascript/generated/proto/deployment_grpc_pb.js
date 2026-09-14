// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var deployment_pb = require('./deployment_pb.js');
var models_pb = require('./models_pb.js');

function serialize_azdext_EmptyRequest(arg) {
  if (!(arg instanceof models_pb.EmptyRequest)) {
    throw new Error('Expected argument of type azdext.EmptyRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_EmptyRequest(buffer_arg) {
  return models_pb.EmptyRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_GetDeploymentContextResponse(arg) {
  if (!(arg instanceof deployment_pb.GetDeploymentContextResponse)) {
    throw new Error('Expected argument of type azdext.GetDeploymentContextResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetDeploymentContextResponse(buffer_arg) {
  return deployment_pb.GetDeploymentContextResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_GetDeploymentResponse(arg) {
  if (!(arg instanceof deployment_pb.GetDeploymentResponse)) {
    throw new Error('Expected argument of type azdext.GetDeploymentResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetDeploymentResponse(buffer_arg) {
  return deployment_pb.GetDeploymentResponse.deserializeBinary(new Uint8Array(buffer_arg));
}


var DeploymentServiceService = exports.DeploymentServiceService = {
  // Gets the current environment.
getDeployment: {
    path: '/azdext.DeploymentService/GetDeployment',
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: deployment_pb.GetDeploymentResponse,
    requestSerialize: serialize_azdext_EmptyRequest,
    requestDeserialize: deserialize_azdext_EmptyRequest,
    responseSerialize: serialize_azdext_GetDeploymentResponse,
    responseDeserialize: deserialize_azdext_GetDeploymentResponse,
  },
  // GetDeploymentContext retrieves the current deployment context.
getDeploymentContext: {
    path: '/azdext.DeploymentService/GetDeploymentContext',
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: deployment_pb.GetDeploymentContextResponse,
    requestSerialize: serialize_azdext_EmptyRequest,
    requestDeserialize: deserialize_azdext_EmptyRequest,
    responseSerialize: serialize_azdext_GetDeploymentContextResponse,
    responseDeserialize: deserialize_azdext_GetDeploymentContextResponse,
  },
};

exports.DeploymentServiceClient = grpc.makeGenericClientConstructor(DeploymentServiceService, 'DeploymentService');
