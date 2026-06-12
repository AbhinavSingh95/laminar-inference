package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/AbhinavSingh95/laminar-inference/proto"
)

const statusClientClosedRequest = 499

type Config struct {
	HTTPListenAddress               string
	WorkerAddress                   string
	WorkerAddresses                 []string
	WorkerDialTimeout               time.Duration
	WorkerRequestTimeout            time.Duration
	BatchPolicy                     string
	MaxBatchSize                    int
	MaxBatchWaitTime                time.Duration
	AdaptiveMinBatchWaitTime        time.Duration
	AdaptiveMaxBatchWaitTime        time.Duration
	AdaptiveTargetWorkerLatency     time.Duration
	AdaptiveQueueHighWatermark      int
	RequestQueueSize                int
	MaxPromptBytes                  int
	AdmissionEnabled                bool
	AdmissionMaxInFlightTokens      int
	AdmissionEstimatedTokensPerByte float64
	AdmissionEstimatedOutputTokens  int
	CircuitBreakerThreshold         int
	CircuitBreakerResetAfter        time.Duration
	OTLPEndpoint                    string
	OTLPBatchSize                   int
	OTLPFlushInterval               time.Duration
	OTLPQueueSize                   int
	OTLPTimeout                     time.Duration
}

func DefaultConfig() Config {
	return Config{
		HTTPListenAddress:               ":8080",
		WorkerAddress:                   "localhost:50051",
		WorkerAddresses:                 []string{"localhost:50051"},
		WorkerDialTimeout:               5 * time.Second,
		WorkerRequestTimeout:            30 * time.Second,
		BatchPolicy:                     BatchPolicyFixed,
		MaxBatchSize:                    32,
		MaxBatchWaitTime:                10 * time.Millisecond,
		AdaptiveMinBatchWaitTime:        1 * time.Millisecond,
		AdaptiveMaxBatchWaitTime:        10 * time.Millisecond,
		AdaptiveTargetWorkerLatency:     150 * time.Millisecond,
		AdaptiveQueueHighWatermark:      32,
		RequestQueueSize:                1000,
		MaxPromptBytes:                  8192,
		AdmissionEnabled:                false,
		AdmissionMaxInFlightTokens:      0,
		AdmissionEstimatedTokensPerByte: 0.25,
		AdmissionEstimatedOutputTokens:  64,
		CircuitBreakerThreshold:         5,
		CircuitBreakerResetAfter:        30 * time.Second,
		OTLPEndpoint:                    "",
		OTLPBatchSize:                   defaultOTLPBatchSize,
		OTLPFlushInterval:               defaultOTLPFlushInterval,
		OTLPQueueSize:                   defaultOTLPQueueSize,
		OTLPTimeout:                     defaultOTLPTimeout,
	}
}

func LoadConfigFromEnv() Config {
	cfg := DefaultConfig()
	cfg.HTTPListenAddress = envString("LAMINAR_HTTP_ADDR", cfg.HTTPListenAddress)
	cfg.WorkerAddress = envString("LAMINAR_WORKER_ADDR", cfg.WorkerAddress)
	cfg.WorkerAddresses = parseWorkerAddresses(os.Getenv("LAMINAR_WORKER_ADDRS"))
	cfg.WorkerDialTimeout = envDuration("LAMINAR_WORKER_DIAL_TIMEOUT", cfg.WorkerDialTimeout)
	cfg.WorkerRequestTimeout = envDuration("LAMINAR_WORKER_REQUEST_TIMEOUT", cfg.WorkerRequestTimeout)
	cfg.BatchPolicy = envString("LAMINAR_BATCH_POLICY", cfg.BatchPolicy)
	cfg.MaxBatchSize = envInt("LAMINAR_MAX_BATCH_SIZE", cfg.MaxBatchSize)
	cfg.MaxBatchWaitTime = envDuration("LAMINAR_MAX_BATCH_WAIT", cfg.MaxBatchWaitTime)
	cfg.AdaptiveMinBatchWaitTime = envDuration("LAMINAR_ADAPTIVE_MIN_WAIT", cfg.AdaptiveMinBatchWaitTime)
	cfg.AdaptiveMaxBatchWaitTime = envDuration("LAMINAR_ADAPTIVE_MAX_WAIT", cfg.AdaptiveMaxBatchWaitTime)
	cfg.AdaptiveTargetWorkerLatency = envDuration("LAMINAR_ADAPTIVE_TARGET_LATENCY", cfg.AdaptiveTargetWorkerLatency)
	cfg.AdaptiveQueueHighWatermark = envInt("LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK", cfg.AdaptiveQueueHighWatermark)
	cfg.RequestQueueSize = envInt("LAMINAR_QUEUE_SIZE", cfg.RequestQueueSize)
	cfg.MaxPromptBytes = envInt("LAMINAR_MAX_PROMPT_BYTES", cfg.MaxPromptBytes)
	cfg.AdmissionEnabled = envBool("LAMINAR_ADMISSION_ENABLED", cfg.AdmissionEnabled)
	cfg.AdmissionMaxInFlightTokens = envInt("LAMINAR_ADMISSION_MAX_IN_FLIGHT_TOKENS", cfg.AdmissionMaxInFlightTokens)
	cfg.AdmissionEstimatedTokensPerByte = envFloat("LAMINAR_ADMISSION_ESTIMATED_TOKENS_PER_BYTE", cfg.AdmissionEstimatedTokensPerByte)
	cfg.AdmissionEstimatedOutputTokens = envInt("LAMINAR_ADMISSION_ESTIMATED_OUTPUT_TOKENS", cfg.AdmissionEstimatedOutputTokens)
	cfg.CircuitBreakerThreshold = envInt("LAMINAR_CIRCUIT_THRESHOLD", cfg.CircuitBreakerThreshold)
	cfg.CircuitBreakerResetAfter = envDuration("LAMINAR_CIRCUIT_RESET_AFTER", cfg.CircuitBreakerResetAfter)
	cfg.OTLPEndpoint = envString("LAMINAR_OTLP_ENDPOINT", cfg.OTLPEndpoint)
	cfg.OTLPBatchSize = envInt("LAMINAR_OTLP_BATCH_SIZE", cfg.OTLPBatchSize)
	cfg.OTLPFlushInterval = envDuration("LAMINAR_OTLP_FLUSH_INTERVAL", cfg.OTLPFlushInterval)
	cfg.OTLPQueueSize = envInt("LAMINAR_OTLP_QUEUE_SIZE", cfg.OTLPQueueSize)
	cfg.OTLPTimeout = envDuration("LAMINAR_OTLP_TIMEOUT", cfg.OTLPTimeout)
	return cfg.normalized()
}

