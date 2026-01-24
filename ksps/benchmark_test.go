package ksps

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	// "github.com/bytedance/sonic"

	"github.com/kamalshkeir/ksmux"
)

var portCounter atomic.Int32

func init() {
	// Standard configuration - It will be fast because WsMessage has its own MarshalJSON/UnmarshalJSON
	// jsonencdec.SetDefaultJsonLib(jsonencdec.JsonLib{
	// 	Marshal:       json.Marshal,
	// 	Unmarshal:     json.Unmarshal,
	// 	MarshalIndent: json.MarshalIndent,
	// 	NewDecoder:    func(r io.Reader) jsonencdec.Decoder { return json.NewDecoder(r) },
	// 	NewEncoder:    func(w io.Writer) jsonencdec.Encoder { return json.NewEncoder(w) },
	// })
	portCounter.Store(10000)
}

func getNextPort() int {
	return int(portCounter.Add(1))
}

func BenchmarkPubSubLatency(b *testing.B) {
	port := getNextPort()
	addr := fmt.Sprintf("localhost:%d", port)
	srv := NewServer(ksmux.Config{Address: addr})
	go srv.Run()
	defer srv.Stop()

	time.Sleep(100 * time.Millisecond)

	client, err := NewClient(ClientConnectOptions{
		Address: addr,
		Path:    "/ws/bus",
	})
	if err != nil {
		b.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	topic := "bench-latency"
	var wg sync.WaitGroup

	client.Subscribe(topic, func(data any, unsub func()) {
		wg.Done()
	})

	time.Sleep(50 * time.Millisecond)
	b.ResetTimer()

	msg := map[string]string{"payload": "test"}

	for i := 0; i < b.N; i++ {
		wg.Add(1)
		client.Publish(topic, msg)
		wg.Wait()
	}
}

func BenchmarkPubSubThroughput(b *testing.B) {
	port := getNextPort()
	addr := fmt.Sprintf("localhost:%d", port)
	srv := NewServer(ksmux.Config{Address: addr})
	go srv.Run()
	defer srv.Stop()

	time.Sleep(100 * time.Millisecond)

	client, err := NewClient(ClientConnectOptions{
		Address: addr,
		Path:    "/ws/bus",
	})
	if err != nil {
		b.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	topic := "bench-throughput"
	var wg sync.WaitGroup

	concurrency := 500
	sem := make(chan struct{}, concurrency)

	client.OnClose(func() {
	})

	client.Subscribe(topic, func(data any, unsub func()) {
		<-sem
		wg.Done()
	})

	time.Sleep(50 * time.Millisecond)
	b.ResetTimer()

	msg := map[string]string{"payload": "test"}

	for i := 0; i < b.N; i++ {
		sem <- struct{}{}
		wg.Add(1)
		client.Publish(topic, msg)
	}

	wg.Wait()
}
