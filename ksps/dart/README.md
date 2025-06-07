# KSPS Dart Client

Ultra-fast Dart/Flutter client for the ksmux pub/sub system with identical API to Go, JavaScript, and Python clients.

## Features

- 🚀 **Ultra-fast performance** with async/await patterns
- 🔄 **Complete ACK system** with timeout handling
- 🔌 **Auto-reconnection** with configurable intervals
- 📱 **Flutter compatible** for mobile apps
- 🎯 **Identical API** across all language clients
- ⚡ **WebSocket optimization** with message queuing
- 🛡️ **Type safety** with strong Dart typing

## Installation

Add to your `pubspec.yaml`:

```yaml
dependencies:
  ksps_dart: ^1.0.0
```

Or install from local path:

```yaml
dependencies:
  ksps_dart:
    path: ../dart
```

## Quick Start

```dart
import 'package:ksps_dart/ksps_dart.dart';

void main() async {
  // Connect to server
  final client = await KspsClient.connect(
    ClientConnectOptions(
      id: 'my-dart-client',
      address: 'localhost:9313',
      autorestart: true,
      onId: (data, unsub) {
        print('Direct message: $data');
      },
    ),
  );

  // Subscribe to topic
  final unsub = client.subscribe('my_topic', (data, unsubFn) {
    print('Received: $data');
  });

  // Publish message
  client.publish('my_topic', {'message': 'Hello from Dart!'});

  // Publish with acknowledgment
  final ack = client.publishWithAck(
    'ack_topic',
    {'data': 'Important message'},
    Duration(seconds: 5),
  );

  final responses = await ack.wait();
  print('ACK responses: ${responses.length}');

  // Clean up
  unsub();
  await client.close();
}
```

## API Reference

### Client Creation

```dart
final client = await KspsClient.connect(ClientConnectOptions(
  id: 'client-id',              // Client identifier
  address: 'localhost:9313',    // Server address
  secure: false,                // Use WSS instead of WS
  path: '/ws/bus',              // WebSocket path
  autorestart: true,            // Auto-reconnect on disconnect
  restartEvery: Duration(seconds: 10), // Reconnection interval
  onDataWs: (data) => {},       // All message callback
  onId: (data, unsub) => {},    // Direct message callback
  onClose: () => {},            // Connection close callback
));
```

### Subscription Methods

```dart
// Subscribe to topic
VoidCallback unsub = client.subscribe('topic', (data, unsubFn) {
  print('Message: $data');
  // unsubFn(); // Call to unsubscribe
});

// Unsubscribe
client.unsubscribe('topic');
// or
unsub(); // Using returned function
```

### Publishing Methods

```dart
// Basic publish
client.publish('topic', {'key': 'value'});

// Direct message to client/server
client.publishToId('target-id', {'message': 'direct'});

// Message to remote server
client.publishToServer('remote:9313', {'data': 'cross-server'});
```

### ACK Methods

```dart
// Publish with acknowledgment
ClientAck ack = client.publishWithAck(
  'topic',
  {'data': 'important'},
  Duration(seconds: 5), // timeout
);

// Wait for all ACKs
Map<String, AckResponse> responses = await ack.wait();

// Wait for first ACK
AckResponse? response = await ack.waitAny();

// Get current status
Map<String, bool> status = await ack.getStatus();

// Check if complete
bool complete = await ack.isComplete();

// Cancel waiting
ack.cancel();

// Direct message with ACK
ClientAck directAck = client.publishToIdWithAck(
  'target-id',
  {'message': 'direct'},
  Duration(seconds: 3),
);
```

### Client Properties

```dart
String clientId = client.id;           // Get client ID
bool connected = client.isConnected;   // Check connection status
await client.close();                  // Close connection
```

## Flutter Integration

For Flutter apps, add the dependency and use in your widgets:

```dart
class MyApp extends StatefulWidget {
  @override
  _MyAppState createState() => _MyAppState();
}

class _MyAppState extends State<MyApp> {
  KspsClient? _client;
  List<String> _messages = [];

  @override
  void initState() {
    super.initState();
    _connectToServer();
  }

  Future<void> _connectToServer() async {
    try {
      _client = await KspsClient.connect(
        ClientConnectOptions(
          id: 'flutter-app',
          address: 'your-server.com:9313',
          secure: true, // Use WSS for production
          autorestart: true,
          onId: (data, unsub) {
            setState(() {
              _messages.add('Direct: ${data.toString()}');
            });
          },
        ),
      );

      _client!.subscribe('app_updates', (data, unsub) {
        setState(() {
          _messages.add('Update: ${data.toString()}');
        });
      });
    } catch (e) {
      print('Connection failed: $e');
    }
  }

  void _sendMessage() {
    _client?.publish('user_action', {
      'action': 'button_pressed',
      'timestamp': DateTime.now().toIso8601String(),
    });
  }

  @override
  void dispose() {
    _client?.close();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text('KSPS Flutter Demo')),
      body: Column(
        children: [
          Expanded(
            child: ListView.builder(
              itemCount: _messages.length,
              itemBuilder: (context, index) {
                return ListTile(
                  title: Text(_messages[index]),
                );
              },
            ),
          ),
          ElevatedButton(
            onPressed: _sendMessage,
            child: Text('Send Message'),
          ),
        ],
      ),
    );
  }
}
```

## Performance

The Dart client is optimized for high performance:

- **Async message handling** with Future/Stream patterns
- **Message queuing** to prevent blocking
- **Automatic reconnection** with exponential backoff
- **Memory efficient** with proper resource cleanup
- **Type safety** to prevent runtime errors

## Error Handling

```dart
try {
  final client = await KspsClient.connect(options);
  
  // Use client...
  
} catch (e) {
  print('Connection error: $e');
  // Handle connection failure
}

// ACK timeout handling
final ack = client.publishWithAck('topic', data, Duration(seconds: 5));
final responses = await ack.wait();

if (responses.isEmpty) {
  print('No ACK responses received (timeout)');
} else {
  for (final entry in responses.entries) {
    if (!entry.value.success) {
      print('ACK error from ${entry.key}: ${entry.value.error}');
    }
  }
}
```

## Development

To work on the package:

1. Install Dart SDK
2. Run `dart pub get` to install dependencies
3. Run `dart pub run build_runner build` to generate JSON serialization
4. Run tests with `dart test`

## License

MIT License - see LICENSE file for details. 