func envString(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		log.Printf("invalid %s=%q, using %d", key, raw, fallback)
		return fallback
	}
	return value
}

func envFloat(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		log.Printf("invalid %s=%q, using %f", key, raw, fallback)
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		log.Printf("invalid %s=%q, using %t", key, raw, fallback)
		return fallback
	}
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		log.Printf("invalid %s=%q, using %s", key, raw, fallback)
		return fallback
	}
	return value
}

func parseWorkerAddresses(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return normalizeWorkerAddresses(strings.Split(raw, ","))
}

func normalizeWorkerAddresses(addresses []string) []string {
	normalized := make([]string, 0, len(addresses))
	seen := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}
		if _, exists := seen[address]; exists {
			continue
		}
		seen[address] = struct{}{}
		normalized = append(normalized, address)
	}
	return normalized
}

func (cfg Config) normalized() Config {
	cfg.BatchPolicy = strings.ToLower(strings.TrimSpace(cfg.BatchPolicy))
	if cfg.BatchPolicy != BatchPolicyAdaptive {
		cfg.BatchPolicy = BatchPolicyFixed
	}
	cfg.WorkerAddress = strings.TrimSpace(cfg.WorkerAddress)
	cfg.WorkerAddresses = normalizeWorkerAddresses(cfg.WorkerAddresses)
	if len(cfg.WorkerAddresses) == 0 && cfg.WorkerAddress != "" {
		cfg.WorkerAddresses = normalizeWorkerAddresses([]string{cfg.WorkerAddress})
	}
	if len(cfg.WorkerAddresses) == 0 {
		cfg.WorkerAddresses = []string{DefaultConfig().WorkerAddress}
	}
	cfg.WorkerAddress = cfg.WorkerAddresses[0]
	if cfg.MaxBatchSize <= 0 {
		cfg.MaxBatchSize = DefaultConfig().MaxBatchSize
	}
	if cfg.MaxBatchWaitTime <= 0 {
		cfg.MaxBatchWaitTime = DefaultConfig().MaxBatchWaitTime
	}
	if cfg.AdaptiveMinBatchWaitTime <= 0 {
		cfg.AdaptiveMinBatchWaitTime = DefaultConfig().AdaptiveMinBatchWaitTime
	}
	if cfg.AdaptiveMaxBatchWaitTime <= 0 {
		cfg.AdaptiveMaxBatchWaitTime = cfg.MaxBatchWaitTime
	}
	if cfg.AdaptiveMaxBatchWaitTime < cfg.AdaptiveMinBatchWaitTime {
		cfg.AdaptiveMaxBatchWaitTime = cfg.AdaptiveMinBatchWaitTime
	}
	if cfg.AdaptiveTargetWorkerLatency <= 0 {
		cfg.AdaptiveTargetWorkerLatency = DefaultConfig().AdaptiveTargetWorkerLatency
	}
	if cfg.AdaptiveQueueHighWatermark <= 0 {
		cfg.AdaptiveQueueHighWatermark = cfg.MaxBatchSize
	}
	if cfg.RequestQueueSize < 0 {
		cfg.RequestQueueSize = 0
	}
	if cfg.MaxPromptBytes <= 0 {
		cfg.MaxPromptBytes = DefaultConfig().MaxPromptBytes
	}
	if cfg.AdmissionMaxInFlightTokens < 0 {
		cfg.AdmissionMaxInFlightTokens = 0
	}
	if cfg.AdmissionEstimatedTokensPerByte <= 0 {
		cfg.AdmissionEstimatedTokensPerByte = DefaultConfig().AdmissionEstimatedTokensPerByte
	}
	if cfg.AdmissionEstimatedOutputTokens < 0 {
		cfg.AdmissionEstimatedOutputTokens = 0
	}
	if cfg.WorkerDialTimeout <= 0 {
		cfg.WorkerDialTimeout = DefaultConfig().WorkerDialTimeout
	}
	if cfg.WorkerRequestTimeout <= 0 {
		cfg.WorkerRequestTimeout = DefaultConfig().WorkerRequestTimeout
	}
	if cfg.CircuitBreakerThreshold <= 0 {
		cfg.CircuitBreakerThreshold = DefaultConfig().CircuitBreakerThreshold
	}
	if cfg.CircuitBreakerResetAfter <= 0 {
		cfg.CircuitBreakerResetAfter = DefaultConfig().CircuitBreakerResetAfter
	}
	cfg.OTLPEndpoint = strings.TrimSpace(cfg.OTLPEndpoint)
	if cfg.OTLPBatchSize <= 0 {
		cfg.OTLPBatchSize = DefaultConfig().OTLPBatchSize
	}
	if cfg.OTLPFlushInterval <= 0 {
		cfg.OTLPFlushInterval = DefaultConfig().OTLPFlushInterval
	}
	if cfg.OTLPQueueSize <= 0 {
		cfg.OTLPQueueSize = DefaultConfig().OTLPQueueSize
	}
	if cfg.OTLPTimeout <= 0 {
		cfg.OTLPTimeout = DefaultConfig().OTLPTimeout
	}
	return cfg
}

