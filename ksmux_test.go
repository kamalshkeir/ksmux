package ksmux

import (
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
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

// 4. Test déterminant pour l'isolation des Context entre requêtes après un panic
//
// Un handler qui panique doit rendre au pool un Context nettoyé. Si le reset est
// une instruction ordinaire placée après handler(c), il est sauté quand la pile
// se déroule, alors que le defer contextPool.Put(c) s'exécute malgré tout : le
// Context pollué retourne dans le pool. La requête suivante passe alors par une
// route sans paramètre, dont la branche n'affecte pas c.Params, et lit les
// paramètres de la requête qui a paniqué.
func TestContextIsolationAfterPanic(t *testing.T) {
	router := New()

	// Le PanicHandler masque complètement la défaillance côté HTTP : la requête
	// qui a paniqué répond un 500 parfaitement banal.
	router.RouterConfig.PanicHandler = func(w http.ResponseWriter, req *http.Request, rcv interface{}) {
		w.WriteHeader(http.StatusInternalServerError)
	}

	// Route avec paramètre, qui panique.
	router.Get("/user/:id", func(c *Context) {
		panic("boom: " + c.Param("id"))
	})

	// Route sans paramètre : elle ne doit rien voir, jamais.
	router.Get("/health", func(c *Context) {
		c.Text(c.Param("id"))
	})

	// La fuite ne concerne qu'une seule requête, celle qui réutilise aussitôt
	// l'objet pollué. On répète donc le cycle pour rendre la détection fiable,
	// sans jamais produire d'échec sur un code correct.
	const cycles = 50
	for i := range cycles {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/user/SECRET-42", nil))

		if got := w.Result().StatusCode; got != http.StatusInternalServerError {
			t.Fatalf("cycle %d: le PanicHandler n'a pas répondu 500, got %d", i, got)
		}

		w = httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
		body, _ := io.ReadAll(w.Result().Body)

		if string(body) != "" {
			t.Fatalf("cycle %d: /health a vu les paramètres d'une autre requête: %q", i, string(body))
		}
	}
}

// 5. Test déterminant pour le contrôle d'origine CORS
//
// Le contrôle compare l'en-tête Origin à la liste d'origines autorisées par
// sous-chaîne (strings.Contains). Toute origine qui est un fragment d'une
// origine autorisée passe donc le contrôle : autoriser "http://example.com"
// laisse passer "http://example.co", "example.com", etc.
func TestCORSRejectsPartialOrigin(t *testing.T) {
	router := New()
	router.EnableDomainCheck()
	router.Get("/api", func(c *Context) { c.Text("secret") }, "http://example.com")

	cases := []struct {
		name     string
		origin   string
		wantCode int
	}{
		{"origine autorisée", "http://example.com", http.StatusOK},
		{"préfixe d'une origine autorisée", "http://example.co", http.StatusForbidden},
		{"fragment d'une origine autorisée", "example.com", http.StatusForbidden},
		{"origine non autorisée", "http://evil.com", http.StatusForbidden},
		{"domaine voisin non autorisé", "http://example.com.evil.com", http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api", nil)
			req.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if got := w.Result().StatusCode; got != tc.wantCode {
				t.Errorf("Origin %q: got %d, want %d", tc.origin, got, tc.wantCode)
			}
		})
	}

	// Une requête sans en-tête Origin n'est pas une requête CORS : elle doit
	// rester acceptée, sinon les appels serveur-à-serveur cassent.
	t.Run("sans en-tête Origin", func(t *testing.T) {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/api", nil))

		if got := w.Result().StatusCode; got != http.StatusOK {
			t.Errorf("sans en-tête Origin: got %d, want %d", got, http.StatusOK)
		}
	})
}

// 6. Test déterminant pour la coexistence entre route statique et catchAll
//
// insertChild remplaçait children par un slice d'un seul élément lors de
// l'enregistrement d'un catchAll, ce qui supprimait les routes statiques déjà
// enregistrées au même niveau. L'ordre d'enregistrement décidait donc de
// l'existence des routes.
func TestCatchAllKeepsStaticSibling(t *testing.T) {
	cases := []struct {
		name     string
		register func(router *Router)
	}{
		{
			name: "statique enregistrée avant le wildcard",
			register: func(router *Router) {
				router.Get("/files/special", func(c *Context) { c.Text("statique") })
				router.Get("/files/*path", func(c *Context) { c.Text("wildcard") })
			},
		},
		{
			name: "statique enregistrée après le wildcard",
			register: func(router *Router) {
				router.Get("/files/*path", func(c *Context) { c.Text("wildcard") })
				router.Get("/files/special", func(c *Context) { c.Text("statique") })
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := New()
			tc.register(router)

			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", "/files/special", nil))
			body, _ := io.ReadAll(w.Result().Body)

			if string(body) != "statique" {
				t.Errorf("GET /files/special: got %q, want %q", string(body), "statique")
			}
		})
	}

	// Le wildcard doit rester joignable pour ce qu'il couvre vraiment.
	t.Run("le wildcard sert toujours son segment", func(t *testing.T) {
		router := New()
		router.Get("/files/special", func(c *Context) { c.Text("statique") })
		router.Get("/files/*path", func(c *Context) { c.Text("wildcard") })

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/files/a/b/c", nil))
		body, _ := io.ReadAll(w.Result().Body)

		if string(body) != "wildcard" {
			t.Errorf("GET /files/a/b/c: got %q, want %q", string(body), "wildcard")
		}
	})
}

