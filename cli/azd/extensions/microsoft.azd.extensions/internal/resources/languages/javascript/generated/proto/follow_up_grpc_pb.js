// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var follow_up_pb = require('./follow_up_pb.js');

function serialize_azd_extensions_v1_SetFollowUpRequest(arg) {
  if (!(arg instanceof follow_up_pb.SetFollowUpRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.SetFollowUpRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_SetFollowUpRequest(buffer_arg) {
  return follow_up_pb.SetFollowUpRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_SetFollowUpResponse(arg) {
  if (!(arg instanceof follow_up_pb.SetFollowUpResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.SetFollowUpResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_SetFollowUpResponse(buffer_arg) {
  return follow_up_pb.SetFollowUpResponse.deserializeBinary(new Uint8Array(buffer_arg));
}


// FollowUpService accepts command follow-up contributions from extensions.
var FollowUpServiceService = exports.FollowUpServiceService = {
  // Sets or clears the contribution for a project handler invocation.
setFollowUp: {
    path: '/azd.extensions.v1.FollowUpService/SetFollowUp',
    requestStream: false,
    responseStream: false,
    requestType: follow_up_pb.SetFollowUpRequest,
    responseType: follow_up_pb.SetFollowUpResponse,
    requestSerialize: serialize_azd_extensions_v1_SetFollowUpRequest,
    requestDeserialize: deserialize_azd_extensions_v1_SetFollowUpRequest,
    responseSerialize: serialize_azd_extensions_v1_SetFollowUpResponse,
    responseDeserialize: deserialize_azd_extensions_v1_SetFollowUpResponse,
  },
};

exports.FollowUpServiceClient = grpc.makeGenericClientConstructor(FollowUpServiceService, 'FollowUpService');
