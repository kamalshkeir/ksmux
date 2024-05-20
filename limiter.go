package ksmux

import (
	"net"
	"net/http"
	"time"

	"github.com/kamalshkeir/kmap"
	"github.com/kamalshkeir/lg"
	"golang.org/x/time/rate"
)

type limiterClient struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

var (
	limited          = kmap.New[string, *limiterClient]()
	limiterQuit      chan struct{}
	limiterUsed      = false
	defCheckEvery    = 5 * time.Minute
	defBlockDuration = 10 * time.Minute
	defRateEvery     = 10 * time.Minute
	defBurstsN       = 100
	defMessage       = "TOO MANY REQUESTS"
)

type ConfigLimiter struct {
	Message       string        // default "TOO MANY REQUESTS"
	RateEvery     time.Duration // default 10 min
	BurstsN       int           // default 100
	CheckEvery    time.Duration // default 5 min
	BlockDuration time.Duration // default 10 min
}

func Limiter(conf *ConfigLimiter) func(http.Handler) http.Handler {
	if conf == nil {
		conf = &ConfigLimiter{
			CheckEvery:    defCheckEvery,
			BlockDuration: defBlockDuration,
			RateEvery:     defRateEvery,
			BurstsN:       defBurstsN,
			Message:       defMessage,
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
	limiterUsed = true
	return func(handler http.Handler) http.Handler {
		return Handler(func(c *Context) {
			ip, _, err := net.SplitHostPort(c.Request.RemoteAddr)
			if lg.CheckError(err) {
				c.SetStatus(http.StatusInternalServerError)
				return
			}
			var ll *rate.Limiter
			if lim, found := limited.Get(ip); !found {
				ll = rate.NewLimiter(rate.Every(conf.RateEvery), conf.BurstsN)
			} else {
				ll = lim.limiter
			}
			limited.Set(ip, &limiterClient{
				limiter:  ll,
				lastSeen: time.Now(),
			})
			if !ll.Allow() {
				c.Status(http.StatusTooManyRequests).Text(conf.Message)
				return
			}
			handler.ServeHTTP(c.ResponseWriter, c.Request)
		})
	}
}
