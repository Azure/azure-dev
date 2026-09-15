// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var user_config_pb = require('./user_config_pb.js');
var models_pb = require('./models_pb.js');

function serialize_azd_extensions_v1beta_EmptyResponse(arg) {
  if (!(arg instanceof models_pb.EmptyResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.EmptyResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_EmptyResponse(buffer_arg) {
  return models_pb.EmptyResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_GetUserConfigRequest(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.GetUserConfigRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_GetUserConfigRequest(buffer_arg) {
  return user_config_pb.GetUserConfigRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_GetUserConfigResponse(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.GetUserConfigResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_GetUserConfigResponse(buffer_arg) {
  return user_config_pb.GetUserConfigResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_GetUserConfigSectionRequest(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigSectionRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.GetUserConfigSectionRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_GetUserConfigSectionRequest(buffer_arg) {
  return user_config_pb.GetUserConfigSectionRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_GetUserConfigSectionResponse(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigSectionResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.GetUserConfigSectionResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_GetUserConfigSectionResponse(buffer_arg) {
  return user_config_pb.GetUserConfigSectionResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_GetUserConfigStringRequest(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigStringRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.GetUserConfigStringRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_GetUserConfigStringRequest(buffer_arg) {
  return user_config_pb.GetUserConfigStringRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_GetUserConfigStringResponse(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigStringResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.GetUserConfigStringResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_GetUserConfigStringResponse(buffer_arg) {
  return user_config_pb.GetUserConfigStringResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_SetUserConfigRequest(arg) {
  if (!(arg instanceof user_config_pb.SetUserConfigRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.SetUserConfigRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_SetUserConfigRequest(buffer_arg) {
  return user_config_pb.SetUserConfigRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_UnsetUserConfigRequest(arg) {
  if (!(arg instanceof user_config_pb.UnsetUserConfigRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.UnsetUserConfigRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_UnsetUserConfigRequest(buffer_arg) {
  return user_config_pb.UnsetUserConfigRequest.deserializeBinary(new Uint8Array(buffer_arg));
}


var UserConfigServiceService = exports.UserConfigServiceService = {
  // Get retrieves a value by path
get: {
    path: '/azd.extensions.v1beta.UserConfigService/Get',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.GetUserConfigRequest,
    responseType: user_config_pb.GetUserConfigResponse,
    requestSerialize: serialize_azd_extensions_v1beta_GetUserConfigRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_GetUserConfigRequest,
    responseSerialize: serialize_azd_extensions_v1beta_GetUserConfigResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_GetUserConfigResponse,
  },
  // GetString retrieves a value by path and returns it as a string
getString: {
    path: '/azd.extensions.v1beta.UserConfigService/GetString',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.GetUserConfigStringRequest,
    responseType: user_config_pb.GetUserConfigStringResponse,
    requestSerialize: serialize_azd_extensions_v1beta_GetUserConfigStringRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_GetUserConfigStringRequest,
    responseSerialize: serialize_azd_extensions_v1beta_GetUserConfigStringResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_GetUserConfigStringResponse,
  },
  // GetSection retrieves a section by path
getSection: {
    path: '/azd.extensions.v1beta.UserConfigService/GetSection',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.GetUserConfigSectionRequest,
    responseType: user_config_pb.GetUserConfigSectionResponse,
    requestSerialize: serialize_azd_extensions_v1beta_GetUserConfigSectionRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_GetUserConfigSectionRequest,
    responseSerialize: serialize_azd_extensions_v1beta_GetUserConfigSectionResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_GetUserConfigSectionResponse,
  },
  // Set sets a value at a given path
set: {
    path: '/azd.extensions.v1beta.UserConfigService/Set',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.SetUserConfigRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azd_extensions_v1beta_SetUserConfigRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_SetUserConfigRequest,
    responseSerialize: serialize_azd_extensions_v1beta_EmptyResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_EmptyResponse,
  },
  // Unset removes a value at a given path
unset: {
    path: '/azd.extensions.v1beta.UserConfigService/Unset',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.UnsetUserConfigRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azd_extensions_v1beta_UnsetUserConfigRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_UnsetUserConfigRequest,
    responseSerialize: serialize_azd_extensions_v1beta_EmptyResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_EmptyResponse,
  },
};

exports.UserConfigServiceClient = grpc.makeGenericClientConstructor(UserConfigServiceService, 'UserConfigService');
