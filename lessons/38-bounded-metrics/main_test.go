package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

func observe(t *testing.T, m *operationMetrics, operation, result string, elapsed time.Duration) {
	t.Helper()
	if err := m.Observe(operation, result, elapsed); err != nil {
		t.Fatal(err)
	}
}

func exposition(t *testing.T, m *operationMetrics) string {
	t.Helper()
	var output bytes.Buffer
	if err := m.WritePrometheus(&output); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func requireSample(t *testing.T, text, suffix, labels, value string) {
	t.Helper()
	want := "go_course_operation_duration_seconds" + suffix + "{" + labels + "} " + value + "\n"
	if !strings.Contains(text, want) {
		t.Fatalf("missing sample %q", want)
	}
}

func TestZeroValueExportsFixedSeriesAndNoDuplicates(t *testing.T) {
	var m operationMetrics
	output := exposition(t, &m)
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(lines) != 74 || lines[1] != "# TYPE go_course_operation_duration_seconds histogram" {
		t.Fatalf("unexpected exposition: %s", output)
	}
	seen := make(map[string]bool)
	for _, line := range lines[2:] {
		sample, value, ok := strings.Cut(line, " ")
		if !ok || seen[sample] || (value != "0" && value != "0.000000000") {
			t.Fatalf("bad zero sample %q", line)
		}
		seen[sample] = true
	}
	for _, operation := range []string{"history.read", "message.write", "other"} {
		for _, result := range []string{"ok", "rejected", "failed", "other"} {
			labels := fmt.Sprintf("operation=%q,result=%q", operation, result)
			for _, upper := range []string{"0.01", "0.1", "1", "+Inf"} {
				requireSample(t, output, "_bucket", labels+fmt.Sprintf(",le=%q", upper), "0")
			}
			requireSample(t, output, "_count", labels, "0")
			requireSample(t, output, "_sum", labels, "0.000000000")
		}
	}
	if output != exposition(t, &m) {
		t.Fatal("unchanged state exported in a different order")
	}
}

func TestInclusiveBucketBoundaries(t *testing.T) {
	for _, tc := range []struct {
		elapsed    time.Duration
		cumulative [4]uint64
	}{
		{0, [4]uint64{1, 1, 1, 1}}, {1, [4]uint64{1, 1, 1, 1}},
		{10*time.Millisecond - 1, [4]uint64{1, 1, 1, 1}},
		{10 * time.Millisecond, [4]uint64{1, 1, 1, 1}},
		{10*time.Millisecond + 1, [4]uint64{0, 1, 1, 1}},
		{100*time.Millisecond - 1, [4]uint64{0, 1, 1, 1}},
		{100 * time.Millisecond, [4]uint64{0, 1, 1, 1}},
		{100*time.Millisecond + 1, [4]uint64{0, 0, 1, 1}},
		{time.Second - 1, [4]uint64{0, 0, 1, 1}},
		{time.Second, [4]uint64{0, 0, 1, 1}},
		{time.Second + 1, [4]uint64{0, 0, 0, 1}},
		{time.Duration(math.MaxInt64), [4]uint64{0, 0, 0, 1}},
	} {
		t.Run(tc.elapsed.String(), func(t *testing.T) {
			var m operationMetrics
			observe(t, &m, "history.read", "ok", tc.elapsed)
			output := exposition(t, &m)
			for i, upper := range []string{"0.01", "0.1", "1", "+Inf"} {
				requireSample(t, output, "_bucket", fmt.Sprintf("operation=\"history.read\",result=\"ok\",le=%q", upper), fmt.Sprint(tc.cumulative[i]))
			}
			requireSample(t, output, "_count", `operation="history.read",result="ok"`, "1")
			cell := m.snapshot()[0][0]
			if cell.totalNanos != uint64(tc.elapsed) {
				t.Fatalf("sum=%d want %d", cell.totalNanos, tc.elapsed)
			}
		})
	}
}

func TestCumulativeBucketsAndNanosecondSum(t *testing.T) {
	var m operationMetrics
	for _, elapsed := range []time.Duration{0, 10 * time.Millisecond, 10*time.Millisecond + 1, time.Second + 1} {
		observe(t, &m, "history.read", "ok", elapsed)
	}
	output := exposition(t, &m)
	for i, upper := range []string{"0.01", "0.1", "1", "+Inf"} {
		requireSample(t, output, "_bucket", fmt.Sprintf("operation=\"history.read\",result=\"ok\",le=%q", upper), []string{"2", "3", "3", "4"}[i])
	}
	requireSample(t, output, "_sum", `operation="history.read",result="ok"`, "1.020000002")
	requireSample(t, output, "_count", `operation="history.read",result="ok"`, "4")
}

func TestNegativeDurationLeavesWholeSnapshotUnchanged(t *testing.T) {
	var m operationMetrics
	observe(t, &m, "history.read", "ok", time.Millisecond)
	before := m.snapshot()
	for _, elapsed := range []time.Duration{-1, time.Duration(math.MinInt64)} {
		if err := m.Observe("untrusted", "secret", elapsed); !errors.Is(err, ErrInvalidDuration) {
			t.Fatalf("error=%v", err)
		}
		if m.snapshot() != before {
			t.Fatal("invalid observation changed state")
		}
	}
}

func TestUnknownInputsAreCollapsedWithoutRetainingPrivateLabels(t *testing.T) {
	var m operationMetrics
	inputs := []string{"", "HISTORY.READ", " history.read ", "tenant-private", "user-private", "token-private\n\"}", "/conversations/private?secret=yes"}
	for _, input := range inputs {
		observe(t, &m, input, input, 1)
	}
	for i := 0; i < 1000; i++ {
		observe(t, &m, fmt.Sprintf("tenant-%d", i), fmt.Sprintf("error-%d", i), 1)
	}
	output := exposition(t, &m)
	requireSample(t, output, "_count", `operation="other",result="other"`, "1007")
	if len(strings.Split(strings.TrimSuffix(output, "\n"), "\n")) != 74 {
		t.Fatal("cardinality grew")
	}
	for _, forbidden := range []string{"tenant-private", "user-private", "token-private", "conversations/", "error-", "secret=yes", "HISTORY.READ"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("private label retained: %q", forbidden)
		}
	}
}