var (
	batchSizeDistribution = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "batch_size_distribution",
		Help:    "Distribution of batch sizes sent to the worker.",
		Buckets: []float64{1, 2, 4, 8, 16, 32, 64},
	})

	requestDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "request_duration_seconds",
		Help:    "Duration of individual requests from the HTTP client perspective.",
		Buckets: prometheus.DefBuckets,
	})

	batchLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "batch_latency_seconds",
		Help:    "Latency of batch processing at the worker RPC boundary.",
		Buckets: prometheus.DefBuckets,
	})

	workerErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "worker_errors_total",
		Help: "Total number of worker errors encountered.",
	})

	requestsCancelled = promauto.NewCounter(prometheus.CounterOpts{
		Name: "requests_cancelled_total",
		Help: "Total number of requests cancelled before a response was delivered.",
	})

	requestsRejected = promauto.NewCounter(prometheus.CounterOpts{
		Name: "requests_rejected_total",
		Help: "Total number of requests rejected before entering the batcher.",
	})

	queueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "request_queue_depth",
		Help: "Current number of requests waiting in the gateway queue.",
	})

	admissionInFlightTokens = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "admission_in_flight_tokens",
		Help: "Estimated tokens currently admitted into gateway work.",
	})

	admissionAcceptedTokens = promauto.NewCounter(prometheus.CounterOpts{
		Name: "admission_accepted_tokens_total",
		Help: "Total estimated tokens accepted by admission control.",
	})

	admissionRejectedTokens = promauto.NewCounter(prometheus.CounterOpts{
		Name: "admission_rejected_tokens_total",
		Help: "Total estimated tokens rejected by admission control.",
	})

	admissionRejectedRequests = promauto.NewCounter(prometheus.CounterOpts{
		Name: "admission_rejected_requests_total",
		Help: "Total requests rejected by token-budget admission control.",
	})
)

type WorkerClient interface {
	ProcessBatch(ctx context.Context, in *pb.BatchRequest, opts ...grpc.CallOption) (*pb.BatchResponse, error)
}

type InferenceStreamClient interface {
	Recv() (*pb.StreamResponse, error)
}

type StreamingWorkerClient interface {
	Stream(ctx context.Context, in *pb.RequestItem, opts ...grpc.CallOption) (InferenceStreamClient, error)
}

type ErrorCode string

const (
	ErrRequestCancelled  ErrorCode = "request_cancelled"
	ErrQueueFull         ErrorCode = "queue_full"
	ErrAdmissionRejected ErrorCode = "admission_rejected"
	ErrWorkerError       ErrorCode = "worker_error"
	ErrWorkerTimeout     ErrorCode = "worker_timeout"
	ErrWorkerUnavailable ErrorCode = "worker_unavailable"
	ErrMissingResponse   ErrorCode = "missing_response"
)

type Request struct {
	ID        string
	Prompt    string
	RespChan  chan *Response
	StartTime time.Time
	Ctx       context.Context
	TraceID   string
	QueueSpan *ActiveSpan
	Admission AdmissionLease
}

type requestTraceSpans struct {
	flush  *ActiveSpan
	worker *ActiveSpan
}

type Response struct {
	ID        string            `json:"id"`
	Result    string            `json:"result,omitempty"`
	Error     string            `json:"error,omitempty"`
	ErrorCode ErrorCode         `json:"error_code,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type PredictRequest struct {
	Prompt string `json:"prompt"`
}

type StreamEvent struct {
	ID       string            `json:"id"`
	Delta    string            `json:"delta,omitempty"`
	Result   string            `json:"result,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type Snapshot struct {
	NextID        int                    `json:"next_request_id"`
	QueueCapacity int                    `json:"queue_capacity"`
	FailureCount  int                    `json:"consecutive_worker_failures"`
	CircuitOpen   bool                   `json:"circuit_open"`
	CircuitState  string                 `json:"circuit_state"`
	BatchPolicy   map[string]interface{} `json:"batch_policy"`
	Admission     AdmissionSnapshot      `json:"admission"`
	Workers       []WorkerSnapshot       `json:"workers,omitempty"`
}

type Batcher struct {
	requestChan chan *Request
	client      WorkerClient
	config      Config
	policy      BatchPolicy
	admission   *AdmissionController
	tracer      *TraceRecorder

	mu            sync.Mutex
	nextID        int
	failureCount  int
	circuitOpen   bool
	circuitOpenAt time.Time
}

