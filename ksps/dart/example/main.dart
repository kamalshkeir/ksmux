import 'dart:io';
import 'package:ksps_dart/ksps_dart.dart';

void main() async {
  print('🚀 Test du client Dart KSPS');

  try {
    // Créer le client
    final client = await KspsClient.connect(
      ClientConnectOptions(
        id: 'dart-test-client',
        address: 'localhost:9313',
        autorestart: true,
        onId: (data, unsub) {
          print('📬 Message direct reçu: $data');
        },
      ),
    );

    print('✅ Client connecté: ${client.id}');

    // Test 1: Subscribe et Publish
    print('\n1️⃣ Test Subscribe/Publish');
    final unsub = client.subscribe('test_topic', (data, unsubFn) {
      print('🔔 Message reçu sur test_topic: $data');
    });

    await Future.delayed(Duration(milliseconds: 500));
    client.publish('test_topic', {'message': 'Hello from Dart!', 'timestamp': DateTime.now().toIso8601String()});

    // Test 2: PublishWithAck
    print('\n2️⃣ Test PublishWithAck');
    final ack = client.publishWithAck(
      'ack_topic',
      {'ack_message': 'Test ACK from Dart', 'id': 123},
      Duration(seconds: 5),
    );

    final responses = await ack.wait();
    print('📬 ACK reçus: ${responses.length} réponses');
    for (final entry in responses.entries) {
      print('  - ${entry.key}: ${entry.value.success ? "✅" : "❌"} ${entry.value.error ?? ""}');
    }

    // Test 3: PublishToIdWithAck
    print('\n3️⃣ Test PublishToIdWithAck');
    final directAck = client.publishToIdWithAck(
      'server-id',
      {'direct_message': 'Direct from Dart', 'priority': 'high'},
      Duration(seconds: 3),
    );

    final directResponse = await directAck.waitAny();
    if (directResponse != null) {
      print('📬 ACK direct reçu: ${directResponse.success ? "✅" : "❌"}');
    } else {
      print('⏰ Timeout ACK direct');
    }

    // Test 4: GetStatus et Cancel
    print('\n4️⃣ Test GetStatus et Cancel');
    final statusAck = client.publishWithAck(
      'status_topic',
      {'test': 'status'},
      Duration(seconds: 10),
    );

    await Future.delayed(Duration(milliseconds: 500));
    final status = await statusAck.getStatus();
    print('📊 Statut ACK: $status');
    print('🏁 Complet: ${await statusAck.isComplete()}');

    // Annuler après 1 seconde
    await Future.delayed(Duration(seconds: 1));
    statusAck.cancel();
    print('🚫 ACK annulé');

    // Test 5: Performance
    print('\n5️⃣ Test de performance');
    final stopwatch = Stopwatch()..start();
    const messageCount = 1000;

    for (int i = 0; i < messageCount; i++) {
      client.publish('perf_topic', {'id': i, 'data': 'Performance test message $i'});
    }

    stopwatch.stop();
    final messagesPerSecond = (messageCount / stopwatch.elapsedMilliseconds * 1000).round();
    print('⚡ Performance: $messagesPerSecond msg/s');

    // Attendre un peu pour voir les messages
    await Future.delayed(Duration(seconds: 2));

    // Nettoyer
    unsub();
    print('\n🧹 Nettoyage...');
    await client.close();
    print('✅ Client fermé');

  } catch (e) {
    print('❌ Erreur: $e');
    exit(1);
  }
} 