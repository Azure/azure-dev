// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var environment_pb = require('./environment_pb.js');
var models_pb = require('./models_pb.js');

function serialize_azd_extensions_v1beta_EmptyRequest(arg) {
  if (!(arg instanceof models_pb.EmptyRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.EmptyRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_EmptyRequest(buffer_arg) {
  return models_pb.EmptyRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_EmptyResponse(arg) {
  if (!(arg instanceof models_pb.EmptyResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.EmptyResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_EmptyResponse(buffer_arg) {
  return models_pb.EmptyResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_EnvironmentListResponse(arg) {
  if (!(arg instanceof environment_pb.EnvironmentListResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.EnvironmentListResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_EnvironmentListResponse(buffer_arg) {
  return environment_pb.EnvironmentListResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_EnvironmentResponse(arg) {
  if (!(arg instanceof environment_pb.EnvironmentResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.EnvironmentResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_EnvironmentResponse(buffer_arg) {
  return environment_pb.EnvironmentResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_GetConfigRequest(arg) {
  if (!(arg instanceof environment_pb.GetConfigRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.GetConfigRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_GetConfigRequest(buffer_arg) {
  return environment_pb.GetConfigRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_GetConfigResponse(arg) {
  if (!(arg instanceof environment_pb.GetConfigResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.GetConfigResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_GetConfigResponse(buffer_arg) {
  return environment_pb.GetConfigResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_GetConfigSectionRequest(arg) {
  if (!(arg instanceof environment_pb.GetConfigSectionRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.GetConfigSectionRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_GetConfigSectionRequest(buffer_arg) {
  return environment_pb.GetConfigSectionRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_GetConfigSectionResponse(arg) {
  if (!(arg instanceof environment_pb.GetConfigSectionResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.GetConfigSectionResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_GetConfigSectionResponse(buffer_arg) {
  return environment_pb.GetConfigSectionResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_GetConfigStringRequest(arg) {
  if (!(arg instanceof environment_pb.GetConfigStringRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.GetConfigStringRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_GetConfigStringRequest(buffer_arg) {
  return environment_pb.GetConfigStringRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_GetConfigStringResponse(arg) {
  if (!(arg instanceof environment_pb.GetConfigStringResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.GetConfigStringResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_GetConfigStringResponse(buffer_arg) {
  return environment_pb.GetConfigStringResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_GetEnvRequest(arg) {
  if (!(arg instanceof environment_pb.GetEnvRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.GetEnvRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_GetEnvRequest(buffer_arg) {
  return environment_pb.GetEnvRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_GetEnvironmentRequest(arg) {
  if (!(arg instanceof environment_pb.GetEnvironmentRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.GetEnvironmentRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_GetEnvironmentRequest(buffer_arg) {
  return environment_pb.GetEnvironmentRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_KeyValueListResponse(arg) {
  if (!(arg instanceof environment_pb.KeyValueListResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.KeyValueListResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_KeyValueListResponse(buffer_arg) {
  return environment_pb.KeyValueListResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_KeyValueResponse(arg) {
  if (!(arg instanceof environment_pb.KeyValueResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.KeyValueResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_KeyValueResponse(buffer_arg) {
  return environment_pb.KeyValueResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_SelectEnvironmentRequest(arg) {
  if (!(arg instanceof environment_pb.SelectEnvironmentRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.SelectEnvironmentRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_SelectEnvironmentRequest(buffer_arg) {
  return environment_pb.SelectEnvironmentRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_SetConfigRequest(arg) {
  if (!(arg instanceof environment_pb.SetConfigRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.SetConfigRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_SetConfigRequest(buffer_arg) {
  return environment_pb.SetConfigRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_SetEnvRequest(arg) {
  if (!(arg instanceof environment_pb.SetEnvRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.SetEnvRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_SetEnvRequest(buffer_arg) {
  return environment_pb.SetEnvRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_UnsetConfigRequest(arg) {
  if (!(arg instanceof environment_pb.UnsetConfigRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.UnsetConfigRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_UnsetConfigRequest(buffer_arg) {
  return environment_pb.UnsetConfigRequest.deserializeBinary(new Uint8Array(buffer_arg));
}


// EnvironmentService defines methods for managing environments and their key-value pairs.
var EnvironmentServiceService = exports.EnvironmentServiceService = {
  // Gets the current environment.
getCurrent: {
    path: '/azd.extensions.v1beta.EnvironmentService/GetCurrent',
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: environment_pb.EnvironmentResponse,
    requestSerialize: serialize_azd_extensions_v1beta_EmptyRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_EmptyRequest,
    responseSerialize: serialize_azd_extensions_v1beta_EnvironmentResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_EnvironmentResponse,
  },
  // List retrieves all azd environments.
list: {
    path: '/azd.extensions.v1beta.EnvironmentService/List',
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: environment_pb.EnvironmentListResponse,
    requestSerialize: serialize_azd_extensions_v1beta_EmptyRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_EmptyRequest,
    responseSerialize: serialize_azd_extensions_v1beta_EnvironmentListResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_EnvironmentListResponse,
  },
  // Get retrieves an environment by its name.
get: {
    path: '/azd.extensions.v1beta.EnvironmentService/Get',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.GetEnvironmentRequest,
    responseType: environment_pb.EnvironmentResponse,
    requestSerialize: serialize_azd_extensions_v1beta_GetEnvironmentRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_GetEnvironmentRequest,
    responseSerialize: serialize_azd_extensions_v1beta_EnvironmentResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_EnvironmentResponse,
  },
  // Select sets the current environment to the specified environment.
select: {
    path: '/azd.extensions.v1beta.EnvironmentService/Select',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.SelectEnvironmentRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azd_extensions_v1beta_SelectEnvironmentRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_SelectEnvironmentRequest,
    responseSerialize: serialize_azd_extensions_v1beta_EmptyResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_EmptyResponse,
  },
  // GetValues retrieves all key-value pairs in the specified environment.
getValues: {
    path: '/azd.extensions.v1beta.EnvironmentService/GetValues',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.GetEnvironmentRequest,
    responseType: environment_pb.KeyValueListResponse,
    requestSerialize: serialize_azd_extensions_v1beta_GetEnvironmentRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_GetEnvironmentRequest,
    responseSerialize: serialize_azd_extensions_v1beta_KeyValueListResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_KeyValueListResponse,
  },
  // GetValue retrieves the value of a specific key in the specified environment.
getValue: {
    path: '/azd.extensions.v1beta.EnvironmentService/GetValue',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.GetEnvRequest,
    responseType: environment_pb.KeyValueResponse,
    requestSerialize: serialize_azd_extensions_v1beta_GetEnvRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_GetEnvRequest,
    responseSerialize: serialize_azd_extensions_v1beta_KeyValueResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_KeyValueResponse,
  },
  // SetValue sets the value of a key in the specified environment.
setValue: {
    path: '/azd.extensions.v1beta.EnvironmentService/SetValue',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.SetEnvRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azd_extensions_v1beta_SetEnvRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_SetEnvRequest,
    responseSerialize: serialize_azd_extensions_v1beta_EmptyResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_EmptyResponse,
  },
  // GetConfig retrieves a config value by path
getConfig: {
    path: '/azd.extensions.v1beta.EnvironmentService/GetConfig',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.GetConfigRequest,
    responseType: environment_pb.GetConfigResponse,
    requestSerialize: serialize_azd_extensions_v1beta_GetConfigRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_GetConfigRequest,
    responseSerialize: serialize_azd_extensions_v1beta_GetConfigResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_GetConfigResponse,
  },
  // GetConfigString retrieves a config value by path and returns it as a string
getConfigString: {
    path: '/azd.extensions.v1beta.EnvironmentService/GetConfigString',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.GetConfigStringRequest,
    responseType: environment_pb.GetConfigStringResponse,
    requestSerialize: serialize_azd_extensions_v1beta_GetConfigStringRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_GetConfigStringRequest,
    responseSerialize: serialize_azd_extensions_v1beta_GetConfigStringResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_GetConfigStringResponse,
  },
  // GetConfigSection retrieves a config section by path
getConfigSection: {
    path: '/azd.extensions.v1beta.EnvironmentService/GetConfigSection',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.GetConfigSectionRequest,
    responseType: environment_pb.GetConfigSectionResponse,
    requestSerialize: serialize_azd_extensions_v1beta_GetConfigSectionRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_GetConfigSectionRequest,
    responseSerialize: serialize_azd_extensions_v1beta_GetConfigSectionResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_GetConfigSectionResponse,
  },
  // SetConfig sets a config value at a given path
setConfig: {
    path: '/azd.extensions.v1beta.EnvironmentService/SetConfig',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.SetConfigRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azd_extensions_v1beta_SetConfigRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_SetConfigRequest,
    responseSerialize: serialize_azd_extensions_v1beta_EmptyResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_EmptyResponse,
  },
  // UnsetConfig removes a config value at a given path
unsetConfig: {
    path: '/azd.extensions.v1beta.EnvironmentService/UnsetConfig',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.UnsetConfigRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azd_extensions_v1beta_UnsetConfigRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_UnsetConfigRequest,
    responseSerialize: serialize_azd_extensions_v1beta_EmptyResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_EmptyResponse,
  },
};

exports.EnvironmentServiceClient = grpc.makeGenericClientConstructor(EnvironmentServiceService, 'EnvironmentService');
