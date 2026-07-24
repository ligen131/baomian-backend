package metrics

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

type Registry struct {
	mu       sync.Mutex
	requests map[string]uint64
	latency  map[string]float64
	ws       atomic.Int64
}

func New() *Registry {
	return &Registry{requests: make(map[string]uint64), latency: make(map[string]float64)}
}

func (r *Registry) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		key := c.Request.Method + "\x00" + c.FullPath() + "\x00" + strconv.Itoa(c.Writer.Status())
		r.mu.Lock()
		r.requests[key]++
		r.latency[key] += time.Since(started).Seconds()
		r.mu.Unlock()
	}
}

func (r *Registry) WebSocketConnected() func() {
	r.ws.Add(1)
	return func() { r.ws.Add(-1) }
}

func (r *Registry) Handler(c *gin.Context) {
	c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	c.Status(http.StatusOK)
	_, _ = fmt.Fprintln(c.Writer, "# HELP baomian_http_requests_total HTTP 请求总数。")
	_, _ = fmt.Fprintln(c.Writer, "# TYPE baomian_http_requests_total counter")
	_, _ = fmt.Fprintln(c.Writer, "# HELP baomian_http_request_duration_seconds_sum HTTP 请求累计耗时。")
	_, _ = fmt.Fprintln(c.Writer, "# TYPE baomian_http_request_duration_seconds_sum counter")
	r.mu.Lock()
	for key, count := range r.requests {
		method, path, status := splitKey(key)
		labels := fmt.Sprintf("method=%q,path=%q,status=%q", method, path, status)
		_, _ = fmt.Fprintf(c.Writer, "baomian_http_requests_total{%s} %d\n", labels, count)
		_, _ = fmt.Fprintf(c.Writer, "baomian_http_request_duration_seconds_sum{%s} %g\n", labels, r.latency[key])
	}
	r.mu.Unlock()
	_, _ = fmt.Fprintln(c.Writer, "# HELP baomian_websocket_connections 当前 WebSocket 连接数。")
	_, _ = fmt.Fprintln(c.Writer, "# TYPE baomian_websocket_connections gauge")
	_, _ = fmt.Fprintf(c.Writer, "baomian_websocket_connections %d\n", r.ws.Load())
}

func splitKey(key string) (string, string, string) {
	parts := [3]string{}
	part := 0
	start := 0
	for i := 0; i < len(key) && part < 2; i++ {
		if key[i] == 0 {
			parts[part] = key[start:i]
			part++
			start = i + 1
		}
	}
	parts[part] = key[start:]
	return parts[0], parts[1], parts[2]
}
