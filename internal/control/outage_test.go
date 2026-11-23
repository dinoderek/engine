package control

import (
	"testing"
	"time"
)

func TestStartPassSavesTheStartAndMarksOnlyAGapOverTheThreshold(t *testing.T) {
	store := service(t).Store
	t0 := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		at     time.Time
		prev   time.Time
		outage bool
	}{
		{"the first pass has no saved time", t0, time.Time{}, false},
		{"a gap under the threshold", t0.Add(4 * time.Minute), t0, false},
		{"a gap of the threshold", t0.Add(9 * time.Minute), t0.Add(4 * time.Minute), false},
		{"a gap over the threshold", t0.Add(15*time.Minute + time.Second), t0.Add(9 * time.Minute), true},
	} {
		prev, outage, err := store.StartPass(tc.at, 5*time.Minute)
		if err != nil {
			t.Fatal(tc.name, err)
		}
		if !prev.Equal(tc.prev) || outage != tc.outage {
			t.Errorf("%s: previous %v, outage %v; want %v, %v", tc.name, prev, outage, tc.prev, tc.outage)
		}
	}
}

func TestAnOutageClearsTheStallTimersOfLiveAttemptsOnly(t *testing.T) {
	store := service(t).Store
	t0 := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	if err := store.modify(func(st *state) error {
		st.Attempts["a1"] = Attempt{ID: "a1", State: "running", WaitingSince: t0, IdleSince: t0}
		st.Attempts["a2"] = Attempt{ID: "a2", State: "ended", WaitingSince: t0, IdleSince: t0}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{t0, t0.Add(10 * time.Minute)} {
		if _, _, err := store.StartPass(at, 5*time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := store.snapshot()
	if a := st.Attempts["a1"]; !a.WaitingSince.IsZero() || !a.IdleSince.IsZero() {
		t.Errorf("live attempt %+v, want both timers cleared", a)
	}
	if a := st.Attempts["a2"]; !a.WaitingSince.Equal(t0) || !a.IdleSince.Equal(t0) {
		t.Errorf("ended attempt %+v, want its timers untouched", a)
	}
}

func TestThePassAtSurvivesAStoreReopen(t *testing.T) {
	store := service(t).Store
	path := store.path
	at := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	if _, _, err := store.StartPass(at, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	st, _ := reopened.snapshot()
	if !st.PassAt.Equal(at) {
		t.Errorf("saved pass start %v after reopen, want %v", st.PassAt, at)
	}
}

func TestEndPassMovesTheSavedTimeForwardOnly(t *testing.T) {
	store := service(t).Store
	t0 := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	if _, _, err := store.StartPass(t0, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{t0.Add(7 * time.Minute), t0.Add(time.Minute)} {
		if err := store.EndPass(at); err != nil {
			t.Fatal(err)
		}
	}
	if _, outage, err := store.StartPass(t0.Add(8*time.Minute), 5*time.Minute); err != nil || outage {
		t.Errorf("pass a minute after a long pass ended: outage %v, err %v; want none", outage, err)
	}
	durable(t, store)
}
