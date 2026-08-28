// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var event_pb = require('./event_pb.js');
var models_pb = require('./models_pb.js');

function serialize_azd_extensions_v1_EventMessage(arg) {
  if (!(arg instanceof event_pb.EventMessage)) {
    throw new Error('Expected argument of type azd.extensions.v1.EventMessage');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azd_extensions_v1_EventMessage(buffer_arg) {
  return event_pb.EventMessage.deserializeBinary(new Uint8Array(buffer_arg));
}


// EventService defines methods for event subscription, invocation, and status updates.
// Clients can subscribe to events and receive notifications via a bidirectional stream.
var EventServiceService = exports.EventServiceService = {
  // Bidirectional stream for event subscription, invocation, and status updates.
eventStream: {
    path: '/azd.extensions.v1.EventService/EventStream',
    requestStream: true,
    responseStream: true,
    requestType: event_pb.EventMessage,
    responseType: event_pb.EventMessage,
    requestSerialize: serialize_azd_extensions_v1_EventMessage,
    requestDeserialize: deserialize_azd_extensions_v1_EventMessage,
    responseSerialize: serialize_azd_extensions_v1_EventMessage,
    responseDeserialize: deserialize_azd_extensions_v1_EventMessage,
  },
};

exports.EventServiceClient = grpc.makeGenericClientConstructor(EventServiceService, 'EventService');
