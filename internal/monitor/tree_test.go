package monitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/shirou/gopsutil/v4/mem"
)

func marshalForTest(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// fakeTree installs a synthetic forest over the listProcs/procInfoOf seams
// (review G5: the enumeration is the seam now — per-node Children() was
// replaced by a single /proc pass plus a ppid index). A forest maps pid →
// its direct children; procInfoOf answers from a per-pid record, and any
// pid missing from infos reads as "process gone" (ok=false) — the
// exited-between-enumeration-and-read race.
type fakeTree struct {
	kids  map[int][]int
	infos map[int]procInfo
	// enumErrOn simulates the WHOLE enumeration failing (procfs unreadable)
	// — every tree then reads unavailable (review G3).
	enumErrOn bool
	// absent: pids deliberately kept OUT of the enumeration — the shape of
	// "gone by snapshot time". A missing ROOT is the other unavailable
	// trigger of review G3.
	absent map[int]bool
	// listCalls counts listProcs invocations (review G5's enumeration
	// budget pin).
	listCalls *int
}

func installFakeTree(t *testing.T, ft fakeTree) {
	t.Helper()
	oldProcs, oldInfo := listProcs, procInfoOf
	t.Cleanup(func() { listProcs, procInfoOf = oldProcs, oldInfo })

	listProcs = func() ([]procEntry, error) {
		if ft.enumErrOn {
			return nil, errors.New("procfs read failed")
		}
		if ft.listCalls != nil {
			*ft.listCalls++
		}
		// One snapshot: every pid mentioned anywhere is alive unless
		// absent; parent edges come from kids, the rest from infos.ppid.
		ppid := map[int]int{}
		mention := func(pid, parent int) {
			if ft.absent[pid] {
				return
			}
			if _, ok := ppid[pid]; !ok {
				ppid[pid] = parent
			}
		}
		for p, ks := range ft.kids {
			for _, k := range ks {
				if !ft.absent[k] {
					ppid[k] = p // the kids relation wins
				}
			}
			if inf, ok := ft.infos[p]; ok {
				mention(p, inf.ppid)
			} else {
				mention(p, 0)
			}
		}
		for pid, inf := range ft.infos {
			mention(pid, inf.ppid)
		}
		out := make([]procEntry, 0, len(ppid))
		for pid, p := range ppid {
			out = append(out, procEntry{pid: pid, ppid: p})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].pid < out[j].pid })
		return out, nil
	}
	procInfoOf = func(pid int) (procInfo, bool) {
		info, ok := ft.infos[pid]
		return info, ok
	}
}

// walk1 builds the index and walks one tree — the shorthand every case
// below shares, mirroring what one Collect does per instance.
func walk1(pid int) TreeStat {
	return walkSubtree(pid, buildProcIndex())
}

func info(ppid int, name string, rss uint64) procInfo {
	return procInfo{ppid: ppid, name: name, rss: rss, createdMS: 1000 + int64(rss%997)}
}

func TestWalkSubtreeAggregatesRSSAndCount(t *testing.T) {
	//        100
	//       /   \
	//     200   300
	//      |
	//     400
	installFakeTree(t, fakeTree{
		kids: map[int][]int{100: {200, 300}, 200: {400}},
		infos: map[int]procInfo{
			100: info(1, "mwbeta", 1000),
			200: info(100, "node", 2000),
			300: info(100, "sleep", 300),
			400: info(200, "fake-serve.py", 4000),
		},
	})

	st := walk1(100)
	if st.ChildCount != 3 {
		t.Fatalf("ChildCount = %d, want 3 (main PID excluded)", st.ChildCount)
	}
	if st.Truncated {
		t.Errorf("Truncated = true on a 4-node tree")
	}
	// The walk collects DESCENDANTS; the caller adds the main RSS.
	wantDesc := uint64(2000 + 300 + 4000)
	if got := st.SubtreeRSSBytes; got != wantDesc {
		t.Errorf("walk SubtreeRSSBytes = %d, want %d (descendants only)", got, wantDesc)
	}
	if len(st.Children) != 3 {
		t.Fatalf("Children len = %d, want 3", len(st.Children))
	}
	// Top by RSS: 400(4000), 200(2000), 300(300).
	if st.Children[0].PID != 400 || st.Children[1].PID != 200 || st.Children[2].PID != 300 {
		t.Errorf("children not RSS-sorted: %+v", st.Children)
	}
	if st.Children[0].PPID != 200 {
		t.Errorf("400's ppid = %d, want 200", st.Children[0].PPID)
	}
	if st.Children[0].Name != "fake-serve.py" {
		t.Errorf("name = %q", st.Children[0].Name)
	}
}

