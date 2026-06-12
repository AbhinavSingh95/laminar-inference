package main

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/AbhinavSingh95/laminar-inference/proto"
)

type mockWorker struct {
	mu       sync.Mutex
	calls    []*pb.BatchRequest
	onBatch  func(context.Context, *pb.BatchRequest) (*pb.BatchResponse, error)
	onStream func(context.Context, *pb.RequestItem) (InferenceStreamClient, error)
}

func (m *mockWorker) ProcessBatch(ctx context.Context, req *pb.BatchRequest, _ ...grpc.CallOption) (*pb.BatchResponse, error) {
	m.mu.Lock()
	m.calls = append(m.calls, req)
	onBatch := m.onBatch
	m.mu.Unlock()

	if onBatch != nil {
		return onBatch(ctx, req)
	}
	return responseFor(req), nil
}

func (m *mockWorker) Stream(ctx context.Context, req *pb.RequestItem, _ ...grpc.CallOption) (InferenceStreamClient, error) {
	m.mu.Lock()
	onStream := m.onStream
	m.mu.Unlock()

	if onStream != nil {
		return onStream(ctx, req)
	}
	return &fakeInferenceStream{
		events: []*pb.StreamResponse{
			{
				Id:        req.Id,
				TraceId:   req.TraceId,
				EventType: "done",
				Result:    fmt.Sprintf("ok:%s", req.Prompt),
			},
		},
	}, nil
}

type fakeInferenceStream struct {
	events []*pb.StreamResponse
	index  int
}

func (s *fakeInferenceStream) Recv() (*pb.StreamResponse, error) {
	if s.index >= len(s.events) {
		return nil, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (m *mockWorker) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func (m *mockWorker) batchSize(call int) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls[call].Requests)
}

func testConfig() Config {
	cfg := DefaultConfig()
	cfg.MaxBatchSize = 3
	cfg.MaxBatchWaitTime = 10 * time.Millisecond
	cfg.WorkerRequestTimeout = time.Second
	cfg.RequestQueueSize = 16
	cfg.CircuitBreakerThreshold = 2
	cfg.CircuitBreakerResetAfter = time.Hour
	return cfg
}

func responseFor(req *pb.BatchRequest) *pb.BatchResponse {
	resp := &pb.BatchResponse{
		Responses: make([]*pb.ResponseItem, len(req.Requests)),
	}
	for i, item := range req.Requests {
		resp.Responses[i] = &pb.ResponseItem{
			Id:      item.Id,
			TraceId: item.TraceId,
			Result:  fmt.Sprintf("ok:%s", item.Prompt),
		}
	}
	return resp
}

func TestBatcherFlushesAtMaxBatchSize(t *testing.T) {
	worker := &mockWorker{}
	cfg := testConfig()
	cfg.MaxBatchSize = 3
	cfg.MaxBatchWaitTime = time.Hour
	batcher := NewBatcher(worker, cfg)

	responses := make(chan *Response, 3)
	for i := 0; i < 3; i++ {
		i := i
		go func() {
			responses <- batcher.Submit(context.Background(), fmt.Sprintf("prompt-%d", i), fmt.Sprintf("trace-%d", i))
		}()
	}

	for i := 0; i < 3; i++ {
		resp := receiveResponse(t, responses)
		if resp.Error != "" {
			t.Fatalf("unexpected response error: %+v", resp)
		}
	}

	if worker.callCount() != 1 {
		t.Fatalf("expected one worker call, got %d", worker.callCount())
	}
	if got := worker.batchSize(0); got != 3 {
		t.Fatalf("expected batch size 3, got %d", got)
	}
}

func TestBatcherFlushesAfterWaitTime(t *testing.T) {
	worker := &mockWorker{}
	cfg := testConfig()
	cfg.MaxBatchSize = 8
	cfg.MaxBatchWaitTime = 5 * time.Millisecond
	batcher := NewBatcher(worker, cfg)

	resp := batcher.Submit(context.Background(), "single", "trace-single")
	if resp.Error != "" {
		t.Fatalf("unexpected response error: %+v", resp)
	}

	if worker.callCount() != 1 {
		t.Fatalf("expected one worker call, got %d", worker.callCount())
	}
	if got := worker.batchSize(0); got != 1 {
		t.Fatalf("expected batch size 1, got %d", got)
	}
}

