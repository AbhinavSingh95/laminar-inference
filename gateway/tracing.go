package main

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultTraceCapacity = 2048
const defaultTraceFlags = "01"

type traceContext struct {
	TraceID      string
	ParentSpanID string
	TraceFlags   string
}

type TraceSpan struct {
	TraceID        string            `json:"trace_id"`
	SpanID         string            `json:"span_id"`
	ParentSpanID   string            `json:"parent_span_id,omitempty"`
	Name           string            `json:"name"`
	StartUnixNanos int64             `json:"start_unix_nanos"`
	DurationMicros int64             `json:"duration_micros"`
	Status         string            `json:"status"`
	Attributes     map[string]string `json:"attributes,omitempty"`
}

type ActiveSpan struct {
	recorder   *TraceRecorder
	traceID    string
	spanID     string
	parentID   string
	name       string
	start      time.Time
	attributes map[string]string
	ended      bool
	mu         sync.Mutex
}

type TraceRecorder struct {
	mu        sync.Mutex
	capacity  int
	spans     []TraceSpan
	exporters []SpanExporter
}

type SpanExporter interface {
	ExportSpan(span TraceSpan)
}

func NewTraceRecorder(capacity int) *TraceRecorder {
	if capacity <= 0 {
		capacity = defaultTraceCapacity
	}
	return &TraceRecorder{
		capacity: capacity,
		spans:    make([]TraceSpan, 0, capacity),
	}
}

func (r *TraceRecorder) AddSpanExporter(exporter SpanExporter) {
	if r == nil || exporter == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.exporters = append(r.exporters, exporter)
}

func (r *TraceRecorder) StartSpan(name string, parentSpanID string, traceID string, attributes map[string]string) *ActiveSpan {
	if r == nil {
		return nil
	}
	if traceID == "" {
		traceID = randomHex(16)
	}
	return &ActiveSpan{
		recorder:   r,
		traceID:    traceID,
		spanID:     randomHex(8),
		parentID:   parentSpanID,
		name:       name,
		start:      time.Now(),
		attributes: cloneAttributes(attributes),
	}
}

func (r *TraceRecorder) RecordSpan(span TraceSpan) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if span.Attributes != nil {
		span.Attributes = cloneAttributes(span.Attributes)
	}
	r.spans = append(r.spans, span)
	if len(r.spans) > r.capacity {
		copy(r.spans, r.spans[len(r.spans)-r.capacity:])
		r.spans = r.spans[:r.capacity]
	}
	exporters := append([]SpanExporter(nil), r.exporters...)
	exportSpan := cloneTraceSpan(span)
	r.mu.Unlock()

	for _, exporter := range exporters {
		exporter.ExportSpan(exportSpan)
	}
}

func (r *TraceRecorder) Snapshot(traceID string) []TraceSpan {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	spans := make([]TraceSpan, 0, len(r.spans))
	for _, span := range r.spans {
		if traceID != "" && span.TraceID != traceID {
			continue
		}
		span.Attributes = cloneAttributes(span.Attributes)
		spans = append(spans, span)
	}
	return spans
}

func (r *TraceRecorder) OTLPExport(traceID string) otlpTraceExport {
	return spansToOTLPExport(r.Snapshot(traceID))
}

func (s *ActiveSpan) End(status string, attributes map[string]string) {
	if s == nil || s.recorder == nil {
		return
	}
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	start := s.start
	baseAttributes := cloneAttributes(s.attributes)
	for key, value := range attributes {
		if baseAttributes == nil {
			baseAttributes = make(map[string]string, len(attributes))
		}
		baseAttributes[key] = value
	}
	span := TraceSpan{
		TraceID:        s.traceID,
		SpanID:         s.spanID,
		ParentSpanID:   s.parentID,
		Name:           s.name,
		StartUnixNanos: start.UnixNano(),
		DurationMicros: time.Since(start).Microseconds(),
		Status:         status,
		Attributes:     baseAttributes,
	}
	s.mu.Unlock()

	s.recorder.RecordSpan(span)
}

func (s *ActiveSpan) SpanID() string {
	if s == nil {
		return ""
	}
	return s.spanID
}

func parseTraceParent(header string) (traceContext, bool) {
	parts := strings.Split(strings.TrimSpace(header), "-")
	if len(parts) != 4 {
		return traceContext{}, false
	}

	version := parts[0]
	traceID := strings.ToLower(parts[1])
	parentSpanID := strings.ToLower(parts[2])
	traceFlags := strings.ToLower(parts[3])

	if version != "00" {
		return traceContext{}, false
	}
	if !isValidTraceID(traceID) || !isValidSpanID(parentSpanID) || !isLowerHex(traceFlags, 2) {
		return traceContext{}, false
	}

	return traceContext{
		TraceID:      traceID,
		ParentSpanID: parentSpanID,
		TraceFlags:   traceFlags,
	}, true
}

func formatTraceParent(traceID string, spanID string, traceFlags string) (string, bool) {
	traceID = strings.ToLower(strings.TrimSpace(traceID))
	spanID = strings.ToLower(strings.TrimSpace(spanID))
	traceFlags = strings.ToLower(strings.TrimSpace(traceFlags))
	if traceFlags == "" {
		traceFlags = defaultTraceFlags
	}
	if !isValidTraceID(traceID) || !isValidSpanID(spanID) || !isLowerHex(traceFlags, 2) {
		return "", false
	}
	return "00-" + traceID + "-" + spanID + "-" + traceFlags, true
}

