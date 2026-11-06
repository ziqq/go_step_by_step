// Package main demonstrates a fixed-cardinality operation duration histogram.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sync"
	"time"
)

var (
	ErrInvalidDuration = errors.New("duration must not be negative")
	ErrMetricOverflow  = errors.New("metric accumulator exhausted")
)

// Each cell retains disjoint bins; exposition turns them into cumulative buckets.
type durationCell struct {
	bins       [4]uint64
	count      uint64
	totalNanos uint64
}

type metricSnapshot [3][4]durationCell

// operationMetrics has a useful zero value and must not be copied after use.
// Only fixed operation/result categories are retained; never attach identities.
type operationMetrics struct {
	mu    sync.Mutex
	cells metricSnapshot
}

func operationIndex(operation string) int {
	switch operation {
	case "history.read":
		return 0
	case "message.write":
		return 1
	default:
		return 2
	}
}

func resultIndex(result string) int {
	switch result {
	case "ok":
		return 0
	case "rejected":
		return 1
	case "failed":
		return 2
	default:
		return 3
	}
}

// Observe records one caller-classified completed operation. Invalid duration or
// exhausted accumulators return an error without changing any observation.
func (m *operationMetrics) Observe(operation, result string, elapsed time.Duration) error {
	if elapsed < 0 {
		return ErrInvalidDuration
	}
	bin := 3
	for i, bound := range [...]time.Duration{10 * time.Millisecond, 100 * time.Millisecond, time.Second} {
		if elapsed <= bound {
			bin = i
			break
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cell := &m.cells[operationIndex(operation)][resultIndex(result)]
	nanos := uint64(elapsed)
	if cell.count == math.MaxUint64 || nanos > math.MaxUint64-cell.totalNanos {
		return ErrMetricOverflow
	}
	cell.bins[bin]++
	cell.count++
	cell.totalNanos += nanos
	return nil
}

// snapshot owns all its arrays and represents one point under the same mutex.
func (m *operationMetrics) snapshot() metricSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cells
}

// WritePrometheus exports all 72 samples in deterministic Prometheus text 0.0.4
// format. The caller supplies a non-nil writer. Writer I/O happens after unlock;
// a failed/short write may have delivered a prefix but never changes the metrics.
func (m *operationMetrics) WritePrometheus(w io.Writer) error {
	snapshot := m.snapshot()
	var body bytes.Buffer
	const name = "go_course_operation_duration_seconds"
	fmt.Fprintln(&body, "# HELP "+name+" Duration of completed operations by fixed category.")
	fmt.Fprintln(&body, "# TYPE "+name+" histogram")
	for op, operation := range [...]string{"history.read", "message.write", "other"} {
		for resultID, result := range [...]string{"ok", "rejected", "failed", "other"} {
			cell := snapshot[op][resultID]
			var cumulative uint64
			for bin, upper := range [...]string{"0.01", "0.1", "1", "+Inf"} {
				cumulative += cell.bins[bin]
				fmt.Fprintf(&body, "%s_bucket{operation=%q,result=%q,le=%q} %d\n", name, operation, result, upper, cumulative)
			}
			// Integer formatting keeps the decimal nanosecond sum exact here;
			// a monitoring system may store it with floating-point precision.
			fmt.Fprintf(&body, "%s_sum{operation=%q,result=%q} %d.%09d\n", name, operation, result, cell.totalNanos/uint64(time.Second), cell.totalNanos%uint64(time.Second))
			fmt.Fprintf(&body, "%s_count{operation=%q,result=%q} %d\n", name, operation, result, cell.count)
		}
	}
	written, err := w.Write(body.Bytes())
	if err != nil {
		return err
	}
	if written != body.Len() {
		return io.ErrShortWrite
	}
	return nil
}

func run(w io.Writer) error {
	var metrics operationMetrics
	for _, elapsed := range []time.Duration{0, 10 * time.Millisecond, 10*time.Millisecond + 1, time.Second + 1} {
		if err := metrics.Observe("history.read", "ok", elapsed); err != nil {
			return err
		}
	}
	if err := metrics.Observe("history.read", "rejected", 5*time.Millisecond); err != nil {
		return err
	}
	return metrics.WritePrometheus(w)
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