func NewBatcher(client WorkerClient, config Config) *Batcher {
	return NewBatcherWithTracer(client, config, NewTraceRecorder(defaultTraceCapacity))
}

func NewBatcherWithTracer(client WorkerClient, config Config, tracer *TraceRecorder) *Batcher {
	config = config.normalized()
	if tracer == nil {
		tracer = NewTraceRecorder(defaultTraceCapacity)
	}
	b := &Batcher{
		requestChan: make(chan *Request, config.RequestQueueSize),
		client:      client,
		config:      config,
		policy:      NewBatchPolicy(config),
		admission:   NewAdmissionController(config),
		tracer:      tracer,
		nextID:      1,
	}
	go b.run()
	return b
}

func (b *Batcher) GenerateID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := fmt.Sprintf("req-%d", b.nextID)
	b.nextID++
	return id
}

func (b *Batcher) Submit(ctx context.Context, prompt string, traceID string) *Response {
	return b.submit(ctx, prompt, traceID, "")
}

func (b *Batcher) SubmitWithParent(ctx context.Context, prompt string, traceID string, parentSpanID string) *Response {
	return b.submit(ctx, prompt, traceID, parentSpanID)
}

func (b *Batcher) submit(ctx context.Context, prompt string, traceID string, parentSpanID string) *Response {
	startTime := time.Now()
	id := b.GenerateID()
	admissionLease, admitted := b.admission.TryAcquire(prompt)
	queueSpan := b.tracer.StartSpan("batch.queue_wait", parentSpanID, traceID, map[string]string{
		"queue.capacity":             strconv.Itoa(cap(b.requestChan)),
		"admission.estimated_tokens": strconv.Itoa(b.admission.Estimate(prompt)),
	})
	if !admitted {
		queueSpan.End("error", map[string]string{"error.code": string(ErrAdmissionRejected)})
		requestsRejected.Inc()
		requestDuration.Observe(time.Since(startTime).Seconds())
		return errorResponse(id, ErrAdmissionRejected, "admission token budget is full")
	}

	req := &Request{
		ID:        id,
		Prompt:    prompt,
		RespChan:  make(chan *Response, 1),
		StartTime: startTime,
		Ctx:       ctx,
		TraceID:   traceID,
		QueueSpan: queueSpan,
		Admission: admissionLease,
	}

	select {
	case <-ctx.Done():
		req.Admission.Release()
		queueSpan.End("cancelled", nil)
		requestsCancelled.Inc()
		requestDuration.Observe(time.Since(req.StartTime).Seconds())
		return errorResponse(req.ID, ErrRequestCancelled, "request cancelled before queueing")
	case b.requestChan <- req:
		queueDepth.Inc()
	default:
		req.Admission.Release()
		queueSpan.End("error", map[string]string{"error.code": string(ErrQueueFull)})
		requestsRejected.Inc()
		requestDuration.Observe(time.Since(req.StartTime).Seconds())
		return errorResponse(req.ID, ErrQueueFull, "request queue is full")
	}

	select {
	case resp := <-req.RespChan:
		requestDuration.Observe(time.Since(req.StartTime).Seconds())
		return resp
	case <-ctx.Done():
		req.Admission.Release()
		requestsCancelled.Inc()
		requestDuration.Observe(time.Since(req.StartTime).Seconds())
		return errorResponse(req.ID, ErrRequestCancelled, "request cancelled while waiting for batch response")
	}
}

func (b *Batcher) run() {
	batch := make([]*Request, 0, b.config.MaxBatchSize)
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	timerActive := false

	for {
		select {
		case req := <-b.requestChan:
			queueDepth.Dec()
			batch = append(batch, req)

			if len(batch) == 1 && !timerActive {
				timer.Reset(b.policy.NextWait(b.policySnapshot(len(batch))))
				timerActive = true
			}

			if b.policy.ShouldFlush(b.policySnapshot(len(batch))) {
				if timerActive {
					stopTimer(timer)
					timerActive = false
				}
				b.flush(batch)
				batch = make([]*Request, 0, b.config.MaxBatchSize)
			}

		case <-timer.C:
			timerActive = false
			if len(batch) > 0 {
				b.flush(batch)
				batch = make([]*Request, 0, b.config.MaxBatchSize)
			}
		}
	}
}