func TestFirstRequestCancellationDoesNotCancelWholeBatch(t *testing.T) {
	rpcStarted := make(chan struct{})
	releaseRPC := make(chan struct{})
	worker := &mockWorker{
		onBatch: func(ctx context.Context, req *pb.BatchRequest) (*pb.BatchResponse, error) {
			close(rpcStarted)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-releaseRPC:
				return responseFor(req), nil
			}
		},
	}

	cfg := testConfig()
	cfg.MaxBatchSize = 2
	cfg.MaxBatchWaitTime = time.Hour
	batcher := NewBatcher(worker, cfg)

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	first := make(chan *Response, 1)
	second := make(chan *Response, 1)
	go func() {
		first <- batcher.Submit(firstCtx, "first", "trace-first")
	}()
	go func() {
		second <- batcher.Submit(context.Background(), "second", "trace-second")
	}()

	select {
	case <-rpcStarted:
	case <-time.After(time.Second):
		t.Fatal("worker RPC did not start")
	}

	cancelFirst()
	close(releaseRPC)

	secondResp := receiveResponse(t, second)
	if secondResp.Error != "" {
		t.Fatalf("second request should not inherit first cancellation: %+v", secondResp)
	}

	firstResp := receiveResponse(t, first)
	if firstResp.ErrorCode != ErrRequestCancelled {
		t.Fatalf("expected first request cancellation, got %+v", firstResp)
	}
}

func TestQueueFullReturnsBackpressureError(t *testing.T) {
	rpcStarted := make(chan struct{})
	releaseRPC := make(chan struct{})
	worker := &mockWorker{
		onBatch: func(ctx context.Context, req *pb.BatchRequest) (*pb.BatchResponse, error) {
			closeOnce(rpcStarted)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-releaseRPC:
				return responseFor(req), nil
			}
		},
	}

	cfg := testConfig()
	cfg.MaxBatchSize = 1
	cfg.MaxBatchWaitTime = time.Hour
	cfg.RequestQueueSize = 1
	cfg.WorkerRequestTimeout = 5 * time.Second
	batcher := NewBatcher(worker, cfg)

	first := make(chan *Response, 1)
	second := make(chan *Response, 1)
	go func() {
		first <- batcher.Submit(context.Background(), "first", "trace-first")
	}()

	select {
	case <-rpcStarted:
	case <-time.After(time.Second):
		t.Fatal("worker RPC did not start")
	}

	go func() {
		second <- batcher.Submit(context.Background(), "second", "trace-second")
	}()
	waitFor(t, func() bool {
		return len(batcher.requestChan) == 1
	})

	resp := batcher.Submit(context.Background(), "third", "trace-third")
	if resp.ErrorCode != ErrQueueFull {
		t.Fatalf("expected queue_full, got %+v", resp)
	}

	close(releaseRPC)
	_ = receiveResponse(t, first)
	_ = receiveResponse(t, second)
}

func TestAdmissionControlRejectsWhenTokenBudgetIsFull(t *testing.T) {
	releaseRPC := make(chan struct{})
	worker := &mockWorker{
		onBatch: func(ctx context.Context, req *pb.BatchRequest) (*pb.BatchResponse, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-releaseRPC:
				return responseFor(req), nil
			}
		},
	}

	cfg := testConfig()
	cfg.MaxBatchSize = 1
	cfg.WorkerRequestTimeout = 5 * time.Second
	cfg.AdmissionEnabled = true
	cfg.AdmissionMaxInFlightTokens = 8
	cfg.AdmissionEstimatedTokensPerByte = 1
	cfg.AdmissionEstimatedOutputTokens = 2
	batcher := NewBatcher(worker, cfg)

	first := make(chan *Response, 1)
	go func() {
		first <- batcher.Submit(context.Background(), "123456", "trace-first")
	}()

	waitFor(t, func() bool {
		return batcher.Snapshot().Admission.InFlightTokens == 8
	})

	resp := batcher.Submit(context.Background(), "x", "trace-rejected")
	if resp.ErrorCode != ErrAdmissionRejected {
		t.Fatalf("expected admission rejection, got %+v", resp)
	}
	if got := batcher.Snapshot().Admission.RejectedRequests; got != 1 {
		t.Fatalf("expected one admission rejection, got %d", got)
	}

	close(releaseRPC)
	firstResp := receiveResponse(t, first)
	if firstResp.Error != "" {
		t.Fatalf("first request should succeed after releasing RPC: %+v", firstResp)
	}
}

