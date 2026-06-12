package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	pb "github.com/AbhinavSingh95/laminar-inference/proto"
)

func TestTraceRecorderCapturesNestedSpanTiming(t *testing.T) {
	recorder := NewTraceRecorder(8)

	root := recorder.StartSpan("http.predict", "", "trace-test", map[string]string{
		"http.method": "POST",
	})
	child := recorder.StartSpan("batch.queue_wait", root.SpanID(), "trace-test", nil)
	child.End("ok", map[string]string{"queue.depth": "0"})
	root.End("ok", nil)

	spans := recorder.Snapshot("trace-test")
	if len(spans) != 2 {
		t.Fatalf("expected two spans, got %d", len(spans))
	}
	if spans[0].Name != "batch.queue_wait" || spans[0].ParentSpanID != root.SpanID() {
		t.Fatalf("unexpected child span: %+v", spans[0])
	}
	if spans[1].Name != "http.predict" || spans[1].SpanID == "" {
		t.Fatalf("unexpected root span: %+v", spans[1])
	}
	for _, span := range spans {
		if span.DurationMicros < 0 {
			t.Fatalf("span duration should be non-negative: %+v", span)
		}
	}
}

func TestBatcherRecordsQueueWorkerAndBackendSpans(t *testing.T) {
	worker := &mockWorker{
		onBatch: func(_ context.Context, req *pb.BatchRequest) (*pb.BatchResponse, error) {
			resp := responseFor(req)
			for _, item := range resp.Responses {
				item.BackendName = "test_backend"
				item.BackendLatencyMicros = 1234
				item.BackendMetadata = map[string]string{
					"stream":           "true",
					"tokens_predicted": "5",
					"ttft_micros":      "321",
				}
			}
			return resp, nil
		},
	}

	cfg := testConfig()
	cfg.MaxBatchSize = 1
	recorder := NewTraceRecorder(16)
	batcher := NewBatcherWithTracer(worker, cfg, recorder)

	traceID := "trace-batcher"
	resp := batcher.Submit(context.Background(), "traced prompt", traceID)
	if resp.Error != "" {
		t.Fatalf("unexpected response error: %+v", resp)
	}

	spans := recorder.Snapshot(traceID)
	seen := map[string]TraceSpan{}
	for _, span := range spans {
		seen[span.Name] = span
	}
	for _, name := range []string{"batch.queue_wait", "batch.flush", "worker.grpc", "backend.inference"} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("missing span %q in %+v", name, spans)
		}
	}
	if got := seen["batch.flush"].Attributes["batch.size"]; got != "1" {
		t.Fatalf("expected batch.size=1, got %q", got)
	}
	if got := seen["backend.inference"].Attributes["backend.name"]; got != "test_backend" {
		t.Fatalf("expected backend name attribute, got %q", got)
	}
	if got := seen["backend.inference"].DurationMicros; got != 1234 {
		t.Fatalf("expected backend duration from worker metadata, got %d", got)
	}
	if got := seen["backend.inference"].Attributes["backend.tokens_predicted"]; got != "5" {
		t.Fatalf("expected token metadata on backend span, got %q", got)
	}
	if got := resp.Metadata["tokens_predicted"]; got != "5" {
		t.Fatalf("expected response metadata to include token count, got %q", got)
	}
}

