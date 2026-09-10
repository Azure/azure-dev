// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var environment_pb = require('./environment_pb.js');
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

function serialize_azdext_EmptyResponse(arg) {
  if (!(arg instanceof models_pb.EmptyResponse)) {
    throw new Error('Expected argument of type azdext.EmptyResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_EmptyResponse(buffer_arg) {
  return models_pb.EmptyResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_EnvironmentListResponse(arg) {
  if (!(arg instanceof environment_pb.EnvironmentListResponse)) {
    throw new Error('Expected argument of type azdext.EnvironmentListResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_EnvironmentListResponse(buffer_arg) {
  return environment_pb.EnvironmentListResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_EnvironmentResponse(arg) {
  if (!(arg instanceof environment_pb.EnvironmentResponse)) {
    throw new Error('Expected argument of type azdext.EnvironmentResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_EnvironmentResponse(buffer_arg) {
  return environment_pb.EnvironmentResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_GetConfigRequest(arg) {
  if (!(arg instanceof environment_pb.GetConfigRequest)) {
    throw new Error('Expected argument of type azdext.GetConfigRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetConfigRequest(buffer_arg) {
  return environment_pb.GetConfigRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_GetConfigResponse(arg) {
  if (!(arg instanceof environment_pb.GetConfigResponse)) {
    throw new Error('Expected argument of type azdext.GetConfigResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetConfigResponse(buffer_arg) {
  return environment_pb.GetConfigResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_GetConfigSectionRequest(arg) {
  if (!(arg instanceof environment_pb.GetConfigSectionRequest)) {
    throw new Error('Expected argument of type azdext.GetConfigSectionRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetConfigSectionRequest(buffer_arg) {
  return environment_pb.GetConfigSectionRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_GetConfigSectionResponse(arg) {
  if (!(arg instanceof environment_pb.GetConfigSectionResponse)) {
    throw new Error('Expected argument of type azdext.GetConfigSectionResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetConfigSectionResponse(buffer_arg) {
  return environment_pb.GetConfigSectionResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_GetConfigStringRequest(arg) {
  if (!(arg instanceof environment_pb.GetConfigStringRequest)) {
    throw new Error('Expected argument of type azdext.GetConfigStringRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetConfigStringRequest(buffer_arg) {
  return environment_pb.GetConfigStringRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_GetConfigStringResponse(arg) {
  if (!(arg instanceof environment_pb.GetConfigStringResponse)) {
    throw new Error('Expected argument of type azdext.GetConfigStringResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetConfigStringResponse(buffer_arg) {
  return environment_pb.GetConfigStringResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_GetEnvRequest(arg) {
  if (!(arg instanceof environment_pb.GetEnvRequest)) {
    throw new Error('Expected argument of type azdext.GetEnvRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetEnvRequest(buffer_arg) {
  return environment_pb.GetEnvRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_GetEnvironmentRequest(arg) {
  if (!(arg instanceof environment_pb.GetEnvironmentRequest)) {
    throw new Error('Expected argument of type azdext.GetEnvironmentRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_GetEnvironmentRequest(buffer_arg) {
  return environment_pb.GetEnvironmentRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_KeyValueListResponse(arg) {
  if (!(arg instanceof environment_pb.KeyValueListResponse)) {
    throw new Error('Expected argument of type azdext.KeyValueListResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_KeyValueListResponse(buffer_arg) {
  return environment_pb.KeyValueListResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_KeyValueResponse(arg) {
  if (!(arg instanceof environment_pb.KeyValueResponse)) {
    throw new Error('Expected argument of type azdext.KeyValueResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_KeyValueResponse(buffer_arg) {
  return environment_pb.KeyValueResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_SelectEnvironmentRequest(arg) {
  if (!(arg instanceof environment_pb.SelectEnvironmentRequest)) {
    throw new Error('Expected argument of type azdext.SelectEnvironmentRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_SelectEnvironmentRequest(buffer_arg) {
  return environment_pb.SelectEnvironmentRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_SetConfigRequest(arg) {
  if (!(arg instanceof environment_pb.SetConfigRequest)) {
    throw new Error('Expected argument of type azdext.SetConfigRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_SetConfigRequest(buffer_arg) {
  return environment_pb.SetConfigRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_SetEnvRequest(arg) {
  if (!(arg instanceof environment_pb.SetEnvRequest)) {
    throw new Error('Expected argument of type azdext.SetEnvRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_SetEnvRequest(buffer_arg) {
  return environment_pb.SetEnvRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azdext_UnsetConfigRequest(arg) {
  if (!(arg instanceof environment_pb.UnsetConfigRequest)) {
    throw new Error('Expected argument of type azdext.UnsetConfigRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_UnsetConfigRequest(buffer_arg) {
  return environment_pb.UnsetConfigRequest.deserializeBinary(new Uint8Array(buffer_arg));
}


// EnvironmentService defines methods for managing environments and their key-value pairs.
var EnvironmentServiceService = exports.EnvironmentServiceService = {
  // Gets the current environment.
getCurrent: {
    path: '/azdext.EnvironmentService/GetCurrent',
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: environment_pb.EnvironmentResponse,
    requestSerialize: serialize_azdext_EmptyRequest,
    requestDeserialize: deserialize_azdext_EmptyRequest,
    responseSerialize: serialize_azdext_EnvironmentResponse,
    responseDeserialize: deserialize_azdext_EnvironmentResponse,
  },
  // List retrieves all azd environments.
list: {
    path: '/azdext.EnvironmentService/List',
    requestStream: false,
    responseStream: false,
    requestType: models_pb.EmptyRequest,
    responseType: environment_pb.EnvironmentListResponse,
    requestSerialize: serialize_azdext_EmptyRequest,
    requestDeserialize: deserialize_azdext_EmptyRequest,
    responseSerialize: serialize_azdext_EnvironmentListResponse,
    responseDeserialize: deserialize_azdext_EnvironmentListResponse,
  },
  // Get retrieves an environment by its name.
get: {
    path: '/azdext.EnvironmentService/Get',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.GetEnvironmentRequest,
    responseType: environment_pb.EnvironmentResponse,
    requestSerialize: serialize_azdext_GetEnvironmentRequest,
    requestDeserialize: deserialize_azdext_GetEnvironmentRequest,
    responseSerialize: serialize_azdext_EnvironmentResponse,
    responseDeserialize: deserialize_azdext_EnvironmentResponse,
  },
  // Select sets the current environment to the specified environment.
select: {
    path: '/azdext.EnvironmentService/Select',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.SelectEnvironmentRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azdext_SelectEnvironmentRequest,
    requestDeserialize: deserialize_azdext_SelectEnvironmentRequest,
    responseSerialize: serialize_azdext_EmptyResponse,
    responseDeserialize: deserialize_azdext_EmptyResponse,
  },
  // GetValues retrieves all key-value pairs in the specified environment.
getValues: {
    path: '/azdext.EnvironmentService/GetValues',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.GetEnvironmentRequest,
    responseType: environment_pb.KeyValueListResponse,
    requestSerialize: serialize_azdext_GetEnvironmentRequest,
    requestDeserialize: deserialize_azdext_GetEnvironmentRequest,
    responseSerialize: serialize_azdext_KeyValueListResponse,
    responseDeserialize: deserialize_azdext_KeyValueListResponse,
  },
  // GetValue retrieves the value of a specific key in the specified environment.
getValue: {
    path: '/azdext.EnvironmentService/GetValue',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.GetEnvRequest,
    responseType: environment_pb.KeyValueResponse,
    requestSerialize: serialize_azdext_GetEnvRequest,
    requestDeserialize: deserialize_azdext_GetEnvRequest,
    responseSerialize: serialize_azdext_KeyValueResponse,
    responseDeserialize: deserialize_azdext_KeyValueResponse,
  },
  // SetValue sets the value of a key in the specified environment.
setValue: {
    path: '/azdext.EnvironmentService/SetValue',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.SetEnvRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azdext_SetEnvRequest,
    requestDeserialize: deserialize_azdext_SetEnvRequest,
    responseSerialize: serialize_azdext_EmptyResponse,
    responseDeserialize: deserialize_azdext_EmptyResponse,
  },
  // GetConfig retrieves a config value by path
getConfig: {
    path: '/azdext.EnvironmentService/GetConfig',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.GetConfigRequest,
    responseType: environment_pb.GetConfigResponse,
    requestSerialize: serialize_azdext_GetConfigRequest,
    requestDeserialize: deserialize_azdext_GetConfigRequest,
    responseSerialize: serialize_azdext_GetConfigResponse,
    responseDeserialize: deserialize_azdext_GetConfigResponse,
  },
  // GetConfigString retrieves a config value by path and returns it as a string
getConfigString: {
    path: '/azdext.EnvironmentService/GetConfigString',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.GetConfigStringRequest,
    responseType: environment_pb.GetConfigStringResponse,
    requestSerialize: serialize_azdext_GetConfigStringRequest,
    requestDeserialize: deserialize_azdext_GetConfigStringRequest,
    responseSerialize: serialize_azdext_GetConfigStringResponse,
    responseDeserialize: deserialize_azdext_GetConfigStringResponse,
  },
  // GetConfigSection retrieves a config section by path
getConfigSection: {
    path: '/azdext.EnvironmentService/GetConfigSection',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.GetConfigSectionRequest,
    responseType: environment_pb.GetConfigSectionResponse,
    requestSerialize: serialize_azdext_GetConfigSectionRequest,
    requestDeserialize: deserialize_azdext_GetConfigSectionRequest,
    responseSerialize: serialize_azdext_GetConfigSectionResponse,
    responseDeserialize: deserialize_azdext_GetConfigSectionResponse,
  },
  // SetConfig sets a config value at a given path
setConfig: {
    path: '/azdext.EnvironmentService/SetConfig',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.SetConfigRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azdext_SetConfigRequest,
    requestDeserialize: deserialize_azdext_SetConfigRequest,
    responseSerialize: serialize_azdext_EmptyResponse,
    responseDeserialize: deserialize_azdext_EmptyResponse,
  },
  // UnsetConfig removes a config value at a given path
unsetConfig: {
    path: '/azdext.EnvironmentService/UnsetConfig',
    requestStream: false,
    responseStream: false,
    requestType: environment_pb.UnsetConfigRequest,
    responseType: models_pb.EmptyResponse,
    requestSerialize: serialize_azdext_UnsetConfigRequest,
    requestDeserialize: deserialize_azdext_UnsetConfigRequest,
    responseSerialize: serialize_azdext_EmptyResponse,
    responseDeserialize: deserialize_azdext_EmptyResponse,
  },
};

exports.EnvironmentServiceClient = grpc.makeGenericClientConstructor(EnvironmentServiceService, 'EnvironmentService');
