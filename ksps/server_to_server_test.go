package ksps

// // Utilitaires pour ports dynamiques
// var (
// 	basePortS2S = 19600
// 	portMuS2S   sync.Mutex
// )

// func getNextAddrS2S() string {
// 	portMuS2S.Lock()
// 	defer portMuS2S.Unlock()
// 	basePortS2S++
// 	return fmt.Sprintf("localhost:%d", basePortS2S)
// }

// // TestServerPublishToServer : S1 publie vers S2 via PublishToServer, S2 reçoit via OnServerData
// func TestServerPublishToServer(t *testing.T) {
// 	// 1. Démarrer Serveur 2 (Destinataire)
// 	addr2 := getNextAddrS2S()
// 	s2 := NewServer(ksmux.Config{Address: addr2})

// 	received := make(chan string, 1)

// 	// Utilisation correcte de OnServerData pour intercepter les messages inter-serveurs
// 	s2.OnServerData(func(msg Message) {
// 		godump.Dump(msg)
// 		// msg.Data contient le payload envoyé
// 		if m, ok := msg.Data.(map[string]any); ok {
// 			if v, ok := m["msg"]; ok {
// 				received <- fmt.Sprintf("%v", v)
// 			} else {
// 				received <- fmt.Sprintf("%v", m)
// 			}
// 		} else if s, ok := msg.Data.(string); ok {
// 			received <- s
// 		} else {
// 			received <- fmt.Sprintf("%v", msg.Data)
// 		}
// 	})

// 	go s2.Run()
// 	defer s2.Stop()
// 	time.Sleep(200 * time.Millisecond)

// 	// 2. Démarrer Serveur 1 (Expéditeur)
// 	addr1 := getNextAddrS2S()
// 	s1 := NewServer(ksmux.Config{Address: addr1})
// 	go s1.Run()
// 	defer s1.Stop()
// 	time.Sleep(100 * time.Millisecond)

// 	// 3. S1 publie vers S2
// 	target := addr2 + "/ws/bus"
// 	payload := map[string]interface{}{
// 		"msg": "hello-direct-s1",
// 	}

// 	t.Logf("S1 publishing to %s", target)
// 	err := s1.PublishToServer(target, payload)
// 	if err != nil {
// 		t.Fatalf("PublishToServer failed: %v", err)
// 	}

// 	// 4. Vérification via OnServerData
// 	select {
// 	case msg := <-received:
// 		if msg != "hello-direct-s1" {
// 			t.Logf("Received content: %v", msg)
// 		}
// 	case <-time.After(2 * time.Second):
// 		t.Fatal("Timeout waiting for OnServerData on S2")
// 	}
// }

// // TestClientPublishToServer : Client -> S1 -> S2 (OnServerData)
// func TestClientPublishToServer(t *testing.T) {
// 	// 1. Démarrer Serveur 2 (Destinataire)
// 	addr2 := getNextAddrS2S()
// 	s2 := NewServer(ksmux.Config{Address: addr2})

// 	received := make(chan string, 1)
// 	s2.OnServerData(func(msg Message) {
// 		godump.Dump(msg)
// 		if m, ok := msg.Data.(map[string]any); ok {
// 			if v, ok := m["msg"]; ok {
// 				received <- fmt.Sprintf("%v", v)
// 			}
// 		}
// 	})

// 	go s2.Run()
// 	defer s2.Stop()
// 	time.Sleep(200 * time.Millisecond)

// 	// 2. Démarrer Serveur 1 (Relais)
// 	addr1 := getNextAddrS2S()
// 	s1 := NewServer(ksmux.Config{Address: addr1})
// 	go s1.Run()
// 	defer s1.Stop()
// 	time.Sleep(100 * time.Millisecond)

// 	// 3. Client se connecte à S1
// 	client, err := NewClient(ClientOptions{
// 		Id:      "client-manual",
// 		Address: addr1,
// 	})
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	defer client.Close()
// 	time.Sleep(200 * time.Millisecond)

// 	// 4. Client publie vers S2
// 	target := addr2 + "/ws/bus"
// 	payload := map[string]interface{}{
// 		"msg": "hello-direct-client",
// 	}

// 	// Retry loop pour la connexion S1->S2
// 	success := false
// Loop:
// 	for i := 0; i < 3; i++ {
// 		t.Logf("Attempt %d", i+1)
// 		client.PublishToServer(target, payload)

// 		select {
// 		case msg := <-received:
// 			if msg == "hello-direct-client" {
// 				success = true
// 				break Loop
// 			}
// 		case <-time.After(1 * time.Second):
// 		}
// 		time.Sleep(500 * time.Millisecond)
// 	}

// 	if !success {
// 		t.Fatal("Timeout waiting for OnServerData on S2 from client")
// 	}
// }

// func TestServerToInternal(t *testing.T) {
// 	srv := NewServer(ksmux.Config{
// 		Address: "localhost:9313",
// 	})
// 	srv.ID = "srv1"
// 	defer srv.Shutdown()

// 	srv.OnID(func(data Message) {
// 		t.Log("OnID got message")
// 		godump.Dump(data)
// 	})

// 	srv.Subscribe("server1", func(data Message, unsub func()) {
// 		t.Log("server1 got message")
// 		godump.Dump(data)
// 	})
// 	srv.Bus.Subscribe("internal1", func(m Message, f func()) {
// 		t.Log("internal1 got message")
// 		godump.Dump(m)
// 	})

// 	time.Sleep(200 * time.Millisecond)

// 	srv.Bus.Publish("server1", "hello from internal")

// 	time.Sleep(200 * time.Millisecond)
// 	srv.Publish("internal1", "hello from server")
// 	time.Sleep(200 * time.Millisecond)
// 	srv.Bus.Publish("internal1", "hello from server.BUS")
// 	time.Sleep(200 * time.Millisecond)

// 	srv.Publish("server1", "hello AUTO MESSAGE")
// 	time.Sleep(200 * time.Millisecond)

// 	srv.PublishToID("srv1", "hellooooooooo")
// 	time.Sleep(200 * time.Millisecond)
// }
