package ksmux

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ExportType defines the type of trace exporter
type ExportType string

const (
	// Export types
	ExportTypeJaeger    ExportType = "jaeger"
	ExportTypeTempo     ExportType = "tempo"
	ExportTypeZipkin    ExportType = "zipkin"
	ExportTypeDatadog   ExportType = "datadog"
	ExportTypeNewRelic  ExportType = "newrelic"
	ExportTypeHoneycomb ExportType = "honeycomb"
	ExportTypeOTLP      ExportType = "otlp"   // OpenTelemetry Protocol
	ExportTypeSignoz    ExportType = "signoz" // Open source alternative

	// Default endpoints
	DefaultJaegerEndpoint    = "http://localhost:14268/api/traces"
	DefaultTempoEndpoint     = "http://localhost:9411/api/v2/spans"
	DefaultZipkinEndpoint    = "http://localhost:9411/api/v2/spans"
	DefaultDatadogEndpoint   = "https://trace.agent.datadoghq.com"
	DefaultNewRelicEndpoint  = "https://trace-api.newrelic.com/trace/v1"
	DefaultHoneycombEndpoint = "https://api.honeycomb.io/1/traces"
	DefaultOTLPEndpoint      = "http://localhost:4318/v1/traces"
	DefaultSignozEndpoint    = "http://localhost:4318/v1/traces"

	// DefaultMaxTraces is the default maximum number of traces to keep in memory
	DefaultMaxTraces = 500
)

// Span represents a single operation within a trace
type Span struct {
	traceID    string
	spanID     string
	parentID   string
	name       string    // Operation name
	startTime  time.Time // Start time of the operation
	endTime    time.Time // End time of the operation
	duration   time.Duration
	tags       map[string]string // Additional metadata
	statusCode int               // HTTP status code
	err        error             // Error if any
}

// Getter methods
func (s *Span) TraceID() string         { return s.traceID }
func (s *Span) SpanID() string          { return s.spanID }
func (s *Span) ParentID() string        { return s.parentID }
func (s *Span) Name() string            { return s.name }
func (s *Span) StartTime() time.Time    { return s.startTime }
func (s *Span) EndTime() time.Time      { return s.endTime }
func (s *Span) Duration() time.Duration { return s.duration }
func (s *Span) Tags() map[string]string { return s.tags }
func (s *Span) StatusCode() int         { return s.statusCode }
func (s *Span) Error() error            { return s.err }

// Tracer handles the creation and management of traces
type Tracer struct {
	mu             sync.RWMutex
	spans          map[string][]*Span
	isEnabled      bool
	handler        TraceHandler
	exportEndpoint string
	exportType     ExportType
	maxTraces      int
}

// TraceHandler is an interface for custom trace handling
type TraceHandler interface {
	HandleTrace(span *Span)
}

var (
	globalTracer = &Tracer{
		spans:     make(map[string][]*Span),
		maxTraces: DefaultMaxTraces,
	}
)

// EnableTracing enables tracing with an optional custom handler
func EnableTracing(handler TraceHandler) {
	globalTracer.mu.Lock()
	defer globalTracer.mu.Unlock()

	globalTracer.isEnabled = true
	globalTracer.handler = handler
}

// DisableTracing disables tracing
func DisableTracing() {
	globalTracer.mu.Lock()
	defer globalTracer.mu.Unlock()

	globalTracer.isEnabled = false
}

const (
	traceIDKey ContextKey = "trace_id"
	spanIDKey  ContextKey = "span_id"
)

// StartSpan creates a new span
func StartSpan(ctx context.Context, name string) (*Span, context.Context) {
	if !globalTracer.isEnabled {
		return nil, ctx
	}

	traceID := ctx.Value(traceIDKey)
	if traceID == nil {
		traceID = GenerateID()
	}

	parentID := ctx.Value(spanIDKey)
	spanID := GenerateID()

	// Initialize parentID string
	var parentIDStr string
	if parentID != nil {
		parentIDStr = parentID.(string)
	}

	span := &Span{
		traceID:   traceID.(string),
		spanID:    spanID,
		parentID:  parentIDStr,
		name:      name,
		startTime: time.Now(),
		tags:      make(map[string]string),
	}

	ctx = context.WithValue(ctx, traceIDKey, traceID)
	ctx = context.WithValue(ctx, spanIDKey, spanID)

	return span, ctx
}