func TestWalkSubtreeDepthCap(t *testing.T) {
	// Chain 1 → 2 → … → 12. maxTreeDepth = 8 counts depth as edges below
	// the root: the root is depth 0, so descendants at depth 1..8 are
	// collected (pids 2..9) and pids 10..12 are NOT — and Truncated must
	// say so (a floor, not a lie).
	kids := map[int][]int{}
	infos := map[int]procInfo{}
	for i := 1; i <= 12; i++ {
		infos[i] = info(i-1, fmt.Sprintf("p%d", i), uint64(i))
		if i < 12 {
			kids[i] = []int{i + 1}
		}
	}
	installFakeTree(t, fakeTree{kids: kids, infos: infos})

	st := walk1(1)
	if st.ChildCount != 8 {
		t.Errorf("ChildCount = %d, want 8 (depth 1..8 of the chain)", st.ChildCount)
	}
	if !st.Truncated {
		t.Errorf("Truncated = false, want true (pids 10..12 sit below the cap)")
	}
	for _, c := range st.Children {
		if c.PID < 2 || c.PID > 9 {
			t.Errorf("pid %d outside the depth budget", c.PID)
		}
	}
}

func TestWalkSubtreeCycleGuard(t *testing.T) {
	// pid-reuse artifact: A reports B as child AND B reports A as child.
	// Without the visited set this walk spins forever.
	installFakeTree(t, fakeTree{
		kids: map[int][]int{1: {2}, 2: {1}},
		infos: map[int]procInfo{
			1: info(2, "a", 10),
			2: info(1, "b", 20),
		},
	})
	st := walk1(1)
	if st.ChildCount != 1 {
		t.Fatalf("ChildCount = %d, want 1 (the cycle must not re-enter pid 1)", st.ChildCount)
	}
	if st.Children[0].PID != 2 {
		t.Errorf("child = %+v, want pid 2", st.Children[0])
	}
}

func TestWalkSubtreeNodeCap(t *testing.T) {
	// One root with 600 children: the 512 cap truncates and reports.
	kids := map[int][]int{}
	infos := map[int]procInfo{1: info(0, "root", 1)}
	wide := make([]int, 0, 600)
	for i := 2; i <= 601; i++ {
		wide = append(wide, i)
		infos[i] = info(1, fmt.Sprintf("kid%d", i), uint64(i))
	}
	kids[1] = wide
	installFakeTree(t, fakeTree{kids: kids, infos: infos})

	st := walk1(1)
	if st.ChildCount != maxTreeChildren {
		t.Errorf("ChildCount = %d, want %d", st.ChildCount, maxTreeChildren)
	}
	if !st.Truncated {
		t.Errorf("Truncated = false, want true")
	}
	if len(st.Children) != defaultTopN {
		t.Errorf("Children len = %d, want %d (top-N)", len(st.Children), defaultTopN)
	}
}

