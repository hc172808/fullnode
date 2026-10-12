package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnsureSyncCaughtUp(t *testing.T) {
	tests := []struct {
		name          string
		localHeight   uint64
		networkHeight uint64
		wantErr       bool
	}{
		{name: "behind peer", localHeight: 12, networkHeight: 13, wantErr: true},
		{name: "at peer height", localHeight: 13, networkHeight: 13},
		{name: "ahead of peer", localHeight: 14, networkHeight: 13},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ensureSyncCaughtUp(tt.localHeight, tt.networkHeight)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ensureSyncCaughtUp(%d, %d) error = %v, wantErr %t",
					tt.localHeight, tt.networkHeight, err, tt.wantErr)
			}
		})
	}
}

func TestBootstrapRetryLoopRetriesUntilPeerIsConnected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var attempts atomic.Int32
	var connected atomic.Bool
	done := make(chan struct{})

	go func() {
		defer close(done)
		bootstrapRetryLoop(
			ctx,
			"peer.example:30303",
			func(string) bool { return connected.Load() },
			func(string) error {
				if attempts.Add(1) == 1 {
					return errors.New("peer temporarily offline")
				}
				connected.Store(true)
				return nil
			},
			time.Millisecond,
			4*time.Millisecond,
		)
	}()

	deadline := time.After(time.Second)
	for !connected.Load() {
		select {
		case <-deadline:
			cancel()
			t.Fatalf("bootstrap retry did not recover after %d dial attempts", attempts.Load())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bootstrap retry loop did not stop after cancellation")
	}
	if got := attempts.Load(); got < 2 {
		t.Fatalf("dial attempts = %d, want at least 2", got)
	}
}
