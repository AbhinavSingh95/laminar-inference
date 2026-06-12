package main

import (
	"math"
	"strings"
	"sync"
)

type AdmissionController struct {
	mu sync.Mutex

	enabled                bool
	maxInFlightTokens      int
	estimatedTokensPerByte float64
	estimatedOutputTokens  int

	inFlightTokens   int
	acceptedTokens   uint64
	rejectedTokens   uint64
	acceptedRequests uint64
	rejectedRequests uint64
}

type AdmissionSnapshot struct {
	Enabled                bool    `json:"enabled"`
	MaxInFlightTokens      int     `json:"max_in_flight_tokens"`
	EstimatedTokensPerByte float64 `json:"estimated_tokens_per_byte"`
	EstimatedOutputTokens  int     `json:"estimated_output_tokens"`
	InFlightTokens         int     `json:"in_flight_tokens"`
	AcceptedTokens         uint64  `json:"accepted_tokens"`
	RejectedTokens         uint64  `json:"rejected_tokens"`
	AcceptedRequests       uint64  `json:"accepted_requests"`
	RejectedRequests       uint64  `json:"rejected_requests"`
}

func NewAdmissionController(cfg Config) *AdmissionController {
	cfg = cfg.normalized()
	return &AdmissionController{
		enabled:                cfg.AdmissionEnabled,
		maxInFlightTokens:      cfg.AdmissionMaxInFlightTokens,
		estimatedTokensPerByte: cfg.AdmissionEstimatedTokensPerByte,
		estimatedOutputTokens:  cfg.AdmissionEstimatedOutputTokens,
	}
}

func (a *AdmissionController) Estimate(prompt string) int {
	if a == nil {
		return 0
	}
	prompt = strings.TrimSpace(prompt)
	promptTokens := int(math.Ceil(float64(len(prompt)) * a.estimatedTokensPerByte))
	if promptTokens < 1 {
		promptTokens = 1
	}
	return promptTokens + a.estimatedOutputTokens
}

func (a *AdmissionController) TryAcquire(prompt string) (AdmissionLease, bool) {
	if a == nil || !a.enabled {
		return AdmissionLease{}, true
	}
	estimatedTokens := a.Estimate(prompt)

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.maxInFlightTokens > 0 &&
		a.inFlightTokens+estimatedTokens > a.maxInFlightTokens {
		a.rejectedRequests++
		a.rejectedTokens += uint64(estimatedTokens)
		admissionRejectedRequests.Inc()
		admissionRejectedTokens.Add(float64(estimatedTokens))
		return AdmissionLease{}, false
	}

	a.inFlightTokens += estimatedTokens
	a.acceptedRequests++
	a.acceptedTokens += uint64(estimatedTokens)
	admissionAcceptedTokens.Add(float64(estimatedTokens))
	admissionInFlightTokens.Set(float64(a.inFlightTokens))
	return AdmissionLease{
		controller: a,
		tokens:     estimatedTokens,
	}, true
}

func (a *AdmissionController) release(tokens int) {
	if a == nil || tokens <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.inFlightTokens -= tokens
	if a.inFlightTokens < 0 {
		a.inFlightTokens = 0
	}
	admissionInFlightTokens.Set(float64(a.inFlightTokens))
}

func (a *AdmissionController) Snapshot() AdmissionSnapshot {
	if a == nil {
		return AdmissionSnapshot{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return AdmissionSnapshot{
		Enabled:                a.enabled,
		MaxInFlightTokens:      a.maxInFlightTokens,
		EstimatedTokensPerByte: a.estimatedTokensPerByte,
		EstimatedOutputTokens:  a.estimatedOutputTokens,
		InFlightTokens:         a.inFlightTokens,
		AcceptedTokens:         a.acceptedTokens,
		RejectedTokens:         a.rejectedTokens,
		AcceptedRequests:       a.acceptedRequests,
		RejectedRequests:       a.rejectedRequests,
	}
}

type AdmissionLease struct {
	controller *AdmissionController
	tokens     int

	once sync.Once
}

func (l *AdmissionLease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		if l.controller != nil {
			l.controller.release(l.tokens)
		}
	})
}
