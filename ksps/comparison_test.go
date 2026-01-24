package ksps

// import (
// 	"context"
// 	"encoding/json"

// 	// "github.com/goccy/go-json"
// 	"io"
// 	"net/http"
// 	"sync"
// 	"sync/atomic"
// 	"testing"
// 	"time"

// 	"github.com/centrifugal/centrifuge"
// 	centrifugego "github.com/centrifugal/centrifuge-go"
// 	"github.com/kamalshkeir/ksmux"
// 	"github.com/kamalshkeir/ksmux/jsonencdec"
// )

// func init() {
// 	jsonencdec.SetDefaultJsonLib(jsonencdec.JsonLib{
// 		Marshal:       json.Marshal,
// 		Unmarshal:     json.Unmarshal,
// 		MarshalIndent: json.MarshalIndent,
// 		NewDecoder:    func(r io.Reader) jsonencdec.Decoder { return json.NewDecoder(r) },
// 		NewEncoder:    func(w io.Writer) jsonencdec.Encoder { return json.NewEncoder(w) },
// 	})
// }

// // Centrifuge setup
// func setupCentrifuge(addr string) (*centrifuge.Node, *http.Server) {
// 	node, _ := centrifuge.New(centrifuge.Config{})

// 	// Allow all connections
// 	node.OnConnecting(func(ctx context.Context, e centrifuge.ConnectEvent) (centrifuge.ConnectReply, error) {
// 		return centrifuge.ConnectReply{
// 			Credentials: &centrifuge.Credentials{UserID: "bench"},
// 		}, nil
// 	})

// 	node.OnConnect(func(c *centrifuge.Client) {
// 		c.OnSubscribe(func(e centrifuge.SubscribeEvent, cb centrifuge.SubscribeCallback) {
// 			// Permissions are now primarily handled by node/client handlers
// 			cb(centrifuge.SubscribeReply{
// 				Options: centrifuge.SubscribeOptions{},
// 			}, nil)
// 		})
// 		c.OnPublish(func(e centrifuge.PublishEvent, cb centrifuge.PublishCallback) {
// 			// Relay message to channel
// 			_, err := node.Publish(e.Channel, e.Data)
// 			cb(centrifuge.PublishReply{}, err)
// 		})
// 	})

// 	if err := node.Run(); err != nil {
// 		panic(err)
// 	}

// 	mux := http.NewServeMux()
// 	mux.Handle("/connection/websocket", centrifuge.NewWebsocketHandler(node, centrifuge.WebsocketConfig{}))

// 	srv := &http.Server{
// 		Addr:    addr,
// 		Handler: mux,
// 	}

// 	go srv.ListenAndServe()
// 	return node, srv
// }

// func BenchmarkCompareLatency(b *testing.B) {
// 	kspsAddr := "localhost:13001"
// 	kspsSrv := NewServer(ksmux.Config{Address: kspsAddr})
// 	go kspsSrv.Run()
// 	defer kspsSrv.Stop()

// 	cfAddr := "localhost:13002"
// 	_, cfSrv := setupCentrifuge(cfAddr)
// 	defer cfSrv.Shutdown(context.Background())

// 	time.Sleep(200 * time.Millisecond)

// 	b.Run("KSPS-Latency", func(b *testing.B) {
// 		client, err := NewClient(ClientConnectOptions{
// 			Address: kspsAddr,
// 			Path:    "/ws/bus",
// 		})
// 		if err != nil {
// 			b.Fatalf("KSPS client error: %v", err)
// 		}
// 		defer client.Close()

// 		resCh := make(chan struct{}, 1)
// 		topic := "lat-topic"
// 		client.Subscribe(topic, func(data any, unsub func()) {
// 			select {
// 			case resCh <- struct{}{}:
// 			default:
// 			}
// 		})
// 		time.Sleep(100 * time.Millisecond)

// 		payload := []byte(`{"input":"hello"}`)
// 		b.ResetTimer()
// 		for i := 0; i < b.N; i++ {
// 			client.Publish(topic, payload)
// 			<-resCh
// 		}
// 	})

