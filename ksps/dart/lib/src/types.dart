import 'dart:async';
import 'package:json_annotation/json_annotation.dart';

part 'types.g.dart';

/// WebSocket message structure
@JsonSerializable()
class WsMessage {
  final String action;
  final String? topic;
  final dynamic data;
  final String? from;
  final String? to;
  @JsonKey(name: 'ack_id')
  final String? ackId;

  const WsMessage({
    required this.action,
    this.topic,
    this.data,
    this.from,
    this.to,
    this.ackId,
  });

  factory WsMessage.fromJson(Map<String, dynamic> json) =>
      _$WsMessageFromJson(json);

  Map<String, dynamic> toJson() => _$WsMessageToJson(this);
}

/// ACK response structure
@JsonSerializable()
class AckResponse {
  @JsonKey(name: 'ack_id')
  final String ackId;
  @JsonKey(name: 'client_id')
  final String clientId;
  final bool success;
  final String? error;

  const AckResponse({
    required this.ackId,
    required this.clientId,
    required this.success,
    this.error,
  });

  factory AckResponse.fromJson(Map<String, dynamic> json) =>
      _$AckResponseFromJson(json);

  Map<String, dynamic> toJson() => _$AckResponseToJson(this);
}

/// Client connection options
class ClientConnectOptions {
  final String id;
  final String address;
  final bool secure;
  final String path;
  final bool autorestart;
  final Duration restartEvery;
  final Function(Map<String, dynamic> data)? onDataWs;
  final Function(Map<String, dynamic> data, ClientSubscriber unsub)? onId;
  final VoidCallback? onClose;

  const ClientConnectOptions({
    required this.id,
    required this.address,
    this.secure = false,
    this.path = '/ws/bus',
    this.autorestart = false,
    this.restartEvery = const Duration(seconds: 10),
    this.onDataWs,
    this.onId,
    this.onClose,
  });
}

/// Subscription callback type
typedef SubscriptionCallback = void Function(dynamic data, VoidCallback unsub);

/// Client subscription data
class ClientSubscription {
  final String topic;
  final SubscriptionCallback callback;
  bool active;

  ClientSubscription({
    required this.topic,
    required this.callback,
    this.active = true,
  });
}

/// Forward declaration for ClientSubscriber
class ClientSubscriber {
  final String id;
  final String topic;
  
  ClientSubscriber({
    required this.id,
    required this.topic,
  });
  
  void unsubscribe() {
    // Implementation will be added in client.dart
  }
} 