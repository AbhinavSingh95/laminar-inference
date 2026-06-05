package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/AbhinavSingh95/laminar-inference/proto"
)

func TestWorkerPoolRoutesConcurrentBatchToLeastLoadedWorker(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})

	slowWorker := &mockWorker{
		onBatch: func(ctx context.Context, req *pb.BatchRequest) (*pb.BatchResponse, error) {
			closeOnce(firstStarted)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-releaseFirst:
				return workerResponseFor("worker-a", req), nil
			}
		},
	}
	fastWorker := &mockWorker{
		onBatch: func(_ context.Context, req *pb.BatchRequest) (*pb.BatchResponse, error) {
			return workerResponseFor("worker-b", req), nil
		},
	}

	pool, err := NewWorkerPool([]WorkerEndpointConfig{
		{ID: "worker-a", Address: "worker-a:50051", Client: slowWorker},
		{ID: "worker-b", Address: "worker-b:50051", Client: fastWorker},
	}, testConfig())
	if err != nil {
		t.Fatalf("NewWorkerPool returned error: %v", err)
	}

	firstDone := make(chan error, 1)
	go func() {
		resp, err := pool.ProcessBatch(context.Background(), batchRequest("first"))
		if err == nil && resp.Responses[0].Result != "worker-a:first" {
			err = fmt.Errorf("first batch routed to %q", resp.Responses[0].Result)
		}
		firstDone <- err
	}()

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first worker call did not start")
	}

	resp, err := pool.ProcessBatch(context.Background(), batchRequest("second"))
	if err != nil {
		t.Fatalf("second batch returned error: %v", err)
	}
	if got := resp.Responses[0].Result; got != "worker-b:second" {
		t.Fatalf("expected second batch to route to idle worker, got %q", got)
	}

	close(releaseFirst)
	if err := receiveError(t, firstDone); err != nil {
		t.Fatalf("first batch failed: %v", err)
	}
}

func TestWorkerPoolIsolatesFailedWorkerBehindCircuit(t *testing.T) {
	badWorker := &mockWorker{
		onBatch: func(context.Context, *pb.BatchRequest) (*pb.BatchResponse, error) {
			return nil, status.Error(codes.Unavailable, "worker down")
		},
	}
	goodWorker := &mockWorker{
		onBatch: func(_ context.Context, req *pb.BatchRequest) (*pb.BatchResponse, error) {
			return workerResponseFor("worker-b", req), nil
		},
	}

	cfg := testConfig()
	cfg.CircuitBreakerThreshold = 1
	cfg.CircuitBreakerResetAfter = time.Hour
	pool, err := NewWorkerPool([]WorkerEndpointConfig{
		{ID: "worker-a", Address: "worker-a:50051", Client: badWorker},
		{ID: "worker-b", Address: "worker-b:50051", Client: goodWorker},
	}, cfg)
	if err != nil {
		t.Fatalf("NewWorkerPool returned error: %v", err)
	}

	if _, err := pool.ProcessBatch(context.Background(), batchRequest("first")); status.Code(err) != codes.Unavailable {
		t.Fatalf("expected first call to fail with unavailable, got %v", err)
	}

	resp, err := pool.ProcessBatch(context.Background(), batchRequest("second"))
	if err != nil {
		t.Fatalf("expected second call to use healthy worker, got %v", err)
	}
	if got := resp.Responses[0].Result; got != "worker-b:second" {
		t.Fatalf("expected healthy worker response, got %q", got)
	}
	if badWorker.callCount() != 1 {
		t.Fatalf("expected failed worker to be skipped after circuit opened, got %d calls", badWorker.callCount())
	}
}

func TestWorkerPoolReturnsUnavailableWhenAllWorkerCircuitsAreOpen(t *testing.T) {
	cfg := testConfig()
	cfg.CircuitBreakerThreshold = 1
	cfg.CircuitBreakerResetAfter = time.Hour

	workerA := unavailableWorker()
	workerB := unavailableWorker()
	pool, err := NewWorkerPool([]WorkerEndpointConfig{
		{ID: "worker-a", Address: "worker-a:50051", Client: workerA},
		{ID: "worker-b", Address: "worker-b:50051", Client: workerB},
	}, cfg)
	if err != nil {
		t.Fatalf("NewWorkerPool returned error: %v", err)
	}

	for i := 0; i < 2; i++ {
		_, _ = pool.ProcessBatch(context.Background(), batchRequest(fmt.Sprintf("probe-%d", i)))
	}

	_, err = pool.ProcessBatch(context.Background(), batchRequest("rejected"))
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected unavailable when every worker circuit is open, got %v", err)
	}
	if workerA.callCount() != 1 || workerB.callCount() != 1 {
		t.Fatalf("expected no worker call after all circuits opened, got workerA=%d workerB=%d", workerA.callCount(), workerB.callCount())
	}
}

func TestWorkerPoolSnapshotsExposePerWorkerState(t *testing.T) {
	cfg := testConfig()
	cfg.CircuitBreakerThreshold = 1
	cfg.CircuitBreakerResetAfter = time.Hour

	pool, err := NewWorkerPool([]WorkerEndpointConfig{
		{ID: "worker-a", Address: "worker-a:50051", Client: unavailableWorker()},
		{ID: "worker-b", Address: "worker-b:50051", Client: &mockWorker{}},
	}, cfg)
	if err != nil {
		t.Fatalf("NewWorkerPool returned error: %v", err)
	}

	_, _ = pool.ProcessBatch(context.Background(), batchRequest("first"))
	_, _ = pool.ProcessBatch(context.Background(), batchRequest("second"))

	snapshots := pool.WorkerSnapshots()
	if len(snapshots) != 2 {
		t.Fatalf("expected two worker snapshots, got %d", len(snapshots))
	}
	if snapshots[0].ID != "worker-a" || !snapshots[0].CircuitOpen || snapshots[0].TotalFailures != 1 {
		t.Fatalf("unexpected failed worker snapshot: %+v", snapshots[0])
	}
	if snapshots[1].ID != "worker-b" || snapshots[1].CircuitOpen || snapshots[1].TotalBatches != 1 {
		t.Fatalf("unexpected healthy worker snapshot: %+v", snapshots[1])
	}
}

func unavailableWorker() *mockWorker {
	return &mockWorker{
		onBatch: func(context.Context, *pb.BatchRequest) (*pb.BatchResponse, error) {
			return nil, status.Error(codes.Unavailable, "worker down")
		},
	}
}

func batchRequest(prompt string) *pb.BatchRequest {
	return &pb.BatchRequest{
		Requests: []*pb.RequestItem{
			{Id: prompt, Prompt: prompt, TraceId: "trace-" + prompt},
		},
	}
}

func workerResponseFor(workerID string, req *pb.BatchRequest) *pb.BatchResponse {
	resp := &pb.BatchResponse{
		Responses: make([]*pb.ResponseItem, len(req.Requests)),
	}
	for i, item := range req.Requests {
		resp.Responses[i] = &pb.ResponseItem{
			Id:      item.Id,
			TraceId: item.TraceId,
			Result:  workerID + ":" + item.Prompt,
		}
	}
	return resp
}

func receiveError(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for error")
		return nil
	}
}
