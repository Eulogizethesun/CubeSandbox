// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0

// Package perftrace collects opt-in, request-local performance observations.
// Wall time is only an anchor; all offsets and durations use Go's monotonic clock.
//
// Ported from the cubesandbox-startup-profiler observation patch, with two
// additions used by the pause/resume paths: Mark (a cumulative checkpoint
// relative to the request origin) and FinishJSON (so callers that do not
// already import a JSON library can emit the "CUBE_PERF_TRACE " log line).
package perftrace

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"
)

var enabled = os.Getenv("CUBE_PERF_TRACE") == "1"

func Enabled() bool { return enabled }

type Span struct {
	SpanID        uint64  `json:"span_id"`
	ParentSpanID  uint64  `json:"parent_span_id"`
	Stage         string  `json:"stage"`
	Kind          string  `json:"kind"`
	StartOffsetUS float64 `json:"start_offset_us"`
	DurationUS    float64 `json:"duration_us"`
	TimingSource  string  `json:"timing_source"`
	Success       bool    `json:"success"`
}

type Batch struct {
	SchemaVersion int    `json:"schema_version"`
	Component     string `json:"component"`
	RequestID     string `json:"request_id"`
	SandboxID     string `json:"sandbox_id"`
	OriginUnixNS  int64  `json:"origin_unix_ns"`
	Spans         []Span `json:"spans"`
}

type Collector struct {
	mu        sync.Mutex
	origin    time.Time
	requestID string
	next      uint64
	closed    bool
	spans     []Span
}
type scope struct {
	collector *Collector
	parent    uint64
}
type contextKey struct{}

func StartRequest(ctx context.Context, origin time.Time, requestID string) (context.Context, *Collector) {
	if !enabled {
		return ctx, nil
	}
	c := &Collector{origin: origin, requestID: requestID, next: 1}
	return context.WithValue(ctx, contextKey{}, scope{c, 1}), c
}

type ActiveSpan struct {
	c           *Collector
	id, parent  uint64
	stage, kind string
	start       time.Time
}

func Begin(ctx context.Context, stage, kind string) (context.Context, *ActiveSpan) {
	if !enabled {
		return ctx, nil
	}
	s, ok := ctx.Value(contextKey{}).(scope)
	if !ok {
		return ctx, nil
	}
	start := time.Now()
	s.collector.mu.Lock()
	s.collector.next++
	id := s.collector.next
	s.collector.mu.Unlock()
	a := &ActiveSpan{s.collector, id, s.parent, stage, kind, start}
	return context.WithValue(ctx, contextKey{}, scope{s.collector, id}), a
}

func (a *ActiveSpan) End(err error) {
	if a == nil {
		return
	}
	a.c.append(a.id, a.parent, a.stage, a.kind, a.start, time.Now(), "monotonic_interval", err)
}

// Mark records a cumulative checkpoint from the request origin to now. Adjacent
// marks within one request delimit intervals; never add cumulative values.
func Mark(ctx context.Context, stage string) {
	if !enabled {
		return
	}
	s, ok := ctx.Value(contextKey{}).(scope)
	if !ok {
		return
	}
	now := time.Now()
	s.collector.mu.Lock()
	s.collector.next++
	id := s.collector.next
	s.collector.mu.Unlock()
	s.collector.append(id, s.parent, stage, "cumulative", s.collector.origin, now, "monotonic_interval", nil)
}

// RecordDuration preserves existing duration-only observations. Their start is
// inferred from the recording time and must not be treated as an exact boundary.
func RecordDuration(ctx context.Context, stage string, cost time.Duration, err error) {
	if !enabled {
		return
	}
	end := time.Now()
	record(ctx, stage, "metric", end.Add(-cost), end, "duration_backfill", err)
}

func RecordInterval(ctx context.Context, stage, kind string, start, end time.Time, err error) {
	if !enabled {
		return
	}
	record(ctx, stage, kind, start, end, "monotonic_interval", err)
}

func record(ctx context.Context, stage, kind string, start, end time.Time, source string, err error) {
	s, ok := ctx.Value(contextKey{}).(scope)
	if !ok {
		return
	}
	s.collector.mu.Lock()
	s.collector.next++
	id := s.collector.next
	s.collector.mu.Unlock()
	s.collector.append(id, s.parent, stage, kind, start, end, source, err)
}

func (c *Collector) append(id, parent uint64, stage, kind string, start, end time.Time, source string, err error) {
	span := Span{id, parent, stage, kind, float64(start.Sub(c.origin)) / float64(time.Microsecond), float64(end.Sub(start)) / float64(time.Microsecond), source, err == nil}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.spans = append(c.spans, span)
	}
}

// Finish freezes a snapshot before asynchronous serialization/logging. No log
// emission timestamp is used as a stage endpoint. Later rollback work is excluded.
func (c *Collector) Finish(sandboxID string, end time.Time, err error) *Batch {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	spans := append([]Span(nil), c.spans...)
	spans = append(spans, Span{1, 0, "cubelet.request", "request", 0, float64(end.Sub(c.origin)) / float64(time.Microsecond), "monotonic_interval", err == nil})
	return &Batch{1, "cubelet", c.requestID, sandboxID, c.origin.UnixNano(), spans}
}

// FinishJSON freezes the batch and returns it as JSON for a "CUBE_PERF_TRACE "
// log line. Empty when disabled, already finished, or on marshal failure.
func (c *Collector) FinishJSON(sandboxID string, end time.Time, err error) string {
	batch := c.Finish(sandboxID, end, err)
	if batch == nil {
		return ""
	}
	payload, marshalErr := json.Marshal(batch)
	if marshalErr != nil {
		return ""
	}
	return string(payload)
}
