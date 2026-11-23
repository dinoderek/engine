package control

import (
	"testing"
	"time"
)

func TestTheUpgradeOpMarksStartAndEndForTheOperatorOnly(t *testing.T) {
	s := service(t)
	t0 := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	now := t0
	s.Now = func() time.Time { return now }
	reject(t, s, Actor{Name: "planner", Role: "planner"}, Request{Op: "upgrade", State: UpgradeStart}, 403)
	reject(t, s, op, Request{Op: "upgrade", State: "begin"}, 400)

	call(t, s, op, Request{Op: "upgrade", State: UpgradeEnd})
	if st, _ := s.Store.snapshot(); !st.Upgrade.Start.IsZero() || !st.Upgrade.End.IsZero() {
		t.Fatalf("an end with no start recorded %+v", st.Upgrade)
	}
	call(t, s, op, Request{Op: "upgrade", State: UpgradeStart})
	now = t0.Add(10 * time.Minute)
	call(t, s, op, Request{Op: "upgrade", State: UpgradeEnd})
	now = t0.Add(20 * time.Minute)
	call(t, s, op, Request{Op: "upgrade", State: UpgradeEnd})
	st, _ := s.Store.snapshot()
	if !st.Upgrade.Start.Equal(t0) || !st.Upgrade.End.Equal(t0.Add(10*time.Minute)) {
		t.Fatalf("upgrade %+v, want the start and the first end", st.Upgrade)
	}

	now = t0.Add(time.Hour)
	call(t, s, op, Request{Op: "upgrade", State: UpgradeStart})
	if st, _ := s.Store.snapshot(); !st.Upgrade.Start.Equal(now) || !st.Upgrade.End.IsZero() {
		t.Fatalf("a new start left %+v, want it alone, running", st.Upgrade)
	}
}

func TestTheUpgradeRecordSurvivesAStoreReopen(t *testing.T) {
	s := service(t)
	call(t, s, op, Request{Op: "upgrade", State: UpgradeStart})
	call(t, s, op, Request{Op: "upgrade", State: UpgradeEnd})
	path := s.Store.path
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	st, _ := reopened.snapshot()
	if want := s.Now(); !st.Upgrade.Start.Equal(want) || !st.Upgrade.End.Equal(want) {
		t.Errorf("upgrade %+v after reopen, want start and end %v", st.Upgrade, want)
	}
}

func TestDuringUpgrade(t *testing.T) {
	start := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	end := start.Add(10 * time.Minute)
	ran := UpgradeRecord{Start: start, End: end}
	running := UpgradeRecord{Start: start}
	for _, c := range []struct {
		name     string
		upgrade  UpgradeRecord
		started  time.Time
		live     time.Time
		readAt   time.Time
		upgraded bool
	}{
		{"no upgrade ever ran", UpgradeRecord{}, start, start, end, false},
		{"seen live during it, gone during it", ran, start.Add(-time.Hour), start.Add(time.Minute), start.Add(2 * time.Minute), true},
		{"seen live just before it, read gone after its end", ran, start.Add(-time.Hour), start.Add(-time.Minute), end.Add(time.Minute), true},
		{"seen live during it, read gone the lead after its end", ran, start.Add(-time.Hour), start.Add(time.Minute), end.Add(UpgradeLead), true},
		{"seen live during it, read gone long after its end", ran, start.Add(-time.Hour), start.Add(time.Minute), end.Add(3 * time.Hour), false},
		{"seen live the lead before it", ran, start.Add(-time.Hour), start.Add(-UpgradeLead), end.Add(time.Minute), true},
		{"seen live long before it, on a host offline since", ran, start.Add(-48 * time.Hour), start.Add(-24 * time.Hour), end.Add(time.Minute), false},
		{"never seen live, started before its end", ran, start.Add(time.Minute), time.Time{}, end.Add(time.Minute), true},
		{"gone before it started", ran, start.Add(-time.Hour), start.Add(-2 * time.Minute), start.Add(-time.Minute), false},
		{"seen live after its end", ran, start.Add(-time.Hour), end.Add(time.Second), end.Add(time.Minute), false},
		{"started after its end", ran, end.Add(time.Minute), time.Time{}, end.Add(2 * time.Minute), false},
		{"seen live while one runs", running, start.Add(-time.Hour), start.Add(30 * time.Minute), start.Add(31 * time.Minute), true},
		{"seen live after an unended one's bound", running, start.Add(-time.Hour), start.Add(UpgradeMax + time.Second), start.Add(2 * UpgradeMax), false},
	} {
		st := newRecords()
		st.Upgrade = c.upgrade
		at := Attempt{Started: c.started, LiveAt: c.live}
		tx := st.load(t)
		if got := duringUpgrade(tx, at, c.readAt); got != c.upgraded {
			t.Errorf("%s: during the upgrade %v, want %v", c.name, got, c.upgraded)
		}
	}
}

