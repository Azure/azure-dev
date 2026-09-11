// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var event_pb = require('./event_pb.js');
var models_pb = require('./models_pb.js');
var errors_pb = require('./errors_pb.js');

function serialize_azdext_EventMessage(arg) {
  if (!(arg instanceof event_pb.EventMessage)) {
    throw new Error('Expected argument of type azdext.EventMessage');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_azdext_EventMessage(buffer_arg) {
  return event_pb.EventMessage.deserializeBinary(new Uint8Array(buffer_arg));
}


// EventService defines methods for event subscription, invocation, and status updates.
// Clients can subscribe to events and receive notifications via a bidirectional stream.
var EventServiceService = exports.EventServiceService = {
  // Bidirectional stream for event subscription, invocation, and status updates.
eventStream: {
    path: '/azdext.EventService/EventStream',
    requestStream: true,
    responseStream: true,
    requestType: event_pb.EventMessage,
    responseType: event_pb.EventMessage,
    requestSerialize: serialize_azdext_EventMessage,
    requestDeserialize: deserialize_azdext_EventMessage,
    responseSerialize: serialize_azdext_EventMessage,
    responseDeserialize: deserialize_azdext_EventMessage,
  },
};

exports.EventServiceClient = grpc.makeGenericClientConstructor(EventServiceService, 'EventService');
