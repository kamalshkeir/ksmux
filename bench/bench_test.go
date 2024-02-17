package bench

// import (
// 	"net/http"
// 	"net/http/httptest"
// 	"testing"

// 	"github.com/gin-gonic/gin"
// 	"github.com/go-chi/chi"
// 	"github.com/julienschmidt/httprouter"
// 	"github.com/kamalshkeir/kmux"
// 	ks "github.com/kamalshkeir/ksmux"
// 	"github.com/labstack/echo/v4"
// )

// func BenchmarkKmux(b *testing.B) {
// 	app := kmux.New()
// 	app.Get("/test/bla/hello/bye", func(c *kmux.Context) { c.WriteHeader(200) })

// 	req := httptest.NewRequest("GET", "/test/bla/hello/bye", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }
// func BenchmarkChi(b *testing.B) {
// 	app := chi.NewRouter()
// 	app.Get("/test/bla/hello/bye", func(w http.ResponseWriter, r *http.Request) {
// 		// w.WriteHeader(200)
// 		// w.Header().Add("Content-Type", "application/json")
// 		// _ = json.NewEncoder(w).Encode(map[string]any{
// 		// 	"data": "hello world",
// 		// })
// 		w.WriteHeader(200)
// 	})

// 	req := httptest.NewRequest("GET", "/test/bla/hello/bye", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }
// func BenchmarkJulien(b *testing.B) {
// 	app := httprouter.New()
// 	app.GET("/test/bla/hello/bye", func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
// 		w.WriteHeader(200)
// 		// w.Header().Add("Content-Type", "application/json")
// 		// _ = json.NewEncoder(w).Encode(map[string]any{
// 		// 	"data": "hello world",
// 		// })
// 	})

// 	req := httptest.NewRequest("GET", "/test/bla/hello/bye", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkKS(b *testing.B) {
// 	app := ks.New()
// 	app.Get("/test/bla/hello/bye", func(c *ks.Context) {
// 		c.WriteHeader(200)
// 		// c.Json(map[string]any{
// 		// 	"data": "hello world",
// 		// })
// 	})

// 	req := httptest.NewRequest("GET", "/test/bla/hello/bye", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkNetHTTP(b *testing.B) {
// 	mux := http.NewServeMux()
// 	mux.HandleFunc("GET /test/bla/hello/bye", func(w http.ResponseWriter, r *http.Request) {
// 		w.WriteHeader(200)
// 		// w.Header().Set("Content-Type", "application/json")
// 		// _ = json.NewEncoder(w).Encode(map[string]any{
// 		// 	"data": "hello world",
// 		// })
// 	})

// 	req := httptest.NewRequest("GET", "/test/bla/hello/bye", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		mux.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkGin(b *testing.B) {
// 	gin.SetMode(gin.ReleaseMode)
// 	app := gin.New()
// 	app.GET("/test/bla/hello/bye", func(ctx *gin.Context) {
// 		ctx.Status(200)
// 		// ctx.JSON(200, map[string]any{
// 		// 	"data": "hello world",
// 		// })
// 	})

// 	req := httptest.NewRequest("GET", "/test/bla/hello/bye", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkEcho(b *testing.B) {
// 	app := echo.New()
// 	app.GET("/test/bla/hello/bye", func(c echo.Context) error {
// 		c.Response().WriteHeader(http.StatusOK)
// 		return nil
// 	})

// 	req := httptest.NewRequest("GET", "/test/bla/hello/bye", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// 	b.Log("--------------------------------------------------")
// }