func TestCategoriesAreIndependent(t *testing.T) {
	var m operationMetrics
	observe(t, &m, "history.read", "ok", 1)
	observe(t, &m, "history.read", "rejected", 2)
	observe(t, &m, "message.write", "failed", 3)
	observe(t, &m, "unknown", "ok", 4)
	observe(t, &m, "history.read", "unknown", 5)
	snapshot := m.snapshot()
	for _, tc := range []struct {
		op, result int
		nanos      uint64
	}{{0, 0, 1}, {0, 1, 2}, {1, 2, 3}, {2, 0, 4}, {0, 3, 5}} {
		if cell := snapshot[tc.op][tc.result]; cell.count != 1 || cell.totalNanos != tc.nanos {
			t.Fatalf("wrong cell %+v", cell)
		}
	}
	if snapshot[1][0].count != 0 || snapshot[2][3].count != 0 {
		t.Fatal("observation charged unrelated cell")
	}
}

func TestSnapshotIsAnOwnedCopy(t *testing.T) {
	var m operationMetrics
	observe(t, &m, "history.read", "ok", 1)
	saved := m.snapshot()
	saved[0][0].bins[0] = 100
	saved[0][0].count = 100
	saved[0][0].totalNanos = 100
	observe(t, &m, "history.read", "ok", 1)
	if got := m.snapshot()[0][0]; got.count != 2 || got.bins[0] != 2 || got.totalNanos != 2 {
		t.Fatalf("aliased snapshot: %+v", got)
	}
	if saved[0][0].count != 100 {
		t.Fatal("observation mutated saved snapshot")
	}
}

func TestAccumulatorOverflowIsAtomic(t *testing.T) {
	t.Run("count", func(t *testing.T) {
		var m operationMetrics
		m.cells[0][0] = durationCell{bins: [4]uint64{math.MaxUint64}, count: math.MaxUint64}
		before := m.snapshot()
		if err := m.Observe("history.read", "ok", 0); !errors.Is(err, ErrMetricOverflow) {
			t.Fatalf("error=%v", err)
		}
		if m.snapshot() != before {
			t.Fatal("count overflow partially recorded")
		}
	})
	t.Run("sum", func(t *testing.T) {
		var m operationMetrics
		observe(t, &m, "history.read", "ok", time.Duration(math.MaxInt64))
		observe(t, &m, "history.read", "ok", time.Duration(math.MaxInt64))
		observe(t, &m, "history.read", "ok", 1)
		before := m.snapshot()
		if err := m.Observe("history.read", "ok", 1); !errors.Is(err, ErrMetricOverflow) {
			t.Fatalf("error=%v", err)
		}
		if m.snapshot() != before {
			t.Fatal("sum overflow partially recorded")
		}
		observe(t, &m, "history.read", "ok", 0)
		requireSample(t, exposition(t, &m), "_sum", `operation="history.read",result="ok"`, "18446744073.709551615")
	})
}

