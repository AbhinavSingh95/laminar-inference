package main

import (
	"testing"
	"time"
)

func TestFixedBatchPolicyFlushesOnlyAtMaxBatchSize(t *testing.T) {
	policy := NewFixedBatchPolicy(10 * time.Millisecond)

	if policy.ShouldFlush(PolicySnapshot{BatchSize: 2, MaxBatchSize: 3}) {
		t.Fatal("fixed policy flushed before max batch size")
	}
	if !policy.ShouldFlush(PolicySnapshot{BatchSize: 3, MaxBatchSize: 3}) {
		t.Fatal("fixed policy did not flush at max batch size")
	}
	if got := policy.NextWait(PolicySnapshot{}); got != 10*time.Millisecond {
		t.Fatalf("unexpected fixed wait: %s", got)
	}
}

func TestAdaptiveBatchPolicyShortensWaitUnderQueuePressure(t *testing.T) {
	cfg := testConfig()
	cfg.BatchPolicy = BatchPolicyAdaptive
	cfg.MaxBatchSize = 8
	cfg.MaxBatchWaitTime = 10 * time.Millisecond
	cfg.AdaptiveMinBatchWaitTime = time.Millisecond
	cfg.AdaptiveMaxBatchWaitTime = 10 * time.Millisecond
	cfg.AdaptiveQueueHighWatermark = 4
	cfg.AdaptiveTargetWorkerLatency = 100 * time.Millisecond

	policy := NewAdaptiveBatchPolicy(cfg)
	policy.Observe(BatchObservation{
		BatchSize:     2,
		QueueDepth:    4,
		WorkerLatency: 20 * time.Millisecond,
		Success:       true,
	})

	if got := policy.NextWait(PolicySnapshot{QueueDepth: 4}); got != time.Millisecond {
		t.Fatalf("expected min wait under queue pressure, got %s", got)
	}
}

func TestAdaptiveBatchPolicyShortensWaitAfterSlowWorker(t *testing.T) {
	cfg := testConfig()
	cfg.BatchPolicy = BatchPolicyAdaptive
	cfg.MaxBatchSize = 8
	cfg.MaxBatchWaitTime = 8 * time.Millisecond
	cfg.AdaptiveMinBatchWaitTime = time.Millisecond
	cfg.AdaptiveMaxBatchWaitTime = 8 * time.Millisecond
	cfg.AdaptiveTargetWorkerLatency = 50 * time.Millisecond
	cfg.AdaptiveQueueHighWatermark = 8

	policy := NewAdaptiveBatchPolicy(cfg)
	policy.Observe(BatchObservation{
		BatchSize:     8,
		QueueDepth:    0,
		WorkerLatency: 80 * time.Millisecond,
		Success:       true,
	})

	if got := policy.NextWait(PolicySnapshot{}); got != 4*time.Millisecond {
		t.Fatalf("expected wait to halve after slow worker, got %s", got)
	}
}

func TestAdaptiveBatchPolicyIncreasesWaitForSmallBatches(t *testing.T) {
	cfg := testConfig()
	cfg.BatchPolicy = BatchPolicyAdaptive
	cfg.MaxBatchSize = 8
	cfg.MaxBatchWaitTime = 2 * time.Millisecond
	cfg.AdaptiveMinBatchWaitTime = time.Millisecond
	cfg.AdaptiveMaxBatchWaitTime = 5 * time.Millisecond
	cfg.AdaptiveTargetWorkerLatency = 100 * time.Millisecond
	cfg.AdaptiveQueueHighWatermark = 8

	policy := NewAdaptiveBatchPolicy(cfg)
	policy.Observe(BatchObservation{
		BatchSize:     1,
		QueueDepth:    0,
		WorkerLatency: 20 * time.Millisecond,
		Success:       true,
	})

	if got := policy.NextWait(PolicySnapshot{}); got != 3*time.Millisecond {
		t.Fatalf("expected wait to increase for small batches, got %s", got)
	}
}

func TestAdaptiveBatchPolicyFlushesHalfBatchUnderQueuePressure(t *testing.T) {
	cfg := testConfig()
	cfg.BatchPolicy = BatchPolicyAdaptive
	cfg.MaxBatchSize = 8
	cfg.AdaptiveQueueHighWatermark = 4

	policy := NewAdaptiveBatchPolicy(cfg)
	if policy.ShouldFlush(PolicySnapshot{BatchSize: 3, MaxBatchSize: 8, QueueDepth: 4}) {
		t.Fatal("adaptive policy flushed before half-full batch")
	}
	if !policy.ShouldFlush(PolicySnapshot{BatchSize: 4, MaxBatchSize: 8, QueueDepth: 4}) {
		t.Fatal("adaptive policy did not flush half-full batch under queue pressure")
	}
}
