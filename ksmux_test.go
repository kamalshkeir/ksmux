package ksmux

import (
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kamalshkeir/ksmux/jsonencdec"
)

func TestKsmux(t *testing.T) {
	router := New()

	router.Get("/", func(c *Context) {
		c.Text("ok")
	})
	router.Get("/users", func(c *Context) {
		c.Text("ok")
	})

	router.Get("/bla/:user/*any", func(c *Context) {
		c.Text("yessss: " + c.Param("user") + " " + c.Param("any"))
	})

	router.Get("/users/:userId", func(c *Context) {
		c.Json(map[string]string{"userId": c.Param("userId")})
	})

	router.Get("/users/:mobileID/device", func(c *Context) {
		c.Json(map[string]string{"mobileID": c.Param("mobileID")})
	})

	router.Get("/users/:userId/devices", func(c *Context) {
		c.Json(map[string]string{"userId": c.Param("userId")})
	})

	router.Get("/users/:userId/posts/:postId", func(c *Context) {
		c.Json(map[string]string{
			"userId": c.Param("userId"),
			"postId": c.Param("postId"),
		})
	})

	router.Get("/users/:userId/posts/:postIDD/team/:teamId", func(c *Context) {
		c.Json(map[string]string{
			"userId":  c.Param("userId"),
			"postIDD": c.Param("postIDD"),
			"teamId":  c.Param("teamId"),
		})
	})

	tests := []struct {
		name       string
		path       string
		wantCode   int
		wantJSON   map[string]string
		wantText   string
		wantIsJSON bool
	}{
		{
			name:       "static index",
			path:       "/",
			wantCode:   200,
			wantText:   "ok",
			wantIsJSON: false,
		},
		{
			name:       "static",
			path:       "/users",
			wantCode:   200,
			wantText:   "ok",
			wantIsJSON: false,
		},
		{
			name:       "wildcard route",
			path:       "/bla/kamal/dqsdsqd/dsqdqeee",
			wantCode:   200,
			wantText:   "yessss: kamal dqsdsqd/dsqdqeee",
			wantIsJSON: false,
		},
		{
			name:     "simple param",
			path:     "/users/123",
			wantCode: 200,
			wantJSON: map[string]string{
				"userId": "123",
			},
			wantIsJSON: true,
		},
		{
			name:     "simple param 3 seg",
			path:     "/users/123/device",
			wantCode: 200,
			wantJSON: map[string]string{
				"mobileID": "123",
			},
			wantIsJSON: true,
		},
		{
			name:     "simple param 3 seg(Other)",
			path:     "/users/123/devices",
			wantCode: 200,
			wantJSON: map[string]string{
				"userId": "123",
			},
			wantIsJSON: true,
		},
		{
			name:     "two params",
			path:     "/users/123/posts/789",
			wantCode: 200,
			wantJSON: map[string]string{
				"userId": "123",
				"postId": "789",
			},
			wantIsJSON: true,
		},
		{
			name:     "three params",
			path:     "/users/123/posts/789/team/1",
			wantCode: 200,
			wantJSON: map[string]string{
				"userId":  "123",
				"postIDD": "789",
				"teamId":  "1",
			},
			wantIsJSON: true,
		},
		{
			name:       "not found",
			path:       "/notfound",
			wantCode:   404,
			wantText:   "404 page not found\n",
			wantIsJSON: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", tt.path, nil)
			router.ServeHTTP(w, req)

			resp := w.Result()
			body, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != tt.wantCode {
				t.Errorf("status code = %d, want %d", resp.StatusCode, tt.wantCode)
			}

			if tt.wantIsJSON {
				var got map[string]string
				if err := jsonencdec.DefaultUnmarshal(body, &got); err != nil {
					t.Fatalf("Failed to parse JSON response: %v", err)
				}
				if !maps.Equal(got, tt.wantJSON) {
					t.Errorf("body = %v, want %v", got, tt.wantJSON)
				}
			} else {
				if string(body) != tt.wantText {
					t.Errorf("body = %q, want %q", string(body), tt.wantText)
				}
			}
		})
	}
}

// 1. Test déterminant pour la séparation des méthodes HTTP (POST, PUT, DELETE)
func TestHTTPMethods(t *testing.T) {
	router := New()

	router.Post("/items/:id", func(c *Context) {
		c.Text("POST:" + c.Param("id"))
	})
	router.Put("/items/:id", func(c *Context) {
		c.Text("PUT:" + c.Param("id"))
	})
	router.Delete("/items/:id", func(c *Context) {
		c.Text("DELETE:" + c.Param("id"))
	})

	cases := []struct {
		method   string
		wantBody string
	}{
		{"POST", "POST:42"},
		{"PUT", "PUT:42"},
		{"DELETE", "DELETE:42"},
	}

	for _, tc := range cases {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, "/items/42", nil)
		router.ServeHTTP(w, req)

		body, _ := io.ReadAll(w.Result().Body)
		if string(body) != tc.wantBody {
			t.Errorf("Method %s: got %q, want %q", tc.method, string(body), tc.wantBody)
		}
	}
}

// 2. Test déterminant pour la Concurrence et Thread-Safety (pools context & params)
func TestParallelConcurrency(t *testing.T) {
	router := New()
	router.Get("/data/:category/:id", func(c *Context) {
		c.Text(c.Param("category") + "-" + c.Param("id"))
	})

	t.Run("concurrent_requests", func(t *testing.T) {
		t.Parallel()
		for i := 0; i < 50; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/data/tech/100", nil)
			router.ServeHTTP(w, req)

			body, _ := io.ReadAll(w.Result().Body)
			if string(body) != "tech-100" {
				t.Errorf("Concurrent request failed: got %q, want %q", string(body), "tech-100")
			}
		}
	})
}

// 3. Test déterminant pour les Middlewares et le ReverseProxy par nom d'hôte
func TestMiddlewareAndReverseProxy(t *testing.T) {
	router := New()

	// Global Middleware
	router.Use(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Global-Middleware", "active")
			h.ServeHTTP(w, r)
		})
	})

	// Serveur Backend Simulé pour le ReverseProxy
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("backend-ok"))
	}))
	defer backend.Close()

	// Reverse Proxy Host-based
	router.ReverseProxy("app.domain.com", backend.URL)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "app.domain.com"
	router.Handler().ServeHTTP(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if string(body) != "backend-ok" {
		t.Errorf("ReverseProxy response: got %q, want 'backend-ok'", string(body))
	}
	if w.Header().Get("X-Global-Middleware") != "active" {
		t.Errorf("Middleware header missing: got %q", w.Header().Get("X-Global-Middleware"))
	}
}
