import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';
import 'package:web_socket_channel/web_socket_channel.dart';
import 'package:web_socket_channel/status.dart' as status;

import 'types.dart';
import 'ack.dart';
import 'subscriber.dart';

/// Ultra-fast Dart/Flutter client for ksmux pub/sub system
class KspsClient {
  late String _id;
  late String _serverAddr;
  late bool _secure;
  late String _path;
  late bool _autorestart;
  late Duration _restartEvery;
  
  Function(Map<String, dynamic> data)? _onDataWs;
  Function(Map<String, dynamic> data, ClientSubscriber unsub)? _onId;
  VoidCallback? _onClose;
  
  WebSocketChannel? _channel;
  bool _connected = false;
  bool _done = false;
  
  final Map<String, ClientSubscription> _subscriptions = {};
  final Map<String, ClientAck> _ackRequests = {};
  final StreamController<WsMessage> _messageQueue = StreamController<WsMessage>();
  
  int _nextAckId = 1;
  
  KspsClient._();

  /// Create a new client with connection options
  static Future<KspsClient> connect(ClientConnectOptions options) async {
    final client = KspsClient._();
    await client._initialize(options);
    return client;
  }

  /// Initialize the client with options
  Future<void> _initialize(ClientConnectOptions options) async {
    _id = options.id.isNotEmpty ? options.id : _generateId();
    _serverAddr = options.address;
    _secure = options.secure;
    _path = options.path;
    _autorestart = options.autorestart;
    _restartEvery = options.restartEvery;
    _onDataWs = options.onDataWs;
    _onId = options.onId;
    _onClose = options.onClose;
    
    await _connect();
  }

  /// Connect to the WebSocket server
  Future<void> _connect() async {
    try {
      final scheme = _secure ? 'wss' : 'ws';
      final uri = Uri.parse('$scheme://$_serverAddr$_path');
      
      _channel = WebSocketChannel.connect(uri);
      _connected = true;
      
      // Start message handlers
      _startMessageHandler();
      _startMessageSender();
      
      // Send initial ping
      _sendMessage(WsMessage(
        action: 'ping',
        from: _id,
      ));
      
      print('Client connected to $uri');
    } catch (e) {
      _connected = false;
      if (_autorestart) {
        print('Connection failed, retrying in ${_restartEvery.inSeconds} seconds');
        await Future.delayed(_restartEvery);
        await _connect();
      } else {
        rethrow;
      }
    }
  }

  /// Start the message handler
  void _startMessageHandler() {
    _channel?.stream.listen(
      (data) {
        if (_done || !_connected) return;
        
        try {
          final json = jsonDecode(data as String) as Map<String, dynamic>;
          _handleMessage(json);
        } catch (e) {
          print('WebSocket read error: $e');
          _connected = false;
          if (_autorestart) {
            _reconnect();
          }
        }
      },
      onError: (error) {
        print('WebSocket error: $error');
        _connected = false;
        if (_autorestart) {
          _reconnect();
        }
      },
      onDone: () {
        _connected = false;
        if (_autorestart && !_done) {
          _reconnect();
        }
      },
    );
  }

  /// Start the message sender
  void _startMessageSender() {
    _messageQueue.stream.listen((message) {
      if (!_connected || _channel == null) return;
      
      try {
        _channel!.sink.add(jsonEncode(message.toJson()));
      } catch (e) {
        print('WebSocket write error: $e');
        _connected = false;
      }
    });
  }

  /// Handle incoming messages
  void _handleMessage(Map<String, dynamic> data) {
    final action = data['action'] as String?;
    if (action == null) return;

    switch (action) {
      case 'pong':
        // Connection confirmed
        if (_onId != null) {
          _onId!(data, ClientSubscriber(
            id: _id,
            topic: '',
            unsubscribeCallback: () {},
          ));
        }
        break;

      case 'publish':
        _handlePublishMessage(data);
        break;

      case 'publish_ack':
        _handlePublishAckMessage(data);
        break;

      case 'direct_message':
        if (_onId != null) {
          final payload = data['data'];
          _onId!({'data': payload}, ClientSubscriber(
            id: _id,
            topic: '',
            unsubscribeCallback: () {},
          ));
        }
        break;

      case 'ack_response':
        _handleAckResponse(data);
        break;

      case 'ack_status':
        _handleAckStatus(data);
        break;

      case 'ack_cancelled':
        _handleAckCancelled(data);
        break;

      case 'subscribed':
      case 'unsubscribed':
      case 'published':
        // Confirmations - can be logged
        print('Received confirmation: $action');
        break;

      case 'error':
        final error = data['error'] as String?;
        if (error != null) {
          print('Server error: $error');
        }
        break;
    }

    // User callback for all messages
    _onDataWs?.call(data);
  }