// 7. Test déterminant pour le segment vide : catchAll et :param se
// comportent différemment, et c'est voulu.
//
// Un catchAll consomme « le reste du chemin », donc une capture vide est
// valide : /files/ matche /files/*path avec une valeur vide. En revanche
// /files ne matche pas du tout, faute de slash à consommer.
//
// Un :param, lui, ne reçoit jamais de valeur vide : c'est ce qui permet à
// /users/ de ne pas matcher /users/:id et de basculer sur la redirection.
func TestEmptySegmentCatchAllAndParam(t *testing.T) {
	catchAll := New()
	catchAll.Get("/files/*path", func(c *Context) { c.Text("[" + c.Param("path") + "]") })

	catchAllCases := []struct {
		path     string
		wantCode int
		wantText string
	}{
		{"/files", http.StatusNotFound, "404 page not found\n"},
		{"/files/", http.StatusOK, "[]"},
		{"/files/a", http.StatusOK, "[a]"},
		{"/files/a/b", http.StatusOK, "[a/b]"},
	}
	for _, tc := range catchAllCases {
		t.Run("catchAll "+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			catchAll.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			body, _ := io.ReadAll(w.Result().Body)

			if got := w.Result().StatusCode; got != tc.wantCode {
				t.Errorf("status = %d, want %d", got, tc.wantCode)
			}
			if string(body) != tc.wantText {
				t.Errorf("body = %q, want %q", string(body), tc.wantText)
			}
		})
	}

	param := New()
	param.Get("/users", func(c *Context) { c.Text("LISTE") })
	param.Get("/users/:id", func(c *Context) { c.Text("DETAIL id=" + c.Param("id")) })

	reports := func(c *Context) { c.Text("REPORTS " + c.Param("id")) }
	// Enregistré sur les trois méthodes : la redirection après slash final est
	// 301 pour GET et 308 pour les autres, qui doivent rejouer le corps.
	param.Get("/users/:id/reports", reports)
	param.Post("/users/:id/reports", reports)
	param.Put("/users/:id/reports", reports)

	// Le handler :id ne doit jamais recevoir une valeur vide.
	w := httptest.NewRecorder()
	param.ServeHTTP(w, httptest.NewRequest("GET", "/users/", nil))
	if got := w.Result().StatusCode; got != http.StatusMovedPermanently {
		t.Errorf("GET /users/ : status = %d, want %d (le handler :id ne doit pas voir une valeur vide)",
			got, http.StatusMovedPermanently)
	}

	for _, tc := range []struct {
		method   string
		wantCode int
	}{
		{"GET", http.StatusMovedPermanently},
		{"POST", http.StatusPermanentRedirect},
		{"PUT", http.StatusPermanentRedirect},
	} {
		w = httptest.NewRecorder()
		param.ServeHTTP(w, httptest.NewRequest(tc.method, "/users/1/reports/", nil))
		if got := w.Result().StatusCode; got != tc.wantCode {
			t.Errorf("%s /users/1/reports/ : status = %d, want %d", tc.method, got, tc.wantCode)
		}
	}

	w = httptest.NewRecorder()
	param.ServeHTTP(w, httptest.NewRequest("GET", "/users/1", nil))
	body, _ := io.ReadAll(w.Result().Body)
	if string(body) != "DETAIL id=1" {
		t.Errorf("GET /users/1 : body = %q, want %q", string(body), "DETAIL id=1")
	}
}

// 8. Test déterminant pour la recommandation de redirection de Lookup()
//
// Lookup documente un troisième retour tsr indiquant s'il faut rediriger vers
// la même route avec ou sans slash final, précisément pour qui veut construire
// un framework par-dessus. Les quatre sorties de getHandler renvoient false.
func TestLookupReportsTrailingSlash(t *testing.T) {
	router := New()
	router.Get("/users", func(c *Context) { c.Text("users") })

	_, _, tsrWithSlash, _ := router.Lookup("GET", "/users/")
	if !tsrWithSlash {
		t.Error(`Lookup("GET", "/users/"): tsr = false, la route /users existe pourtant`)
	}

	_, _, tsrWithoutSlash, _ := router.Lookup("GET", "/users")
	if tsrWithoutSlash {
		t.Error(`Lookup("GET", "/users"): tsr = true alors que la route correspond`)
	}
}