// TestWalkSubtreeTopNSliceIsNotTruncated pins the ARB-F1 semantics: a subtree
// with MORE than defaultTopN descendants but well under the walk cap reports
// child_count == the EXACT census and children_truncated == FALSE. The top-N
// display slice is the same walk with an undisplayed tail (shown as "and M
// more"), NOT dropped data — before the fix the display cut set Truncated, so
// any 11..511-child instance lied "counts are floors".
func TestWalkSubtreeTopNSliceIsNotTruncated(t *testing.T) {
	const n = 25 // > defaultTopN (10), far below maxTreeChildren (512)
	kids := map[int][]int{}
	infos := map[int]procInfo{1: info(0, "root", 1)}
	flat := make([]int, 0, n)
	for i := 2; i <= n+1; i++ {
		flat = append(flat, i)
		infos[i] = info(1, fmt.Sprintf("kid%d", i), uint64(i)) // rss == pid
	}
	kids[1] = flat
	installFakeTree(t, fakeTree{kids: kids, infos: infos})

	st := walk1(1)
	if st.ChildCount != n {
		t.Errorf("ChildCount = %d, want %d (exact census, untouched by the display slice)", st.ChildCount, n)
	}
	if st.Truncated {
		t.Error("Truncated = true on a walk that hit NO cap — the top-N slice must not claim truncation (ARB-F1)")
	}
	if len(st.Children) != defaultTopN {
		t.Fatalf("Children len = %d, want %d", len(st.Children), defaultTopN)
	}
	// RSS-sorted desc: rss == pid, so the top-10 are pids 26..17.
	for i, c := range st.Children {
		wantPID := (n + 1) - i
		if c.PID != wantPID {
			t.Errorf("children[%d].PID = %d, want %d (top-N by RSS desc)", i, c.PID, wantPID)
		}
	}
}

func TestWalkSubtreeToleratesExitsMidWalk(t *testing.T) {
	// 100 has children 200 and 300, and 200 has child 400. 300 and 400
	// vanished between the enumeration (both still listed as kids) and the
	// per-child detail read (procInfoOf gone). NOTHING may error out of
	// the walk — it shrinks the visible tree and keeps going. (The old
	// shape of this test injected childrenOf failing for a LIVE pid; with
	// the single-enumeration seam of review G5 that race is structurally
	// gone — one snapshot answers every structural question — so what
	// remains to tolerate is exactly this enumerate-then-read churn.)
	installFakeTree(t, fakeTree{
		kids: map[int][]int{100: {200, 300}, 200: {400}},
		infos: map[int]procInfo{
			100: info(1, "root", 1000),
			200: info(100, "node", 2000),
			// 300 and 400 deliberately absent: procInfoOf says "gone".
		},
	})

	st := walk1(100)
	if st.ChildCount != 1 {
		t.Fatalf("ChildCount = %d, want 1 (only pid 200 survives)", st.ChildCount)
	}
	if st.Children[0].PID != 200 {
		t.Errorf("child = %+v, want 200", st.Children[0])
	}
	if st.Unavailable {
		t.Error("churn misses must not mark the tree unavailable — the root WAS readable")
	}
}

func TestWalkSubtreeRootUnavailableMarksTruth(t *testing.T) {
	// Review G3, shape 1: the enumeration does not know the instance's main
	// pid (gone or unreadable). The walk must NOT emit a quiet zero — the
	// marker is the difference between "measured 0 children" and "could not
	// read".
	installFakeTree(t, fakeTree{
		kids:   map[int][]int{},
		infos:  map[int]procInfo{999: info(1, "stranger", 10)},
		absent: map[int]bool{100: true}, // the instance root is not enumerated
	})
	st := walk1(100)
	if !st.Unavailable {
		t.Fatal("root missing from enumeration must mark the tree unavailable")
	}
	if st.ChildCount != 0 || st.SubtreeRSSBytes != 0 || len(st.Children) != 0 {
		t.Errorf("unavailable walk must collect nothing: %+v", st)
	}
	if b := marshalForTest(t, st); !strings.Contains(b, `"tree_unavailable":true`) {
		t.Errorf("json must carry the marker: %s", b)
	}

	// Shape 2: the WHOLE enumeration fails — every tree is unavailable,
	// and the walk still returns a valid (empty + marked) stat.
	installFakeTree(t, fakeTree{
		kids:      map[int][]int{100: {200}},
		infos:     map[int]procInfo{100: info(1, "root", 1000), 200: info(100, "kid", 10)},
		enumErrOn: true,
	})
	st = walk1(100)
	if !st.Unavailable {
		t.Fatalf("failed enumeration must mark the tree unavailable: %+v", st)
	}
	if st.ChildCount != 0 {
		t.Errorf("collected %d from a failed enumeration", st.ChildCount)
	}
}

