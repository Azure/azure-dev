// source: event.proto
// GENERATED CODE -- DO NOT EDIT!
/* eslint-disable */
// @ts-nocheck
"use strict";

const grpc = require("@grpc/grpc-js");
const event_pb = require("./event_pb.js");
const models_pb = require("./models_pb.js");

function serialize_azdext_EventMessage(arg) {
  if (!arg instanceof event_pb.EventMessage) {
    throw new Error("Expected argument of type azdext.EventMessage");
  }

  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_EventMessage(arg) {
  return event_pb.EventMessage.deserializeBinary(new Uint8Array(arg));
}

const EventServiceService = exports.EventServiceService = {
  eventStream: {
    path: "/azdext.EventService/EventStream",
    requestStream: true,
    responseStream: true,
    requestType: event_pb.EventMessage,
    responseType: event_pb.EventMessage,
    requestSerialize: serialize_azdext_EventMessage,
    requestDeserialize: deserialize_azdext_EventMessage,
    responseSerialize: serialize_azdext_EventMessage,
    responseDeserialize: deserialize_azdext_EventMessage
  },
};

exports.EventServiceClient = grpc.makeGenericClientConstructor(EventServiceService);