func (b *Batcher) policySnapshot(batchSize int) PolicySnapshot {
	b.mu.Lock()
	failures := b.failureCount
	b.mu.Unlock()

	return PolicySnapshot{
		BatchSize:           batchSize,
		MaxBatchSize:        b.config.MaxBatchSize,
		QueueDepth:          len(b.requestChan),
		QueueCapacity:       cap(b.requestChan),
		ConsecutiveFailures: failures,
	}
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func (b *Batcher) flush(batch []*Request) {
	if len(batch) == 0 {
		return
	}

	activeBatch := make([]*Request, 0, len(batch))
	for _, req := range batch {
		select {
		case <-req.Ctx.Done():
			req.QueueSpan.End("cancelled", nil)
			requestsCancelled.Inc()
			deliverResponse(req, errorResponse(req.ID, ErrRequestCancelled, "cancelled before batch processing"))
		default:
			req.QueueSpan.End("ok", map[string]string{
				"queue.depth": strconv.Itoa(len(b.requestChan)),
			})
			activeBatch = append(activeBatch, req)
		}
	}

	if len(activeBatch) == 0 {
		log.Printf("[gateway] skipped batch: all %d requests were cancelled", len(batch))
		return
	}

	if b.isCircuitOpen(time.Now()) {
		log.Printf("[gateway] rejecting batch of %d requests: circuit breaker is open", len(activeBatch))
		b.policy.Observe(BatchObservation{
			BatchSize:  len(activeBatch),
			QueueDepth: len(b.requestChan),
			Success:    false,
		})
		for _, req := range activeBatch {
			flushSpan := b.tracer.StartSpan("batch.flush", req.QueueSpan.SpanID(), req.TraceID, map[string]string{
				"batch.size":          strconv.Itoa(len(activeBatch)),
				"batch.original_size": strconv.Itoa(len(batch)),
				"request.id":          req.ID,
			})
			flushSpan.End("error", map[string]string{"error.code": string(ErrWorkerUnavailable)})
			deliverResponse(req, errorResponse(req.ID, ErrWorkerUnavailable, "worker temporarily unavailable"))
		}
		return
	}

	batchSize := len(activeBatch)
	batchSizeDistribution.Observe(float64(batchSize))
	log.Printf("[gateway] flushing batch size=%d original_size=%d", batchSize, len(batch))

	traceSpans := make(map[string]requestTraceSpans, len(activeBatch))
	for _, req := range activeBatch {
		flushSpan := b.tracer.StartSpan("batch.flush", req.QueueSpan.SpanID(), req.TraceID, map[string]string{
			"batch.size":          strconv.Itoa(batchSize),
			"batch.original_size": strconv.Itoa(len(batch)),
			"request.id":          req.ID,
		})
		traceSpans[req.ID] = requestTraceSpans{flush: flushSpan}
	}

	batchReq := &pb.BatchRequest{
		Requests: make([]*pb.RequestItem, len(activeBatch)),
	}
	for i, req := range activeBatch {
		batchReq.Requests[i] = &pb.RequestItem{
			Id:      req.ID,
			Prompt:  req.Prompt,
			TraceId: req.TraceID,
		}
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), b.config.WorkerRequestTimeout)
	defer cancel()

	for _, req := range activeBatch {
		spans := traceSpans[req.ID]
		spans.worker = b.tracer.StartSpan("worker.grpc", spans.flush.SpanID(), req.TraceID, map[string]string{
			"rpc.system": "grpc",
			"rpc.method": "ProcessBatch",
			"batch.size": strconv.Itoa(batchSize),
			"request.id": req.ID,
		})
		traceSpans[req.ID] = spans
	}

	batchResp, err := b.client.ProcessBatch(ctx, batchReq)
	latency := time.Since(start)
	batchLatency.Observe(latency.Seconds())

	if err != nil {
		for _, spans := range traceSpans {
			spans.worker.End("error", map[string]string{"error": err.Error()})
			spans.flush.End("error", map[string]string{"error": err.Error()})
		}
		b.recordFailure()
		b.policy.Observe(BatchObservation{
			BatchSize:     batchSize,
			QueueDepth:    len(b.requestChan),
			WorkerLatency: latency,
			Success:       false,
		})
		code, message := classifyWorkerError(err)
		log.Printf("[gateway] worker error batch_size=%d latency=%s err=%v", batchSize, latency, err)
		for _, req := range activeBatch {
			deliverResponse(req, errorResponse(req.ID, code, message))
		}
		return
	}

	for _, spans := range traceSpans {
		spans.worker.End("ok", nil)
	}
	b.recordSuccess()
	b.policy.Observe(BatchObservation{
		BatchSize:     batchSize,
		QueueDepth:    len(b.requestChan),
		WorkerLatency: latency,
		Success:       true,
	})
	responseMap := make(map[string]*pb.ResponseItem, len(batchResp.Responses))
	for _, resp := range batchResp.Responses {
		responseMap[resp.Id] = resp
	}

	for _, req := range activeBatch {
		resp, found := responseMap[req.ID]
		if !found {
			deliverResponse(req, errorResponse(req.ID, ErrMissingResponse, "worker response missing request id"))
			continue
		}
		spans := traceSpans[req.ID]
		if resp.BackendLatencyMicros > 0 || resp.BackendName != "" {
			attributes := map[string]string{
				"backend.name": resp.BackendName,
				"request.id":   req.ID,
			}
			for key, value := range resp.BackendMetadata {
				if strings.TrimSpace(key) == "" {
					continue
				}
				attributes["backend."+key] = value
			}
			b.tracer.RecordSpan(TraceSpan{
				TraceID:        req.TraceID,
				SpanID:         randomHex(8),
				ParentSpanID:   spans.worker.SpanID(),
				Name:           "backend.inference",
				StartUnixNanos: start.UnixNano(),
				DurationMicros: resp.BackendLatencyMicros,
				Status:         "ok",
				Attributes:     attributes,
			})
		}
		deliverResponse(req, &Response{
			ID:       resp.Id,
			Result:   resp.Result,
			Metadata: cloneAttributes(resp.BackendMetadata),
		})
	}

	for _, spans := range traceSpans {
		spans.flush.End("ok", map[string]string{"worker.latency_micros": strconv.FormatInt(latency.Microseconds(), 10)})
	}
	log.Printf("[gateway] completed batch size=%d latency=%s", batchSize, latency)
}

