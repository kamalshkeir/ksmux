
typedef VoidCallback = void Function();

/// WebSocket message structure
class WsMessage {
  final String action;
  final String? topic;
  final dynamic data;
  final String? from;
  final String? to;
  final String? ackId;
  final Map<String, bool>? status;
  final Map<String, dynamic>? responses;

  const WsMessage({
    required this.action,
    this.topic,
    this.data,
    this.from,
    this.to,
    this.ackId,
    this.status,
    this.responses,
  });

  factory WsMessage.fromJson(Map<String, dynamic> json) {
    return WsMessage(
      action: json['action'] as String,
      topic: json['topic'] as String?,
      data: json['data'],
      from: json['from'] as String?,
      to: json['to'] as String?,
      ackId: json['ack_id'] as String?,
      status: json['status'] != null ? Map<String, bool>.from(json['status'] as Map) : null,
      responses: json['responses'] != null ? Map<String, dynamic>.from(json['responses'] as Map) : null,
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'action': action,
      if (topic != null) 'topic': topic,
      if (data != null) 'data': data,
      if (from != null) 'from': from,
      if (to != null) 'to': to,
      if (ackId != null) 'ack_id': ackId,
      if (status != null) 'status': status,
      if (responses != null) 'responses': responses,
    };
  }
}

/// ACK response structure
class AckResponse {
  final String ackId;
  final String clientId;
  final bool success;
  final String? error;

  const AckResponse({
    required this.ackId,
    required this.clientId,
    required this.success,
    this.error,
  });

  factory AckResponse.fromJson(Map<String, dynamic> json) {
    return AckResponse(
      ackId: json['ack_id'] as String,
      clientId: json['client_id'] as String,
      success: json['success'] as bool,
      error: json['error'] as String?,
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'ack_id': ackId,
      'client_id': clientId,
      'success': success,
      if (error != null) 'error': error,
    };
  }
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
  final Function(Map<String, dynamic> data)? onId;
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