  /// Handle publish messages
  void _handlePublishMessage(Map<String, dynamic> data) {
    final topic = data['topic'] as String?;
    if (topic == null) return;

    final payload = data['data'];
    final subscription = _subscriptions[topic];

    if (subscription != null && subscription.active) {
      // Create unsubscribe function
      void unsubFn() => unsubscribe(topic);
      
      // Execute callback
      Future.microtask(() => subscription.callback(payload, unsubFn));
    }
  }

  /// Handle publish ACK messages
  void _handlePublishAckMessage(Map<String, dynamic> data) {
    final topic = data['topic'] as String?;
    if (topic == null) return;

    final payload = data['data'];
    final ackId = data['ack_id'] as String?;
    final subscription = _subscriptions[topic];

    if (subscription != null && subscription.active) {
      void unsubFn() => unsubscribe(topic);
      
      Future.microtask(() async {
        bool success = true;
        String? errorMsg;

        try {
          subscription.callback(payload, unsubFn);
        } catch (e) {
          success = false;
          errorMsg = e.toString();
        }

        // Send ACK after processing
        if (ackId != null) {
          _sendAck(ackId, success, errorMsg);
        }
      });
    } else if (ackId != null) {
      // No subscriber, send error ACK
      _sendAck(ackId, false, 'no subscriber for topic');
    }
  }

  /// Send acknowledgment
  void _sendAck(String ackId, bool success, String? errorMsg) {
    final ackData = <String, dynamic>{
      'ack_id': ackId,
      'client_id': _id,
      'success': success,
    };
    
    if (errorMsg != null) {
      ackData['error'] = errorMsg;
    }

    _sendMessage(WsMessage(
      action: 'ack',
      ackId: ackId,
      from: _id,
      data: ackData,
    ));
  }

  /// Handle ACK responses
  void _handleAckResponse(Map<String, dynamic> data) {
    // Support optimized protocol (flat) or legacy (nested in data)
    final ackId = data['ack_id'] as String? ?? (data['data'] is Map ? data['data']['ack_id'] as String? : null);
    if (ackId == null) return;

    final clientAck = _ackRequests[ackId];
    if (clientAck == null || clientAck.isCancelled) return;

    final responsesData = data['responses'] as Map<String, dynamic>? ?? 
                         (data['data'] is Map ? data['data']['responses'] as Map<String, dynamic>? : null);
    if (responsesData != null) {
      final responses = <String, AckResponse>{};
      
      for (final entry in responsesData.entries) {
        final respData = entry.value as Map<String, dynamic>?;
        if (respData != null) {
          responses[entry.key] = AckResponse.fromJson(respData);
        }
      }
      
      clientAck.handleResponse(responses);
    }
  }

  /// Handle ACK status
  void _handleAckStatus(Map<String, dynamic> data) {
    final ackId = data['ack_id'] as String? ?? (data['data'] is Map ? data['data']['ack_id'] as String? : null);
    if (ackId == null) return;

    final clientAck = _ackRequests[ackId];
    if (clientAck == null || clientAck.isCancelled) return;

    final statusData = data['status'] as Map<String, dynamic>? ?? 
                      (data['data'] is Map ? data['data']['status'] as Map<String, dynamic>? : null);
    if (statusData != null) {
      final status = <String, bool>{};
      for (final entry in statusData.entries) {
        final received = entry.value as bool?;
        if (received != null) {
          status[entry.key] = received;
        }
      }
      
      clientAck.handleStatus(status);
    }
  }

  /// Handle ACK cancellation
  void _handleAckCancelled(Map<String, dynamic> data) {
    final ackId = data['ack_id'] as String? ?? (data['data'] is Map ? data['data']['ack_id'] as String? : null);
    if (ackId == null) return;

    _ackRequests.remove(ackId);
  }

