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
client, _ := ksps.NewClient(ksps.ClientOptions{
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
    Address: "localhost:9313", // default: window.location.host
    Path: "/ws/newPath", // default: /ws/bus
    Secure: false //  protocol == 'http:' -> false
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
        ClientOptions(
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

## 📩 ACK System - Confirmation de livraison

Le système d'ACK vous permet de **savoir si vos messages ont été reçus et traités**.

### Concept simple

```
Serveur/Client → Publish → Subscribers → ACK → Serveur/Client
                              ↓
                        "J'ai bien reçu !"
```

### 🎯 Exemple 1 : Attendre toutes les réponses (Wait)

**Go - Serveur**
```go
// Publier et attendre que TOUS les subscribers aient traité le message
ack := server.PublishWithAck("notifications", map[string]any{
    "type": "alert",
    "message": "Mise à jour disponible",
}, 5*time.Second)

// Bloque jusqu'à ce que tous aient répondu (ou timeout)
responses := ack.Wait()

// Vérifier les résultats
for clientID, resp := range responses {
    if resp.Success {
        fmt.Printf("✅ %s a bien reçu\n", clientID)
    } else {
        fmt.Printf("❌ %s erreur: %s\n", clientID, resp.Error)
    }
}
```

**JavaScript - Client**
```javascript
// Publier avec ACK (timeout en millisecondes)
const ack = client.PublishWithAck("notifications", {
    type: "alert",
    message: "Mise à jour disponible"
}, 5000);

// Attendre toutes les réponses
const responses = await ack.Wait();

for (const [clientID, resp] of Object.entries(responses)) {
    console.log(`${clientID}: ${resp.success ? '✅' : '❌'}`);
}
```

**Python - Client**
```python
# Publier avec ACK (timeout en secondes)
ack = await client.PublishWithAck("notifications", {
    "type": "alert",
    "message": "Mise à jour disponible"
}, 5.0)

# Attendre toutes les réponses
responses = await ack.Wait()

for client_id, resp in responses.items():
    status = "✅" if resp.get("success") else "❌"
    print(f"{client_id}: {status}")
```

### 🎯 Exemple 2 : Attendre la première réponse (WaitAny)

Utile quand vous avez plusieurs services et qu'un seul doit répondre.

**Go**
```go
// Publier vers plusieurs clients
ack := server.PublishWithAck("process-task", taskData, 10*time.Second)

// Retourne dès que le PREMIER client répond
resp, ok := ack.WaitAny()

if ok && resp.Success {
    fmt.Printf("Tâche prise en charge par: %s\n", resp.ClientID)
} else {
    fmt.Println("Aucun service disponible")
}
```

**JavaScript**
```javascript
const ack = client.PublishWithAck("process-task", taskData, 10000);

const { response, success } = await ack.WaitAny();

if (success) {
    console.log(`Tâche prise en charge par: ${response.client_id}`);
}
```

### 🎯 Exemple 3 : Message direct avec ACK (PublishToIDWithAck)

Envoyer un message à UN client spécifique et attendre sa confirmation.

**Go**
```go
// Envoyer directement à un client par son ID
ack := server.PublishToIDWithAck("user-123", map[string]any{
    "action": "sync",
    "data": userData,
}, 3*time.Second)

responses := ack.Wait()

if resp, ok := responses["user-123"]; ok && resp.Success {
    fmt.Println("Utilisateur synchronisé !")
}
```

### 🎯 Exemple 4 : Annuler une attente (Cancel)

**Go**
```go
ack := server.PublishWithAck("slow-topic", data, 30*time.Second)

// Annuler après 5 secondes si pas de réponse
go func() {
    time.Sleep(5 * time.Second)
    ack.Cancel() // Libère immédiatement Wait()
}()

responses := ack.Wait() // Retourne dès Cancel()
```

**JavaScript**
```javascript
const ack = client.PublishWithAck("slow-topic", data, 30000);

// Annuler après 5 secondes
setTimeout(() => ack.Cancel(), 5000);

const responses = await ack.Wait(); // Retourne dès Cancel()
```

### 🎯 Exemple 5 : Vérifier le statut en temps réel

**Go**
```go
ack := server.PublishWithAck("broadcast", data, 10*time.Second)

// Vérifier le statut sans bloquer
go func() {
    for !ack.IsComplete() {
        status := ack.GetStatus()
        received := 0
        for _, got := range status {
            if got { received++ }
        }
        fmt.Printf("Progression: %d/%d\n", received, len(status))
        time.Sleep(500 * time.Millisecond)
    }
}()

responses := ack.Wait()
```

### 🎯 Côté Subscriber : le callback traite et répond automatiquement

```go
// L'ACK est envoyé automatiquement quand le callback termine
client.Subscribe("notifications", func(data any, unsub func()) {
    // Traiter le message
    fmt.Printf("Reçu: %v\n", data)
    
    // ✅ ACK Success est envoyé automatiquement ici
})

// Si le callback panic, un ACK Error est envoyé
client.Subscribe("risky-topic", func(data any, unsub func()) {
    panic("Erreur !") // ❌ ACK Error avec message d'erreur
})
```

### 📊 Structure de AckResponse

```go
type AckResponse struct {
    AckID    string // ID unique de l'ACK
    ClientID string // ID du client qui répond
    Success  bool   // true = traité avec succès
    Error    string // Message d'erreur si Success=false
}
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

## 🔒 Security & Authentication

KSPS provides two powerful ways to secure your WebSocket connections:

### 1. Connection Hook (`OnUpgradeWS`)
Best for quick validation using query parameters or headers.

```go
server := ksps.NewServer()

// Hook runs BEFORE connection upgrade
server.OnUpgradeWS(func(r *http.Request) bool {
    // Check Query Param
    token := r.URL.Query().Get("token")
    if token == "secret-token" {
        return true // Accept
    }
    
    // Check Header (Go/Native clients only)
    if r.Header.Get("X-Auth-Token") == "valid" {
        return true
    }
    
    return false // Reject (403 Forbidden)
})

server.Run()
```

### 2. Middleware (`WsMidws`)
Best for advanced logic, session validation, or reusing existing `ksmux` middleware.

```go
server := ksps.NewServer()

authMiddleware := func(next ksmux.Handler) ksmux.Handler {
    return func(c *ksmux.Context) {
        // Example: Validate Session Cookie
        cookie, err := c.Cookie("session_id")
        if err != nil || !isValidSession(cookie) {
            c.Status(401).Text("Unauthorized")
            return
        }
        
        // Example: Validate Query Param
        if c.QueryParam("token") != "secret" {
            c.Status(401).Text("Invalid Token")
            return
        }
        
        next(c)
    }
}

// Apply middleware
server.WsMidws = append(server.WsMidws, authMiddleware)

server.Run()
``` 