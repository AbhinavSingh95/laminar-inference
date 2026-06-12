package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const (
	defaultOTLPBatchSize     = 32
	defaultOTLPFlushInterval = 5 * time.Second
	defaultOTLPQueueSize     = 4096
	defaultOTLPTimeout       = 2 * time.Second
)

type OTLPHTTPExporterConfig struct {
	Endpoint      string
	MaxBatchSize  int
	FlushInterval time.Duration
	QueueSize     int
	Timeout       time.Duration
	Client        *http.Client
}

type OTLPExporterSnapshot struct {
	Endpoint      string `json:"endpoint"`
	PendingSpans  int    `json:"pending_spans"`
	QueueDepth    int    `json:"queue_depth"`
	ExportedSpans int64  `json:"exported_spans"`
	DroppedSpans  int64  `json:"dropped_spans"`
	FailedExports int64  `json:"failed_exports"`
	LastError     string `json:"last_error,omitempty"`
}

type OTLPHTTPExporter struct {
	endpoint      string
	maxBatchSize  int
	flushInterval time.Duration
	timeout       time.Duration
	client        *http.Client

	queue   chan TraceSpan
	done    chan struct{}
	closed  chan struct{}
	closeMu sync.Once
	flushMu sync.Mutex

	mu            sync.Mutex
	pending       []TraceSpan
	exportedSpans int64
	droppedSpans  int64
	failedExports int64
	lastError     string
}

func NewOTLPHTTPExporter(config OTLPHTTPExporterConfig) (*OTLPHTTPExporter, error) {
	config = normalizeOTLPExporterConfig(config)
	if config.Endpoint == "" {
		return nil, errors.New("OTLP endpoint is required")
	}
	if err := validateOTLPEndpoint(config.Endpoint); err != nil {
		return nil, err
	}

	exporter := &OTLPHTTPExporter{
		endpoint:      config.Endpoint,
		maxBatchSize:  config.MaxBatchSize,
		flushInterval: config.FlushInterval,
		timeout:       config.Timeout,
		client:        config.Client,
		queue:         make(chan TraceSpan, config.QueueSize),
		done:          make(chan struct{}),
		closed:        make(chan struct{}),
		pending:       make([]TraceSpan, 0, config.MaxBatchSize),
	}
	go exporter.run()
	return exporter, nil
}

func normalizeOTLPExporterConfig(config OTLPHTTPExporterConfig) OTLPHTTPExporterConfig {
	if config.MaxBatchSize <= 0 {
		config.MaxBatchSize = defaultOTLPBatchSize
	}
	if config.FlushInterval <= 0 {
		config.FlushInterval = defaultOTLPFlushInterval
	}
	if config.QueueSize <= 0 {
		config.QueueSize = defaultOTLPQueueSize
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultOTLPTimeout
	}
	if config.Client == nil {
		config.Client = &http.Client{}
	}
	return config
}

func validateOTLPEndpoint(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid OTLP endpoint: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("invalid OTLP endpoint scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return errors.New("invalid OTLP endpoint: host is required")
	}
	return nil
}

func (e *OTLPHTTPExporter) ExportSpan(span TraceSpan) {
	if e == nil {
		return
	}
	span = cloneTraceSpan(span)
	select {
	case e.queue <- span:
	default:
		e.mu.Lock()
		e.droppedSpans++
		e.mu.Unlock()
	}
}

func (e *OTLPHTTPExporter) Flush(ctx context.Context) error {
	if e == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	e.flushMu.Lock()
	defer e.flushMu.Unlock()

	e.drainQueue()

	e.mu.Lock()
	if len(e.pending) == 0 {
		e.mu.Unlock()
		return nil
	}
	batch := append([]TraceSpan(nil), e.pending...)
	e.mu.Unlock()

	if err := e.post(ctx, batch); err != nil {
		e.mu.Lock()
		e.failedExports++
		e.lastError = err.Error()
		e.mu.Unlock()
		return err
	}

	e.mu.Lock()
	e.pending = e.pending[len(batch):]
	e.exportedSpans += int64(len(batch))
	e.lastError = ""
	e.mu.Unlock()
	return nil
}

func (e *OTLPHTTPExporter) Snapshot() OTLPExporterSnapshot {
	if e == nil {
		return OTLPExporterSnapshot{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return OTLPExporterSnapshot{
		Endpoint:      e.endpoint,
		PendingSpans:  len(e.pending),
		QueueDepth:    len(e.queue),
		ExportedSpans: e.exportedSpans,
		DroppedSpans:  e.droppedSpans,
		FailedExports: e.failedExports,
		LastError:     e.lastError,
	}
}

func (e *OTLPHTTPExporter) Close() {
	if e == nil {
		return
	}
	e.closeMu.Do(func() {
		close(e.done)
		<-e.closed
	})
}

func (e *OTLPHTTPExporter) run() {
	ticker := time.NewTicker(e.flushInterval)
	defer ticker.Stop()
	defer close(e.closed)

	for {
		select {
		case span := <-e.queue:
			pending := e.addPending(span)
			if pending >= e.maxBatchSize {
				_ = e.Flush(context.Background())
			}
		case <-ticker.C:
			_ = e.Flush(context.Background())
		case <-e.done:
			e.drainQueue()
			_ = e.Flush(context.Background())
			return
		}
	}
}

func (e *OTLPHTTPExporter) addPending(span TraceSpan) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pending = append(e.pending, cloneTraceSpan(span))
	return len(e.pending)
}

func (e *OTLPHTTPExporter) drainQueue() {
	for {
		select {
		case span := <-e.queue:
			e.addPending(span)
		default:
			return
		}
	}
}

func (e *OTLPHTTPExporter) post(ctx context.Context, spans []TraceSpan) error {
	payload := spansToOTLPExport(spans)
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal OTLP payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build OTLP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("post OTLP spans: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("collector returned HTTP %d", resp.StatusCode)
	}
	return nil
}