func TestWalkSubtreeDepthCapLeafIsNotTruncated(t *testing.T) {
	// Review G4: a chain that ends EXACTLY at maxTreeDepth (pid at depth 8
	// is a leaf) has no data past the cap — Truncated must stay false. The
	// old shape set it unconditionally at the cap line: honest about the
	// cap, dishonest about the data.
	kids := map[int][]int{}
	infos := map[int]procInfo{}
	for i := 1; i <= 1+maxTreeDepth; i++ { // 1..9: pid 9 sits at depth 8
		infos[i] = info(i-1, fmt.Sprintf("p%d", i), uint64(i))
		if i < 1+maxTreeDepth {
			kids[i] = []int{i + 1}
		}
	}
	installFakeTree(t, fakeTree{kids: kids, infos: infos})

	st := walk1(1)
	if st.ChildCount != maxTreeDepth {
		t.Errorf("ChildCount = %d, want %d", st.ChildCount, maxTreeDepth)
	}
	if st.Truncated {
		t.Errorf("Truncated = true on a chain that ENDS at the depth cap — nothing was dropped (review G4)")
	}

	// One node deeper past the cap and truncation must light up (the
	// boundary's other side; the 12-node chain test covers the wide case).
	kids[1+maxTreeDepth] = []int{2 + maxTreeDepth}
	infos[2+maxTreeDepth] = info(1+maxTreeDepth, "past", 1)
	installFakeTree(t, fakeTree{kids: kids, infos: infos})
	st = walk1(1)
	if !st.Truncated {
		t.Errorf("Truncated = false with a descendant past the cap")
	}
}

// TestWalkSubtreeDepthCapBackEdgeIsNotTruncated pins the F-m4 half of G4: a
// BACK-EDGE at the cap is not dropped data. Chain 1→2→…→9 puts pid 9 at the
// depth cap; its only kid is pid 1 (already visited — the pid-reuse shape
// where a deep descendant points back at an ancestor). The old shape read
// `!known || len(kids) > 0` at the cap, so it counted that back-edge as
// data lost past the cap and reported Truncated=true over a census that was
// complete. ChildCount is then a promised floor on a number that was never
// a floor.
func TestWalkSubtreeDepthCapBackEdgeIsNotTruncated(t *testing.T) {
	kids := map[int][]int{}
	infos := map[int]procInfo{}
	for i := 1; i <= 1+maxTreeDepth; i++ { // 1..9: pid 9 sits at depth 8
		infos[i] = info(i-1, fmt.Sprintf("p%d", i), uint64(i))
		if i < 1+maxTreeDepth {
			kids[i] = []int{i + 1}
		}
	}
	// The cap node's only kid is a cycle back to the root.
	kids[1+maxTreeDepth] = []int{1}
	installFakeTree(t, fakeTree{kids: kids, infos: infos})

	st := walk1(1)
	if st.ChildCount != maxTreeDepth {
		t.Errorf("ChildCount = %d, want %d", st.ChildCount, maxTreeDepth)
	}
	if st.Truncated {
		t.Error("Truncated = true when the cap node's only kid is a visited back-edge — nothing was dropped (F-m4)")
	}

	// The boundary's other side: a REAL unvisited grandchild past the cap
	// is genuinely uncollected data, and the flag must light up.
	kids[1+maxTreeDepth] = []int{1, 2 + maxTreeDepth}
	infos[2+maxTreeDepth] = info(1+maxTreeDepth, "unvisited", 1)
	installFakeTree(t, fakeTree{kids: kids, infos: infos})
	st = walk1(1)
	if !st.Truncated {
		t.Error("Truncated = false with an unvisited descendant past the cap — data WAS dropped")
	}
}

