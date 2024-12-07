package bench

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kamalshkeir/ksmux"
)

// BenchmarkKsmux-4                           49567             25296 ns/op           51237 B/op         90 allocs/op
// BenchmarkKsmux-4                           43378             27946 ns/op           51236 B/op         90 allocs/op
// BenchmarkKsmux-4                           46275             28272 ns/op           51236 B/op         90 allocs/op
// BenchmarkKsmuxParallel-4                   51164             24343 ns/op           51242 B/op         90 allocs/op
// BenchmarkKsmuxParallel-4                   54880             21746 ns/op           51243 B/op         90 allocs/op
// BenchmarkKsmuxParallel-4                   54429             21866 ns/op           51242 B/op         90 allocs/op
// BenchmarkGin-4                             39356             27099 ns/op           51232 B/op         90 allocs/op
// BenchmarkGin-4                             47739             26824 ns/op           51232 B/op         90 allocs/op
// BenchmarkGin-4                             48643             26622 ns/op           51233 B/op         90 allocs/op
// BenchmarkGinParallel-4                     51730             21755 ns/op           51235 B/op         90 allocs/op
// BenchmarkGinParallel-4                     52563             21442 ns/op           51237 B/op         90 allocs/op
// BenchmarkGinParallel-4                     54391             21821 ns/op           51236 B/op         90 allocs/op
// BenchmarkStdRouter-4                       38364             30295 ns/op           51513 B/op        107 allocs/op
// BenchmarkStdRouter-4                       40028             29439 ns/op           51513 B/op        107 allocs/op
// BenchmarkStdRouter-4                       42261             28101 ns/op           51513 B/op        107 allocs/op
// BenchmarkStdRouterParallel-4               48412             24717 ns/op           51517 B/op        107 allocs/op
// BenchmarkStdRouterParallel-4               47328             24524 ns/op           51518 B/op        107 allocs/op
// BenchmarkStdRouterParallel-4               47215             23905 ns/op           51518 B/op        107 allocs/op
// BenchmarkHttprouter-4                      51360             26363 ns/op           51505 B/op         98 allocs/op
// BenchmarkHttprouter-4                      40518             26867 ns/op           51505 B/op         98 allocs/op
// BenchmarkHttprouter-4                      44841             28087 ns/op           51505 B/op         98 allocs/op
// BenchmarkHttprouterParallel-4              52129             21178 ns/op           51510 B/op         98 allocs/op
// BenchmarkHttprouterParallel-4              53569             21499 ns/op           51510 B/op         98 allocs/op
// BenchmarkHttprouterParallel-4              54886             21418 ns/op           51510 B/op         98 allocs/op

type route struct {
	method string
	path   string
}

// Common test routes for both routers
var routes = []route{
	{"GET", "/"},
	{"GET", "/users/:id"},
	{"GET", "/users/:id/posts"},
	{"GET", "/posts/:id"},
	{"GET", "/api/:version/users/:id/profile"},
	{"POST", "/users"},
	{"POST", "/posts/:id"},
	{"PUT", "/users/:id"},
	{"DELETE", "/users/:id"},
	{"GET", "/files/*filepath"},
}

var requests = []route{
	{"GET", "/"},
	{"GET", "/users/123"},
	{"GET", "/users/123/posts"},
	{"GET", "/posts/456"},
	{"GET", "/api/v1/users/123/profile"},
	{"POST", "/users"},
	{"POST", "/posts/123"},
	{"PUT", "/users/123"},
	{"DELETE", "/users/123"},
	{"GET", "/files/css/style.css"},
}

// Routes for standard library router using Go 1.22 pattern syntax
var stdRoutes = []route{
	{"GET", "/"},
	{"GET", "/users/{id}"},
	{"GET", "/users/{id}/posts"},
	{"GET", "/api/{version}/users/{id}/profile"},
	{"POST", "/users"},
	{"GET", "/files/{filepath...}"}, // Using ... for catch-all wildcard
}

