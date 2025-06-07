/// Client subscriber for managing subscriptions
class ClientSubscriber {
  final String id;
  final String topic;
  final void Function() _unsubscribeCallback;

  ClientSubscriber({
    required this.id,
    required this.topic,
    required void Function() unsubscribeCallback,
  }) : _unsubscribeCallback = unsubscribeCallback;

  /// Unsubscribe from the topic
  void unsubscribe() {
    _unsubscribeCallback();
  }
} 