// 	b.Run("Centrifuge-Latency", func(b *testing.B) {
// 		client := centrifugego.NewJsonClient("ws://"+cfAddr+"/connection/websocket", centrifugego.Config{})
// 		err := client.Connect()
// 		if err != nil {
// 			b.Fatalf("Centrifuge connect error: %v", err)
// 		}
// 		defer client.Close()

// 		sub, err := client.NewSubscription("lat-topic", centrifugego.SubscriptionConfig{})
// 		if err != nil {
// 			b.Fatalf("Centrifuge sub error: %v", err)
// 		}

// 		var subWG sync.WaitGroup
// 		subWG.Add(1)
// 		sub.OnSubscribed(func(e centrifugego.SubscribedEvent) {
// 			subWG.Done()
// 		})
// 		_ = sub.Subscribe()
// 		subWG.Wait()

// 		resCh := make(chan struct{}, 1)
// 		sub.OnPublication(func(e centrifugego.PublicationEvent) {
// 			select {
// 			case resCh <- struct{}{}:
// 			default:
// 			}
// 		})

// 		payload := []byte(`{"input":"hello"}`)
// 		b.ResetTimer()
// 		for i := 0; i < b.N; i++ {
// 			_, err := sub.Publish(context.Background(), payload)
// 			if err != nil {
// 				b.Fatalf("Centrifuge publish error: %v", err)
// 			}
// 			<-resCh
// 		}
// 	})
// }

// func BenchmarkCompareThroughput(b *testing.B) {
// 	kspsAddr := "localhost:13003"
// 	kspsSrv := NewServer(ksmux.Config{Address: kspsAddr})
// 	go kspsSrv.Run()
// 	defer kspsSrv.Stop()

// 	cfAddr := "localhost:13004"
// 	_, cfSrv := setupCentrifuge(cfAddr)
// 	defer cfSrv.Shutdown(context.Background())

// 	time.Sleep(200 * time.Millisecond)

// 	b.Run("KSPS-Throughput", func(b *testing.B) {
// 		client, _ := NewClient(ClientConnectOptions{
// 			Address: kspsAddr,
// 			Path:    "/ws/bus",
// 		})
// 		defer client.Close()

// 		// Increase internal buffers for throughput test if possible
// 		// (Actually NewClient hardcodes it, but we can't easily change it here without modifying client.go)
// 		// but we can ensure we don't block the receiver.

// 		doneCh := make(chan struct{})
// 		var received atomic.Int64
// 		topic := "thru-topic"
// 		client.Subscribe(topic, func(data any, unsub func()) {
// 			if received.Add(1) == int64(b.N) {
// 				close(doneCh)
// 			}
// 		})
// 		time.Sleep(100 * time.Millisecond)

// 		payload := []byte(`{"input":"hello"}`)
// 		b.ResetTimer()
// 		for i := 0; i < b.N; i++ {
// 			client.Publish(topic, payload)
// 		}

// 		select {
// 		case <-doneCh:
// 		case <-time.After(30 * time.Second):
// 			b.Logf("Throughput timeout at %d/%d", received.Load(), b.N)
// 		}
// 	})

// 	b.Run("Centrifuge-Throughput", func(b *testing.B) {
// 		client := centrifugego.NewJsonClient("ws://"+cfAddr+"/connection/websocket", centrifugego.Config{})
// 		_ = client.Connect()
// 		defer client.Close()

// 		sub, _ := client.NewSubscription("thru-topic", centrifugego.SubscriptionConfig{})
// 		var subWG sync.WaitGroup
// 		subWG.Add(1)
// 		sub.OnSubscribed(func(e centrifugego.SubscribedEvent) {
// 			subWG.Done()
// 		})
// 		_ = sub.Subscribe()
// 		subWG.Wait()

// 		doneCh := make(chan struct{})
// 		var received atomic.Int64
// 		sub.OnPublication(func(e centrifugego.PublicationEvent) {
// 			if received.Add(1) == int64(b.N) {
// 				close(doneCh)
// 			}
// 		})

