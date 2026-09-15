// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var prompt_pb = require('./prompt_pb.js');
var models_pb = require('./models_pb.js');

function serialize_azd_extensions_v1beta_ConfirmRequest(arg) {
  if (!(arg instanceof prompt_pb.ConfirmRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.ConfirmRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_ConfirmRequest(buffer_arg) {
  return prompt_pb.ConfirmRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_ConfirmResponse(arg) {
  if (!(arg instanceof prompt_pb.ConfirmResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.ConfirmResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_ConfirmResponse(buffer_arg) {
  return prompt_pb.ConfirmResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_MultiSelectRequest(arg) {
  if (!(arg instanceof prompt_pb.MultiSelectRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.MultiSelectRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_MultiSelectRequest(buffer_arg) {
  return prompt_pb.MultiSelectRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_MultiSelectResponse(arg) {
  if (!(arg instanceof prompt_pb.MultiSelectResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.MultiSelectResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_MultiSelectResponse(buffer_arg) {
  return prompt_pb.MultiSelectResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_PromptLocationRequest(arg) {
  if (!(arg instanceof prompt_pb.PromptLocationRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.PromptLocationRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_PromptLocationRequest(buffer_arg) {
  return prompt_pb.PromptLocationRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_PromptLocationResponse(arg) {
  if (!(arg instanceof prompt_pb.PromptLocationResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.PromptLocationResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_PromptLocationResponse(buffer_arg) {
  return prompt_pb.PromptLocationResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_PromptRequest(arg) {
  if (!(arg instanceof prompt_pb.PromptRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.PromptRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_PromptRequest(buffer_arg) {
  return prompt_pb.PromptRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_PromptResourceGroupRequest(arg) {
  if (!(arg instanceof prompt_pb.PromptResourceGroupRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.PromptResourceGroupRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_PromptResourceGroupRequest(buffer_arg) {
  return prompt_pb.PromptResourceGroupRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_PromptResourceGroupResourceRequest(arg) {
  if (!(arg instanceof prompt_pb.PromptResourceGroupResourceRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.PromptResourceGroupResourceRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_PromptResourceGroupResourceRequest(buffer_arg) {
  return prompt_pb.PromptResourceGroupResourceRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_PromptResourceGroupResourceResponse(arg) {
  if (!(arg instanceof prompt_pb.PromptResourceGroupResourceResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.PromptResourceGroupResourceResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_PromptResourceGroupResourceResponse(buffer_arg) {
  return prompt_pb.PromptResourceGroupResourceResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_PromptResourceGroupResponse(arg) {
  if (!(arg instanceof prompt_pb.PromptResourceGroupResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.PromptResourceGroupResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_PromptResourceGroupResponse(buffer_arg) {
  return prompt_pb.PromptResourceGroupResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_PromptResponse(arg) {
  if (!(arg instanceof prompt_pb.PromptResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.PromptResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_PromptResponse(buffer_arg) {
  return prompt_pb.PromptResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_PromptSubscriptionRequest(arg) {
  if (!(arg instanceof prompt_pb.PromptSubscriptionRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.PromptSubscriptionRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_PromptSubscriptionRequest(buffer_arg) {
  return prompt_pb.PromptSubscriptionRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_PromptSubscriptionResourceRequest(arg) {
  if (!(arg instanceof prompt_pb.PromptSubscriptionResourceRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.PromptSubscriptionResourceRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_PromptSubscriptionResourceRequest(buffer_arg) {
  return prompt_pb.PromptSubscriptionResourceRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_PromptSubscriptionResourceResponse(arg) {
  if (!(arg instanceof prompt_pb.PromptSubscriptionResourceResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.PromptSubscriptionResourceResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_PromptSubscriptionResourceResponse(buffer_arg) {
  return prompt_pb.PromptSubscriptionResourceResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_PromptSubscriptionResponse(arg) {
  if (!(arg instanceof prompt_pb.PromptSubscriptionResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.PromptSubscriptionResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_PromptSubscriptionResponse(buffer_arg) {
  return prompt_pb.PromptSubscriptionResponse.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_SelectRequest(arg) {
  if (!(arg instanceof prompt_pb.SelectRequest)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.SelectRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_SelectRequest(buffer_arg) {
  return prompt_pb.SelectRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_azd_extensions_v1beta_SelectResponse(arg) {
  if (!(arg instanceof prompt_pb.SelectResponse)) {
    throw new Error('Expected argument of type azd.extensions.v1beta.SelectResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1beta_SelectResponse(buffer_arg) {
  return prompt_pb.SelectResponse.deserializeBinary(new Uint8Array(buffer_arg));
}


var PromptServiceService = exports.PromptServiceService = {
  // PromptSubscription prompts the user to select a subscription.
promptSubscription: {
    path: '/azd.extensions.v1beta.PromptService/PromptSubscription',
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.PromptSubscriptionRequest,
    responseType: prompt_pb.PromptSubscriptionResponse,
    requestSerialize: serialize_azd_extensions_v1beta_PromptSubscriptionRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_PromptSubscriptionRequest,
    responseSerialize: serialize_azd_extensions_v1beta_PromptSubscriptionResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_PromptSubscriptionResponse,
  },
  // PromptLocation prompts the user to select a location.
promptLocation: {
    path: '/azd.extensions.v1beta.PromptService/PromptLocation',
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.PromptLocationRequest,
    responseType: prompt_pb.PromptLocationResponse,
    requestSerialize: serialize_azd_extensions_v1beta_PromptLocationRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_PromptLocationRequest,
    responseSerialize: serialize_azd_extensions_v1beta_PromptLocationResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_PromptLocationResponse,
  },
  // PromptResourceGroup prompts the user to select a resource group.
promptResourceGroup: {
    path: '/azd.extensions.v1beta.PromptService/PromptResourceGroup',
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.PromptResourceGroupRequest,
    responseType: prompt_pb.PromptResourceGroupResponse,
    requestSerialize: serialize_azd_extensions_v1beta_PromptResourceGroupRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_PromptResourceGroupRequest,
    responseSerialize: serialize_azd_extensions_v1beta_PromptResourceGroupResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_PromptResourceGroupResponse,
  },
  // Confirm prompts the user to confirm an action.
confirm: {
    path: '/azd.extensions.v1beta.PromptService/Confirm',
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.ConfirmRequest,
    responseType: prompt_pb.ConfirmResponse,
    requestSerialize: serialize_azd_extensions_v1beta_ConfirmRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_ConfirmRequest,
    responseSerialize: serialize_azd_extensions_v1beta_ConfirmResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_ConfirmResponse,
  },
  // Prompt prompts the user for text input.
prompt: {
    path: '/azd.extensions.v1beta.PromptService/Prompt',
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.PromptRequest,
    responseType: prompt_pb.PromptResponse,
    requestSerialize: serialize_azd_extensions_v1beta_PromptRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_PromptRequest,
    responseSerialize: serialize_azd_extensions_v1beta_PromptResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_PromptResponse,
  },
  // Select prompts the user to select an option from a list.
select: {
    path: '/azd.extensions.v1beta.PromptService/Select',
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.SelectRequest,
    responseType: prompt_pb.SelectResponse,
    requestSerialize: serialize_azd_extensions_v1beta_SelectRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_SelectRequest,
    responseSerialize: serialize_azd_extensions_v1beta_SelectResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_SelectResponse,
  },
  // MultiSelect prompts the user to select multiple options from a list.
multiSelect: {
    path: '/azd.extensions.v1beta.PromptService/MultiSelect',
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.MultiSelectRequest,
    responseType: prompt_pb.MultiSelectResponse,
    requestSerialize: serialize_azd_extensions_v1beta_MultiSelectRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_MultiSelectRequest,
    responseSerialize: serialize_azd_extensions_v1beta_MultiSelectResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_MultiSelectResponse,
  },
  // PromptSubscriptionResource prompts the user to select a resource from a subscription.
promptSubscriptionResource: {
    path: '/azd.extensions.v1beta.PromptService/PromptSubscriptionResource',
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.PromptSubscriptionResourceRequest,
    responseType: prompt_pb.PromptSubscriptionResourceResponse,
    requestSerialize: serialize_azd_extensions_v1beta_PromptSubscriptionResourceRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_PromptSubscriptionResourceRequest,
    responseSerialize: serialize_azd_extensions_v1beta_PromptSubscriptionResourceResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_PromptSubscriptionResourceResponse,
  },
  // PromptResourceGroupResource prompts the user to select a resource from a resource group.
promptResourceGroupResource: {
    path: '/azd.extensions.v1beta.PromptService/PromptResourceGroupResource',
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.PromptResourceGroupResourceRequest,
    responseType: prompt_pb.PromptResourceGroupResourceResponse,
    requestSerialize: serialize_azd_extensions_v1beta_PromptResourceGroupResourceRequest,
    requestDeserialize: deserialize_azd_extensions_v1beta_PromptResourceGroupResourceRequest,
    responseSerialize: serialize_azd_extensions_v1beta_PromptResourceGroupResourceResponse,
    responseDeserialize: deserialize_azd_extensions_v1beta_PromptResourceGroupResourceResponse,
  },
};

exports.PromptServiceClient = grpc.makeGenericClientConstructor(PromptServiceService, 'PromptService');