// func BenchmarkKmuxWithParam(b *testing.B) {
// 	app := kmux.New()
// 	app.Get("/test/:something", func(c *kmux.Context) {
// 		c.WriteHeader(200)
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkChiWithParam(b *testing.B) {
// 	app := chi.NewRouter()
// 	app.Get("/test/{something}", func(w http.ResponseWriter, r *http.Request) {
// 		w.WriteHeader(200)
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkJulienWithParam(b *testing.B) {
// 	app := httprouter.New()
// 	app.GET("/test/:something", func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
// 		w.WriteHeader(200)
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkKSWithParam(b *testing.B) {
// 	app := ks.New()
// 	app.Get("/test/:something", func(c *ks.Context) {
// 		c.WriteHeader(200)
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkNetHTTPWithParam(b *testing.B) {
// 	mux := http.NewServeMux()
// 	mux.HandleFunc("GET /test/{something}/{$}", func(w http.ResponseWriter, r *http.Request) {
// 		w.WriteHeader(200)
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		mux.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkGinWithParam(b *testing.B) {
// 	gin.SetMode(gin.ReleaseMode)
// 	app := gin.New()
// 	app.GET("/test/:something", func(ctx *gin.Context) {
// 		ctx.Status(200)
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkEchoWithParam(b *testing.B) {
// 	app := echo.New()
// 	app.GET("/test/:something", func(c echo.Context) error {
// 		c.Response().WriteHeader(200)
// 		return nil
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// 	b.Log("--------------------------------------------------")
// }

// func BenchmarkKmuxWith2Param(b *testing.B) {
// 	app := kmux.New()
// 	app.Get("/test/:something/:another", func(c *kmux.Context) {
// 		c.WriteHeader(200)
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1/more", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkKSWith2Param(b *testing.B) {
// 	app := ks.New()
// 	app.Get("/test/:something/:another", func(c *ks.Context) {
// 		c.WriteHeader(200)
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1/more", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkJulienWith2Param(b *testing.B) {
// 	app := httprouter.New()
// 	app.GET("/test/:something/:another", func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
// 		w.WriteHeader(200)
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1/more", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkNetHTTPWith2Param(b *testing.B) {
// 	mux := http.NewServeMux()
// 	mux.HandleFunc("GET /test/{something}/{another}/{$}", func(w http.ResponseWriter, r *http.Request) {
// 		w.WriteHeader(200)
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1/more", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		mux.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkGinWith2Param(b *testing.B) {
// 	gin.SetMode(gin.ReleaseMode)
// 	app := gin.New()
// 	app.GET("/test/:something/:another", func(ctx *gin.Context) {
// 		ctx.Status(200)
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1/more", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkEchoWith2Param(b *testing.B) {
// 	app := echo.New()
// 	app.GET("/test/:something/:another", func(c echo.Context) error {
// 		c.Response().WriteHeader(200)
// 		return nil
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1/more", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// 	b.Log("--------------------------------------------------")
// }

// func BenchmarkKmuxWith5Param(b *testing.B) {
// 	app := kmux.New()
// 	app.Get("/test/:first/:second/:third/:fourth/:fifth", func(c *kmux.Context) {
// 		c.WriteHeader(200)
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1/more/one/two/three", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkKSWith5Param(b *testing.B) {
// 	app := ks.New()
// 	app.Get("/test/:first/:second/:third/:fourth/:fifth", func(c *ks.Context) {
// 		c.WriteHeader(200)
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1/more/one/two/three", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkJulienWith5Param(b *testing.B) {
// 	app := httprouter.New()
// 	app.GET("/test/:first/:second/:third/:fourth/:fifth", func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
// 		w.WriteHeader(200)
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1/more/one/two/three", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkNetHTTPWith5Param(b *testing.B) {
// 	mux := http.NewServeMux()
// 	mux.HandleFunc("GET /test/{first}/{second}/{third}/{fourth}/{fifth}/{$}", func(w http.ResponseWriter, r *http.Request) {
// 		w.WriteHeader(200)
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1/more/one/two/three", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		mux.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkGinWith5Param(b *testing.B) {
// 	gin.SetMode(gin.ReleaseMode)
// 	app := gin.New()
// 	app.GET("/test/:first/:second/:third/:fourth/:fifth", func(ctx *gin.Context) {
// 		ctx.Status(200)
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1/more/one/two/three", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }

// func BenchmarkEchoWith5Param(b *testing.B) {
// 	app := echo.New()
// 	app.GET("/test/:first/:second/:third/:fourth/:fifth", func(c echo.Context) error {
// 		c.Response().WriteHeader(200)
// 		return nil
// 	})

// 	req := httptest.NewRequest("GET", "/test/user1/more/one/two/three", nil)
// 	w := httptest.NewRecorder()

// 	for i := 0; i < b.N; i++ {
// 		app.ServeHTTP(w, req)
// 	}
// }

// // goos: windows
// // goarch: amd64
// // pkg: github.com/kamalshkeir/ksmux/bench
// // cpu: Intel(R) Core(TM) i5-7300HQ CPU @ 2.50GHz
// // BenchmarkKmux-4                  9724984               121.0 ns/op            24 B/op          1 allocs/op
// // BenchmarkChi-4                   3180050               336.8 ns/op           336 B/op          2 allocs/op
// // BenchmarkKS-4                   28551510                38.00 ns/op            0 B/op          0 allocs/op
// // BenchmarkNetHTTP-4               2085325               570.6 ns/op           112 B/op          3 allocs/op
// // BenchmarkGin-4                  22135360                48.94 ns/op            0 B/op          0 allocs/op
// // BenchmarkEcho-4                 14723275                73.28 ns/op            0 B/op          0 allocs/op
// // --- BENCH: BenchmarkEcho-4
// //     bench_test.go:129: --------------------------------------------------
// //     bench_test.go:129: --------------------------------------------------
// //     bench_test.go:129: --------------------------------------------------
// //     bench_test.go:129: --------------------------------------------------
// //     bench_test.go:129: --------------------------------------------------
// // BenchmarkKmuxWithParam-4         7867820               159.2 ns/op            16 B/op          1 allocs/op
// // BenchmarkChiWithParam-4          2792502               400.2 ns/op           336 B/op          2 allocs/op
// // BenchmarkKSWithParam-4          21564194                62.28 ns/op            0 B/op          0 allocs/op
// // BenchmarkNetHTTPWithParam-4      2107048               541.4 ns/op           111 B/op          3 allocs/op
// // BenchmarkGinWithParam-4         22987933                61.22 ns/op            0 B/op          0 allocs/op
// // BenchmarkEchoWithParam-4        19582468                69.10 ns/op            0 B/op          0 allocs/op
// // --- BENCH: BenchmarkEchoWithParam-4
// //     bench_test.go:230: --------------------------------------------------
// //     bench_test.go:230: --------------------------------------------------
// //     bench_test.go:230: --------------------------------------------------
// //     bench_test.go:230: --------------------------------------------------
// //     bench_test.go:230: --------------------------------------------------
// // BenchmarkKmuxWith2Param-4        6796736               174.0 ns/op            24 B/op          1 allocs/op
// // BenchmarkKSWith2Param-4         15564121                70.02 ns/op            0 B/op          0 allocs/op
// // BenchmarkNetHTTPWith2Param-4     2057446               554.2 ns/op           113 B/op          3 allocs/op
// // BenchmarkGinWith2Param-4        14700008                72.74 ns/op            0 B/op          0 allocs/op
// // BenchmarkEchoWith2Param-4       14306100                79.51 ns/op            0 B/op          0 allocs/op
// // --- BENCH: BenchmarkEchoWith2Param-4
// //     bench_test.go:317: --------------------------------------------------
// //     bench_test.go:317: --------------------------------------------------
// //     bench_test.go:317: --------------------------------------------------
// //     bench_test.go:317: --------------------------------------------------
// //     bench_test.go:317: --------------------------------------------------
// // BenchmarkKmuxWith5Param-4        5611684               215.4 ns/op            32 B/op          1 allocs/op
// // BenchmarkKSWith5Param-4         10747718               102.9 ns/op             0 B/op          0 allocs/op
// // BenchmarkNetHTTPWith5Param-4     1713324               645.8 ns/op           231 B/op          6 allocs/op
// // BenchmarkGinWith5Param-4        10685263               115.9 ns/op             0 B/op          0 allocs/op
// // BenchmarkEchoWith5Param-4        9887089               128.1 ns/op             0 B/op          0 allocs/op
// // PASS
// // ok      github.com/kamalshkeir/ksmux/bench      35.793s
