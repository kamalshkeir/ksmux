package ksmux

import (
	"io"
	"maps"
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
