package main

import (
	"math"
	"sync"
	"time"
)

const (
	BatchPolicyFixed    = "fixed"
	BatchPolicyAdaptive = "adaptive"
)

type PolicySnapshot struct {
	BatchSize           int
	MaxBatchSize        int
	QueueDepth          int
	QueueCapacity       int
	ConsecutiveFailures int
}

type BatchObservation struct {
	BatchSize     int
	QueueDepth    int
	WorkerLatency time.Duration
	Success       bool
}

type BatchPolicy interface {
	Name() string
	NextWait(snapshot PolicySnapshot) time.Duration
	ShouldFlush(snapshot PolicySnapshot) bool
	Observe(observation BatchObservation)
	Snapshot() map[string]interface{}
}

type FixedBatchPolicy struct {
	maxWait time.Duration
}

func NewFixedBatchPolicy(maxWait time.Duration) *FixedBatchPolicy {
	return &FixedBatchPolicy{maxWait: maxWait}
}

func (p *FixedBatchPolicy) Name() string {
	return BatchPolicyFixed
}

func (p *FixedBatchPolicy) NextWait(PolicySnapshot) time.Duration {
	return p.maxWait
}

func (p *FixedBatchPolicy) ShouldFlush(snapshot PolicySnapshot) bool {
	return snapshot.BatchSize >= snapshot.MaxBatchSize
}

func (p *FixedBatchPolicy) Observe(BatchObservation) {}

func (p *FixedBatchPolicy) Snapshot() map[string]interface{} {
	return map[string]interface{}{
		"name":     p.Name(),
		"max_wait": p.maxWait.String(),
	}
}

type AdaptiveBatchPolicy struct {
	mu sync.Mutex

	minWait             time.Duration
	maxWait             time.Duration
	currentWait         time.Duration
	targetWorkerLatency time.Duration
	queueHighWatermark  int
	maxBatchSize        int

	lastBatchSize     int
	lastQueueDepth    int
	lastWorkerLatency time.Duration
	lastSuccess       bool
	adjustments       int
}

func NewAdaptiveBatchPolicy(cfg Config) *AdaptiveBatchPolicy {
	cfg = cfg.normalized()
	currentWait := clampDuration(cfg.MaxBatchWaitTime, cfg.AdaptiveMinBatchWaitTime, cfg.AdaptiveMaxBatchWaitTime)
	return &AdaptiveBatchPolicy{
		minWait:             cfg.AdaptiveMinBatchWaitTime,
		maxWait:             cfg.AdaptiveMaxBatchWaitTime,
		currentWait:         currentWait,
		targetWorkerLatency: cfg.AdaptiveTargetWorkerLatency,
		queueHighWatermark:  cfg.AdaptiveQueueHighWatermark,
		maxBatchSize:        cfg.MaxBatchSize,
		lastSuccess:         true,
	}
}

func (p *AdaptiveBatchPolicy) Name() string {
	return BatchPolicyAdaptive
}

func (p *AdaptiveBatchPolicy) NextWait(snapshot PolicySnapshot) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()

	if snapshot.QueueDepth >= p.queueHighWatermark {
		return p.minWait
	}
	return p.currentWait
}

func (p *AdaptiveBatchPolicy) ShouldFlush(snapshot PolicySnapshot) bool {
	if snapshot.BatchSize >= snapshot.MaxBatchSize {
		return true
	}
	if snapshot.QueueDepth >= p.queueHighWatermark {
		return snapshot.BatchSize >= int(math.Ceil(float64(snapshot.MaxBatchSize)/2))
	}
	return false
}

func (p *AdaptiveBatchPolicy) Observe(observation BatchObservation) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.lastBatchSize = observation.BatchSize
	p.lastQueueDepth = observation.QueueDepth
	p.lastWorkerLatency = observation.WorkerLatency
	p.lastSuccess = observation.Success

	previous := p.currentWait
	switch {
	case !observation.Success:
		p.currentWait = p.minWait
	case observation.WorkerLatency > p.targetWorkerLatency:
		p.currentWait = maxDuration(p.minWait, p.currentWait/2)
	case observation.QueueDepth >= p.queueHighWatermark:
		p.currentWait = maxDuration(p.minWait, p.currentWait/2)
	case observation.BatchSize < maxInt(1, p.maxBatchSize/2):
		p.currentWait = minDuration(p.maxWait, p.currentWait+time.Millisecond)
	default:
		p.currentWait = clampDuration(p.currentWait, p.minWait, p.maxWait)
	}

	if p.currentWait != previous {
		p.adjustments++
	}
}

func (p *AdaptiveBatchPolicy) Snapshot() map[string]interface{} {
	p.mu.Lock()
	defer p.mu.Unlock()

	return map[string]interface{}{
		"name":                  p.Name(),
		"min_wait":              p.minWait.String(),
		"max_wait":              p.maxWait.String(),
		"current_wait":          p.currentWait.String(),
		"target_worker_latency": p.targetWorkerLatency.String(),
		"queue_high_watermark":  p.queueHighWatermark,
		"last_batch_size":       p.lastBatchSize,
		"last_queue_depth":      p.lastQueueDepth,
		"last_worker_latency":   p.lastWorkerLatency.String(),
		"last_success":          p.lastSuccess,
		"adjustments":           p.adjustments,
	}
}

func NewBatchPolicy(cfg Config) BatchPolicy {
	cfg = cfg.normalized()
	if cfg.BatchPolicy == BatchPolicyAdaptive {
		return NewAdaptiveBatchPolicy(cfg)
	}
	return NewFixedBatchPolicy(cfg.MaxBatchWaitTime)
}

func clampDuration(value, minimum, maximum time.Duration) time.Duration {
	return maxDuration(minimum, minDuration(value, maximum))
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