func TestAdmissionControlReleasesBudgetAfterWorkerResponse(t *testing.T) {
	worker := &mockWorker{}
	cfg := testConfig()
	cfg.MaxBatchSize = 1
	cfg.AdmissionEnabled = true
	cfg.AdmissionMaxInFlightTokens = 8
	cfg.AdmissionEstimatedTokensPerByte = 1
	cfg.AdmissionEstimatedOutputTokens = 2
	batcher := NewBatcher(worker, cfg)

	first := batcher.Submit(context.Background(), "123456", "trace-first")
	if first.Error != "" {
		t.Fatalf("unexpected first response error: %+v", first)
	}

	snapshot := batcher.Snapshot().Admission
	if snapshot.InFlightTokens != 0 {
		t.Fatalf("expected admission tokens released, got %d", snapshot.InFlightTokens)
	}

	second := batcher.Submit(context.Background(), "123456", "trace-second")
	if second.Error != "" {
		t.Fatalf("second request should fit after release: %+v", second)
	}
}

func TestAdmissionConfigLoadsFromEnv(t *testing.T) {
	t.Setenv("LAMINAR_ADMISSION_ENABLED", "true")
	t.Setenv("LAMINAR_ADMISSION_MAX_IN_FLIGHT_TOKENS", "123")
	t.Setenv("LAMINAR_ADMISSION_ESTIMATED_TOKENS_PER_BYTE", "0.25")
	t.Setenv("LAMINAR_ADMISSION_ESTIMATED_OUTPUT_TOKENS", "17")

	cfg := LoadConfigFromEnv()

	if !cfg.AdmissionEnabled {
		t.Fatal("expected admission control to load as enabled")
	}
	if cfg.AdmissionMaxInFlightTokens != 123 {
		t.Fatalf("expected max in-flight tokens 123, got %d", cfg.AdmissionMaxInFlightTokens)
	}
	if cfg.AdmissionEstimatedTokensPerByte != 0.25 {
		t.Fatalf("expected token estimate 0.25, got %f", cfg.AdmissionEstimatedTokensPerByte)
	}
	if cfg.AdmissionEstimatedOutputTokens != 17 {
		t.Fatalf("expected output token estimate 17, got %d", cfg.AdmissionEstimatedOutputTokens)
	}
}

func TestCircuitBreakerRejectsAfterConsecutiveFailures(t *testing.T) {
	worker := &mockWorker{
		onBatch: func(context.Context, *pb.BatchRequest) (*pb.BatchResponse, error) {
			return nil, status.Error(codes.Unavailable, "worker down")
		},
	}

	cfg := testConfig()
	cfg.MaxBatchSize = 1
	cfg.CircuitBreakerThreshold = 2
	batcher := NewBatcher(worker, cfg)

	for i := 0; i < 2; i++ {
		resp := batcher.Submit(context.Background(), fmt.Sprintf("fail-%d", i), fmt.Sprintf("trace-%d", i))
		if resp.ErrorCode != ErrWorkerUnavailable {
			t.Fatalf("expected worker_unavailable, got %+v", resp)
		}
	}

	resp := batcher.Submit(context.Background(), "rejected", "trace-rejected")
	if resp.ErrorCode != ErrWorkerUnavailable {
		t.Fatalf("expected circuit breaker rejection, got %+v", resp)
	}
	if worker.callCount() != 2 {
		t.Fatalf("expected circuit breaker to stop third worker call, got %d calls", worker.callCount())
	}
}

func receiveResponse(t *testing.T, ch <-chan *Response) *Response {
	t.Helper()
	select {
	case resp := <-ch:
		return resp
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for response")
		return nil
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}

func closeOnce(ch chan struct{}) {
	defer func() {
		_ = recover()
	}()
	close(ch)
}
