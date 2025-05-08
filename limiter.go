package ksmux

import (
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/kamalshkeir/kmap"
	"golang.org/x/time/rate"
)

// LimitStrategy defines how we identify requests for rate limiting
type LimitStrategy int

const (
	LimitByIP LimitStrategy = iota
	LimitByPath
	LimitByHeader
	LimitByIPAndPath
)

type limiterClient struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

var (
	limited          = kmap.New[string, *limiterClient]()
	limiterQuit      chan struct{}
	defCheckEvery    = 5 * time.Minute
	defBlockDuration = 10 * time.Minute
	defRateEvery     = 10 * time.Minute
	defBurstsN       = 100
	defMessage       = "TOO MANY REQUESTS"
)

type PathConfig struct {
	Pattern   string        // Path pattern to match (supports wildcards with *)
	RateEvery time.Duration // Rate limit for this path
	BurstsN   int           // Burst limit for this path
}

type ConfigLimiter struct {
	Message        string           // default "TOO MANY REQUESTS"
	RateEvery      time.Duration    // default 10 min
	BurstsN        int              // default 100
	CheckEvery     time.Duration    // default 5 min
	BlockDuration  time.Duration    // default 10 min
	OnLimitReached func(c *Context) // Custom handler for when limit is reached
	Strategy       LimitStrategy    // Strategy for rate limiting
	HeaderName     string           // Header name for LimitByHeader strategy
	PathConfigs    []PathConfig     // Path-specific configurations
}

func getPathConfig(path string, configs []PathConfig) *PathConfig {
	for _, conf := range configs {
		if conf.Pattern == path {
			return &conf
		}
		// Handle wildcard patterns
		if strings.Contains(conf.Pattern, "*") {
			pattern := strings.ReplaceAll(conf.Pattern, "*", ".*")
			matched, err := regexp.MatchString("^"+pattern+"$", path)
			if err == nil && matched {
				return &conf
			}
		}
	}
	return nil
}

func getLimiterKey(c *Context, strategy LimitStrategy, headerName string) string {
	switch strategy {
	case LimitByPath:
		return c.Request.URL.Path
	case LimitByHeader:
		return c.Request.Header.Get(headerName)
	case LimitByIPAndPath:
		ip, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
		return ip + ":" + c.Request.URL.Path
	default: // LimitByIP
		ip, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
		return ip
	}
}

func Limiter(conf *ConfigLimiter) func(http.Handler) http.Handler {
	if conf == nil {
		conf = &ConfigLimiter{
			CheckEvery:    defCheckEvery,
			BlockDuration: defBlockDuration,
			RateEvery:     defRateEvery,
			BurstsN:       defBurstsN,
			Message:       defMessage,
			Strategy:      LimitByIP,
		}
	} else {
		if conf.CheckEvery == 0 {
			conf.CheckEvery = defCheckEvery
		}
		if conf.BlockDuration == 0 {
			conf.BlockDuration = defBlockDuration
		}
		if conf.RateEvery == 0 {
			conf.RateEvery = defRateEvery
		}
		if conf.BurstsN == 0 {
			conf.BurstsN = defBurstsN
		}
		if conf.Message == "" {
			conf.Message = defMessage
		}
		if conf.OnLimitReached == nil {
			conf.OnLimitReached = func(c *Context) {
				c.Status(http.StatusTooManyRequests).Text(conf.Message)
			}
		}
	}

	ticker := time.NewTicker(conf.CheckEvery)

	limiterQuit = make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				limited.Range(func(key string, value *limiterClient) bool {
					if time.Since(value.lastSeen) > conf.BlockDuration {
						go limited.Delete(key)
					}
					return true
				})
			case <-limiterQuit:
				ticker.Stop()
				return
			}
		}
	}()

	return func(handler http.Handler) http.Handler {
		return Handler(func(c *Context) {
			key := getLimiterKey(c, conf.Strategy, conf.HeaderName)
			if key == "" {
				c.SetStatus(http.StatusInternalServerError)
				return
			}

			var ll *rate.Limiter
			rateEvery := conf.RateEvery
			burstsN := conf.BurstsN

			// Check for path-specific configuration
			if len(conf.PathConfigs) > 0 {
				if pathConf := getPathConfig(c.Request.URL.Path, conf.PathConfigs); pathConf != nil {
					rateEvery = pathConf.RateEvery
					burstsN = pathConf.BurstsN
				}
			}

			if lim, found := limited.Get(key); !found {
				ll = rate.NewLimiter(rate.Every(rateEvery), burstsN)
			} else {
				ll = lim.limiter
			}

			limited.Set(key, &limiterClient{
				limiter:  ll,
				lastSeen: time.Now(),
			})

			if !ll.Allow() {
				conf.OnLimitReached(c)
				return
			}

			handler.ServeHTTP(c.ResponseWriter, c.Request)
		})
	}
}

// // Path-based rate limiting example
// router.Use(ksmux.Limiter(&ksmux.ConfigLimiter{
//     Strategy: ksmux.LimitByPath,
//     PathConfigs: []ksmux.PathConfig{
//         {
//             Pattern: "/api/*",    // All API routes
//             RateEvery: time.Second,
//             BurstsN: 10,         // 10 requests per second
//         },
//         {
//             Pattern: "/static/*", // Static files
//             RateEvery: time.Minute,
//             BurstsN: 1000,       // 1000 requests per minute
//         },
//     },
// }))

// // API key based limiting
// router.Use(ksmux.Limiter(&ksmux.ConfigLimiter{
//     Strategy: ksmux.LimitByHeader,
//     HeaderName: "X-API-Key",
//     RateEvery: time.Hour,
//     BurstsN: 1000,  // 1000 requests per hour per API key
// }))