func TestCollectEnumeratesOncePerPoll(t *testing.T) {
	// Review G5: the modal polls every 2s on the incident machine. One
	// Collect must enumerate /proc EXACTLY once regardless of instance
	// count — before the fix each instance's BFS paid a gopsutil Children()
	// full scan PER NODE (up to 512 × N per poll).
	calls := 0
	installFakeTree(t, fakeTree{
		kids: map[int][]int{1: {2, 3}, 2: {4}, 5: {6}},
		infos: map[int]procInfo{
			1: info(0, "a", 10), 2: info(1, "b", 20), 3: info(1, "c", 30),
			4: info(2, "d", 40), 5: info(0, "e", 50), 6: info(5, "f", 60),
		},
		listCalls: &calls,
	})
	instances := []InputInstance{
		{ID: "i1", Name: "one", PID: 1, Status: "running"},
		{ID: "i2", Name: "two", PID: 5, Status: "running"},
	}
	c := &Collector{}
	st := c.Collect(instances, map[string]string{})

	if calls != 1 {
		t.Errorf("listProcs called %d times per Collect, want exactly 1 (review G5)", calls)
	}
	// Both trees still aggregate correctly off the shared index.
	byID := map[string]InstanceStat{}
	for _, is := range st.Instances {
		byID[is.ID] = is
	}
	if byID["i1"].ChildCount != 3 {
		t.Errorf("i1 child_count = %d, want 3", byID["i1"].ChildCount)
	}
	if byID["i2"].ChildCount != 1 {
		t.Errorf("i2 child_count = %d, want 1", byID["i2"].ChildCount)
	}
	if byID["i1"].TreeUnavailable || byID["i2"].TreeUnavailable {
		t.Error("healthy forest must not mark trees unavailable")
	}

	// A root the enumeration does not know rides the row as
	// tree_unavailable (review G3 end-to-end through the collector).
	installFakeTree(t, fakeTree{
		kids:   map[int][]int{1: {2}},
		infos:  map[int]procInfo{1: info(0, "a", 10), 2: info(1, "b", 20)},
		absent: map[int]bool{5: true},
	})
	st = c.Collect(instances, map[string]string{})
	byID = map[string]InstanceStat{}
	for _, is := range st.Instances {
		byID[is.ID] = is
	}
	if !byID["i2"].TreeUnavailable {
		t.Errorf("unenumerable root must mark the row: %+v", byID["i2"])
	}
	if byID["i2"].ChildCount != 0 {
		t.Errorf("unavailable row must not claim children: %+v", byID["i2"])
	}
	if byID["i1"].TreeUnavailable {
		t.Errorf("the readable instance must stay readable: %+v", byID["i1"])
	}
}

// TestCollectSkipsProcIndexWhenNoRunningInstances pins ARB-F10: a collect
// with no running instance carrying a live PID must not pay for a /proc
// enumeration it will never read (listProcs never called). The per-instance
// walk already skipped such rows; before this fix the index was built up
// front regardless — a wasted glob-scan on a fully-stopped instance set.
func TestCollectSkipsProcIndexWhenNoRunningInstances(t *testing.T) {
	calls := 0
	installFakeTree(t, fakeTree{
		kids:      map[int][]int{1: {2}},
		infos:     map[int]procInfo{1: info(0, "a", 10), 2: info(1, "b", 20)},
		listCalls: &calls,
	})
	instances := []InputInstance{
		{ID: "stopped", Name: "s", PID: 1, Status: "stopped"}, // not running → never walked
		{ID: "norun", Name: "n", PID: 0, Status: "running"},   // running but no live PID → never walked
	}
	st := (&Collector{}).Collect(instances, map[string]string{})
	if calls != 0 {
		t.Errorf("listProcs called %d times, want 0 (no walking instance ⇒ no enumeration, ARB-F10)", calls)
	}
	for _, is := range st.Instances {
		if is.TreeUnavailable {
			t.Errorf("unwalked instance %q must not mark the tree unavailable: %+v", is.ID, is)
		}
	}

	// One running instance with a live PID flips it back on — the index IS
	// built (one enumeration) so the optimization never hides a real tree.
	calls = 0
	installFakeTree(t, fakeTree{
		kids:      map[int][]int{1: {2}},
		infos:     map[int]procInfo{1: info(0, "a", 10), 2: info(1, "b", 20)},
		listCalls: &calls,
	})
	st = (&Collector{}).Collect([]InputInstance{{ID: "live", Name: "l", PID: 1, Status: "running"}}, map[string]string{})
	if calls != 1 {
		t.Errorf("listProcs called %d times, want 1 (a live instance needs its tree)", calls)
	}
}

