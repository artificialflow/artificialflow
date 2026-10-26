package application

import "sync/atomic"

type EngineMetricsSnapshot struct {
	OutboxPublishSuccess int64
	OutboxPublishFailure int64
	OutboxRetries        int64
	OutboxDeadLetters    int64
	OutboxPending        int64
	OutboxPublishLagSec  int64
	IdempotencyHit       int64
	IdempotencyMiss      int64
}

type engineMetrics struct {
	outboxPublishSuccess atomic.Int64
	outboxPublishFailure atomic.Int64
	outboxRetries        atomic.Int64
	outboxDeadLetters    atomic.Int64
	outboxPending        atomic.Int64
	outboxPublishLagSec  atomic.Int64
	idempotencyHit       atomic.Int64
	idempotencyMiss      atomic.Int64
}

func newEngineMetrics() *engineMetrics {
	return &engineMetrics{}
}

func (m *engineMetrics) incOutboxPublishSuccess() {
	m.outboxPublishSuccess.Add(1)
}

func (m *engineMetrics) incOutboxPublishFailure() {
	m.outboxPublishFailure.Add(1)
}

func (m *engineMetrics) incOutboxRetry() {
	m.outboxRetries.Add(1)
}

func (m *engineMetrics) incOutboxDeadLetter() {
	m.outboxDeadLetters.Add(1)
}

func (m *engineMetrics) setOutboxPending(n int64) {
	if n < 0 {
		n = 0
	}
	m.outboxPending.Store(n)
}

func (m *engineMetrics) setOutboxPublishLagSec(n int64) {
	if n < 0 {
		n = 0
	}
	m.outboxPublishLagSec.Store(n)
}

func (m *engineMetrics) incIdempotencyHit() {
	m.idempotencyHit.Add(1)
}

func (m *engineMetrics) incIdempotencyMiss() {
	m.idempotencyMiss.Add(1)
}

func (m *engineMetrics) snapshot() EngineMetricsSnapshot {
	return EngineMetricsSnapshot{
		OutboxPublishSuccess: m.outboxPublishSuccess.Load(),
		OutboxPublishFailure: m.outboxPublishFailure.Load(),
		OutboxRetries:        m.outboxRetries.Load(),
		OutboxDeadLetters:    m.outboxDeadLetters.Load(),
		OutboxPending:        m.outboxPending.Load(),
		OutboxPublishLagSec:  m.outboxPublishLagSec.Load(),
		IdempotencyHit:       m.idempotencyHit.Load(),
		IdempotencyMiss:      m.idempotencyMiss.Load(),
	}
}
