package ksmux

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoadBalancer(t *testing.T) {
	// Create test servers
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("server1"))
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("server2"))
	}))
	defer server2.Close()

	// Test cases
	tests := []struct {
		name     string
		backends []string
		setup    func(*LoadBalancer)
		check    func(*testing.T, *LoadBalancer)
	}{
		{
			name:     "Basic Round Robin",
			backends: []string{server1.URL, server2.URL},
			setup:    nil,
			check: func(t *testing.T, lb *LoadBalancer) {
				first := lb.NextBackend("/")
				second := lb.NextBackend("/")
				third := lb.NextBackend("/")

				if first == nil || second == nil {
					t.Fatal("Expected non-nil backends")
				}

				if first.URL.String() == second.URL.String() {
					t.Error("Expected different backends in round robin")
				}

				if first.URL.String() != third.URL.String() {
					t.Error("Expected round robin to wrap around")
				}
			},
		},
		{
			name:     "Health Check",
			backends: []string{server1.URL, "http://invalid-host:9999"},
			setup: func(lb *LoadBalancer) {
				// Wait for health check to mark invalid backend as unhealthy
				time.Sleep(time.Second)
			},
			check: func(t *testing.T, lb *LoadBalancer) {
				healthyCount := 0
				lb.mux.RLock()
				for _, b := range lb.backends {
					if b.Alive {
						healthyCount++
					}
				}
				lb.mux.RUnlock()

				if healthyCount != 1 {
					t.Errorf("Expected 1 healthy backend, got %d", healthyCount)
				}
			},
		},
		{
			name:     "Circuit Breaker",
			backends: []string{server1.URL},
			setup: func(lb *LoadBalancer) {
				backend := lb.backends[0]
				// Simulate failures
				for i := 0; i < 6; i++ {
					backend.circuitBreaker.RecordFailure()
				}
			},
			check: func(t *testing.T, lb *LoadBalancer) {
				backend := lb.NextBackend("/")
				if backend != nil {
					t.Error("Expected nil backend due to open circuit")
				}
			},
		},
	}

	// Run tests
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb := newLoadBalancer(true)
			for _, backend := range tt.backends {
				err := lb.AddBackend(backend)
				if err != nil {
					t.Fatalf("Failed to add backend: %v", err)
				}
			}

			if tt.setup != nil {
				tt.setup(lb)
			}

			tt.check(t, lb)
		})
	}
}

func TestLoadBalancerIntegration(t *testing.T) {
	// Create test servers
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("server1"))
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("server2"))
	}))
	defer server2.Close()

	// Create router with load balancer
	router := New()
	err := router.LoadBalancer("/test/*path", BackendOpt{Url: server1.URL}, BackendOpt{Url: server2.URL})
	if err != nil {
		t.Fatalf("Failed to setup load balancer: %v", err)
	}

	// Create test request
	req := httptest.NewRequest("GET", "/test/hello", nil)
	w := httptest.NewRecorder()

	// Make multiple requests to test round robin
	responses := make(map[string]int)
	for i := 0; i < 10; i++ {
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		responses[w.Body.String()]++
	}

	// Check if both servers were used
	if len(responses) != 2 {
		t.Error("Expected responses from both servers")
	}

	// Check if distribution is roughly even
	for _, count := range responses {
		if count < 3 || count > 7 {
			t.Error("Expected roughly even distribution of requests")
		}
	}
}

func TestCircuitBreaker(t *testing.T) {
	cb := NewCircuitBreaker()

	// Test initial state
	if !cb.IsAvailable() {
		t.Error("Expected circuit breaker to be initially closed")
	}

	// Test opening circuit
	for i := 0; i < 5; i++ {
		cb.RecordFailure()
	}

	if cb.IsAvailable() {
		t.Error("Expected circuit breaker to be open after failures")
	}

	// Test half-open state
	time.Sleep(1 * time.Second)
	if !cb.IsAvailable() {
		t.Error("Expected circuit breaker to be half-open after timeout")
	}

	// Test success recovery
	cb.RecordSuccess()
	if !cb.IsAvailable() {
		t.Error("Expected circuit breaker to be closed after success")
	}
}

func BenchmarkLoadBalancer(b *testing.B) {
	// Create test servers
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("server1"))
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("server2"))
	}))
	defer server2.Close()

	lb := newLoadBalancer(true)
	lb.AddBackend(server1.URL)
	lb.AddBackend(server2.URL)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			backend := lb.NextBackend("/")
			if backend == nil {
				b.Fatal("Expected non-nil backend")
			}
		}
	})
}
