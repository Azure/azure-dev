// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var deployment_pb = require('./deployment_pb.js');
var models_pb = require('./models_pb.js');

function serialize_azd_extensions_v1_EmptyRequest(arg) {
  if (!(arg instanceof models_pb.EmptyRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.EmptyRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_EmptyRequest(buffer_arg) {
  return models_pb.EmptyRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_GetDeploymentContextResponse(arg) {
  if (!(arg instanceof deployment_pb.GetDeploymentContextResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.GetDeploymentContextResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_GetDeploymentContextResponse(buffer_arg) {
  return deployment_pb.GetDeploymentContextResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_GetDeploymentResponse(arg) {
  if (!(arg instanceof deployment_pb.GetDeploymentResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.GetDeploymentResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_GetDeploymentResponse(buffer_arg) {
  return deployment_pb.GetDeploymentResponse.deserializeBinary(new Uint8Array(buffer_arg));
}


var DeploymentServiceService = exports.DeploymentServiceService = {
  // GetDeployment retrieves the current deployment.
getDeployment: {
    path: '/azd.extensions.v1.DeploymentService/GetDeployment',
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: deployment_pb.GetDeploymentResponse,
    requestSerialize: serialize_azd_extensions_v1_EmptyRequest,
    requestDeserialize: deserialize_azd_extensions_v1_EmptyRequest,
    responseSerialize: serialize_azd_extensions_v1_GetDeploymentResponse,
    responseDeserialize: deserialize_azd_extensions_v1_GetDeploymentResponse,
  },
  // GetDeploymentContext retrieves the current deployment context.
getDeploymentContext: {
    path: '/azd.extensions.v1.DeploymentService/GetDeploymentContext',
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: deployment_pb.GetDeploymentContextResponse,
    requestSerialize: serialize_azd_extensions_v1_EmptyRequest,
    requestDeserialize: deserialize_azd_extensions_v1_EmptyRequest,
    responseSerialize: serialize_azd_extensions_v1_GetDeploymentContextResponse,
    responseDeserialize: deserialize_azd_extensions_v1_GetDeploymentContextResponse,
  },
};

exports.DeploymentServiceClient = grpc.makeGenericClientConstructor(DeploymentServiceService, 'DeploymentService');