func TestWalkSubtreeLeafRoot(t *testing.T) {
	installFakeTree(t, fakeTree{
		kids:  map[int][]int{},
		infos: map[int]procInfo{7: info(1, "solo", 100)},
	})
	st := walk1(7)
	if st.ChildCount != 0 || st.Truncated || len(st.Children) != 0 {
		t.Fatalf("leaf root: %+v", st)
	}
}

// TestWalkSubtreeNilIndexIsUnavailable pins the ARB-R2-F3 defensive guard:
// walkSubtree(pid, nil) must degrade to Unavailable=true — the same marker
// the root-miss path emits — instead of panicking on ix.known. Today the
// collector only ever passes a non-nil index (it builds one exactly when a
// walk will run — ARB-F10, mirrored by anyRunningInstance), so this pins
// the shape for the day the walk's `running && PID>0` guard is ever widened
// without that predicate: unreadable must stay unreadable, never a crash
// and never a measured zero.
// (Moved here from sampler_test.go, F-n4: it tests tree.go.)
func TestWalkSubtreeNilIndexIsUnavailable(t *testing.T) {
	st := walkSubtree(12345, nil)
	if !st.Unavailable {
		t.Error("walkSubtree(pid, nil) did not report Unavailable")
	}
	if st.ChildCount != 0 || st.SubtreeRSSBytes != 0 || len(st.Children) != 0 {
		t.Errorf("nil-index walk emitted numbers: %+v", st)
	}
}

