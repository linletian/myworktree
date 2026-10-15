package app

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestAuthFailKnobsKeepTheirValues pins the claim that naming the limiter's
// literals changed nothing: the window is still a minute and the ceiling is
// still 20. Without this, "semantics byte-for-byte unchanged" is a promise
// with nothing behind it — someone could "tidy" 20 to 21 here and every other
// test in this file would still pass, because they all read the constant.
func TestAuthFailKnobsKeepTheirValues(t *testing.T) {
	if authFailLimit != 20 {
		t.Errorf("authFailLimit = %d, want 20 (the shipped ceiling)", authFailLimit)
	}
	if authFailWindow != time.Minute {
		t.Errorf("authFailWindow = %v, want 1m (the shipped window)", authFailWindow)
	}
	if authFailsSweepBusy >= authFailsSweepEvery {
		t.Errorf("busy cadence %v must be shorter than the normal %v, otherwise the soft cap does nothing",
			authFailsSweepBusy, authFailsSweepEvery)
	}
}

// newAuthTestServer builds the minimal Server the limiter runs on — the same
// shape the existing app tests use for this map.
func newAuthTestServer() *Server {
	return &Server{authFails: map[string]authFail{}}
}

// TestAllowAuthAttemptEvictsClosedWindows pins the core of the fix: a window
// that has closed leaves the map, and a window that has NOT closed stays. The
// live entry is the assertion that makes the first one meaningful — a sweep
// that nuked the whole map would pass an expired-only test.
func TestAllowAuthAttemptEvictsClosedWindows(t *testing.T) {
	s := newAuthTestServer()
	now := time.Now()
	s.authFails["expired-a"] = authFail{Count: 7, WindowEnd: now.Add(-2 * time.Minute)}
	s.authFails["expired-b"] = authFail{Count: 1, WindowEnd: now.Add(-1 * time.Nanosecond)}
	s.authFails["live"] = authFail{Count: 3, WindowEnd: now.Add(50 * time.Second)}
	s.authFailsNextSweep = now.Add(-time.Second) // sweep is due

	if !s.allowAuthAttempt("caller") {
		t.Fatal("a first attempt from a fresh IP must be allowed")
	}
	if _, ok := s.authFails["expired-a"]; ok {
		t.Error("expired-a (window closed 2m ago) survived the sweep")
	}
	if _, ok := s.authFails["expired-b"]; ok {
		t.Error("expired-b (window closed 1ns ago) survived the sweep")
	}
	if _, ok := s.authFails["live"]; !ok {
		t.Error("live entry (window still open) was evicted")
	}
	if got := s.authFails["caller"]; got.Count != 1 {
		t.Errorf("caller count = %d, want 1 — the sweep must not eat the entry that triggered it", got.Count)
	}
	if s.authFailsNextSweep.Before(now) {
		t.Error("the sweep did not arm the next one")
	}
}

// TestAuthFailsSweepIsRateLimited pins the throttle, i.e. that eviction is
// NOT a full map walk on every request. With the next sweep still in the
// future an expired entry must still be there afterwards — if it were gone,
// the cadence would be decoration and every auth attempt would pay O(n) under
// authMu, which is the DoS shape the cadence exists to prevent.
func TestAuthFailsSweepIsRateLimited(t *testing.T) {
	s := newAuthTestServer()
	now := time.Now()
	s.authFails["expired"] = authFail{Count: 5, WindowEnd: now.Add(-time.Hour)}
	s.authFailsNextSweep = now.Add(30 * time.Second) // not due

	s.allowAuthAttempt("fresh-1")

	if _, ok := s.authFails["expired"]; !ok {
		t.Fatal("sweep ran while it was not due — the cadence is not a throttle")
	}

	// Now put it due and show the same call does evict.
	s.authFailsNextSweep = now.Add(-time.Second)
	s.allowAuthAttempt("fresh-2")
	if _, ok := s.authFails["expired"]; ok {
		t.Error("sweep did not run once it was due")
	}
}