// 		payload := []byte(`{"input":"hello"}`)
// 		b.ResetTimer()
// 		for i := 0; i < b.N; i++ {
// 			_, _ = sub.Publish(context.Background(), payload)
// 		}

// 		select {
// 		case <-doneCh:
// 		case <-time.After(10 * time.Second):
// 			b.Logf("Throughput timeout at %d/%d", received.Load(), b.N)
// 		}
// 	})
// }

// WITH goccy
// ❯ go test -bench=Compare -benchmem -v ./ksps
// goos: darwin
// goarch: arm64
// pkg: github.com/kamalshkeir/ksmux/ksps
// cpu: Apple M4
// BenchmarkCompareLatency
// running on http://localhost:13001
// BenchmarkCompareLatency/KSPS-Latency
// client connected to ws://localhost:13001/ws/bus
// client connected to ws://localhost:13001/ws/bus
// client connected to ws://localhost:13001/ws/bus
// client connected to ws://localhost:13001/ws/bus
// BenchmarkCompareLatency/KSPS-Latency-10         	   47222	     23249 ns/op	    2950 B/op	      25 allocs/op
// BenchmarkCompareLatency/Centrifuge-Latency
// BenchmarkCompareLatency/Centrifuge-Latency-10   	   31570	     40041 ns/op	   39011 B/op	      70 allocs/op
// BenchmarkCompareThroughput
// INFO Server Off time=24/01/2026 08:05
// running on http://localhost:13003
// BenchmarkCompareThroughput/KSPS-Throughput
// client connected to ws://localhost:13003/ws/bus
// client connected to ws://localhost:13003/ws/bus
// client connected to ws://localhost:13003/ws/bus
// client connected to ws://localhost:13003/ws/bus
// client connected to ws://localhost:13003/ws/bus
// BenchmarkCompareThroughput/KSPS-Throughput-10   	  262684	      4426 ns/op	    2953 B/op	      25 allocs/op
// BenchmarkCompareThroughput/Centrifuge-Throughput
// BenchmarkCompareThroughput/Centrifuge-Throughput-10         	   29598	     39805 ns/op	   38636 B/op	      69 allocs/op
// PASS
// ok  	github.com/kamalshkeir/ksmux/ksps	8.111s

// WITH encoding/json
// ❯ go test -bench=Compare -benchmem -v ./ksps
// goos: darwin
// goarch: arm64
// pkg: github.com/kamalshkeir/ksmux/ksps
// cpu: Apple M4
// BenchmarkCompareLatency
// running on http://localhost:13001
// BenchmarkCompareLatency/KSPS-Latency
// client connected to ws://localhost:13001/ws/bus
// client connected to ws://localhost:13001/ws/bus
// client connected to ws://localhost:13001/ws/bus
// client connected to ws://localhost:13001/ws/bus
// BenchmarkCompareLatency/KSPS-Latency-10         	   43120	     25718 ns/op	    3932 B/op	      40 allocs/op
// BenchmarkCompareLatency/Centrifuge-Latency
// BenchmarkCompareLatency/Centrifuge-Latency-10   	   30448	     40524 ns/op	   39021 B/op	      70 allocs/op
// BenchmarkCompareThroughput
// INFO Server Off time=24/01/2026 08:12
// running on http://localhost:13003
// BenchmarkCompareThroughput/KSPS-Throughput
// client connected to ws://localhost:13003/ws/bus
// client connected to ws://localhost:13003/ws/bus
// client connected to ws://localhost:13003/ws/bus
// client connected to ws://localhost:13003/ws/bus
// client connected to ws://localhost:13003/ws/bus
// BenchmarkCompareThroughput/KSPS-Throughput-10   	  257006	      4500 ns/op	    3959 B/op	      40 allocs/op
// BenchmarkCompareThroughput/Centrifuge-Throughput
// BenchmarkCompareThroughput/Centrifuge-Throughput-10         	   29908	     40658 ns/op	   38634 B/op	      69 allocs/op
// PASS
// ok  	github.com/kamalshkeir/ksmux/ksps	8.146s