func isValidTraceID(value string) bool {
	return isLowerHex(value, 32) && !isAllZero(value)
}

func isValidSpanID(value string) bool {
	return isLowerHex(value, 16) && !isAllZero(value)
}

func isLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, ch := range value {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return false
		}
	}
	return true
}

func isAllZero(value string) bool {
	for _, ch := range value {
		if ch != '0' {
			return false
		}
	}
	return true
}

func randomHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000000")))
	}
	return hex.EncodeToString(buf)
}

func cloneAttributes(attributes map[string]string) map[string]string {
	if len(attributes) == 0 {
		return nil
	}
	out := make(map[string]string, len(attributes))
	for key, value := range attributes {
		out[key] = value
	}
	return out
}

func cloneTraceSpan(span TraceSpan) TraceSpan {
	span.Attributes = cloneAttributes(span.Attributes)
	return span
}

type otlpTraceExport struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpResource struct {
	Attributes []otlpKeyValue `json:"attributes,omitempty"`
}

type otlpScopeSpans struct {
	Scope otlpInstrumentationScope `json:"scope"`
	Spans []otlpSpan               `json:"spans"`
}

type otlpInstrumentationScope struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type otlpSpan struct {
	TraceID           string         `json:"traceId"`
	SpanID            string         `json:"spanId"`
	ParentSpanID      string         `json:"parentSpanId,omitempty"`
	Name              string         `json:"name"`
	Kind              int            `json:"kind"`
	StartTimeUnixNano string         `json:"startTimeUnixNano"`
	EndTimeUnixNano   string         `json:"endTimeUnixNano"`
	Attributes        []otlpKeyValue `json:"attributes,omitempty"`
	Status            otlpStatus     `json:"status"`
}

type otlpStatus struct {
	Code int `json:"code"`
}

type otlpKeyValue struct {
	Key   string       `json:"key"`
	Value otlpAnyValue `json:"value"`
}

type otlpAnyValue struct {
	StringValue string `json:"stringValue"`
}

func (span TraceSpan) toOTLP() (otlpSpan, bool) {
	traceID := strings.ToLower(span.TraceID)
	spanID := strings.ToLower(span.SpanID)
	parentSpanID := strings.ToLower(span.ParentSpanID)
	if !isValidTraceID(traceID) || !isValidSpanID(spanID) {
		return otlpSpan{}, false
	}
	if parentSpanID != "" && !isValidSpanID(parentSpanID) {
		return otlpSpan{}, false
	}

	endUnixNanos := span.StartUnixNanos + span.DurationMicros*int64(time.Microsecond)
	return otlpSpan{
		TraceID:           traceID,
		SpanID:            spanID,
		ParentSpanID:      parentSpanID,
		Name:              span.Name,
		Kind:              otlpSpanKind(span.Name),
		StartTimeUnixNano: strconv.FormatInt(span.StartUnixNanos, 10),
		EndTimeUnixNano:   strconv.FormatInt(endUnixNanos, 10),
		Attributes:        otlpAttributes(span.Attributes),
		Status:            otlpStatus{Code: otlpStatusCode(span.Status)},
	}, true
}

func spansToOTLPExport(spans []TraceSpan) otlpTraceExport {
	spans = append([]TraceSpan(nil), spans...)
	sort.SliceStable(spans, func(i int, j int) bool {
		if spans[i].StartUnixNanos == spans[j].StartUnixNanos {
			return spans[i].Name < spans[j].Name
		}
		return spans[i].StartUnixNanos < spans[j].StartUnixNanos
	})

	otlpSpans := make([]otlpSpan, 0, len(spans))
	for _, span := range spans {
		converted, ok := span.toOTLP()
		if !ok {
			continue
		}
		otlpSpans = append(otlpSpans, converted)
	}

	return otlpTraceExport{
		ResourceSpans: []otlpResourceSpans{
			{
				Resource: otlpResource{
					Attributes: []otlpKeyValue{
						stringAttribute("service.name", "laminar-inference-gateway"),
						stringAttribute("telemetry.sdk.language", "go"),
					},
				},
				ScopeSpans: []otlpScopeSpans{
					{
						Scope: otlpInstrumentationScope{
							Name:    "laminar-inference/gateway",
							Version: "local",
						},
						Spans: otlpSpans,
					},
				},
			},
		},
	}
}

func otlpSpanKind(name string) int {
	switch name {
	case "http.predict":
		return 2
	case "worker.grpc":
		return 3
	default:
		return 1
	}
}

func otlpStatusCode(status string) int {
	switch status {
	case "ok":
		return 1
	case "error", "cancelled":
		return 2
	default:
		return 0
	}
}

func otlpAttributes(attributes map[string]string) []otlpKeyValue {
	if len(attributes) == 0 {
		return nil
	}
	keys := make([]string, 0, len(attributes))
	for key := range attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]otlpKeyValue, 0, len(keys))
	for _, key := range keys {
		out = append(out, stringAttribute(key, attributes[key]))
	}
	return out
}

func stringAttribute(key string, value string) otlpKeyValue {
	return otlpKeyValue{
		Key: key,
		Value: otlpAnyValue{
			StringValue: value,
		},
	}
}