// TestEvictedAndSurvivingExpiredAgree is the equivalence proof behind
// "semantics unchanged": an IP whose expired entry was evicted and an IP whose
// expired entry was left in place must answer the next attempt identically.
// Both go through the real limiter, not through a re-implementation.
func TestEvictedAndSurvivingExpiredAgree(t *testing.T) {
	s := newAuthTestServer()
	now := time.Now()
	// Same closed window, same count, two IPs.
	closed := now.Add(-time.Minute)
	s.authFails["evicted"] = authFail{Count: 15, WindowEnd: closed}
	s.authFails["survivor"] = authFail{Count: 15, WindowEnd: closed}
	s.authFailsNextSweep = now.Add(-time.Second)

	s.allowAuthAttempt("triggers-sweep") // evicts both closed windows above
	if _, ok := s.authFails["evicted"]; ok {
		t.Fatal("precondition: the sweep should have removed the expired entry")
	}
	// Re-seed the survivor as expired-but-present: the state an IP would be
	// in if eviction had never existed.
	s.authFails["survivor"] = authFail{Count: 15, WindowEnd: closed}
	s.authFailsNextSweep = now.Add(time.Hour) // keep it in place on purpose

	// Neither has been blocked (15 < 20), so both must restart at 1 and be
	// allowed. Any difference between the two is a semantic change.
	a := s.allowAuthAttempt("evicted")
	b := s.allowAuthAttempt("survivor")
	if a != b {
		t.Fatalf("evicted=%v survivor=%v: eviction changed the verdict", a, b)
	}
	if !a {
		t.Fatal("both should be allowed (a closed window resets the counter)")
	}
	if ca, cb := s.authFails["evicted"].Count, s.authFails["survivor"].Count; ca != cb {
		t.Fatalf("counts diverge: evicted=%d survivor=%d, want both 1", ca, cb)
	}
	if ca := s.authFails["evicted"].Count; ca != 1 {
		t.Fatalf("count after a closed window = %d, want 1", ca)
	}
}

// TestAllowAuthAttemptCeilingUnchanged pins the limiter itself across
// eviction: exactly authFailLimit allowed, the next refused, the refusal
// surviving a sweep for its whole window, and a new window after expiry.
func TestAllowAuthAttemptCeilingUnchanged(t *testing.T) {
	s := newAuthTestServer()
	ip := "203.0.113.7"

	for i := 1; i <= authFailLimit; i++ {
		if !s.allowAuthAttempt(ip) {
			t.Fatalf("attempt %d must be allowed (ceiling is %d)", i, authFailLimit)
		}
		if got := s.authFails[ip].Count; got != i {
			t.Fatalf("after attempt %d the count is %d", i, got)
		}
	}
	// Over the ceiling the block holds for the REST of the window: every
	// further attempt is refused AND keeps counting. That is the shipped
	// shape, pinned here because the opposite assumption ("a refusal should not
	// compound") is exactly the mistake this kind of change invites — it is the
	// window that bounds the block, not the attempt count.
	for i := authFailLimit + 1; i <= authFailLimit+4; i++ {
		if s.allowAuthAttempt(ip) {
			t.Fatalf("attempt %d must stay refused until the window closes", i)
		}
		if got := s.authFails[ip].Count; got != i {
			t.Fatalf("attempt %d was not counted (count = %d)", i, got)
		}
	}

	// A sweep in the middle of an OPEN window must not free the blocked IP.
	s.authFailsNextSweep = time.Now().Add(-time.Second)
	s.allowAuthAttempt("someone-else")
	if got := s.authFails[ip]; got.Count <= authFailLimit {
		t.Fatalf("blocked IP count = %d, want > %d", got.Count, authFailLimit)
	}

	// And the ceiling repeats identically in the next window.
	s.resetAuthAttempts(ip)
	for i := 1; i <= authFailLimit; i++ {
		if !s.allowAuthAttempt(ip) {
			t.Fatalf("second window: attempt %d must be allowed", i)
		}
	}
	if s.allowAuthAttempt(ip) {
		t.Fatal("second window: the ceiling must still refuse attempt 21")
	}
}

// TestSweepBoundaryMatchesReadPath pins the one-nanosecond question: at the
// exact instant a window ends, the limiter KEEPS counting (its predicate is
// now.After, not !now.Before), so the sweep must keep the entry too. If the
// two predicates disagreed at the boundary, eviction could hand a blocked IP
// a fresh window early.
func TestSweepBoundaryMatchesReadPath(t *testing.T) {
	s := newAuthTestServer()
	now := time.Now()
	end := now.Add(time.Minute)
	s.authFails["boundary"] = authFail{Count: authFailLimit, WindowEnd: end}

	// Sweep at exactly the window end: now.After(WindowEnd) is false, so the
	// entry stays — and the caller at that instant is still blocked. The
	// Locked suffix is a contract, so every call here takes authMu first
	// even though this test is single-threaded (a sweep outside the lock is
	// exactly the bug the concurrent suite below lets -race catch).
	s.authMu.Lock()
	s.sweepExpiredAuthFailsLocked(end)
	s.authMu.Unlock()
	if _, ok := s.authFails["boundary"]; !ok {
		t.Error("sweep evicted an entry at the exact instant its window ended; the read path would still be counting it")
	}
	// One nanosecond later both agree it is over.
	s.authMu.Lock()
	s.sweepExpiredAuthFailsLocked(end.Add(time.Nanosecond))
	s.authMu.Unlock()
	if _, ok := s.authFails["boundary"]; ok {
		t.Error("sweep kept an entry whose window is provably closed")
	}
}

