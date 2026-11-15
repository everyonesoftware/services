package dockerapi

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestLogsPreservesTimestampedPartialRecordBoundaries(t *testing.T) {
	stamp := "2026-01-01T00:00:00.000000000Z "
	input := append(logFrame(1, stamp+strings.Repeat("x", 16<<10)), logFrame(1, stamp+"end\n")...)
	rows, err := decodeLogs(bytes.NewReader(input), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || len(rows[0].Message) != 16<<10 || rows[1].Message != "end" {
		t.Fatalf("partial records lost/corrupted: rows=%d", len(rows))
	}
	for _, row := range rows {
		if strings.Contains(row.Message, stamp) {
			t.Fatal("Engine timestamp leaked into application message")
		}
	}
}

func TestLogsPartialBoundaryTimestampMaySpanFrames(t *testing.T) {
	for _, stamp := range []string{"2026-01-01T00:00:00.000000000Z ", "2026-01-01T00:00:00Z ", "2026-01-01T00:00:00.123456789+01:00 "} {
		for split := 1; split < len(stamp); split++ {
			input := append(logFrame(1, stamp+"partial"), logFrame(1, stamp[:split])...)
			input = append(input, logFrame(1, stamp[split:]+"end\n")...)
			rows, err := decodeLogs(bytes.NewReader(input), false)
			if err != nil || len(rows) != 2 || rows[0].Message != "partial" || rows[1].Message != "end" {
				t.Fatalf("split partial boundary at %d of %q corrupted: rows=%+v error=%v", split, stamp, rows, err)
			}
		}
	}
}

func TestLogsRejectsAmbiguousTTYPartialBoundaries(t *testing.T) {
	stamp := "2026-01-01T00:00:00.000000000Z "
	rows, err := decodeLogs(strings.NewReader(stamp+"partial"+stamp+"end\n"), true)
	if rows != nil || err == nil || !strings.Contains(err.Error(), "TTY") {
		t.Fatalf("ambiguous raw records must be explicitly unavailable: rows=%+v error=%v", rows, err)
	}
	if errors.Is(err, ErrLogLimit) {
		t.Fatal("ambiguity is not a safety byte-limit error")
	}
}
