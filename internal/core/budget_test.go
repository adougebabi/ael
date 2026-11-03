package core

import (
	"testing"
	"time"
)

func TestTimeBudgetCountsParallelWorkOnceAndStopsAtLeaseExpiry(t *testing.T) {
	end := time.Now().Add(-time.Minute)
	start := end.Add(-time.Minute)
	parallel := Task{Definition: Definition{MaxSeconds: 90}, Attempts: []Attempt{{Started: start, Ended: &end}, {Started: start, Ended: &end}}}
	if runtimeUsed(&parallel) != time.Minute || timeBudgetExhausted(&parallel) {
		t.Fatal("parallel checks charged twice")
	}
	expired := Task{Attempts: []Attempt{{Started: start, Lease: end}}}
	if runtimeUsed(&expired) != time.Minute {
		t.Fatal("offline time after lease expiry charged as execution")
	}
}
