package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	HTTPListenAddress           string
	WorkerAddress               string
	WorkerAddresses             []string
	WorkerDialTimeout           time.Duration
	WorkerRequestTimeout        time.Duration
	BatchPolicy                 string
	MaxBatchSize                int
	MaxBatchWaitTime            time.Duration
	AdaptiveMinBatchWaitTime    time.Duration
	AdaptiveMaxBatchWaitTime    time.Duration
	AdaptiveTargetWorkerLatency time.Duration
	AdaptiveQueueHighWatermark  int
	RequestQueueSize            int
	MaxPromptBytes              int
	CircuitBreakerThreshold     int
	CircuitBreakerResetAfter    time.Duration
}

func DefaultConfig() Config {
	return Config{
		HTTPListenAddress:           ":8080",
		WorkerAddress:               "localhost:50051",
		WorkerAddresses:             []string{"localhost:50051"},
		WorkerDialTimeout:           5 * time.Second,
		WorkerRequestTimeout:        30 * time.Second,
		BatchPolicy:                 BatchPolicyFixed,
		MaxBatchSize:                32,
		MaxBatchWaitTime:            10 * time.Millisecond,
		AdaptiveMinBatchWaitTime:    1 * time.Millisecond,
		AdaptiveMaxBatchWaitTime:    10 * time.Millisecond,
		AdaptiveTargetWorkerLatency: 150 * time.Millisecond,
		AdaptiveQueueHighWatermark:  32,
		RequestQueueSize:            1000,
		MaxPromptBytes:              8192,
		CircuitBreakerThreshold:     5,
		CircuitBreakerResetAfter:    30 * time.Second,
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
	cfg.CircuitBreakerThreshold = envInt("LAMINAR_CIRCUIT_THRESHOLD", cfg.CircuitBreakerThreshold)
	cfg.CircuitBreakerResetAfter = envDuration("LAMINAR_CIRCUIT_RESET_AFTER", cfg.CircuitBreakerResetAfter)
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
)

type WorkerClient interface {
	ProcessBatch(ctx context.Context, in *pb.BatchRequest, opts ...grpc.CallOption) (*pb.BatchResponse, error)
}

type ErrorCode string

const (
	ErrRequestCancelled  ErrorCode = "request_cancelled"
	ErrQueueFull         ErrorCode = "queue_full"
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
}

type Response struct {
	ID        string    `json:"id"`
	Result    string    `json:"result,omitempty"`
	Error     string    `json:"error,omitempty"`
	ErrorCode ErrorCode `json:"error_code,omitempty"`
}

type PredictRequest struct {
	Prompt string `json:"prompt"`
}

type Snapshot struct {
	NextID        int                    `json:"next_request_id"`
	QueueCapacity int                    `json:"queue_capacity"`
	FailureCount  int                    `json:"consecutive_worker_failures"`
	CircuitOpen   bool                   `json:"circuit_open"`
	CircuitState  string                 `json:"circuit_state"`
	BatchPolicy   map[string]interface{} `json:"batch_policy"`
	Workers       []WorkerSnapshot       `json:"workers,omitempty"`
}

type Batcher struct {
	requestChan chan *Request
	client      WorkerClient
	config      Config
	policy      BatchPolicy

	mu            sync.Mutex
	nextID        int
	failureCount  int
	circuitOpen   bool
	circuitOpenAt time.Time
}

func NewBatcher(client WorkerClient, config Config) *Batcher {
	config = config.normalized()
	b := &Batcher{
		requestChan: make(chan *Request, config.RequestQueueSize),
		client:      client,
		config:      config,
		policy:      NewBatchPolicy(config),
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
	req := &Request{
		ID:        b.GenerateID(),
		Prompt:    prompt,
		RespChan:  make(chan *Response, 1),
		StartTime: time.Now(),
		Ctx:       ctx,
		TraceID:   traceID,
	}

	select {
	case <-ctx.Done():
		requestsCancelled.Inc()
		requestDuration.Observe(time.Since(req.StartTime).Seconds())
		return errorResponse(req.ID, ErrRequestCancelled, "request cancelled before queueing")
	case b.requestChan <- req:
		queueDepth.Inc()
	default:
		requestsRejected.Inc()
		requestDuration.Observe(time.Since(req.StartTime).Seconds())
		return errorResponse(req.ID, ErrQueueFull, "request queue is full")
	}

	select {
	case resp := <-req.RespChan:
		requestDuration.Observe(time.Since(req.StartTime).Seconds())
		return resp
	case <-ctx.Done():
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
			requestsCancelled.Inc()
			deliverResponse(req, errorResponse(req.ID, ErrRequestCancelled, "cancelled before batch processing"))
		default:
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
			deliverResponse(req, errorResponse(req.ID, ErrWorkerUnavailable, "worker temporarily unavailable"))
		}
		return
	}

	batchSize := len(activeBatch)
	batchSizeDistribution.Observe(float64(batchSize))
	log.Printf("[gateway] flushing batch size=%d original_size=%d", batchSize, len(batch))

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

	batchResp, err := b.client.ProcessBatch(ctx, batchReq)
	latency := time.Since(start)
	batchLatency.Observe(latency.Seconds())

	if err != nil {
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
		deliverResponse(req, &Response{
			ID:     resp.Id,
			Result: resp.Result,
		})
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
		if traceID == "" {
			traceID = fmt.Sprintf("trace-%d", time.Now().UnixNano())
		}

		resp := batcher.Submit(r.Context(), req.Prompt, traceID)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Trace-ID", traceID)
		if resp.Error != "" {
			w.WriteHeader(statusForError(resp.ErrorCode))
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func statusForError(code ErrorCode) int {
	switch code {
	case ErrRequestCancelled:
		return statusClientClosedRequest
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
				"max_batch_size":         batcher.config.MaxBatchSize,
				"max_batch_wait_time":    batcher.config.MaxBatchWaitTime.String(),
				"batch_policy":           batcher.config.BatchPolicy,
				"request_queue_size":     batcher.config.RequestQueueSize,
				"worker_address":         batcher.config.WorkerAddress,
				"worker_addresses":       batcher.config.WorkerAddresses,
				"worker_request_timeout": batcher.config.WorkerRequestTimeout.String(),
			},
			"runtime": batcher.Snapshot(),
		})
	}
}

func newServeMux(batcher *Batcher) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/predict", handlePredict(batcher))
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/ready", handleReady(batcher))
	mux.HandleFunc("/stats", handleStats(batcher))
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

	batcher := NewBatcher(workerPool, cfg)
	server := newHTTPServer(cfg, newServeMux(batcher))

	log.Printf("gateway listening on http://localhost%s", cfg.HTTPListenAddress)
	log.Printf("batching max_size=%d max_wait=%s queue=%d", cfg.MaxBatchSize, cfg.MaxBatchWaitTime, cfg.RequestQueueSize)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server failed: %v", err)
	}
}