// EndSpan ends a span and records it
func (s *Span) End() {
	if s == nil {
		return
	}

	s.endTime = time.Now()
	s.duration = s.endTime.Sub(s.startTime)

	globalTracer.mu.Lock()
	// Check if we need to remove old traces
	if len(globalTracer.spans) >= globalTracer.maxTraces {
		// Remove oldest trace
		var oldestTime time.Time
		var oldestTraceID string
		first := true

		for traceID, spans := range globalTracer.spans {
			if len(spans) > 0 {
				spanStartTime := spans[0].startTime
				if first || spanStartTime.Before(oldestTime) {
					oldestTime = spanStartTime
					oldestTraceID = traceID
					first = false
				}
			}
		}

		if oldestTraceID != "" {
			delete(globalTracer.spans, oldestTraceID)
		}
	}

	if globalTracer.spans[s.traceID] == nil {
		globalTracer.spans[s.traceID] = make([]*Span, 0)
	}
	globalTracer.spans[s.traceID] = append(globalTracer.spans[s.traceID], s)
	globalTracer.mu.Unlock()

	if globalTracer.handler != nil {
		globalTracer.handler.HandleTrace(s)
	}

	// Export the span
	go s.export()
}

// SetTag adds a tag to the span
func (s *Span) SetTag(key, value string) {
	if s == nil {
		return
	}
	s.tags[key] = value
}

// SetError sets an error on the span
func (s *Span) SetError(err error) {
	if s == nil {
		return
	}
	s.err = err
}

// SetStatusCode sets the HTTP status code
func (s *Span) SetStatusCode(code int) {
	if s == nil {
		return
	}
	s.statusCode = code
}

// SetStatusCode sets the HTTP status code
func (s *Span) SetDuration(d time.Duration) {
	if s == nil {
		return
	}
	s.duration = d
}

// GenerateID generates a unique ID for traces and spans
func GenerateID() string {
	uuid, _ := GenerateUUID()
	return uuid
}

// GetTraces returns all recorded traces
func GetTraces() map[string][]*Span {
	globalTracer.mu.RLock()
	defer globalTracer.mu.RUnlock()

	traces := make(map[string][]*Span)
	for k, v := range globalTracer.spans {
		traces[k] = v
	}
	return traces
}

// ClearTraces clears all recorded traces
func ClearTraces() {
	globalTracer.mu.Lock()
	defer globalTracer.mu.Unlock()

	globalTracer.spans = make(map[string][]*Span)
}

// ConfigureExport sets the export endpoint and type
func ConfigureExport(endpoint string, exportType ExportType) {
	globalTracer.mu.Lock()
	defer globalTracer.mu.Unlock()

	// Validate export type
	switch exportType {
	case ExportTypeJaeger, ExportTypeTempo, ExportTypeZipkin, ExportTypeDatadog, ExportTypeNewRelic, ExportTypeHoneycomb, ExportTypeOTLP, ExportTypeSignoz:
		globalTracer.exportType = exportType
	default:
		// Default to Jaeger if invalid type provided
		globalTracer.exportType = ExportTypeJaeger
	}

	// Use default endpoints if none provided
	if endpoint == "" {
		switch globalTracer.exportType {
		case ExportTypeJaeger:
			globalTracer.exportEndpoint = DefaultJaegerEndpoint
		case ExportTypeTempo:
			globalTracer.exportEndpoint = DefaultTempoEndpoint
		case ExportTypeZipkin:
			globalTracer.exportEndpoint = DefaultZipkinEndpoint
		case ExportTypeDatadog:
			globalTracer.exportEndpoint = DefaultDatadogEndpoint
		case ExportTypeNewRelic:
			globalTracer.exportEndpoint = DefaultNewRelicEndpoint
		case ExportTypeHoneycomb:
			globalTracer.exportEndpoint = DefaultHoneycombEndpoint
		case ExportTypeOTLP:
			globalTracer.exportEndpoint = DefaultOTLPEndpoint
		case ExportTypeSignoz:
			globalTracer.exportEndpoint = DefaultSignozEndpoint
		}
	} else {
		globalTracer.exportEndpoint = endpoint
	}
}

