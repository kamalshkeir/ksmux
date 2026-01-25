package ksps

// import (
// 	"fmt"
// 	"testing"
// 	"time"

// 	"github.com/goforj/godump"
// 	"github.com/kamalshkeir/ksmux"
// )

// func TestServerAndClient(t *testing.T) {
// 	srv := NewServer(ksmux.Config{
// 		Address: "localhost:9313",
// 	})
// 	srv.ID = "srv1"
// 	go srv.Run()
// 	defer srv.Shutdown()

// 	srv.OnID(func(data Message) {
// 		t.Log("Server OnID got message")
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
// 	// init client
// 	client, err := NewClient(ClientOptions{
// 		Id:      "client1",
// 		Address: "localhost:9313",
// 		OnID: func(data Message) {
// 			t.Log("Client OnID got message")
// 			godump.Dump(data)
// 		},
// 	})
// 	if err != nil {
// 		t.Error(err)
// 	}
// 	defer client.Close()

// 	client.Subscribe("clientsub", func(data Message, unsub func()) {
// 		t.Log("clientsub got message")
// 		godump.Dump(data)
// 	})
// 	time.Sleep(200 * time.Millisecond)

// 	client.Publish("server1", "msg from client")
// 	time.Sleep(200 * time.Millisecond)
// 	client.PublishToID("srv1", "msg from client to srv id")
// 	time.Sleep(200 * time.Millisecond)
// 	srv.Publish("clientsub", "hello fril server")
// 	time.Sleep(200 * time.Millisecond)
// 	srv.PublishToID("client1", "hello from server to client id")
// 	time.Sleep(200 * time.Millisecond)
// 	client.PublishToID("client1", "hello from client to client id")
// 	time.Sleep(200 * time.Millisecond)
// 	ack := srv.PublishToIDWithAck("srv1", "hello from client to server id with ack", time.Second*3)
// 	resps := ack.Wait()
// 	for k, v := range resps {
// 		fmt.Println("client==", k)
// 		fmt.Println("v==", v)
// 	}

// 	time.Sleep(200 * time.Millisecond)
// }
