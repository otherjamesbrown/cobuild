package cmd

import (
	"testing"
	"time"
)

func TestStatusHealthFor(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name         string
		status       string
		lastProgress time.Time
		want         string
	}{
		{"completed run not shown", "completed", now, "-"},
		{"cancelled run not shown", "cancelled", now, "-"},
		{"active fresh", "active", now.Add(-2 * time.Minute), "ACTIVE"},
		{"active right at stale boundary", "active", now.Add(-(statusStaleAfter + time.Second)), "STALE"},
		{"active between stale and dead", "active", now.Add(-30 * time.Minute), "STALE"},
		{"active past dead boundary", "active", now.Add(-(statusDeadAfter + time.Second)), "DEAD"},
		{"active with unknown last progress", "active", time.Time{}, "UNKNOWN"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := statusHealthFor(tc.status, tc.lastProgress)
			if got != tc.want {
				t.Fatalf("health = %q, want %q", got, tc.want)
			}
		})
	}
}
