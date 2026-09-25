// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var user_config_pb = require('./user_config_pb.js');
var models_pb = require('./models_pb.js');

function serialize_azd_extensions_v1_CompareExchangeUserConfigMapEntryRequest(arg) {
  if (!(arg instanceof user_config_pb.CompareExchangeUserConfigMapEntryRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.CompareExchangeUserConfigMapEntryRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_CompareExchangeUserConfigMapEntryRequest(buffer_arg) {
  return user_config_pb.CompareExchangeUserConfigMapEntryRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_CompareExchangeUserConfigMapEntryResponse(arg) {
  if (!(arg instanceof user_config_pb.CompareExchangeUserConfigMapEntryResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.CompareExchangeUserConfigMapEntryResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_CompareExchangeUserConfigMapEntryResponse(buffer_arg) {
  return user_config_pb.CompareExchangeUserConfigMapEntryResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_DeleteUserConfigMapEntryRequest(arg) {
  if (!(arg instanceof user_config_pb.DeleteUserConfigMapEntryRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.DeleteUserConfigMapEntryRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_DeleteUserConfigMapEntryRequest(buffer_arg) {
  return user_config_pb.DeleteUserConfigMapEntryRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_EmptyResponse(arg) {
  if (!(arg instanceof models_pb.EmptyResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.EmptyResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_EmptyResponse(buffer_arg) {
  return models_pb.EmptyResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_GetUserConfigMapEntryRequest(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigMapEntryRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.GetUserConfigMapEntryRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_GetUserConfigMapEntryRequest(buffer_arg) {
  return user_config_pb.GetUserConfigMapEntryRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_GetUserConfigMapEntryResponse(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigMapEntryResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.GetUserConfigMapEntryResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_GetUserConfigMapEntryResponse(buffer_arg) {
  return user_config_pb.GetUserConfigMapEntryResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_GetUserConfigRequest(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.GetUserConfigRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_GetUserConfigRequest(buffer_arg) {
  return user_config_pb.GetUserConfigRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_GetUserConfigResponse(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.GetUserConfigResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_GetUserConfigResponse(buffer_arg) {
  return user_config_pb.GetUserConfigResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_GetUserConfigSectionRequest(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigSectionRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.GetUserConfigSectionRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_GetUserConfigSectionRequest(buffer_arg) {
  return user_config_pb.GetUserConfigSectionRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_GetUserConfigSectionResponse(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigSectionResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.GetUserConfigSectionResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_GetUserConfigSectionResponse(buffer_arg) {
  return user_config_pb.GetUserConfigSectionResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_GetUserConfigStringRequest(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigStringRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.GetUserConfigStringRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_GetUserConfigStringRequest(buffer_arg) {
  return user_config_pb.GetUserConfigStringRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_GetUserConfigStringResponse(arg) {
  if (!(arg instanceof user_config_pb.GetUserConfigStringResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.GetUserConfigStringResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_GetUserConfigStringResponse(buffer_arg) {
  return user_config_pb.GetUserConfigStringResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_SetUserConfigMapEntryRequest(arg) {
  if (!(arg instanceof user_config_pb.SetUserConfigMapEntryRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.SetUserConfigMapEntryRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_SetUserConfigMapEntryRequest(buffer_arg) {
  return user_config_pb.SetUserConfigMapEntryRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_SetUserConfigRequest(arg) {
  if (!(arg instanceof user_config_pb.SetUserConfigRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.SetUserConfigRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_SetUserConfigRequest(buffer_arg) {
  return user_config_pb.SetUserConfigRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_UnsetUserConfigRequest(arg) {
  if (!(arg instanceof user_config_pb.UnsetUserConfigRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.UnsetUserConfigRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_UnsetUserConfigRequest(buffer_arg) {
  return user_config_pb.UnsetUserConfigRequest.deserializeBinary(new Uint8Array(buffer_arg));
}


var UserConfigServiceService = exports.UserConfigServiceService = {
  // Get retrieves a value by path
get: {
    path: '/azd.extensions.v1.UserConfigService/Get',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.GetUserConfigRequest,
    responseType: user_config_pb.GetUserConfigResponse,
    requestSerialize: serialize_azd_extensions_v1_GetUserConfigRequest,
    requestDeserialize: deserialize_azd_extensions_v1_GetUserConfigRequest,
    responseSerialize: serialize_azd_extensions_v1_GetUserConfigResponse,
    responseDeserialize: deserialize_azd_extensions_v1_GetUserConfigResponse,
  },
  // GetString retrieves a value by path and returns it as a string
getString: {
    path: '/azd.extensions.v1.UserConfigService/GetString',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.GetUserConfigStringRequest,
    responseType: user_config_pb.GetUserConfigStringResponse,
    requestSerialize: serialize_azd_extensions_v1_GetUserConfigStringRequest,
    requestDeserialize: deserialize_azd_extensions_v1_GetUserConfigStringRequest,
    responseSerialize: serialize_azd_extensions_v1_GetUserConfigStringResponse,
    responseDeserialize: deserialize_azd_extensions_v1_GetUserConfigStringResponse,
  },
  // GetSection retrieves a section by path
getSection: {
    path: '/azd.extensions.v1.UserConfigService/GetSection',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.GetUserConfigSectionRequest,
    responseType: user_config_pb.GetUserConfigSectionResponse,
    requestSerialize: serialize_azd_extensions_v1_GetUserConfigSectionRequest,
    requestDeserialize: deserialize_azd_extensions_v1_GetUserConfigSectionRequest,
    responseSerialize: serialize_azd_extensions_v1_GetUserConfigSectionResponse,
    responseDeserialize: deserialize_azd_extensions_v1_GetUserConfigSectionResponse,
  },
  // Set sets a value at a given path
set: {
    path: '/azd.extensions.v1.UserConfigService/Set',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.SetUserConfigRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azd_extensions_v1_SetUserConfigRequest,
    requestDeserialize: deserialize_azd_extensions_v1_SetUserConfigRequest,
    responseSerialize: serialize_azd_extensions_v1_EmptyResponse,
    responseDeserialize: deserialize_azd_extensions_v1_EmptyResponse,
  },
  // Unset removes a value at a given path
unset: {
    path: '/azd.extensions.v1.UserConfigService/Unset',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.UnsetUserConfigRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azd_extensions_v1_UnsetUserConfigRequest,
    requestDeserialize: deserialize_azd_extensions_v1_UnsetUserConfigRequest,
    responseSerialize: serialize_azd_extensions_v1_EmptyResponse,
    responseDeserialize: deserialize_azd_extensions_v1_EmptyResponse,
  },
  // GetMapEntry retrieves a value under an opaque key in a map.
getMapEntry: {
    path: '/azd.extensions.v1.UserConfigService/GetMapEntry',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.GetUserConfigMapEntryRequest,
    responseType: user_config_pb.GetUserConfigMapEntryResponse,
    requestSerialize: serialize_azd_extensions_v1_GetUserConfigMapEntryRequest,
    requestDeserialize: deserialize_azd_extensions_v1_GetUserConfigMapEntryRequest,
    responseSerialize: serialize_azd_extensions_v1_GetUserConfigMapEntryResponse,
    responseDeserialize: deserialize_azd_extensions_v1_GetUserConfigMapEntryResponse,
  },
  // SetMapEntry sets a value under an opaque key in a map.
setMapEntry: {
    path: '/azd.extensions.v1.UserConfigService/SetMapEntry',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.SetUserConfigMapEntryRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azd_extensions_v1_SetUserConfigMapEntryRequest,
    requestDeserialize: deserialize_azd_extensions_v1_SetUserConfigMapEntryRequest,
    responseSerialize: serialize_azd_extensions_v1_EmptyResponse,
    responseDeserialize: deserialize_azd_extensions_v1_EmptyResponse,
  },
  // DeleteMapEntry removes an opaque key from a map.
deleteMapEntry: {
    path: '/azd.extensions.v1.UserConfigService/DeleteMapEntry',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.DeleteUserConfigMapEntryRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azd_extensions_v1_DeleteUserConfigMapEntryRequest,
    requestDeserialize: deserialize_azd_extensions_v1_DeleteUserConfigMapEntryRequest,
    responseSerialize: serialize_azd_extensions_v1_EmptyResponse,
    responseDeserialize: deserialize_azd_extensions_v1_EmptyResponse,
  },
  // CompareExchangeMapEntry conditionally updates an opaque map entry.
compareExchangeMapEntry: {
    path: '/azd.extensions.v1.UserConfigService/CompareExchangeMapEntry',
    requestStream: false,
    responseStream: false,
    requestType: user_config_pb.CompareExchangeUserConfigMapEntryRequest,
    responseType: user_config_pb.CompareExchangeUserConfigMapEntryResponse,
    requestSerialize: serialize_azd_extensions_v1_CompareExchangeUserConfigMapEntryRequest,
    requestDeserialize: deserialize_azd_extensions_v1_CompareExchangeUserConfigMapEntryRequest,
    responseSerialize: serialize_azd_extensions_v1_CompareExchangeUserConfigMapEntryResponse,
    responseDeserialize: deserialize_azd_extensions_v1_CompareExchangeUserConfigMapEntryResponse,
  },
};

exports.UserConfigServiceClient = grpc.makeGenericClientConstructor(UserConfigServiceService, 'UserConfigService');
