// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var user_config_pb = require('./user_config_pb.js');
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

function serialize_azdext_GetUserConfigRequest(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigRequest)) {
    throw new Error('Expected argument of type azdext.GetUserConfigRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetUserConfigRequest(buffer_arg) {
  return user_config_pb.GetUserConfigRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_GetUserConfigResponse(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigResponse)) {
    throw new Error('Expected argument of type azdext.GetUserConfigResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetUserConfigResponse(buffer_arg) {
  return user_config_pb.GetUserConfigResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_GetUserConfigSectionRequest(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigSectionRequest)) {
    throw new Error('Expected argument of type azdext.GetUserConfigSectionRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetUserConfigSectionRequest(buffer_arg) {
  return user_config_pb.GetUserConfigSectionRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_GetUserConfigSectionResponse(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigSectionResponse)) {
    throw new Error('Expected argument of type azdext.GetUserConfigSectionResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetUserConfigSectionResponse(buffer_arg) {
  return user_config_pb.GetUserConfigSectionResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_GetUserConfigStringRequest(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigStringRequest)) {
    throw new Error('Expected argument of type azdext.GetUserConfigStringRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetUserConfigStringRequest(buffer_arg) {
  return user_config_pb.GetUserConfigStringRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_GetUserConfigStringResponse(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigStringResponse)) {
    throw new Error('Expected argument of type azdext.GetUserConfigStringResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetUserConfigStringResponse(buffer_arg) {
  return user_config_pb.GetUserConfigStringResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_SetUserConfigRequest(arg) {
  if (!(arg instanceof user_config_pb.SetUserConfigRequest)) {
    throw new Error('Expected argument of type azdext.SetUserConfigRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_SetUserConfigRequest(buffer_arg) {
  return user_config_pb.SetUserConfigRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_UnsetUserConfigRequest(arg) {
  if (!(arg instanceof user_config_pb.UnsetUserConfigRequest)) {
    throw new Error('Expected argument of type azdext.UnsetUserConfigRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_UnsetUserConfigRequest(buffer_arg) {
  return user_config_pb.UnsetUserConfigRequest.deserializeBinary(new Uint8Array(buffer_arg));
}


var UserConfigServiceService = exports.UserConfigServiceService = {
  // Get retrieves a value by path
get: {
    path: '/azdext.UserConfigService/Get',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.GetUserConfigRequest,
    responseType: user_config_pb.GetUserConfigResponse,
    requestSerialize: serialize_azdext_GetUserConfigRequest,
    requestDeserialize: deserialize_azdext_GetUserConfigRequest,
    responseSerialize: serialize_azdext_GetUserConfigResponse,
    responseDeserialize: deserialize_azdext_GetUserConfigResponse,
  },
  // GetString retrieves a value by path and returns it as a string
getString: {
    path: '/azdext.UserConfigService/GetString',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.GetUserConfigStringRequest,
    responseType: user_config_pb.GetUserConfigStringResponse,
    requestSerialize: serialize_azdext_GetUserConfigStringRequest,
    requestDeserialize: deserialize_azdext_GetUserConfigStringRequest,
    responseSerialize: serialize_azdext_GetUserConfigStringResponse,
    responseDeserialize: deserialize_azdext_GetUserConfigStringResponse,
  },
  // GetSection retrieves a section by path
getSection: {
    path: '/azdext.UserConfigService/GetSection',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.GetUserConfigSectionRequest,
    responseType: user_config_pb.GetUserConfigSectionResponse,
    requestSerialize: serialize_azdext_GetUserConfigSectionRequest,
    requestDeserialize: deserialize_azdext_GetUserConfigSectionRequest,
    responseSerialize: serialize_azdext_GetUserConfigSectionResponse,
    responseDeserialize: deserialize_azdext_GetUserConfigSectionResponse,
  },
  // Set sets a value at a given path
set: {
    path: '/azdext.UserConfigService/Set',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.SetUserConfigRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azdext_SetUserConfigRequest,
    requestDeserialize: deserialize_azdext_SetUserConfigRequest,
    responseSerialize: serialize_azdext_EmptyResponse,
    responseDeserialize: deserialize_azdext_EmptyResponse,
  },
  // Unset removes a value at a given path
unset: {
    path: '/azdext.UserConfigService/Unset',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.UnsetUserConfigRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azdext_UnsetUserConfigRequest,
    requestDeserialize: deserialize_azdext_UnsetUserConfigRequest,
    responseSerialize: serialize_azdext_EmptyResponse,
    responseDeserialize: deserialize_azdext_EmptyResponse,
  },
};

exports.UserConfigServiceClient = grpc.makeGenericClientConstructor(UserConfigServiceService, 'UserConfigService');