// TestCollectUnavailableTreeFiresLowerBoundSubtreeAlert pins F-A2m1: an
// UNREADABLE tree is UNKNOWN for its descendants (child_count stays 0 and
// the child-count signal stays silent), but its main-PID RSS is a MEASURED
// number — memRSS comes from sampleProcess(inst.PID) in Phase 1,
// INDEPENDENT of the enumeration — so SubtreeRSS = memRSS + 0 = memRSS is a
// LOWER BOUND. memRSS > 40% of total RAM therefore PROVES the real subtree
// crosses 40%, and the alert must fire, rendered as a "≥" bound rather than
// an exact figure.
//
// Non-vacuity is structural: the readable control proves the same row's
// numbers CAN cross the threshold on this machine, the unavailable row
// proves they DO cross it when the descendants are unenumerated, and the
// under-threshold control proves the alert is not unconditionally emitted.
// Re-introducing the `if is.TreeUnavailable { continue }` skip (round-2 m3)
// or dropping the Partial "≥" branch turns this RED.
func TestCollectUnavailableTreeFiresLowerBoundSubtreeAlert(t *testing.T) {
	oldPSI, oldMem := readPSIMemory, readVirtualMemory
	t.Cleanup(func() { readPSIMemory, readVirtualMemory = oldPSI, oldMem })
	readPSIMemory = func() (string, error) { return "some avg10=0 avg60=0 avg300=0 total=0\n", nil }
	// A 1 MiB "system": any live main-PID RSS is then far past the 40%
	// subtree threshold, while available RAM stays at 100% so the RAM signal
	// is silent — the only alert that can possibly fire is subtree-rss.
	readVirtualMemory = func() (*mem.VirtualMemoryStat, error) {
		return &mem.VirtualMemoryStat{Total: 1 << 20, Available: 1 << 20}, nil
	}

	self := os.Getpid() // a live main PID: sampleProcess returns real RSS
	instances := []InputInstance{{ID: "inst-x", Name: "x", PID: self, Status: "running"}}

	// CONTROL first: the SAME instance with a READABLE tree must fire, in
	// the EXACT (non-≥) form. That is what keeps the assertions below
	// non-vacuous — it proves the numbers on this row can cross the
	// threshold at all, and that the readable path is unchanged.
	installFakeTree(t, fakeTree{
		kids:  map[int][]int{},
		infos: map[int]procInfo{self: info(0, "test-bin", 1)},
	})
	st := (&Collector{}).Collect(instances, map[string]string{})
	row := st.Instances[0]
	if row.TreeUnavailable {
		t.Fatalf("control precondition: the readable root was marked unavailable: %+v", row)
	}
	if row.SubtreeRSSBytes == 0 {
		t.Skip("control precondition: this process sampled 0 RSS — the threshold cannot be crossed here")
	}
	readable := subtreeAlert(st.Alerts)
	if readable == nil {
		t.Fatalf("control: a readable tree with %d bytes of subtree RSS fired nothing — the numbers cannot cross the threshold on this machine", row.SubtreeRSSBytes)
	}
	if strings.HasPrefix(readable.Current, "≥") {
		t.Errorf("a READABLE tree must render the exact form, got %q", readable.Current)
	}
	if !strings.Contains(readable.Current, "("+formatBytes(row.SubtreeRSSBytes)+")") {
		t.Errorf("readable row must carry the plain bytes paren, got %q", readable.Current)
	}
	if strings.Contains(readable.Current, "unenumerated") {
		t.Errorf("readable row must not claim unenumerated children: %q", readable.Current)
	}

	// Now the tree is UNREADABLE: the enumeration does not know the root.
	// The row keeps its MEASURED main-PID RSS and fires as a lower bound.
	installFakeTree(t, fakeTree{
		kids:   map[int][]int{},
		infos:  map[int]procInfo{self: info(0, "test-bin", 1)},
		absent: map[int]bool{self: true},
	})
	st = (&Collector{}).Collect(instances, map[string]string{})
	row = st.Instances[0]
	if !row.TreeUnavailable {
		t.Fatalf("precondition: the absent root was not marked unavailable: %+v", row)
	}
	if row.SubtreeRSSBytes == 0 {
		t.Fatal("precondition: the unavailable row carries no RSS — this test would pass vacuously")
	}
	if row.ChildCount != 0 {
		t.Errorf("unavailable row must not claim children: %+v", row)
	}
	partial := subtreeAlert(st.Alerts)
	if partial == nil {
		t.Fatalf("an unreadable tree with %d bytes of measured main-PID RSS fired nothing — the lower-bound alert was silenced (F-A2m1)", row.SubtreeRSSBytes)
	}
	if !strings.HasPrefix(partial.Current, "≥") {
		t.Errorf("unavailable row must render a LOWER BOUND, got %q", partial.Current)
	}
	if !strings.Contains(partial.Current, "≥ "+formatBytes(row.SubtreeRSSBytes)) {
		t.Errorf("the bytes themselves must carry the ≥ bound, got %q", partial.Current)
	}
	if !strings.Contains(partial.Current, "children unenumerated") {
		t.Errorf("the bound must say WHY it is a bound: %q", partial.Current)
	}
	// Child-count stays silent on an unreadable tree — the descendants are
	// unknown, and 0 must never be reported as a measured census.
	for _, a := range st.Alerts {
		if strings.HasPrefix(a.ID, "child-count:") {
			t.Errorf("child-count fired on an unenumerated tree: %q", a.ID)
		}
	}

	// UNDER-THRESHOLD control: the same measurable main-PID RSS against a
	// system total it cannot cross fires nothing — the alert above is the
	// ratio's doing, not an unconditional emission for unavailable rows.
	readVirtualMemory = func() (*mem.VirtualMemoryStat, error) {
		return &mem.VirtualMemoryStat{Total: 256 << 30, Available: 256 << 30}, nil
	}
	st = (&Collector{}).Collect(instances, map[string]string{})
	if fired := subtreeAlertIDs(st.Alerts); len(fired) != 0 {
		t.Errorf("under-threshold control fired %v — the alert must be the 40%% ratio's doing", fired)
	}
}