func classifyWorkerError(err error) (ErrorCode, string) {
	workerErrors.Inc()
	if errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
		return ErrWorkerTimeout, "worker request timed out"
	}
	if status.Code(err) == codes.Unavailable {
		return ErrWorkerUnavailable, "worker unavailable"
	}
	return ErrWorkerError, "worker failed to process batch"
}

func deliverResponse(req *Request, resp *Response) {
	req.Admission.Release()
	select {
	case req.RespChan <- resp:
	default:
	}
}

func errorResponse(id string, code ErrorCode, message string) *Response {
	return &Response{
		ID:        id,
		Error:     message,
		ErrorCode: code,
	}
}

func (b *Batcher) isCircuitOpen(now time.Time) bool {
	if provider, ok := b.client.(WorkerReadinessProvider); ok {
		if provider.WorkersReady() {
			b.mu.Lock()
			b.failureCount = 0
			b.circuitOpen = false
			b.mu.Unlock()
			return false
		}

		b.mu.Lock()
		b.failureCount = maxInt(b.failureCount, b.config.CircuitBreakerThreshold)
		b.circuitOpen = true
		b.circuitOpenAt = now
		b.mu.Unlock()
		return true
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.failureCount >= b.config.CircuitBreakerThreshold && !b.circuitOpen {
		b.circuitOpen = true
		b.circuitOpenAt = now
		log.Printf("[gateway] circuit breaker opened after %d consecutive failures", b.failureCount)
	}

	if b.circuitOpen && now.Sub(b.circuitOpenAt) >= b.config.CircuitBreakerResetAfter {
		b.circuitOpen = false
		b.failureCount = 0
		log.Printf("[gateway] circuit breaker half-open: allowing worker probe")
	}

	return b.circuitOpen
}

func (b *Batcher) recordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failureCount = 0
	if b.circuitOpen {
		b.circuitOpen = false
		log.Printf("[gateway] circuit breaker closed after successful worker call")
	}
}

func (b *Batcher) recordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failureCount++
}

func (b *Batcher) Ready() bool {
	return !b.isCircuitOpen(time.Now())
}

func (b *Batcher) Snapshot() Snapshot {
	b.mu.Lock()
	state := "closed"
	if b.circuitOpen {
		state = "open"
	}
	snapshot := Snapshot{
		NextID:        b.nextID,
		QueueCapacity: cap(b.requestChan),
		FailureCount:  b.failureCount,
		CircuitOpen:   b.circuitOpen,
		CircuitState:  state,
	}
	b.mu.Unlock()

	snapshot.BatchPolicy = b.policy.Snapshot()
	snapshot.Admission = b.admission.Snapshot()
	if provider, ok := b.client.(WorkerStatsProvider); ok {
		snapshot.Workers = provider.WorkerSnapshots()
	}
	return snapshot
}