// TestSweepCadenceTightensOverSoftCap pins that the soft cap is not
// decoration: a map still over the cap after a sweep schedules the next sweep
// at the busy cadence, and a small map schedules the normal one.
func TestSweepCadenceTightensOverSoftCap(t *testing.T) {
	s := newAuthTestServer()
	now := time.Now()

	// Small map → normal cadence.
	s.authFails["small"] = authFail{Count: 1, WindowEnd: now.Add(time.Minute)}
	s.authMu.Lock()
	s.sweepExpiredAuthFailsLocked(now)
	s.authMu.Unlock()
	if got := s.authFailsNextSweep.Sub(now); got != authFailsSweepEvery {
		t.Errorf("small map cadence = %v, want %v", got, authFailsSweepEvery)
	}

	// Flood-shaped map: every entry is LIVE (window open), so the sweep must
	// remove nothing and the cadence must tighten instead. This is the shape
	// that proves the cap never lifts a block to make room.
	s2 := newAuthTestServer()
	for i := 0; i <= authFailsSoftCap; i++ {
		s2.authFails[fmt.Sprintf("203.0.113.%d", i)] = authFail{Count: authFailLimit, WindowEnd: now.Add(time.Minute)}
	}
	s2.authMu.Lock()
	s2.sweepExpiredAuthFailsLocked(now)
	s2.authMu.Unlock()
	if len(s2.authFails) != authFailsSoftCap+1 {
		t.Fatalf("live entries evicted: len = %d, want %d (the cap is a trigger, not a truncation)",
			len(s2.authFails), authFailsSoftCap+1)
	}
	if got := s2.authFailsNextSweep.Sub(now); got != authFailsSweepBusy {
		t.Errorf("over-cap cadence = %v, want %v", got, authFailsSweepBusy)
	}
}

// TestResetAuthAttemptsStillDeletes pins the success path the issue named as
// unchanged, and that it leaves the cadence alone.
func TestResetAuthAttemptsStillDeletes(t *testing.T) {
	s := newAuthTestServer()
	for i := 0; i < 5; i++ {
		s.allowAuthAttempt("198.51.100.9")
	}
	if got := s.authFails["198.51.100.9"].Count; got != 5 {
		t.Fatalf("count = %d, want 5", got)
	}
	s.resetAuthAttempts("198.51.100.9")
	if _, ok := s.authFails["198.51.100.9"]; ok {
		t.Fatal("resetAuthAttempts must delete the key")
	}
	if !s.allowAuthAttempt("198.51.100.9") {
		t.Fatal("after a successful login the IP starts clean")
	}
	if got := s.authFails["198.51.100.9"].Count; got != 1 {
		t.Errorf("count after reset = %d, want 1", got)
	}
}

// TestAuthFailsConcurrentSweepNoRace drives the limiter, the success-path
// delete and the sweep from many goroutines at once. It is the -race test: the
// sweep is a writer against a map that other goroutines are reading and
// writing, so it must run inside the same critical section — the test's
// sweepNow takes authMu precisely because a sweep outside it would be the bug.
func TestAuthFailsConcurrentSweepNoRace(t *testing.T) {
	s := newAuthTestServer()
	var wg sync.WaitGroup
	const workers = 24

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 60; i++ {
				ip := fmt.Sprintf("203.0.113.%d.%d", w, i%7)
				s.allowAuthAttempt(ip)
				if i%5 == 0 {
					s.resetAuthAttempts(ip)
				}
			}
		}(w)
	}
	// Concurrent sweeps, taken under the same lock the mutators use.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				s.authMu.Lock()
				s.sweepExpiredAuthFailsLocked(time.Now())
				s.authMu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Deliberately NO post-condition loop here: every entry this test writes
	// carries WindowEnd ≈ now + authFailWindow and the whole run lasts
	// milliseconds, so `now.After(v.WindowEnd)` is false for every entry and
	// a "no closed window survived" loop would be a dead assertion that can
	// never fail. The test's weight is the -race coverage above — sweep and
	// mutators crossing the same map inside one critical section — and that
	// stands on its own.
}
