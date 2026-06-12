package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOTLPHTTPExporterFlushesBatchedSpans(t *testing.T) {
	requests := make(chan otlpTraceExport, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("expected application/json content type, got %q", got)
		}
		var payload otlpTraceExport
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode OTLP payload: %v", err)
		}
		requests <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	exporter, err := NewOTLPHTTPExporter(OTLPHTTPExporterConfig{
		Endpoint:      server.URL,
		MaxBatchSize:  2,
		FlushInterval: time.Hour,
		QueueSize:     8,
		Timeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("new exporter: %v", err)
	}
	defer exporter.Close()

	recorder := NewTraceRecorder(8)
	recorder.AddSpanExporter(exporter)

	first := recorder.StartSpan("http.predict", "", "4bf92f3577b34da6a3ce929d0e0e4736", nil)
	second := recorder.StartSpan("worker.grpc", first.SpanID(), "4bf92f3577b34da6a3ce929d0e0e4736", nil)
	first.End("ok", nil)
	second.End("ok", nil)

	select {
	case payload := <-requests:
		spans := payload.ResourceSpans[0].ScopeSpans[0].Spans
		if len(spans) != 2 {
			t.Fatalf("expected two exported spans, got %d", len(spans))
		}
		if spans[0].TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
			t.Fatalf("unexpected trace id: %+v", spans[0])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for OTLP export")
	}
}

func TestOTLPHTTPExporterRetriesFailedFlushOnNextFlush(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		if attempt == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	exporter, err := NewOTLPHTTPExporter(OTLPHTTPExporterConfig{
		Endpoint:      server.URL,
		MaxBatchSize:  10,
		FlushInterval: time.Hour,
		QueueSize:     8,
		Timeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("new exporter: %v", err)
	}
	defer exporter.Close()

	recorder := NewTraceRecorder(8)
	recorder.AddSpanExporter(exporter)

	span := recorder.StartSpan("http.predict", "", "4bf92f3577b34da6a3ce929d0e0e4736", nil)
	span.End("ok", nil)

	if err := exporter.Flush(context.Background()); err == nil {
		t.Fatal("expected first flush to fail")
	}
	if got := exporter.Snapshot().PendingSpans; got != 1 {
		t.Fatalf("expected failed span to remain pending, got %d", got)
	}
	if err := exporter.Flush(context.Background()); err != nil {
		t.Fatalf("expected retry flush to succeed: %v", err)
	}
	if got := exporter.Snapshot().PendingSpans; got != 0 {
		t.Fatalf("expected no pending spans after retry, got %d", got)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("expected two export attempts, got %d", got)
	}
}

func TestLoadConfigEnablesOTLPExporterFromEnv(t *testing.T) {
	t.Setenv("LAMINAR_OTLP_ENDPOINT", "http://collector.example/v1/traces")
	t.Setenv("LAMINAR_OTLP_BATCH_SIZE", "7")
	t.Setenv("LAMINAR_OTLP_FLUSH_INTERVAL", "250ms")
	t.Setenv("LAMINAR_OTLP_QUEUE_SIZE", "99")
	t.Setenv("LAMINAR_OTLP_TIMEOUT", "2s")

	cfg := LoadConfigFromEnv()

	if cfg.OTLPEndpoint != "http://collector.example/v1/traces" {
		t.Fatalf("unexpected endpoint: %q", cfg.OTLPEndpoint)
	}
	if cfg.OTLPBatchSize != 7 {
		t.Fatalf("unexpected batch size: %d", cfg.OTLPBatchSize)
	}
	if cfg.OTLPFlushInterval != 250*time.Millisecond {
		t.Fatalf("unexpected flush interval: %s", cfg.OTLPFlushInterval)
	}
	if cfg.OTLPQueueSize != 99 {
		t.Fatalf("unexpected queue size: %d", cfg.OTLPQueueSize)
	}
	if cfg.OTLPTimeout != 2*time.Second {
		t.Fatalf("unexpected timeout: %s", cfg.OTLPTimeout)
	}
}