func handlePredict(batcher *Batcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, int64(batcher.config.MaxPromptBytes)+1024)
		var req PredictRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		req.Prompt = strings.TrimSpace(req.Prompt)
		if req.Prompt == "" {
			http.Error(w, "prompt is required", http.StatusBadRequest)
			return
		}
		if len(req.Prompt) > batcher.config.MaxPromptBytes {
			http.Error(w, "prompt is too large", http.StatusRequestEntityTooLarge)
			return
		}

		traceID := strings.TrimSpace(r.Header.Get("X-Trace-ID"))
		parentSpanID := ""
		traceFlags := defaultTraceFlags
		if traceContext, ok := parseTraceParent(r.Header.Get("Traceparent")); ok {
			traceID = traceContext.TraceID
			parentSpanID = traceContext.ParentSpanID
			traceFlags = traceContext.TraceFlags
		} else if traceID == "" {
			traceID = randomHex(16)
		} else if isValidTraceID(strings.ToLower(traceID)) {
			traceID = strings.ToLower(traceID)
		}

		httpSpan := batcher.tracer.StartSpan("http.predict", parentSpanID, traceID, map[string]string{
			"http.method":          r.Method,
			"http.route":           "/predict",
			"request.prompt_bytes": strconv.Itoa(len(req.Prompt)),
			"trace.flags":          traceFlags,
		})
		resp := batcher.SubmitWithParent(r.Context(), req.Prompt, traceID, httpSpan.SpanID())
		statusCode := http.StatusOK
		spanStatus := "ok"
		if resp.Error != "" {
			statusCode = statusForError(resp.ErrorCode)
			spanStatus = "error"
			if resp.ErrorCode == ErrRequestCancelled {
				spanStatus = "cancelled"
			}
		}
		httpSpan.End(spanStatus, map[string]string{
			"http.status_code": strconv.Itoa(statusCode),
			"request.id":       resp.ID,
		})

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Trace-ID", traceID)
		if traceParent, ok := formatTraceParent(traceID, httpSpan.SpanID(), traceFlags); ok {
			w.Header().Set("Traceparent", traceParent)
		}
		if resp.Error != "" {
			w.WriteHeader(statusCode)
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func handlePredictStream(batcher *Batcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		streamingClient, ok := batcher.client.(StreamingWorkerClient)
		if !ok {
			http.Error(w, "streaming worker client is not available", http.StatusNotImplemented)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, int64(batcher.config.MaxPromptBytes)+1024)
		var req PredictRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		req.Prompt = strings.TrimSpace(req.Prompt)
		if req.Prompt == "" {
			http.Error(w, "prompt is required", http.StatusBadRequest)
			return
		}
		if len(req.Prompt) > batcher.config.MaxPromptBytes {
			http.Error(w, "prompt is too large", http.StatusRequestEntityTooLarge)
			return
		}

		traceID := strings.TrimSpace(r.Header.Get("X-Trace-ID"))
		parentSpanID := ""
		traceFlags := defaultTraceFlags
		if traceContext, ok := parseTraceParent(r.Header.Get("Traceparent")); ok {
			traceID = traceContext.TraceID
			parentSpanID = traceContext.ParentSpanID
			traceFlags = traceContext.TraceFlags
		} else if traceID == "" {
			traceID = randomHex(16)
		} else if isValidTraceID(strings.ToLower(traceID)) {
			traceID = strings.ToLower(traceID)
		}

		requestID := batcher.GenerateID()
		httpSpan := batcher.tracer.StartSpan("http.predict.stream", parentSpanID, traceID, map[string]string{
			"http.method":          r.Method,
			"http.route":           "/predict/stream",
			"request.prompt_bytes": strconv.Itoa(len(req.Prompt)),
			"trace.flags":          traceFlags,
		})
		workerSpan := batcher.tracer.StartSpan("worker.grpc.stream", httpSpan.SpanID(), traceID, map[string]string{
			"rpc.system": "grpc",
			"rpc.method": "Stream",
			"request.id": requestID,
		})

		ctx, cancel := context.WithTimeout(r.Context(), batcher.config.WorkerRequestTimeout)
		defer cancel()
		stream, err := streamingClient.Stream(ctx, &pb.RequestItem{
			Id:      requestID,
			Prompt:  req.Prompt,
			TraceId: traceID,
		})
		if err != nil {
			code, message := classifyWorkerError(err)
			statusCode := statusForError(code)
			workerSpan.End("error", map[string]string{"error": err.Error()})
			httpSpan.End("error", map[string]string{
				"http.status_code": strconv.Itoa(statusCode),
				"request.id":       requestID,
			})
			http.Error(w, message, statusCode)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			workerSpan.End("error", map[string]string{"error": "streaming unsupported"})
			httpSpan.End("error", map[string]string{
				"http.status_code": strconv.Itoa(http.StatusInternalServerError),
				"request.id":       requestID,
			})
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Trace-ID", traceID)
		if traceParent, ok := formatTraceParent(traceID, httpSpan.SpanID(), traceFlags); ok {
			w.Header().Set("Traceparent", traceParent)
		}
		w.WriteHeader(http.StatusOK)

		backendSpan := batcher.tracer.StartSpan("backend.inference", workerSpan.SpanID(), traceID, map[string]string{
			"request.id": requestID,
		})
		tokenIndex := 0
		doneSeen := false
		spanStatus := "ok"

		for {
			event, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				spanStatus = "error"
				_ = writeSSEEvent(w, "error", StreamEvent{
					ID: requestID,
					Metadata: map[string]string{
						"error": "worker stream failed",
					},
				})
				flusher.Flush()
				backendSpan.End("error", map[string]string{"error": err.Error()})
				workerSpan.End("error", map[string]string{"error": err.Error()})
				httpSpan.End("error", map[string]string{
					"http.status_code": strconv.Itoa(http.StatusOK),
					"request.id":       requestID,
				})
				return
			}

			eventType := strings.TrimSpace(event.EventType)
			if eventType == "" {
				eventType = "token"
			}
			payload := StreamEvent{
				ID:       event.Id,
				Delta:    event.Delta,
				Result:   event.Result,
				Metadata: cloneAttributes(event.BackendMetadata),
			}
			if err := writeSSEEvent(w, eventType, payload); err != nil {
				spanStatus = "error"
				backendSpan.End("error", map[string]string{"error": err.Error()})
				workerSpan.End("error", map[string]string{"error": err.Error()})
				httpSpan.End("error", map[string]string{
					"http.status_code": strconv.Itoa(statusClientClosedRequest),
					"request.id":       requestID,
				})
				return
			}
			flusher.Flush()

			if eventType == "token" {
				attrs := backendAttributes(event.BackendName, requestID, event.BackendMetadata)
				attrs["token.index"] = strconv.Itoa(tokenIndex)
				attrs["token.delta_chars"] = strconv.Itoa(len(event.Delta))
				batcher.tracer.RecordSpan(TraceSpan{
					TraceID:        traceID,
					SpanID:         randomHex(8),
					ParentSpanID:   backendSpan.SpanID(),
					Name:           "backend.token",
					StartUnixNanos: time.Now().UnixNano(),
					DurationMicros: 0,
					Status:         "ok",
					Attributes:     attrs,
				})
				tokenIndex++
				continue
			}
			if eventType == "done" {
				doneSeen = true
				backendSpan.End("ok", backendAttributes(event.BackendName, requestID, event.BackendMetadata))
			}
		}

		if !doneSeen {
			spanStatus = "error"
			backendSpan.End("error", map[string]string{"error": "stream ended before done event"})
		}
		workerSpan.End(spanStatus, nil)
		httpSpan.End(spanStatus, map[string]string{
			"http.status_code": strconv.Itoa(http.StatusOK),
			"request.id":       requestID,
		})
	}
}

