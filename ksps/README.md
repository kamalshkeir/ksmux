# 🚀 Ultra-Fast Pub/Sub System

**The fastest pub/sub system with identical APIs across Go, JavaScript, Python, and Dart/Flutter.**

Built on **ksmux** framework with Go 1.24 optimizations, WebSocket transport, and complete ACK system.

## ⚡ Why This Pub/Sub?

- **🔥 Ultra-Fast**: Go 1.24 Swiss Tables, weak pointers, worker pools
- **🌍 Universal**: Identical API in Go, JavaScript, Python, Dart/Flutter
- **✅ Reliable**: Complete ACK system with Wait/WaitAny/Cancel
- **🔄 Resilient**: Auto-reconnection, error handling
- **📦 Simple**: One-command installation

## 🎯 Quick Examples

### 🌐 Server (Go)
```go
package main

import "github.com/kamalshkeir/ksmux/ksps"

func main() {
    server := ksps.NewServer()
    
    // Subscribe to messages
    server.Subscribe("events", func(data any, unsub func()) {
        fmt.Printf("Received: %v\n", data)
    })
    
    // Publish with acknowledgment
    ack := server.PublishWithAck("events", "Hello World!", 5*time.Second)
    responses := ack.Wait()
    
    server.Run() // Start on :9313
}
```

### 📱 Client Go
```go
client, _ := ksps.NewClient(ksps.ClientConnectOptions{
    Address: "localhost:9313",
})

// Subscribe
client.Subscribe("events", func(data any, unsub func()) {
    fmt.Printf("Got: %v\n", data)
})

// Publish with ACK
ack := client.PublishWithAck("events", "From Go!", 3*time.Second)
response, ok := ack.WaitAny()
```

### 🌍 Client JavaScript
```javascript
// Browser or Node.js
const client = await BusClient.Client.NewClient({
    Address: "localhost:9313"
});

// Subscribe
client.Subscribe("events", (data, unsub) => {
    console.log("Received:", data);
});

// Publish with ACK
const ack = client.PublishWithAck("events", "From JS!", 3000);
const responses = await ack.Wait();
```

### 🐍 Client Python
```bash
pip install ksps
```

```python
import asyncio
from ksps import Client

async def main():
    client = await Client.NewClient(Address="localhost:9313")
    
    # Subscribe
    await client.Subscribe("events", 
        lambda data, unsub: print(f"Got: {data}"))
    
    # Publish with ACK
    ack = await client.PublishWithAck("events", "From Python!", 3.0)
    responses = await ack.Wait()

asyncio.run(main())
```

### 🎯 Client Dart/Flutter
```dart
import 'package:ksps_dart/ksps_dart.dart';

void main() async {
    final client = await KspsClient.connect(
        ClientConnectOptions(
            id: 'dart-client',
            address: 'localhost:9313',
        ),
    );
    
    // Subscribe
    client.subscribe('events', (data, unsub) {
        print('Received: $data');
    });
    
    // Publish with ACK
    final ack = client.publishWithAck(
        'events', 
        {'msg': 'From Dart!'}, 
        Duration(seconds: 3),
    );
    final responses = await ack.wait();
}
```

## 🔧 Installation

### Go Server
```bash
go mod init myapp
go get github.com/kamalshkeir/ksmux/ksps
```

### JavaScript Client
```html
<script src="client.js"></script>
```

### Python Client
```bash
pip install ksps
```



## 📊 Performance

- **Go Server**: ~50ns per operation, microsecond WebSocket
- **Python Client**: ~15,000 msg/s, ~70 ACK/s
- **JavaScript**: Native WebSocket performance
- **Memory**: <10MB usage, minimal allocations

## 🎯 Use Cases

- **Real-time Apps**: Chat, notifications, live updates
- **Microservices**: Inter-service communication
- **IoT**: Sensor data collection and distribution
- **Gaming**: Multi-player synchronization
- **Monitoring**: Metrics and alerting systems

## 🔄 Complete API

All clients support identical methods:

```
✅ Subscribe(topic, callback)
✅ Unsubscribe(topic)
✅ Publish(topic, data)
✅ PublishToID(targetID, data)
✅ PublishToServer(addr, data)
✅ PublishWithAck(topic, data, timeout)
✅ PublishToIDWithAck(targetID, data, timeout)

// ACK Management
✅ Wait() - Wait for all acknowledgments
✅ WaitAny() - Wait for first acknowledgment
✅ GetStatus() - Real-time status
✅ IsComplete() - All received?
✅ Cancel() - Cancel waiting
```

## 🚀 Getting Started

1. **Start Server**:
   ```bash
   go run cmd/main.go
   ```

2. **Connect Clients** (any language):
   ```
   Address: localhost:9313
   Path: /ws/bus
   ```

3. **Publish & Subscribe**:
   - Same API across all languages
   - WebSocket transport
   - Automatic reconnection


## 🏆 Why Choose This?

- **Fastest**: Go 1.24 optimizations, uvloop, orjson
- **Universal**: Write once, use everywhere
- **Reliable**: Complete ACK system, auto-reconnection
- **Modern**: Latest language features and best practices
- **Simple**: Minimal setup, maximum performance

---

**Built with ❤️ using ksmux, Go 1.24, uvloop, and modern web standards.** 