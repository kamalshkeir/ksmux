import 'dart:async';
import 'types.dart';
import 'client.dart';

/// Client ACK handle for waiting acknowledgments
class ClientAck {
  final String id;
  final Duration timeout;
  bool _cancelled = false;
  bool _completed = false;
  
  final Completer<Map<String, AckResponse>> _responseCompleter = Completer();
  final StreamController<Map<String, bool>> _statusController = StreamController.broadcast();
  
  Map<String, AckResponse>? _responses;
  Map<String, bool>? _status;

  final KspsClient? _client;

  ClientAck({
    required this.id,
    required this.timeout,
    KspsClient? client,
  }) : _client = client;

  /// Wait for all acknowledgments with timeout
  Future<Map<String, AckResponse>> wait() async {
    if (_cancelled || _completed) {
      return _responses ?? {};
    }

    try {
      return await _responseCompleter.future.timeout(timeout);
    } on TimeoutException {
      cancel();
      return {};
    }
  }

  /// Wait for any (first) acknowledgment
  Future<AckResponse?> waitAny() async {
    if (_cancelled || _completed) {
      final responses = _responses ?? {};
      return responses.isNotEmpty ? responses.values.first : null;
    }

    try {
      final responses = await _responseCompleter.future.timeout(timeout);
      return responses.isNotEmpty ? responses.values.first : null;
    } on TimeoutException {
      cancel();
      return null;
    }
  }

  /// Get current status of acknowledgments
  Future<Map<String, bool>> getStatus() async {
    if (_cancelled || _completed) {
      return _status ?? {};
    }

    if (_client != null) {
       _client!.internalSendMessage(WsMessage(
        action: 'get_ack_status',
        ackId: id,
        from: _client!.id,
      ));
    }

    try {
      // Wait for status update on the stream with a short timeout
      return await statusStream.first.timeout(const Duration(seconds: 2));
    } catch (_) {
      return _status ?? {};
    }
  }

  /// Check if all ACKs are received
  Future<bool> isComplete() async {
    final status = await getStatus();
    return status.isNotEmpty && status.values.every((received) => received);
  }

  /// Cancel waiting for acknowledgments
  void cancel() {
    if (_cancelled || _completed) return;
    
    _cancelled = true;
    _completed = true;
    
    if (_client != null) {
      _client!.internalSendMessage(WsMessage(
        action: 'cancel_ack',
        ackId: id,
        from: _client!.id,
      ));
      _client!.internalRemoveAckRequest(id);
    }

    if (!_responseCompleter.isCompleted) {
      _responseCompleter.complete({});
    }
    
    _statusController.close();
  }

  /// Handle response from server (internal use)
  void handleResponse(Map<String, AckResponse> responses) {
    if (_cancelled || _completed) return;
    
    _responses = responses;
    _completed = true;
    
    if (!_responseCompleter.isCompleted) {
      _responseCompleter.complete(responses);
    }
  }

  /// Handle status from server (internal use)
  void handleStatus(Map<String, bool> status) {
    if (_cancelled || _completed) return;
    
    _status = status;
    _statusController.add(status);
  }

  /// Stream of status updates
  Stream<Map<String, bool>> get statusStream => _statusController.stream;

  /// Check if cancelled
  bool get isCancelled => _cancelled;

  /// Check if completed
  bool get isCompleted => _completed;
} 