package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	pb "github.com/AbhinavSingh95/laminar-inference/proto"
)

var (
	workerPoolInFlight = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "worker_in_flight_batches",
		Help: "Current number of batches in flight per worker endpoint.",
	}, []string{"worker", "address"})

	workerPoolBatches = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "worker_batches_total",
		Help: "Total number of batches routed to each worker endpoint.",
	}, []string{"worker", "address"})

	workerPoolFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "worker_failures_total",
		Help: "Total number of failed batch calls per worker endpoint.",
	}, []string{"worker", "address"})

	workerPoolLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "worker_batch_latency_seconds",
		Help:    "Worker batch latency by endpoint.",
		Buckets: prometheus.DefBuckets,
	}, []string{"worker", "address"})

	workerPoolCircuitOpen = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "worker_circuit_open",
		Help: "Whether the per-worker circuit breaker is open. 1 means open, 0 means closed.",
	}, []string{"worker", "address"})
)

type WorkerEndpointConfig struct {
	ID      string
	Address string
	Client  WorkerClient
	Closer  io.Closer
}

type WorkerSnapshot struct {
	ID                  string `json:"id"`
	Address             string `json:"address"`
	InFlight            int    `json:"in_flight"`
	TotalBatches        uint64 `json:"total_batches"`
	TotalFailures       uint64 `json:"total_failures"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	CircuitOpen         bool   `json:"circuit_open"`
	CircuitState        string `json:"circuit_state"`
	CircuitOpenUntil    string `json:"circuit_open_until,omitempty"`
	LastLatency         string `json:"last_latency"`
	LastError           string `json:"last_error,omitempty"`
	LastSelected        string `json:"last_selected,omitempty"`
}

type WorkerStatsProvider interface {
	WorkerSnapshots() []WorkerSnapshot
}

type WorkerReadinessProvider interface {
	WorkersReady() bool
}

type WorkerPool struct {
	config    Config
	endpoints []*workerEndpoint
}

type grpcWorkerClient struct {
	client pb.InferenceServiceClient
}

var _ WorkerClient = grpcWorkerClient{}
var _ StreamingWorkerClient = grpcWorkerClient{}

func (c grpcWorkerClient) ProcessBatch(ctx context.Context, req *pb.BatchRequest, opts ...grpc.CallOption) (*pb.BatchResponse, error) {
	return c.client.ProcessBatch(ctx, req, opts...)
}

func (c grpcWorkerClient) Stream(ctx context.Context, req *pb.RequestItem, opts ...grpc.CallOption) (InferenceStreamClient, error) {
	return c.client.Stream(ctx, req, opts...)
}

type workerEndpoint struct {
	mu sync.Mutex

	index   int
	id      string
	address string
	client  WorkerClient
	closer  io.Closer

	inFlight            int
	totalBatches        uint64
	totalFailures       uint64
	consecutiveFailures int
	circuitOpenUntil    time.Time
	lastLatency         time.Duration
	lastError           string
	lastSelected        time.Time
}

type workerSelectionState struct {
	endpoint     *workerEndpoint
	index        int
	inFlight     int
	totalBatches uint64
	circuitOpen  bool
}

func NewWorkerPool(endpointConfigs []WorkerEndpointConfig, cfg Config) (*WorkerPool, error) {
	cfg = cfg.normalized()
	if len(endpointConfigs) == 0 {
		return nil, fmt.Errorf("worker pool requires at least one endpoint")
	}

	pool := &WorkerPool{
		config:    cfg,
		endpoints: make([]*workerEndpoint, 0, len(endpointConfigs)),
	}
	for i, endpointConfig := range endpointConfigs {
		if endpointConfig.Client == nil {
			return nil, fmt.Errorf("worker endpoint %d has nil client", i)
		}
		address := strings.TrimSpace(endpointConfig.Address)
		id := strings.TrimSpace(endpointConfig.ID)
		if id == "" {
			id = fmt.Sprintf("worker-%d", i+1)
		}
		if address == "" {
			address = id
		}

		endpoint := &workerEndpoint{
			index:   i,
			id:      id,
			address: address,
			client:  endpointConfig.Client,
			closer:  endpointConfig.Closer,
		}
		pool.endpoints = append(pool.endpoints, endpoint)
		endpoint.setCircuitMetric(false)
		endpoint.setInFlightMetric(0)
	}

	return pool, nil
}

func NewGRPCWorkerPool(ctx context.Context, addresses []string, cfg Config) (*WorkerPool, error) {
	cfg = cfg.normalized()
	addresses = normalizeWorkerAddresses(addresses)
	if len(addresses) == 0 {
		return nil, fmt.Errorf("worker pool requires at least one address")
	}

	endpointConfigs := make([]WorkerEndpointConfig, 0, len(addresses))
	failures := make([]string, 0)
	for i, address := range addresses {
		dialCtx, cancel := context.WithTimeout(ctx, cfg.WorkerDialTimeout)
		conn, err := grpc.DialContext(
			dialCtx,
			address,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
		cancel()
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", address, err))
			continue
		}
		endpointConfigs = append(endpointConfigs, WorkerEndpointConfig{
			ID:      fmt.Sprintf("worker-%d", i+1),
			Address: address,
			Client:  grpcWorkerClient{client: pb.NewInferenceServiceClient(conn)},
			Closer:  conn,
		})
	}
	if len(endpointConfigs) == 0 {
		return nil, fmt.Errorf("failed to connect to any worker: %s", strings.Join(failures, "; "))
	}

	pool, err := NewWorkerPool(endpointConfigs, cfg)
	if err != nil {
		closeEndpointConfigs(endpointConfigs)
		return nil, err
	}
	return pool, nil
}

func (p *WorkerPool) ProcessBatch(ctx context.Context, req *pb.BatchRequest, opts ...grpc.CallOption) (*pb.BatchResponse, error) {
	endpoint, err := p.selectEndpoint(time.Now())
	if err != nil {
		return nil, err
	}

	endpoint.begin(time.Now())
	start := time.Now()
	resp, err := endpoint.client.ProcessBatch(ctx, req, opts...)
	latency := time.Since(start)
	endpoint.finish(latency, err, p.config, time.Now())
	return resp, err
}

func (p *WorkerPool) Stream(ctx context.Context, req *pb.RequestItem, opts ...grpc.CallOption) (InferenceStreamClient, error) {
	endpoint, err := p.selectEndpoint(time.Now())
	if err != nil {
		return nil, err
	}
	streamingClient, ok := endpoint.client.(StreamingWorkerClient)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "worker does not support streaming")
	}

	endpoint.begin(time.Now())
	start := time.Now()
	stream, err := streamingClient.Stream(ctx, req, opts...)
	if err != nil {
		endpoint.finish(time.Since(start), err, p.config, time.Now())
		return nil, err
	}
	return &pooledInferenceStream{
		InferenceStreamClient: stream,
		endpoint:              endpoint,
		start:                 start,
		config:                p.config,
	}, nil
}

type pooledInferenceStream struct {
	InferenceStreamClient
	endpoint *workerEndpoint
	start    time.Time
	config   Config

	once sync.Once
}

func (s *pooledInferenceStream) Recv() (*pb.StreamResponse, error) {
	resp, err := s.InferenceStreamClient.Recv()
	if err != nil {
		finishErr := err
		if errors.Is(err, io.EOF) {
			finishErr = nil
		}
		s.finish(finishErr)
	}
	return resp, err
}

func (s *pooledInferenceStream) finish(err error) {
	s.once.Do(func() {
		s.endpoint.finish(time.Since(s.start), err, s.config, time.Now())
	})
}

func (p *WorkerPool) WorkerSnapshots() []WorkerSnapshot {
	now := time.Now()
	snapshots := make([]WorkerSnapshot, 0, len(p.endpoints))
	for _, endpoint := range p.endpoints {
		snapshots = append(snapshots, endpoint.snapshot(now))
	}
	return snapshots
}

func (p *WorkerPool) WorkersReady() bool {
	now := time.Now()
	for _, endpoint := range p.endpoints {
		if endpoint.available(now) {
			return true
		}
	}
	return false
}

func (p *WorkerPool) Close() error {
	errs := make([]error, 0)
	for _, endpoint := range p.endpoints {
		if endpoint.closer == nil {
			continue
		}
		if err := endpoint.closer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", endpoint.address, err))
		}
	}
	return errors.Join(errs...)
}

func (p *WorkerPool) selectEndpoint(now time.Time) (*workerEndpoint, error) {
	var selected workerSelectionState
	found := false
	for _, endpoint := range p.endpoints {
		state := endpoint.selectionState(now)
		if state.circuitOpen {
			continue
		}
		if !found || lessLoaded(state, selected) {
			selected = state
			found = true
		}
	}
	if !found {
		return nil, status.Error(codes.Unavailable, "no available workers")
	}
	return selected.endpoint, nil
}

func lessLoaded(candidate workerSelectionState, selected workerSelectionState) bool {
	if candidate.inFlight != selected.inFlight {
		return candidate.inFlight < selected.inFlight
	}
	if candidate.totalBatches != selected.totalBatches {
		return candidate.totalBatches < selected.totalBatches
	}
	return candidate.index < selected.index
}

func (e *workerEndpoint) begin(now time.Time) {
	e.mu.Lock()
	e.inFlight++
	e.totalBatches++
	e.lastSelected = now
	inFlight := e.inFlight
	e.mu.Unlock()

	workerPoolBatches.WithLabelValues(e.id, e.address).Inc()
	e.setInFlightMetric(inFlight)
}

func (e *workerEndpoint) finish(latency time.Duration, err error, cfg Config, now time.Time) {
	e.mu.Lock()
	if e.inFlight > 0 {
		e.inFlight--
	}
	e.lastLatency = latency

	if err != nil {
		e.totalFailures++
		e.consecutiveFailures++
		e.lastError = err.Error()
		if e.consecutiveFailures >= cfg.CircuitBreakerThreshold {
			e.circuitOpenUntil = now.Add(cfg.CircuitBreakerResetAfter)
		}
	} else {
		e.consecutiveFailures = 0
		e.circuitOpenUntil = time.Time{}
		e.lastError = ""
	}

	inFlight := e.inFlight
	circuitOpen := e.circuitOpenLocked(now)
	e.mu.Unlock()

	workerPoolLatency.WithLabelValues(e.id, e.address).Observe(latency.Seconds())
	if err != nil {
		workerPoolFailures.WithLabelValues(e.id, e.address).Inc()
	}
	e.setInFlightMetric(inFlight)
	e.setCircuitMetric(circuitOpen)
}

func (e *workerEndpoint) selectionState(now time.Time) workerSelectionState {
	e.mu.Lock()
	defer e.mu.Unlock()

	circuitOpen := e.refreshCircuitLocked(now)
	return workerSelectionState{
		endpoint:     e,
		index:        e.index,
		inFlight:     e.inFlight,
		totalBatches: e.totalBatches,
		circuitOpen:  circuitOpen,
	}
}

func (e *workerEndpoint) available(now time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.refreshCircuitLocked(now)
}

func (e *workerEndpoint) snapshot(now time.Time) WorkerSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()

	circuitOpen := e.refreshCircuitLocked(now)
	circuitState := "closed"
	circuitOpenUntil := ""
	if circuitOpen {
		circuitState = "open"
		circuitOpenUntil = e.circuitOpenUntil.UTC().Format(time.RFC3339Nano)
	}

	lastSelected := ""
	if !e.lastSelected.IsZero() {
		lastSelected = e.lastSelected.UTC().Format(time.RFC3339Nano)
	}

	return WorkerSnapshot{
		ID:                  e.id,
		Address:             e.address,
		InFlight:            e.inFlight,
		TotalBatches:        e.totalBatches,
		TotalFailures:       e.totalFailures,
		ConsecutiveFailures: e.consecutiveFailures,
		CircuitOpen:         circuitOpen,
		CircuitState:        circuitState,
		CircuitOpenUntil:    circuitOpenUntil,
		LastLatency:         e.lastLatency.String(),
		LastError:           e.lastError,
		LastSelected:        lastSelected,
	}
}

func (e *workerEndpoint) refreshCircuitLocked(now time.Time) bool {
	if e.circuitOpenUntil.IsZero() {
		e.setCircuitMetric(false)
		return false
	}
	if now.Before(e.circuitOpenUntil) {
		e.setCircuitMetric(true)
		return true
	}
	e.circuitOpenUntil = time.Time{}
	e.consecutiveFailures = 0
	e.lastError = ""
	e.setCircuitMetric(false)
	return false
}

func (e *workerEndpoint) circuitOpenLocked(now time.Time) bool {
	return !e.circuitOpenUntil.IsZero() && now.Before(e.circuitOpenUntil)
}

func (e *workerEndpoint) setInFlightMetric(value int) {
	workerPoolInFlight.WithLabelValues(e.id, e.address).Set(float64(value))
}

func (e *workerEndpoint) setCircuitMetric(open bool) {
	value := 0.0
	if open {
		value = 1
	}
	workerPoolCircuitOpen.WithLabelValues(e.id, e.address).Set(value)
}

func closeEndpointConfigs(endpointConfigs []WorkerEndpointConfig) {
	for _, endpointConfig := range endpointConfigs {
		if endpointConfig.Closer != nil {
			_ = endpointConfig.Closer.Close()
		}
	}
}