func TestADeathDuringAnUpgradeResumesUncountedWithItsTail(t *testing.T) {
	start := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	live := start.Add(time.Hour)
	upgrade := UpgradeRecord{Start: live.Add(-time.Minute), End: live.Add(5 * time.Minute)}
	crash := prior{reason: EndVanished, crashed: true}
	for _, c := range []struct {
		name          string
		at            Attempt
		before        []prior
		readAt        time.Time
		reason, state string
		upgraded      bool
		early         bool
	}{
		{name: "a claimed death during the upgrade, after two crashes, resumes", at: Attempt{Started: start, LiveAt: live},
			before: []prior{crash, crash}, readAt: live.Add(time.Minute), reason: EndVanished, state: "running", upgraded: true},
		{name: "an unclaimed death during the upgrade is no strike", at: Attempt{Started: live.Add(-time.Minute)},
			before: []prior{{reason: EndEarlyDeath, early: true}}, readAt: live.Add(time.Minute), reason: EndEarlyDeath, state: "todo", upgraded: true},
		{name: "a claimed death after the end follows the cap", at: Attempt{Started: start, LiveAt: live.Add(10 * time.Minute)},
			before: []prior{crash, crash}, readAt: live.Add(11 * time.Minute), reason: EndFailed, state: "failed"},
		{name: "an unclaimed death after the end is a strike", at: Attempt{Started: live.Add(10 * time.Minute)},
			before: []prior{{reason: EndEarlyDeath, early: true}}, readAt: live.Add(11 * time.Minute), reason: EndFailed, state: "failed", early: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := vanishState(Item{}, Item{}, c.before)
			st.Upgrade = upgrade
			c.at.ID, c.at.Item, c.at.State, c.at.Window, c.at.Tail = "a99", "t2", "running", "@1", "the tail"
			st.Attempts[c.at.ID] = c.at
			s := Service{Now: func() time.Time { return c.readAt }}
			tx := st.load(t)
			at := s.endDead(tx, Actor{Name: "coordinator", Role: "coordinator"}, c.at, "gone")
			if at.Reason != c.reason || at.Upgraded != c.upgraded || at.Early != c.early || tx.item("t2").State != c.state {
				t.Fatalf("ended %s upgraded %v early %v, task %s; want %s upgraded %v early %v, task %s",
					at.Reason, at.Upgraded, at.Early, tx.item("t2").State, c.reason, c.upgraded, c.early, c.state)
			}
			if c.upgraded && (at.Crashed || at.Tail != "the tail") {
				t.Errorf("an upgrade death crashed %v with tail %q; want no crash, its tail kept", at.Crashed, at.Tail)
			}
		})
	}
}

func TestAnUpgradeDeathNeitherCountsNorResetsTheCap(t *testing.T) {
	start := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	crash := prior{reason: EndVanished, crashed: true}
	for _, between := range []prior{{reason: EndVanished, upgraded: true}, {reason: EndEarlyDeath, upgraded: true}} {
		st := vanishState(Item{}, Item{}, []prior{crash, between, crash})
		at := Attempt{ID: "a99", Item: "t2", State: "running", Window: "@1", Started: start, LiveAt: start.Add(time.Hour)}
		st.Attempts[at.ID] = at
		s := Service{Now: func() time.Time { return start.Add(2 * time.Hour) }}
		tx := st.load(t)
		if got := s.endDead(tx, Actor{Name: "coordinator", Role: "coordinator"}, at, "gone"); got.Reason != EndFailed {
			t.Errorf("with %+v between two crashes the third ended %s, want failed", between, got.Reason)
		}
	}
}

func TestAPlannerDeadDuringAnUpgradeIsUncounted(t *testing.T) {
	start := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	for _, wrote := range []bool{true, false} {
		st := newRecords()
		st.Items["p1"] = Item{ID: "p1", Kind: "plan", State: "approved", Attempt: "a2", Coordinating: true}
		if wrote {
			st.Items["t1"] = Item{ID: "t1", Kind: "task", Plan: "p1", State: "todo"}
		}
		st.Attempts["a1"] = Attempt{ID: "a1", Item: "p1", State: "ended", Window: "@1", Reason: EndEarlyDeath, Early: true, Started: start}
		at := Attempt{ID: "a2", Item: "p1", State: "running", Window: "@2", Started: start, LiveAt: start.Add(time.Minute)}
		st.Attempts[at.ID] = at
		st.Upgrade = UpgradeRecord{Start: start}
		s := Service{Now: func() time.Time { return start.Add(2 * time.Minute) }}
		tx := st.load(t)
		got := s.endDead(tx, Actor{Name: "coordinator", Role: "coordinator"}, at, "gone")
		want := EndVanished
		if !wrote {
			want = EndEarlyDeath
		}
		if got.Reason != want || !got.Upgraded || got.Crashed || got.Early || !tx.item("p1").Coordinating {
			t.Errorf("wrote tasks %v: ended %+v, coordinating %v; want %s, upgraded, uncounted", wrote, got, tx.item("p1").Coordinating, want)
		}
	}
}