func backendAttributes(backendName string, requestID string, metadata map[string]string) map[string]string {
	attributes := map[string]string{
		"backend.name": backendName,
		"request.id":   requestID,
	}
	for key, value := range metadata {
		if strings.TrimSpace(key) == "" {
			continue
		}
		attributes["backend."+key] = value
	}
	return attributes
}

func writeSSEEvent(w io.Writer, eventType string, payload StreamEvent) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
	return err
}

func statusForError(code ErrorCode) int {
	switch code {
	case ErrRequestCancelled:
		return statusClientClosedRequest
	case ErrAdmissionRejected:
		return http.StatusTooManyRequests
	case ErrQueueFull, ErrWorkerUnavailable:
		return http.StatusServiceUnavailable
	case ErrWorkerTimeout:
		return http.StatusGatewayTimeout
	case ErrMissingResponse:
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "healthy",
		"service": "laminar-inference-gateway",
	})
}

func handleReady(batcher *Batcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !batcher.Ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status": "not_ready",
				"reason": "worker circuit breaker is open",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ready",
		})
	}
}

func handleStats(batcher *Batcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"service": "laminar-inference-gateway",
			"config": map[string]interface{}{
				"max_batch_size":                      batcher.config.MaxBatchSize,
				"max_batch_wait_time":                 batcher.config.MaxBatchWaitTime.String(),
				"batch_policy":                        batcher.config.BatchPolicy,
				"request_queue_size":                  batcher.config.RequestQueueSize,
				"worker_address":                      batcher.config.WorkerAddress,
				"worker_addresses":                    batcher.config.WorkerAddresses,
				"worker_request_timeout":              batcher.config.WorkerRequestTimeout.String(),
				"admission_enabled":                   batcher.config.AdmissionEnabled,
				"admission_max_in_flight_tokens":      batcher.config.AdmissionMaxInFlightTokens,
				"admission_estimated_tokens_per_byte": batcher.config.AdmissionEstimatedTokensPerByte,
				"admission_estimated_output_tokens":   batcher.config.AdmissionEstimatedOutputTokens,
				"otlp_endpoint":                       batcher.config.OTLPEndpoint,
				"otlp_batch_size":                     batcher.config.OTLPBatchSize,
				"otlp_flush_interval":                 batcher.config.OTLPFlushInterval.String(),
				"otlp_queue_size":                     batcher.config.OTLPQueueSize,
			},
			"runtime": batcher.Snapshot(),
		})
	}
}

func handleTraces(batcher *Batcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		traceID := strings.TrimSpace(r.URL.Query().Get("trace_id"))
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"spans": batcher.tracer.Snapshot(traceID),
		})
	}
}

func handleOTLPTraces(batcher *Batcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		traceID := strings.TrimSpace(r.URL.Query().Get("trace_id"))
		_ = json.NewEncoder(w).Encode(batcher.tracer.OTLPExport(traceID))
	}
}

func newServeMux(batcher *Batcher) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/predict", handlePredict(batcher))
	mux.HandleFunc("/predict/stream", handlePredictStream(batcher))
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/ready", handleReady(batcher))
	mux.HandleFunc("/stats", handleStats(batcher))
	mux.HandleFunc("/traces", handleTraces(batcher))
	mux.HandleFunc("/traces/otlp", handleOTLPTraces(batcher))
	mux.Handle("/metrics", promhttp.Handler())
	return mux
}

func newHTTPServer(cfg Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.HTTPListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      cfg.WorkerRequestTimeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func main() {
	cfg := LoadConfigFromEnv()
	log.Println("=== Laminar Inference Gateway ===")
	log.Printf("connecting to workers: %s", strings.Join(cfg.WorkerAddresses, ", "))

	workerPool, err := NewGRPCWorkerPool(context.Background(), cfg.WorkerAddresses, cfg)
	if err != nil {
		log.Fatalf("failed to connect to workers: %v", err)
	}
	defer workerPool.Close()

	tracer := NewTraceRecorder(defaultTraceCapacity)
	var otlpExporter *OTLPHTTPExporter
	if cfg.OTLPEndpoint != "" {
		otlpExporterConfig := OTLPHTTPExporterConfig{
			Endpoint:      cfg.OTLPEndpoint,
			MaxBatchSize:  cfg.OTLPBatchSize,
			FlushInterval: cfg.OTLPFlushInterval,
			QueueSize:     cfg.OTLPQueueSize,
			Timeout:       cfg.OTLPTimeout,
		}
		var exporterErr error
		otlpExporter, exporterErr = NewOTLPHTTPExporter(otlpExporterConfig)
		if exporterErr != nil {
			log.Fatalf("failed to configure OTLP exporter: %v", exporterErr)
		}
		defer otlpExporter.Close()
		tracer.AddSpanExporter(otlpExporter)
		log.Printf("OTLP exporter enabled endpoint=%s batch_size=%d flush_interval=%s queue=%d", cfg.OTLPEndpoint, cfg.OTLPBatchSize, cfg.OTLPFlushInterval, cfg.OTLPQueueSize)
	}

	batcher := NewBatcherWithTracer(workerPool, cfg, tracer)
	server := newHTTPServer(cfg, newServeMux(batcher))

	log.Printf("gateway listening on http://localhost%s", cfg.HTTPListenAddress)
	log.Printf("batching max_size=%d max_wait=%s queue=%d", cfg.MaxBatchSize, cfg.MaxBatchWaitTime, cfg.RequestQueueSize)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server failed: %v", err)
	}
}