// func BenchmarkGin(b *testing.B) {
// 	gin.SetMode(gin.ReleaseMode)
// 	router := gin.New()
// 	handle := func(c *gin.Context) {}

// 	for _, r := range routes {
// 		router.Handle(r.method, r.path, handle)
// 	}

// 	w := httptest.NewRecorder()
// 	b.ResetTimer()

// 	for i := 0; i < b.N; i++ {
// 		for _, r := range requests {
// 			req := httptest.NewRequest(r.method, r.path, nil)
// 			router.ServeHTTP(w, req)
// 		}
// 	}
// }

// func BenchmarkGinParallel(b *testing.B) {
// 	gin.SetMode(gin.ReleaseMode)
// 	router := gin.New()
// 	handle := func(c *gin.Context) {}

// 	for _, r := range routes {
// 		router.Handle(r.method, r.path, handle)
// 	}

// 	b.ResetTimer()

// 	b.RunParallel(func(pb *testing.PB) {
// 		w := httptest.NewRecorder()
// 		for pb.Next() {
// 			for _, r := range requests {
// 				req := httptest.NewRequest(r.method, r.path, nil)
// 				router.ServeHTTP(w, req)
// 			}
// 		}
// 	})
// }

func BenchmarkStdRouter(b *testing.B) {
	router := http.NewServeMux()
	handle := func(w http.ResponseWriter, r *http.Request) {}

	for _, r := range stdRoutes {
		router.HandleFunc(r.path, handle)
	}

	w := httptest.NewRecorder()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, r := range requests {
			req := httptest.NewRequest(r.method, r.path, nil)
			router.ServeHTTP(w, req)
		}
	}
}

func BenchmarkStdRouterParallel(b *testing.B) {
	router := http.NewServeMux()
	handle := func(w http.ResponseWriter, r *http.Request) {}

	for _, r := range stdRoutes {
		router.HandleFunc(r.path, handle)
	}

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		w := httptest.NewRecorder()
		for pb.Next() {
			for _, r := range requests {
				req := httptest.NewRequest(r.method, r.path, nil)
				router.ServeHTTP(w, req)
			}
		}
	})
}

func BenchmarkKsmux(b *testing.B) {
	router := ksmux.New()
	for _, r := range routes {
		router.Handle(r.method, r.path, func(c *ksmux.Context) {})
	}

	w := httptest.NewRecorder()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, r := range requests {
			req := httptest.NewRequest(r.method, r.path, nil)
			router.ServeHTTP(w, req)
		}
	}
}

func BenchmarkKsmuxParallel(b *testing.B) {
	router := ksmux.New()

	for _, r := range routes {
		router.Handle(r.method, r.path, func(c *ksmux.Context) {})
	}

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		w := httptest.NewRecorder()
		for pb.Next() {
			for _, r := range requests {
				req := httptest.NewRequest(r.method, r.path, nil)
				router.ServeHTTP(w, req)
			}
		}
	})
}

// func BenchmarkHttprouter(b *testing.B) {
// 	router := httprouter.New()
// 	handle := func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {}

// 	for _, r := range routes {
// 		router.Handle(r.method, r.path, handle)
// 	}

// 	w := httptest.NewRecorder()

// 	b.ResetTimer()

// 	for i := 0; i < b.N; i++ {
// 		for _, r := range requests {
// 			req := httptest.NewRequest(r.method, r.path, nil)
// 			router.ServeHTTP(w, req)
// 		}
// 	}
// }

// func BenchmarkHttprouterParallel(b *testing.B) {
// 	router := httprouter.New()
// 	handle := func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {}

// 	for _, r := range routes {
// 		router.Handle(r.method, r.path, handle)
// 	}

// 	b.ResetTimer()

// 	b.RunParallel(func(pb *testing.PB) {
// 		w := httptest.NewRecorder()
// 		for pb.Next() {
// 			for _, r := range requests {
// 				req := httptest.NewRequest(r.method, r.path, nil)
// 				router.ServeHTTP(w, req)
// 			}
// 		}
// 	})
// }
