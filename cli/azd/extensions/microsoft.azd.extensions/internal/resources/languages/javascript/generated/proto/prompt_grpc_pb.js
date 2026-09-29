// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var prompt_pb = require('./prompt_pb.js');
var models_pb = require('./models_pb.js');

function serialize_azd_extensions_v1_ConfirmRequest(arg) {
  if (!(arg instanceof prompt_pb.ConfirmRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.ConfirmRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_ConfirmRequest(buffer_arg) {
  return prompt_pb.ConfirmRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_ConfirmResponse(arg) {
  if (!(arg instanceof prompt_pb.ConfirmResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.ConfirmResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_ConfirmResponse(buffer_arg) {
  return prompt_pb.ConfirmResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_MultiSelectRequest(arg) {
  if (!(arg instanceof prompt_pb.MultiSelectRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.MultiSelectRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_MultiSelectRequest(buffer_arg) {
  return prompt_pb.MultiSelectRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_MultiSelectResponse(arg) {
  if (!(arg instanceof prompt_pb.MultiSelectResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.MultiSelectResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_MultiSelectResponse(buffer_arg) {
  return prompt_pb.MultiSelectResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_PromptLocationRequest(arg) {
  if (!(arg instanceof prompt_pb.PromptLocationRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.PromptLocationRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_PromptLocationRequest(buffer_arg) {
  return prompt_pb.PromptLocationRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_PromptLocationResponse(arg) {
  if (!(arg instanceof prompt_pb.PromptLocationResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.PromptLocationResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_PromptLocationResponse(buffer_arg) {
  return prompt_pb.PromptLocationResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_PromptRequest(arg) {
  if (!(arg instanceof prompt_pb.PromptRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.PromptRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_PromptRequest(buffer_arg) {
  return prompt_pb.PromptRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_PromptResourceGroupRequest(arg) {
  if (!(arg instanceof prompt_pb.PromptResourceGroupRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.PromptResourceGroupRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_PromptResourceGroupRequest(buffer_arg) {
  return prompt_pb.PromptResourceGroupRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_PromptResourceGroupResourceRequest(arg) {
  if (!(arg instanceof prompt_pb.PromptResourceGroupResourceRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.PromptResourceGroupResourceRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_PromptResourceGroupResourceRequest(buffer_arg) {
  return prompt_pb.PromptResourceGroupResourceRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_PromptResourceGroupResourceResponse(arg) {
  if (!(arg instanceof prompt_pb.PromptResourceGroupResourceResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.PromptResourceGroupResourceResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_PromptResourceGroupResourceResponse(buffer_arg) {
  return prompt_pb.PromptResourceGroupResourceResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_PromptResourceGroupResponse(arg) {
  if (!(arg instanceof prompt_pb.PromptResourceGroupResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.PromptResourceGroupResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_PromptResourceGroupResponse(buffer_arg) {
  return prompt_pb.PromptResourceGroupResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_PromptResponse(arg) {
  if (!(arg instanceof prompt_pb.PromptResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.PromptResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_PromptResponse(buffer_arg) {
  return prompt_pb.PromptResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_PromptSubscriptionRequest(arg) {
  if (!(arg instanceof prompt_pb.PromptSubscriptionRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.PromptSubscriptionRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_PromptSubscriptionRequest(buffer_arg) {
  return prompt_pb.PromptSubscriptionRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_PromptSubscriptionResourceRequest(arg) {
  if (!(arg instanceof prompt_pb.PromptSubscriptionResourceRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.PromptSubscriptionResourceRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_PromptSubscriptionResourceRequest(buffer_arg) {
  return prompt_pb.PromptSubscriptionResourceRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_PromptSubscriptionResourceResponse(arg) {
  if (!(arg instanceof prompt_pb.PromptSubscriptionResourceResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.PromptSubscriptionResourceResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_PromptSubscriptionResourceResponse(buffer_arg) {
  return prompt_pb.PromptSubscriptionResourceResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_PromptSubscriptionResponse(arg) {
  if (!(arg instanceof prompt_pb.PromptSubscriptionResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.PromptSubscriptionResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_PromptSubscriptionResponse(buffer_arg) {
  return prompt_pb.PromptSubscriptionResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_SelectRequest(arg) {
  if (!(arg instanceof prompt_pb.SelectRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1.SelectRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_SelectRequest(buffer_arg) {
  return prompt_pb.SelectRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1_SelectResponse(arg) {
  if (!(arg instanceof prompt_pb.SelectResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1.SelectResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_SelectResponse(buffer_arg) {
  return prompt_pb.SelectResponse.deserializeBinary(new Uint8Array(buffer_arg));
}


var PromptServiceService = exports.PromptServiceService = {
  // PromptSubscription prompts the user to select a subscription.
promptSubscription: {
    path: '/azd.extensions.v1.PromptService/PromptSubscription',
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.PromptSubscriptionRequest,
    responseType: prompt_pb.PromptSubscriptionResponse,
    requestSerialize: serialize_azd_extensions_v1_PromptSubscriptionRequest,
    requestDeserialize: deserialize_azd_extensions_v1_PromptSubscriptionRequest,
    responseSerialize: serialize_azd_extensions_v1_PromptSubscriptionResponse,
    responseDeserialize: deserialize_azd_extensions_v1_PromptSubscriptionResponse,
  },
  // PromptLocation prompts the user to select a location.
promptLocation: {
    path: '/azd.extensions.v1.PromptService/PromptLocation',
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.PromptLocationRequest,
    responseType: prompt_pb.PromptLocationResponse,
    requestSerialize: serialize_azd_extensions_v1_PromptLocationRequest,
    requestDeserialize: deserialize_azd_extensions_v1_PromptLocationRequest,
    responseSerialize: serialize_azd_extensions_v1_PromptLocationResponse,
    responseDeserialize: deserialize_azd_extensions_v1_PromptLocationResponse,
  },
  // PromptResourceGroup prompts the user to select a resource group.
promptResourceGroup: {
    path: '/azd.extensions.v1.PromptService/PromptResourceGroup',
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.PromptResourceGroupRequest,
    responseType: prompt_pb.PromptResourceGroupResponse,
    requestSerialize: serialize_azd_extensions_v1_PromptResourceGroupRequest,
    requestDeserialize: deserialize_azd_extensions_v1_PromptResourceGroupRequest,
    responseSerialize: serialize_azd_extensions_v1_PromptResourceGroupResponse,
    responseDeserialize: deserialize_azd_extensions_v1_PromptResourceGroupResponse,
  },
  // Confirm prompts the user to confirm an action.
confirm: {
    path: '/azd.extensions.v1.PromptService/Confirm',
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.ConfirmRequest,
    responseType: prompt_pb.ConfirmResponse,
    requestSerialize: serialize_azd_extensions_v1_ConfirmRequest,
    requestDeserialize: deserialize_azd_extensions_v1_ConfirmRequest,
    responseSerialize: serialize_azd_extensions_v1_ConfirmResponse,
    responseDeserialize: deserialize_azd_extensions_v1_ConfirmResponse,
  },
  // Prompt prompts the user for text input.
prompt: {
    path: '/azd.extensions.v1.PromptService/Prompt',
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.PromptRequest,
    responseType: prompt_pb.PromptResponse,
    requestSerialize: serialize_azd_extensions_v1_PromptRequest,
    requestDeserialize: deserialize_azd_extensions_v1_PromptRequest,
    responseSerialize: serialize_azd_extensions_v1_PromptResponse,
    responseDeserialize: deserialize_azd_extensions_v1_PromptResponse,
  },
  // Select prompts the user to select an option from a list.
select: {
    path: '/azd.extensions.v1.PromptService/Select',
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.SelectRequest,
    responseType: prompt_pb.SelectResponse,
    requestSerialize: serialize_azd_extensions_v1_SelectRequest,
    requestDeserialize: deserialize_azd_extensions_v1_SelectRequest,
    responseSerialize: serialize_azd_extensions_v1_SelectResponse,
    responseDeserialize: deserialize_azd_extensions_v1_SelectResponse,
  },
  // MultiSelect prompts the user to select multiple options from a list.
multiSelect: {
    path: '/azd.extensions.v1.PromptService/MultiSelect',
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.MultiSelectRequest,
    responseType: prompt_pb.MultiSelectResponse,
    requestSerialize: serialize_azd_extensions_v1_MultiSelectRequest,
    requestDeserialize: deserialize_azd_extensions_v1_MultiSelectRequest,
    responseSerialize: serialize_azd_extensions_v1_MultiSelectResponse,
    responseDeserialize: deserialize_azd_extensions_v1_MultiSelectResponse,
  },
  // PromptSubscriptionResource prompts the user to select a resource from a subscription.
promptSubscriptionResource: {
    path: '/azd.extensions.v1.PromptService/PromptSubscriptionResource',
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.PromptSubscriptionResourceRequest,
    responseType: prompt_pb.PromptSubscriptionResourceResponse,
    requestSerialize: serialize_azd_extensions_v1_PromptSubscriptionResourceRequest,
    requestDeserialize: deserialize_azd_extensions_v1_PromptSubscriptionResourceRequest,
    responseSerialize: serialize_azd_extensions_v1_PromptSubscriptionResourceResponse,
    responseDeserialize: deserialize_azd_extensions_v1_PromptSubscriptionResourceResponse,
  },
  // PromptResourceGroupResource prompts the user to select a resource from a resource group.
promptResourceGroupResource: {
    path: '/azd.extensions.v1.PromptService/PromptResourceGroupResource',
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.PromptResourceGroupResourceRequest,
    responseType: prompt_pb.PromptResourceGroupResourceResponse,
    requestSerialize: serialize_azd_extensions_v1_PromptResourceGroupResourceRequest,
    requestDeserialize: deserialize_azd_extensions_v1_PromptResourceGroupResourceRequest,
    responseSerialize: serialize_azd_extensions_v1_PromptResourceGroupResourceResponse,
    responseDeserialize: deserialize_azd_extensions_v1_PromptResourceGroupResourceResponse,
  },
};

exports.PromptServiceClient = grpc.makeGenericClientConstructor(PromptServiceService, 'PromptService');