func TestHandlePredictRecordsHTTPRequestSpan(t *testing.T) {
	worker := &mockWorker{
		onBatch: func(_ context.Context, req *pb.BatchRequest) (*pb.BatchResponse, error) {
			resp := responseFor(req)
			for _, item := range resp.Responses {
				item.BackendName = "test_backend"
				item.BackendLatencyMicros = 321
			}
			return resp, nil
		},
	}

	cfg := testConfig()
	cfg.MaxBatchSize = 1
	recorder := NewTraceRecorder(16)
	batcher := NewBatcherWithTracer(worker, cfg, recorder)

	req := httptest.NewRequest(http.MethodPost, "/predict", strings.NewReader(`{"prompt":"trace me"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Trace-ID", "trace-http")
	resp := httptest.NewRecorder()

	handlePredict(batcher).ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}

	spans := recorder.Snapshot("trace-http")
	seen := map[string]TraceSpan{}
	for _, span := range spans {
		seen[span.Name] = span
	}

	httpSpan, ok := seen["http.predict"]
	if !ok {
		t.Fatalf("missing http.predict span in %+v", spans)
	}
	queueSpan, ok := seen["batch.queue_wait"]
	if !ok {
		t.Fatalf("missing batch.queue_wait span in %+v", spans)
	}
	if queueSpan.ParentSpanID != httpSpan.SpanID {
		t.Fatalf("expected queue span parent %q, got %q", httpSpan.SpanID, queueSpan.ParentSpanID)
	}
	flushSpan, ok := seen["batch.flush"]
	if !ok {
		t.Fatalf("missing batch.flush span in %+v", spans)
	}
	if flushSpan.ParentSpanID != queueSpan.SpanID {
		t.Fatalf("expected flush span parent %q, got %q", queueSpan.SpanID, flushSpan.ParentSpanID)
	}
	workerSpan, ok := seen["worker.grpc"]
	if !ok {
		t.Fatalf("missing worker.grpc span in %+v", spans)
	}
	if workerSpan.ParentSpanID != flushSpan.SpanID {
		t.Fatalf("expected worker span parent %q, got %q", flushSpan.SpanID, workerSpan.ParentSpanID)
	}
	backendSpan, ok := seen["backend.inference"]
	if !ok {
		t.Fatalf("missing backend.inference span in %+v", spans)
	}
	if backendSpan.ParentSpanID != workerSpan.SpanID {
		t.Fatalf("expected backend span parent %q, got %q", workerSpan.SpanID, backendSpan.ParentSpanID)
	}
	if got := httpSpan.Attributes["http.status_code"]; got != "200" {
		t.Fatalf("expected http.status_code=200, got %q", got)
	}
}

func TestHandlePredictStreamEmitsSSEAndTokenSpans(t *testing.T) {
	worker := &mockWorker{
		onStream: func(_ context.Context, req *pb.RequestItem) (InferenceStreamClient, error) {
			return &fakeInferenceStream{
				events: []*pb.StreamResponse{
					{
						Id:          req.Id,
						TraceId:     req.TraceId,
						EventType:   "token",
						Delta:       "hello ",
						BackendName: "llama_server",
						BackendMetadata: map[string]string{
							"tokens_predicted": "1",
						},
					},
					{
						Id:          req.Id,
						TraceId:     req.TraceId,
						EventType:   "token",
						Delta:       "world",
						BackendName: "llama_server",
						BackendMetadata: map[string]string{
							"tokens_predicted": "2",
						},
					},
					{
						Id:                   req.Id,
						TraceId:              req.TraceId,
						EventType:            "done",
						Result:               "hello world",
						BackendName:          "llama_server",
						BackendLatencyMicros: 4321,
						BackendMetadata: map[string]string{
							"stream":           "true",
							"stream_chunks":    "2",
							"tokens_predicted": "2",
							"ttft_micros":      "100",
						},
					},
				},
			}, nil
		},
	}

	cfg := testConfig()
	recorder := NewTraceRecorder(16)
	batcher := NewBatcherWithTracer(worker, cfg, recorder)

	traceID := "abcdefabcdefabcdefabcdefabcdef12"
	req := httptest.NewRequest(http.MethodPost, "/predict/stream", strings.NewReader(`{"prompt":"stream me"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Trace-ID", traceID)
	resp := httptest.NewRecorder()

	handlePredictStream(batcher).ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("expected SSE content type, got %q", got)
	}
	body := resp.Body.String()
	for _, expected := range []string{
		"event: token\n",
		`"delta":"hello "`,
		`"delta":"world"`,
		"event: done\n",
		`"result":"hello world"`,
		`"tokens_predicted":"2"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("stream body missing %q:\n%s", expected, body)
		}
	}

	spans := recorder.Snapshot(traceID)
	tokenSpans := 0
	seen := map[string]TraceSpan{}
	for _, span := range spans {
		if span.Name == "backend.token" {
			tokenSpans++
		}
		seen[span.Name] = span
	}
	if tokenSpans != 2 {
		t.Fatalf("expected two token spans, got %d in %+v", tokenSpans, spans)
	}
	if got := seen["backend.inference"].Attributes["backend.tokens_predicted"]; got != "2" {
		t.Fatalf("expected final backend token metadata, got %q", got)
	}
	if got := seen["http.predict.stream"].Attributes["http.status_code"]; got != "200" {
		t.Fatalf("expected streaming HTTP span status, got %q", got)
	}
}

func TestParseTraceParentAcceptsW3CHeader(t *testing.T) {
	ctx, ok := parseTraceParent("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	if !ok {
		t.Fatal("expected traceparent to parse")
	}
	if ctx.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("unexpected trace id: %q", ctx.TraceID)
	}
	if ctx.ParentSpanID != "00f067aa0ba902b7" {
		t.Fatalf("unexpected parent span id: %q", ctx.ParentSpanID)
	}
	if ctx.TraceFlags != "01" {
		t.Fatalf("unexpected trace flags: %q", ctx.TraceFlags)
	}

	for _, header := range []string{
		"",
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7",
		"ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	} {
		if _, ok := parseTraceParent(header); ok {
			t.Fatalf("expected invalid traceparent %q to be rejected", header)
		}
	}
}

func TestHandlePredictContinuesTraceParent(t *testing.T) {
	worker := &mockWorker{
		onBatch: func(_ context.Context, req *pb.BatchRequest) (*pb.BatchResponse, error) {
			resp := responseFor(req)
			for _, item := range resp.Responses {
				item.BackendName = "test_backend"
				item.BackendLatencyMicros = 321
			}
			return resp, nil
		},
	}

	cfg := testConfig()
	cfg.MaxBatchSize = 1
	recorder := NewTraceRecorder(16)
	batcher := NewBatcherWithTracer(worker, cfg, recorder)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	remoteParentSpanID := "00f067aa0ba902b7"
	req := httptest.NewRequest(http.MethodPost, "/predict", strings.NewReader(`{"prompt":"trace me"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Traceparent", "00-"+traceID+"-"+remoteParentSpanID+"-01")
	resp := httptest.NewRecorder()

	handlePredict(batcher).ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("X-Trace-ID"); got != traceID {
		t.Fatalf("expected X-Trace-ID %q, got %q", traceID, got)
	}
	responseTraceParent := resp.Header().Get("Traceparent")
	pattern := regexp.MustCompile(`^00-` + traceID + `-[0-9a-f]{16}-01$`)
	if !pattern.MatchString(responseTraceParent) {
		t.Fatalf("unexpected response traceparent: %q", responseTraceParent)
	}

	var httpSpan TraceSpan
	for _, span := range recorder.Snapshot(traceID) {
		if span.Name == "http.predict" {
			httpSpan = span
			break
		}
	}
	if httpSpan.Name == "" {
		t.Fatalf("missing http.predict span")
	}
	if httpSpan.ParentSpanID != remoteParentSpanID {
		t.Fatalf("expected remote parent %q, got %q", remoteParentSpanID, httpSpan.ParentSpanID)
	}
}

func TestHandleOTLPTracesExportsOTLPJSON(t *testing.T) {
	recorder := NewTraceRecorder(8)
	root := recorder.StartSpan("http.predict", "00f067aa0ba902b7", "4bf92f3577b34da6a3ce929d0e0e4736", map[string]string{
		"http.method": "POST",
	})
	child := recorder.StartSpan("worker.grpc", root.SpanID(), "4bf92f3577b34da6a3ce929d0e0e4736", map[string]string{
		"rpc.system": "grpc",
	})
	child.End("ok", nil)
	root.End("ok", map[string]string{"http.status_code": "200"})

	batcher := NewBatcherWithTracer(&mockWorker{}, testConfig(), recorder)
	req := httptest.NewRequest(http.MethodGet, "/traces/otlp?trace_id=4bf92f3577b34da6a3ce929d0e0e4736", nil)
	resp := httptest.NewRecorder()

	handleOTLPTraces(batcher).ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode OTLP payload: %v", err)
	}
	resourceSpans := payload["resourceSpans"].([]any)
	scopeSpans := resourceSpans[0].(map[string]any)["scopeSpans"].([]any)
	spans := scopeSpans[0].(map[string]any)["spans"].([]any)
	if len(spans) != 2 {
		t.Fatalf("expected two OTLP spans, got %d", len(spans))
	}
	span := spans[0].(map[string]any)
	if span["traceId"] != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("unexpected traceId: %+v", span)
	}
	if _, ok := span["startTimeUnixNano"].(string); !ok {
		t.Fatalf("startTimeUnixNano should be a string: %+v", span)
	}
	if _, ok := span["endTimeUnixNano"].(string); !ok {
		t.Fatalf("endTimeUnixNano should be a string: %+v", span)
	}
	if _, ok := span["attributes"].([]any); !ok {
		t.Fatalf("attributes should use OTLP key/value array: %+v", span)
	}
}

func TestTraceRecorderEvictsOldSpans(t *testing.T) {
	recorder := NewTraceRecorder(2)
	for i := 0; i < 3; i++ {
		span := recorder.StartSpan(fmt.Sprintf("span-%d", i), "", "trace-evict", nil)
		span.End("ok", nil)
		time.Sleep(time.Microsecond)
	}

	spans := recorder.Snapshot("")
	if len(spans) != 2 {
		t.Fatalf("expected two retained spans, got %d", len(spans))
	}
	if spans[0].Name != "span-1" || spans[1].Name != "span-2" {
		t.Fatalf("unexpected retained spans: %+v", spans)
	}
}