// subtreeAlert returns the single subtree-rss alert in the set, or nil.
func subtreeAlert(alerts []Alert) *Alert {
	for i := range alerts {
		if strings.HasPrefix(alerts[i].ID, "subtree-rss:") {
			return &alerts[i]
		}
	}
	return nil
}

func subtreeAlertIDs(alerts []Alert) []string {
	var out []string
	for _, a := range alerts {
		if strings.HasPrefix(a.ID, "subtree-rss:") || strings.HasPrefix(a.ID, "child-count:") {
			out = append(out, a.ID)
		}
	}
	return out
}

func TestParsePSIsomeAvg10(t *testing.T) {
	// Canonical content, byte-for-byte the /proc/pressure/memory shape.
	content := "some avg10=47.36 avg60=38.50 avg300=20.06 total=1931789490\nfull avg10=12.00 avg60=9.50 avg300=5.00 total=1454200264\n"
	v, ok := parsePSIsomeAvg10(content)
	if !ok || v != 47.36 {
		t.Fatalf("parse(%q) = %v,%v, want 47.36,true", content, v, ok)
	}
	// some line with extra spacing.
	v, ok = parsePSIsomeAvg10("  some   avg10=30.00   avg60=1.00 avg300=0.00 total=5\n")
	if !ok || v != 30.0 {
		t.Fatalf("spacing: %v,%v", v, ok)
	}
	// full must never be mistaken for some.
	v, ok = parsePSIsomeAvg10("full avg10=99.00 avg60=0 avg300=0 total=0\n")
	if ok || v != 0 {
		t.Fatalf("full-only content must miss: %v,%v", v, ok)
	}
	for _, bad := range []string{
		"",
		"some avg10=abc avg60=0 avg300=0 total=0",
		"some avg60=1.0 total=0", // no avg10 on the some line
		"garbage\nlines\nhere",   // no some line at all
		"someone avg10=5.0",      // prefix must be the whole token
	} {
		if v, ok := parsePSIsomeAvg10(bad); ok || v != 0 {
			t.Errorf("parse(%q) = %v,%v, want 0,false", bad, v, ok)
		}
	}
}

func TestFormatBytesAndPct(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0 B"}, {1023, "1023 B"}, {1024, "1.0 KB"},
		{1024*1024 - 1, "1024.0 KB"},
		{1024 * 1024, "1.0 MB"},
		{uint64(1.5 * 1024 * 1024 * 1024), "1.50 GB"},
	}
	for _, c := range cases {
		if got := formatBytes(c.in); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
	if pct(-0.04) != "0.0%" {
		t.Errorf("pct(-0.04) = %q, want 0.0%% (no negative zero)", pct(-0.04))
	}
}

func TestStatsJSONFieldNamesAreStable(t *testing.T) {
	// The UI slices these names; renaming any of them silently breaks the
	// served page, so pin them here at the Go side too.
	s := Stats{}
	b := marshalForTest(t, s)
	for _, want := range []string{`"instances"`, `"worktrees"`, `"global"`, `"alerts"`, `"system"`} {
		if !strings.Contains(b, want) {
			t.Errorf("Stats json missing %s: %s", want, b)
		}
	}
	is := InstanceStat{}
	b = marshalForTest(t, is)
	for _, want := range []string{`"child_count"`, `"subtree_rss_bytes"`, `"children_truncated"`, `"tree_unavailable"`} {
		if !strings.Contains(b, want) {
			t.Errorf("InstanceStat json missing %s: %s", want, b)
		}
	}
}