// 9. Point de décision : ordre d'exécution des middlewares
//
// Handler() compose la chaîne de façon inversée par rapport à la convention
// (Express, Chi, chaînage standard) : le dernier middleware enregistré est le
// plus externe. Ce test énonce la convention usuelle, donc il échoue. Corriger
// l'implémentation le fera passer ; le garder tel quel impose d'inverser ce
// test et d'assumer la divergence.
func TestMiddlewareOrderFollowsRegistration(t *testing.T) {
	router := New()

	var order []string
	mw := func(name string) func(http.Handler) http.Handler {
		return func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				h.ServeHTTP(w, r)
			})
		}
	}

	router.Use(mw("A"))
	router.Use(mw("B"))
	router.Get("/", func(c *Context) { c.Text("ok") })

	order = nil
	router.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	want := []string{"A", "B"}
	if !slices.Equal(order, want) {
		t.Errorf("ordre d'exécution = %v, want %v (le premier enregistré doit envelopper le suivant)", order, want)
	}
}

func TestCORSMiddleware(t *testing.T) {
	// 1. Test nil-safe initialization before router
	corsMid := Cors("http://localhost:3000", "https://app.example.com", "api.example.com")
	router := New()
	router.Use(corsMid)

	router.Get("/items", func(c *Context) {
		c.Text("items-list")
	})

	handler := router.Handler()

	// A. Preflight OPTIONS request for http://localhost:3000
	wOpt := httptest.NewRecorder()
	reqOpt := httptest.NewRequest("OPTIONS", "/items", nil)
	reqOpt.Header.Set("Origin", "http://localhost:3000")
	reqOpt.Header.Set("Access-Control-Request-Method", "GET")
	reqOpt.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
	handler.ServeHTTP(wOpt, reqOpt)

	if wOpt.Code != http.StatusNoContent {
		t.Errorf("OPTIONS expected status 204, got %d", wOpt.Code)
	}
	if wOpt.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Errorf("OPTIONS expected origin http://localhost:3000, got %s", wOpt.Header().Get("Access-Control-Allow-Origin"))
	}
	if wOpt.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("OPTIONS expected credentials true, got %s", wOpt.Header().Get("Access-Control-Allow-Credentials"))
	}
	if wOpt.Header().Get("Access-Control-Allow-Headers") != "Authorization, Content-Type" {
		t.Errorf("OPTIONS expected requested headers echoed, got %s", wOpt.Header().Get("Access-Control-Allow-Headers"))
	}

	// B. Regular GET from second allowed origin https://app.example.com
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/items", nil)
	req2.Header.Set("Origin", "https://app.example.com")
	handler.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("GET expected status 200, got %d", w2.Code)
	}
	if w2.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Errorf("GET expected origin https://app.example.com, got %s", w2.Header().Get("Access-Control-Allow-Origin"))
	}

	// C. Regular GET from scheme-less origin api.example.com
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/items", nil)
	req3.Header.Set("Origin", "https://api.example.com")
	handler.ServeHTTP(w3, req3)

	if w3.Header().Get("Access-Control-Allow-Origin") != "https://api.example.com" {
		t.Errorf("GET expected origin https://api.example.com, got %s", w3.Header().Get("Access-Control-Allow-Origin"))
	}

	// D. Request from disallowed origin
	wDis := httptest.NewRecorder()
	reqDis := httptest.NewRequest("GET", "/items", nil)
	reqDis.Header.Set("Origin", "https://malicious.com")
	handler.ServeHTTP(wDis, reqDis)

	if wDis.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("Expected empty Allow-Origin for disallowed origin, got %s", wDis.Header().Get("Access-Control-Allow-Origin"))
	}

	// E. Preflight from disallowed origin should return 403 Forbidden
	wOptDis := httptest.NewRecorder()
	reqOptDis := httptest.NewRequest("OPTIONS", "/items", nil)
	reqOptDis.Header.Set("Origin", "https://malicious.com")
	handler.ServeHTTP(wOptDis, reqOptDis)

	if wOptDis.Code != http.StatusForbidden {
		t.Errorf("Disallowed preflight expected 403, got %d", wOptDis.Code)
	}
}

func TestCORSWildcard(t *testing.T) {
	router := New()
	router.Use(Cors("*"))

	router.Get("/public", func(c *Context) {
		c.Text("public-content")
	})

	handler := router.Handler()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/public", nil)
	req.Header.Set("Origin", "https://anydomain.org")
	handler.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "https://anydomain.org" {
		t.Errorf("Wildcard CORS expected origin echoed for credentials compatibility, got %s", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