type callbackWriter struct{ write func([]byte) (int, error) }

func (w callbackWriter) Write(p []byte) (int, error) { return w.write(p) }

func TestWriterRunsOutsideLockAndUsesOneEarlierSnapshot(t *testing.T) {
	var m operationMetrics
	var output bytes.Buffer
	err := m.WritePrometheus(callbackWriter{func(p []byte) (int, error) {
		observe(t, &m, "history.read", "ok", time.Second)
		return output.Write(p)
	}})
	if err != nil {
		t.Fatal(err)
	}
	requireSample(t, output.String(), "_count", `operation="history.read",result="ok"`, "0")
	requireSample(t, exposition(t, &m), "_count", `operation="history.read",result="ok"`, "1")
}

func TestFailedAndShortWritesLeaveMetricsUnchanged(t *testing.T) {
	sentinel := errors.New("writer failed")
	for _, tc := range []struct {
		name   string
		writer io.Writer
		want   error
	}{
		{"failed", callbackWriter{func(p []byte) (int, error) { return 5, sentinel }}, sentinel},
		{"short", callbackWriter{func(p []byte) (int, error) { return len(p) - 1, nil }}, io.ErrShortWrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m operationMetrics
			observe(t, &m, "message.write", "ok", time.Second)
			before := m.snapshot()
			if err := m.WritePrometheus(tc.writer); !errors.Is(err, tc.want) {
				t.Fatalf("error=%v", err)
			}
			if m.snapshot() != before {
				t.Fatal("export changed metrics")
			}
		})
	}
}

func TestConcurrentObserveAndExportRemainCoherent(t *testing.T) {
	var m operationMetrics
	const workers, perWorker = 16, 200
	start := make(chan struct{})
	var group sync.WaitGroup
	failures := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			for i := 0; i < perWorker; i++ {
				if err := m.Observe("history.read", "ok", 7); err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	close(start)
	defer group.Wait()
	for i := 0; i < 100; i++ {
		snapshot := m.snapshot()
		cell := snapshot[0][0]
		if cell.count != cell.bins[0] || cell.totalNanos != 7*cell.count || cell.bins[1] != 0 || cell.bins[2] != 0 || cell.bins[3] != 0 {
			t.Fatalf("torn snapshot %+v", cell)
		}
		text := exposition(t, &m)
		var count, bucket uint64
		for _, line := range strings.Split(text, "\n") {
			const countPrefix = `go_course_operation_duration_seconds_count{operation="history.read",result="ok"} `
			const infPrefix = `go_course_operation_duration_seconds_bucket{operation="history.read",result="ok",le="+Inf"} `
			if strings.HasPrefix(line, countPrefix) {
				if _, err := fmt.Sscan(strings.TrimPrefix(line, countPrefix), &count); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(line, infPrefix) {
				if _, err := fmt.Sscan(strings.TrimPrefix(line, infPrefix), &bucket); err != nil {
					t.Fatal(err)
				}
			}
		}
		if count != bucket {
			t.Fatalf("scrape count=%d != +Inf=%d", count, bucket)
		}
	}
	group.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if got := m.snapshot()[0][0]; got.count != workers*perWorker || got.totalNanos != workers*perWorker*7 {
		t.Fatalf("lost observations %+v", got)
	}
}

func TestDemoAndOutputFailure(t *testing.T) {
	var output bytes.Buffer
	if err := run(&output); err != nil {
		t.Fatal(err)
	}
	requireSample(t, output.String(), "_count", `operation="history.read",result="ok"`, "4")
	requireSample(t, output.String(), "_count", `operation="history.read",result="rejected"`, "1")
	requireSample(t, output.String(), "_sum", `operation="history.read",result="ok"`, "1.020000002")
	sentinel := errors.New("output failed")
	if err := run(callbackWriter{func([]byte) (int, error) { return 0, sentinel }}); !errors.Is(err, sentinel) {
		t.Fatalf("error=%v", err)
	}
}