// Export the span to the configured endpoint
func (s *Span) export() error {
	if globalTracer.exportEndpoint == "" {
		return nil
	}

	var payload interface{}
	switch globalTracer.exportType {
	case ExportTypeJaeger:
		payload = convertToJaeger(s)
	case ExportTypeTempo:
		payload = convertToTempo(s)
	case ExportTypeZipkin:
		payload = convertToZipkin(s)
	case ExportTypeDatadog:
		payload = convertToDatadog(s)
	case ExportTypeNewRelic:
		payload = convertToNewRelic(s)
	case ExportTypeHoneycomb:
		payload = convertToHoneycomb(s)
	case ExportTypeOTLP:
		payload = convertToOTLP(s)
	case ExportTypeSignoz:
		payload = convertToSignoz(s)
	default:
		return nil
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", globalTracer.exportEndpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// Conversion functions
func convertToJaeger(s *Span) map[string]interface{} {
	return map[string]interface{}{
		"traceID":       s.traceID,
		"spanID":        s.spanID,
		"parentSpanID":  s.parentID,
		"operationName": s.name,
		"startTime":     s.startTime.UnixNano() / 1000, // Jaeger expects microseconds
		"duration":      s.duration.Microseconds(),
		"tags":          convertTags(s),
	}
}

func convertToTempo(s *Span) map[string]interface{} {
	return map[string]interface{}{
		"traceId":   s.traceID,
		"id":        s.spanID,
		"parentId":  s.parentID,
		"name":      s.name,
		"timestamp": s.startTime.UnixNano() / 1000,
		"duration":  s.duration.Microseconds(),
		"tags":      convertTags(s),
	}
}

func convertToZipkin(s *Span) map[string]interface{} {
	return map[string]interface{}{
		"traceId":   s.traceID,
		"id":        s.spanID,
		"parentId":  s.parentID,
		"name":      s.name,
		"timestamp": s.startTime.UnixNano() / 1000,
		"duration":  s.duration.Microseconds(),
		"localEndpoint": map[string]string{
			"serviceName": "your-service-name",
		},
		"tags": convertTagsToMap(s),
	}
}

func convertToDatadog(s *Span) map[string]interface{} {
	return map[string]interface{}{
		"trace_id":  s.traceID,
		"span_id":   s.spanID,
		"parent_id": s.parentID,
		"name":      s.name,
		"start":     s.startTime.UnixNano(),
		"duration":  s.duration.Nanoseconds(),
		"service":   "your-service-name",
		"resource":  s.name,
		"meta":      convertTagsToMap(s),
	}
}

func convertToNewRelic(s *Span) map[string]interface{} {
	return map[string]interface{}{
		"id":          s.spanID,
		"trace.id":    s.traceID,
		"parent.id":   s.parentID,
		"name":        s.name,
		"timestamp":   s.startTime.UnixNano() / 1000000, // milliseconds
		"duration.ms": s.duration.Milliseconds(),
		"attributes":  convertTagsToMap(s),
	}
}

func convertToHoneycomb(s *Span) map[string]interface{} {
	data := make(map[string]interface{})
	// Copy tags
	for k, v := range convertTagsToMap(s) {
		data[k] = v
	}
	data["name"] = s.name
	data["trace.trace_id"] = s.traceID
	data["trace.parent_id"] = s.parentID
	data["trace.span_id"] = s.spanID
	data["duration_ms"] = fmt.Sprintf("%d", s.duration.Milliseconds())
	return data
}

func convertToOTLP(s *Span) map[string]interface{} {
	var errMsg string
	if s.err != nil {
		errMsg = s.err.Error()
	}

	return map[string]interface{}{
		"traceId":           s.traceID,
		"spanId":            s.spanID,
		"parentSpanId":      s.parentID,
		"name":              s.name,
		"startTimeUnixNano": s.startTime.UnixNano(),
		"endTimeUnixNano":   s.endTime.UnixNano(),
		"attributes":        convertTagsToOTLP(s),
		"status": map[string]interface{}{
			"code":    s.statusCode,
			"message": errMsg,
		},
	}
}

func convertToSignoz(s *Span) map[string]interface{} {
	return map[string]interface{}{
		"traceID":      s.traceID,
		"spanID":       s.spanID,
		"parentSpanID": s.parentID,
		"name":         s.name,
		"startTime":    s.startTime.UnixNano() / 1000,
		"duration":     s.duration.Microseconds(),
		"tags":         convertTags(s),
	}
}

func convertTags(s *Span) []map[string]interface{} {
	tags := make([]map[string]interface{}, 0)

	// Add status code tag
	if s.statusCode != 0 {
		tags = append(tags, map[string]interface{}{
			"key":   "http.status_code",
			"type":  "int64",
			"value": s.statusCode,
		})
	}

	// Add error tag
	if s.err != nil {
		tags = append(tags, map[string]interface{}{
			"key":   "error",
			"type":  "string",
			"value": s.err.Error(),
		})
	}

	// Add custom tags
	for k, v := range s.tags {
		tags = append(tags, map[string]interface{}{
			"key":   k,
			"type":  "string",
			"value": v,
		})
	}

	return tags
}

// Helper function for flat tag conversion
func convertTagsToMap(s *Span) map[string]string {
	tags := make(map[string]string)

	if s.statusCode != 0 {
		tags["http.status_code"] = fmt.Sprintf("%d", s.statusCode)
	}

	if s.err != nil {
		tags["error"] = s.err.Error()
	}

	for k, v := range s.tags {
		tags[k] = v
	}

	return tags
}

// Helper function for OTLP attributes
func convertTagsToOTLP(s *Span) []map[string]interface{} {
	attrs := make([]map[string]interface{}, 0)

	if s.statusCode != 0 {
		attrs = append(attrs, map[string]interface{}{
			"key": "http.status_code",
			"value": map[string]interface{}{
				"intValue": s.statusCode,
			},
		})
	}

	// ... similar for other tags

	return attrs
}

// SetMaxTraces sets the maximum number of traces to keep in memory
func SetMaxTraces(max int) {
	globalTracer.mu.Lock()
	defer globalTracer.mu.Unlock()
	globalTracer.maxTraces = max
}
