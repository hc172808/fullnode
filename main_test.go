package main

import "testing"

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