  /// Subscribe to a topic
  VoidCallback subscribe(String topic, SubscriptionCallback callback) {
    if (!_connected) {
      print('Cannot subscribe: client not connected');
      return () {};
    }

    // Create local subscription
    final subscription = ClientSubscription(
      topic: topic,
      callback: callback,
    );
    
    _subscriptions[topic] = subscription;

    // Send subscription request to server
    _sendMessage(WsMessage(
      action: 'subscribe',
      topic: topic,
      from: _id,
    ));

    // Return unsubscribe function
    return () => unsubscribe(topic);
  }

  /// Unsubscribe from a topic
  void unsubscribe(String topic) {
    final subscription = _subscriptions[topic];
    if (subscription != null) {
      subscription.active = false;
      _subscriptions.remove(topic);
    }

    if (_connected) {
      _sendMessage(WsMessage(
        action: 'unsubscribe',
        topic: topic,
        from: _id,
      ));
    }
  }

  /// Publish data to a topic
  void publish(String topic, dynamic data) {
    if (!_connected) {
      print('Cannot publish: client not connected');
      return;
    }

    _sendMessage(WsMessage(
      action: 'publish',
      topic: topic,
      data: data,
      from: _id,
    ));
  }

  /// Send direct message to a specific ID
  void publishToId(String targetId, dynamic data) {
    if (!_connected) {
      print('Cannot send direct message: client not connected');
      return;
    }

    _sendMessage(WsMessage(
      action: 'direct_message',
      to: targetId,
      data: data,
      from: _id,
    ));
  }

  /// Send message to a remote server
  void publishToServer(String addr, dynamic data, {bool secure = false}) {
    if (!_connected) {
      print('Cannot send to server: client not connected');
      return;
    }

    _sendMessage(WsMessage(
      action: 'publish_to_server',
      to: addr,
      data: data,
      from: _id,
    ));
  }

  /// Publish with acknowledgment
  ClientAck publishWithAck(String topic, dynamic data, Duration timeout) {
    if (!_connected) {
      print('Cannot publish with ACK: client not connected');
      return ClientAck(id: 'disconnected', timeout: timeout)..cancel();
    }

    final ackId = _generateAckId();
    final clientAck = ClientAck(id: ackId, timeout: timeout);

    _ackRequests[ackId] = clientAck;

    _sendMessage(WsMessage(
      action: 'publish_with_ack',
      topic: topic,
      data: data,
      ackId: ackId,
      from: _id,
    ));

    return clientAck;
  }

  /// Send direct message with acknowledgment
  ClientAck publishToIdWithAck(String targetId, dynamic data, Duration timeout) {
    if (!_connected) {
      print('Cannot send direct message with ACK: client not connected');
      return ClientAck(id: 'disconnected', timeout: timeout)..cancel();
    }

    final ackId = _generateAckId();
    final clientAck = ClientAck(id: ackId, timeout: timeout);

    _ackRequests[ackId] = clientAck;

    _sendMessage(WsMessage(
      action: 'publish_to_id_with_ack',
      to: targetId,
      data: data,
      ackId: ackId,
      from: _id,
    ));

    return clientAck;
  }

  /// Send message to queue
  void _sendMessage(WsMessage message) {
    if (!_messageQueue.isClosed) {
      _messageQueue.add(message);
    }
  }

  /// Generate unique ACK ID
  String _generateAckId() {
    return 'client_ack_${_id}_${_nextAckId++}';
  }

  /// Generate unique client ID
  String _generateId() {
    final random = Random();
    final timestamp = DateTime.now().millisecondsSinceEpoch;
    final randomPart = random.nextInt(999999).toString().padLeft(6, '0');
    return 'dart_client_${timestamp}_$randomPart';
  }

  /// Reconnect to server
  Future<void> _reconnect() async {
    if (!_autorestart || _done) return;

    await Future.delayed(_restartEvery);

    try {
      await _connect();
      _resubscribeAll();
    } catch (e) {
      print('Reconnection failed: $e');
    }
  }

  /// Resubscribe to all topics after reconnection
  void _resubscribeAll() {
    final topics = _subscriptions.keys.toList();
    
    for (final topic in topics) {
      _sendMessage(WsMessage(
        action: 'subscribe',
        topic: topic,
        from: _id,
      ));
    }
  }

  /// Close the client connection
  Future<void> close() async {
    _connected = false;
    _done = true;

    _onClose?.call();

    await _channel?.sink.close(status.normalClosure);
    _channel = null;

    await _messageQueue.close();
  }

  /// Get client ID
  String get id => _id;

  /// Check if connected
  bool get isConnected => _connected;
} 