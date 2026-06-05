package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/AbhinavSingh95/laminar-inference/proto"
)

type mockWorker struct {
	mu      sync.Mutex
	calls   []*pb.BatchRequest
	onBatch func(context.Context, *pb.BatchRequest) (*pb.BatchResponse, error)
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
