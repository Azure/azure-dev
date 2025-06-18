// source: prompt.proto
// GENERATED CODE -- DO NOT EDIT!
/* eslint-disable */
// @ts-nocheck
"use strict";

const grpc = require("@grpc/grpc-js");
const prompt_pb = require("./prompt_pb.js");
const models_pb = require("./models_pb.js");

function serialize_azdext_PromptSubscriptionRequest(arg) {
  if (!arg instanceof prompt_pb.PromptSubscriptionRequest) {
    throw new Error("Expected argument of type azdext.PromptSubscriptionRequest");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_PromptSubscriptionRequest(arg) {
  return prompt_pb.PromptSubscriptionRequest.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_PromptSubscriptionResponse(arg) {
  if (!arg instanceof prompt_pb.PromptSubscriptionResponse) {
    throw new Error("Expected argument of type azdext.PromptSubscriptionResponse");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_PromptSubscriptionResponse(arg) {
  return prompt_pb.PromptSubscriptionResponse.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_PromptLocationRequest(arg) {
  if (!arg instanceof prompt_pb.PromptLocationRequest) {
    throw new Error("Expected argument of type azdext.PromptLocationRequest");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_PromptLocationRequest(arg) {
  return prompt_pb.PromptLocationRequest.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_PromptLocationResponse(arg) {
  if (!arg instanceof prompt_pb.PromptLocationResponse) {
    throw new Error("Expected argument of type azdext.PromptLocationResponse");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_PromptLocationResponse(arg) {
  return prompt_pb.PromptLocationResponse.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_PromptResourceGroupRequest(arg) {
  if (!arg instanceof prompt_pb.PromptResourceGroupRequest) {
    throw new Error("Expected argument of type azdext.PromptResourceGroupRequest");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_PromptResourceGroupRequest(arg) {
  return prompt_pb.PromptResourceGroupRequest.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_PromptResourceGroupResponse(arg) {
  if (!arg instanceof prompt_pb.PromptResourceGroupResponse) {
    throw new Error("Expected argument of type azdext.PromptResourceGroupResponse");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_PromptResourceGroupResponse(arg) {
  return prompt_pb.PromptResourceGroupResponse.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_ConfirmRequest(arg) {
  if (!arg instanceof prompt_pb.ConfirmRequest) {
    throw new Error("Expected argument of type azdext.ConfirmRequest");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_ConfirmRequest(arg) {
  return prompt_pb.ConfirmRequest.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_ConfirmResponse(arg) {
  if (!arg instanceof prompt_pb.ConfirmResponse) {
    throw new Error("Expected argument of type azdext.ConfirmResponse");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_ConfirmResponse(arg) {
  return prompt_pb.ConfirmResponse.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_PromptRequest(arg) {
  if (!arg instanceof prompt_pb.PromptRequest) {
    throw new Error("Expected argument of type azdext.PromptRequest");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_PromptRequest(arg) {
  return prompt_pb.PromptRequest.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_PromptResponse(arg) {
  if (!arg instanceof prompt_pb.PromptResponse) {
    throw new Error("Expected argument of type azdext.PromptResponse");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_PromptResponse(arg) {
  return prompt_pb.PromptResponse.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_SelectRequest(arg) {
  if (!arg instanceof prompt_pb.SelectRequest) {
    throw new Error("Expected argument of type azdext.SelectRequest");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_SelectRequest(arg) {
  return prompt_pb.SelectRequest.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_SelectResponse(arg) {
  if (!arg instanceof prompt_pb.SelectResponse) {
    throw new Error("Expected argument of type azdext.SelectResponse");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_SelectResponse(arg) {
  return prompt_pb.SelectResponse.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_MultiSelectRequest(arg) {
  if (!arg instanceof prompt_pb.MultiSelectRequest) {
    throw new Error("Expected argument of type azdext.MultiSelectRequest");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_MultiSelectRequest(arg) {
  return prompt_pb.MultiSelectRequest.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_MultiSelectResponse(arg) {
  if (!arg instanceof prompt_pb.MultiSelectResponse) {
    throw new Error("Expected argument of type azdext.MultiSelectResponse");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_MultiSelectResponse(arg) {
  return prompt_pb.MultiSelectResponse.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_PromptSubscriptionResourceRequest(arg) {
  if (!arg instanceof prompt_pb.PromptSubscriptionResourceRequest) {
    throw new Error("Expected argument of type azdext.PromptSubscriptionResourceRequest");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_PromptSubscriptionResourceRequest(arg) {
  return prompt_pb.PromptSubscriptionResourceRequest.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_PromptSubscriptionResourceResponse(arg) {
  if (!arg instanceof prompt_pb.PromptSubscriptionResourceResponse) {
    throw new Error("Expected argument of type azdext.PromptSubscriptionResourceResponse");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_PromptSubscriptionResourceResponse(arg) {
  return prompt_pb.PromptSubscriptionResourceResponse.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_PromptResourceGroupResourceRequest(arg) {
  if (!arg instanceof prompt_pb.PromptResourceGroupResourceRequest) {
    throw new Error("Expected argument of type azdext.PromptResourceGroupResourceRequest");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_PromptResourceGroupResourceRequest(arg) {
  return prompt_pb.PromptResourceGroupResourceRequest.deserializeBinary(new Uint8Array(arg));
}

function serialize_azdext_PromptResourceGroupResourceResponse(arg) {
  if (!arg instanceof prompt_pb.PromptResourceGroupResourceResponse) {
    throw new Error("Expected argument of type azdext.PromptResourceGroupResourceResponse");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_PromptResourceGroupResourceResponse(arg) {
  return prompt_pb.PromptResourceGroupResourceResponse.deserializeBinary(new Uint8Array(arg));
}

const PromptServiceService = exports.PromptServiceService = {
  promptSubscription: {
    path: "/azdext.PromptService/PromptSubscription",
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.PromptSubscriptionRequest,
    responseType: prompt_pb.PromptSubscriptionResponse,
    requestSerialize: serialize_azdext_PromptSubscriptionRequest,
    requestDeserialize: deserialize_azdext_PromptSubscriptionRequest,
    responseSerialize: serialize_azdext_PromptSubscriptionResponse,
    responseDeserialize: deserialize_azdext_PromptSubscriptionResponse
  },
  promptLocation: {
    path: "/azdext.PromptService/PromptLocation",
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.PromptLocationRequest,
    responseType: prompt_pb.PromptLocationResponse,
    requestSerialize: serialize_azdext_PromptLocationRequest,
    requestDeserialize: deserialize_azdext_PromptLocationRequest,
    responseSerialize: serialize_azdext_PromptLocationResponse,
    responseDeserialize: deserialize_azdext_PromptLocationResponse
  },
  promptResourceGroup: {
    path: "/azdext.PromptService/PromptResourceGroup",
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.PromptResourceGroupRequest,
    responseType: prompt_pb.PromptResourceGroupResponse,
    requestSerialize: serialize_azdext_PromptResourceGroupRequest,
    requestDeserialize: deserialize_azdext_PromptResourceGroupRequest,
    responseSerialize: serialize_azdext_PromptResourceGroupResponse,
    responseDeserialize: deserialize_azdext_PromptResourceGroupResponse
  },
  confirm: {
    path: "/azdext.PromptService/Confirm",
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.ConfirmRequest,
    responseType: prompt_pb.ConfirmResponse,
    requestSerialize: serialize_azdext_ConfirmRequest,
    requestDeserialize: deserialize_azdext_ConfirmRequest,
    responseSerialize: serialize_azdext_ConfirmResponse,
    responseDeserialize: deserialize_azdext_ConfirmResponse
  },
  prompt: {
    path: "/azdext.PromptService/Prompt",
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.PromptRequest,
    responseType: prompt_pb.PromptResponse,
    requestSerialize: serialize_azdext_PromptRequest,
    requestDeserialize: deserialize_azdext_PromptRequest,
    responseSerialize: serialize_azdext_PromptResponse,
    responseDeserialize: deserialize_azdext_PromptResponse
  },
  select: {
    path: "/azdext.PromptService/Select",
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.SelectRequest,
    responseType: prompt_pb.SelectResponse,
    requestSerialize: serialize_azdext_SelectRequest,
    requestDeserialize: deserialize_azdext_SelectRequest,
    responseSerialize: serialize_azdext_SelectResponse,
    responseDeserialize: deserialize_azdext_SelectResponse
  },
  multiSelect: {
    path: "/azdext.PromptService/MultiSelect",
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.MultiSelectRequest,
    responseType: prompt_pb.MultiSelectResponse,
    requestSerialize: serialize_azdext_MultiSelectRequest,
    requestDeserialize: deserialize_azdext_MultiSelectRequest,
    responseSerialize: serialize_azdext_MultiSelectResponse,
    responseDeserialize: deserialize_azdext_MultiSelectResponse
  },
  promptSubscriptionResource: {
    path: "/azdext.PromptService/PromptSubscriptionResource",
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.PromptSubscriptionResourceRequest,
    responseType: prompt_pb.PromptSubscriptionResourceResponse,
    requestSerialize: serialize_azdext_PromptSubscriptionResourceRequest,
    requestDeserialize: deserialize_azdext_PromptSubscriptionResourceRequest,
    responseSerialize: serialize_azdext_PromptSubscriptionResourceResponse,
    responseDeserialize: deserialize_azdext_PromptSubscriptionResourceResponse
  },
  promptResourceGroupResource: {
    path: "/azdext.PromptService/PromptResourceGroupResource",
    requestStream: false,
    responseStream: false,
    requestType: prompt_pb.PromptResourceGroupResourceRequest,
    responseType: prompt_pb.PromptResourceGroupResourceResponse,
    requestSerialize: serialize_azdext_PromptResourceGroupResourceRequest,
    requestDeserialize: deserialize_azdext_PromptResourceGroupResourceRequest,
    responseSerialize: serialize_azdext_PromptResourceGroupResourceResponse,
    responseDeserialize: deserialize_azdext_PromptResourceGroupResourceResponse
  },
};

exports.PromptServiceClient = grpc.makeGenericClientConstructor(PromptServiceService);