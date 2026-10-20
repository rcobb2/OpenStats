package monitor

import (
	"context"
	"testing"
	"time"
)

func TestBackoffDoublesAndCaps(t *testing.T) {
	b := newBackoff(time.Second, 30*time.Second)
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30}
	for i, w := range want {
		if got := b.Next(); got != w*time.Second {
			t.Fatalf("step %d: got %v want %v", i, got, w*time.Second)
		}
	}
	b.Reset()
	if got := b.Next(); got != time.Second {
		t.Fatalf("after reset got %v", got)
	}
}

func TestSleepCtxCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepCtx(ctx, time.Hour) {
		t.Fatal("expected false when ctx cancelled")
	}
	if !sleepCtx(context.Background(), time.Millisecond) {
		t.Fatal("expected true when wait elapsed")
	}
}
