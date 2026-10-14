package monitor

import (
	"os"
	"sort"

	"github.com/shirou/gopsutil/v4/process"
)

// Instance process-tree visibility (issue #111 P2, design comment #2).
//
// Zero-intervention by construction: this file only READS procfs (via
// gopsutil) — it never signals, waits, reaps, or limits anything it finds.
// The walk's whole purpose is to make a runaway subtree VISIBLE (issue #111:
// 230 leaked fixture processes hid in the "children not counted" blind spot);
// deciding what to do about it stays with the user.
//
// Why ONE enumeration per collect (review G5): the first cut walked via
// gopsutil Children(), which on Linux is a full /proc glob-scan PER CALL —
// a modal poll over N instances was up to 512 full scans TIMES N per poll,
// spent on exactly the incident machine this feature exists to watch. One
// process.Processes() pass builds a ppid→children index and every subtree
// lookup below becomes a map read. The enumeration is now the injectable
// seam (see listProcs); the fake-tree tests keep their synthetic forests.

// Walk knobs. The numbers come from the incident evidence (issue #111):
// leaked fixture subtrees ran ~200+ processes, so the caps must be high
// enough to show a real leak, low enough to bound one collect on a hostile
// tree. Depth 8 covers any realistic shell→node→tool chain; deeper is
// almost certainly a pid-reuse artifact, not a dependency chain.
const (
	// maxTreeDepth bounds how deep below an instance's main PID the walk
	// goes (main PID = depth 0).
	maxTreeDepth = 8
	// maxTreeChildren bounds how many descendant entries ONE instance
	// collects. A subtree over the cap reports Truncated=true — the number
	// becomes a floor, never a lie.
	maxTreeChildren = 512
	// defaultTopN is how many descendant entries ride along with the
	// aggregation, sorted by RSS desc ("top offenders first" — the answer
	// to "WHO is eating it", not the full census).
	defaultTopN = 10
)

// ChildProc is one entry of an instance's subtree detail.
type ChildProc struct {
	PID      int    `json:"pid"`
	PPID     int    `json:"ppid"`
	Name     string `json:"name"`
	RSSBytes uint64 `json:"rss_bytes"`
	// CPUPercent is the LIFETIME average (total CPU time / uptime), not a
	// windowed delta like the instance rows' cpu_percent — gopsutil's
	// process.CPUPercent is per-Process-stateful and the descendant set
	// churns between collects, so lifetime-average is the only figure a
	// stateless walk can compute honestly. Use it for ranking, not alarm.
	CPUPercent float64 `json:"cpu_percent"`
	// CreatedAtMS is the process start time in Unix milliseconds.
	CreatedAtMS int64 `json:"created_at_ms"`
}

// TreeStat is the subtree aggregation for one instance main PID.
type TreeStat struct {
	// ChildCount is the number of collected DESCENDANTS (main PID excluded).
	ChildCount int `json:"child_count"`
	// SubtreeRSSBytes is main PID RSS + all collected descendants' RSS,
	// as reported by the caller (the collector owns the main-PID sample).
	SubtreeRSSBytes uint64 `json:"subtree_rss_bytes"`
	// Children is the top defaultTopN descendants by RSS (may be fewer).
	Children []ChildProc `json:"children,omitempty"`
	// Truncated is true ONLY when the walk stopped at a depth/count cap
	// before exhausting the tree — ChildCount is then a floor. Review G4
	// narrowed it to the honest rule: set ONLY when data was actually
	// dropped (descendants existed past the depth cap, the census hit
	// maxTreeChildren, or a depth-cap probe could not prove the node was
	// a leaf). The top-N display slice (len(collected) > defaultTopN) is
	// deliberately NOT truncation (ARB-F1): that tail is undisplayed, not
	// dropped — child_count stays the exact census and the UI shows the
	// tail as "and M more".
	Truncated bool `json:"children_truncated"`
	// Unavailable is true when the instance's tree could not be READ at
	// all — the enumeration failed or the main PID is not in it (review
	// G3). It distinguishes "unreadable" from the measured zero: without
	// this marker the UI renders a procfs failure as "0 children", which
	// is a fabricated all-clear. Same honesty family as psi_available and
	// sampled_at_ms=0. Mid-walk branch misses do NOT set this — they
	// collapse silently by design (see walkSubtree); Truncated plus the
	// footnote carry the partial-failure semantics.
	Unavailable bool `json:"tree_unavailable"`
}

// procInfo is the raw per-PID data the tree walk needs.
type procInfo struct {
	ppid       int
	name       string
	rss        uint64
	cpuPercent float64
	createdMS  int64
}

// procEntry is one line of the single process enumeration.
type procEntry struct {
	pid  int
	ppid int
}

// listProcs is the procfs enumeration seam (review G5): ONE pass over
// /proc per collect, replacing the per-node gopsutil Children() full
// scans. Tests inject a synthetic forest here (see tree_test.go). A pid
// whose Ppid() read fails is skipped — it vanished between readdir and
// stat, and an unknown parent is a forest edge we must not invent.
var listProcs = func() ([]procEntry, error) {
	ps, err := process.Processes()
	if err != nil {
		return nil, err
	}
	out := make([]procEntry, 0, len(ps))
	for _, p := range ps {
		ppid, err := p.Ppid()
		if err != nil {
			continue
		}
		out = append(out, procEntry{pid: int(p.Pid), ppid: int(ppid)})
	}
	return out, nil
}

// procIndex answers "who are pid's children" from ONE enumeration
// snapshot. known=false means "the snapshot does not speak for this pid"
// — gone or unreadable — and is deliberately distinct from "known childless"
// (an empty but known list), because the root of a walk treats the two
// differently (G3): gone ⇒ tree_unavailable, childless ⇒ a real zero.
type procIndex struct {
	kids  map[int][]int
	known map[int]bool
}

// buildProcIndex enumerates once and indexes by ppid. A failed enumeration
// yields an EMPTY index, not a nil — every root lookup then misses, and
// every tree is marked unavailable, which is the truth: we read nothing.
func buildProcIndex() *procIndex {
	ix := &procIndex{kids: map[int][]int{}, known: map[int]bool{}}
	entries, err := listProcs()
	if err != nil {
		return ix
	}
	for _, e := range entries {
		ix.known[e.pid] = true
		ix.kids[e.ppid] = append(ix.kids[e.ppid], e.pid)
	}
	return ix
}

func (ix *procIndex) children(pid int) ([]int, bool) {
	if !ix.known[pid] {
		return nil, false
	}
	return ix.kids[pid], true
}

// procInfoOf reads one process's display fields. ok=false means the process
// is gone (or unreadable) — the walk tolerates that silently: a process can
// exit between the enumeration and this read, which is normal churn, not an
// error worth surfacing.
var procInfoOf = func(pid int) (procInfo, bool) {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return procInfo{}, false
	}
	var info procInfo
	if ppid, err := p.Ppid(); err == nil {
		info.ppid = int(ppid)
	}
	if name, err := p.Name(); err == nil {
		info.name = name
	}
	if mem, err := p.MemoryInfo(); err == nil {
		info.rss = mem.RSS
	}
	if cpu, err := p.CPUPercent(); err == nil {
		info.cpuPercent = cpu
	}
	if ct, err := p.CreateTime(); err == nil {
		info.createdMS = ct
	}
	return info, true
}

// walkSubtree collects pid's descendants (pid itself excluded — its RSS and
// CPU are sampled by the collector's own phase and merged by the caller into
// SubtreeRSSBytes) by BFS over the caller's procIndex (built once per
// collect — review G5).
//
// Bounds: maxTreeDepth (a pid at depth maxTreeDepth is collected but NOT
// expanded) and maxTreeChildren total. The visited set is BOTH the cycle
// guard (pid reuse can make A report B as child and B report A — the
// enumeration reads a live, racy /proc) and the pid-reuse dedup.
//
// Honesty rules, each pinned by a test:
//   - root miss (G3): the enumeration does not know pid → Unavailable=true
//     and nothing else; the numbers stay zero but the marker says "zero is
//     unknown", never "zero is measured".
//   - depth cap (G4): Truncated is set at a depth-cap node only when that
//     node actually had descendants to drop (or the probe could not prove
//     it leafless). A chain that ends exactly at maxTreeDepth lost no
//     data and must not claim truncation.
//   - child cap (G5): reaching maxTreeChildren stops the WHOLE walk at
//     once (labeled break), not just the inner loop — the census is capped,
//     every further lookup would answer a question nobody gets to read.
//   - mid-walk misses stay silent by design (the documented collapse):
//     churn shrinks the visible tree, never the collect; the consequence —
//     counts can be floors during churn — is stated in API.md and the
//     modal footnote rather than surfaced per branch.
func walkSubtree(pid int, ix *procIndex) TreeStat {
	st := TreeStat{Children: nil}
	// ARB-R2-F3: a nil index means "no enumeration exists" — the collect
	// builds one only when a running instance will be walked (ARB-F10),
	// so a nil reaching here is the walk's `running && PID>0` guard ever
	// being widened without the collector predicate. Degrade to the same
	// Unavailable marker the root-miss path emits instead of panicking on
	// ix.known: a tree that cannot be read is UNKNOWN, never a measured
	// zero.
	if ix == nil {
		st.Unavailable = true
		return st
	}
	if _, known := ix.children(pid); !known {
		// G3: we could not read this tree at all. Do not emit a quiet
		// zero — the caller renders "unavailable" from this marker.
		st.Unavailable = true
		return st
	}
	visited := map[int]bool{pid: true}
	type node struct {
		pid   int
		depth int
	}
	queue := []node{{pid: pid, depth: 0}}
	var collected []ChildProc

walk:
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxTreeDepth {
			// G4: truncation is a fact about dropped data, not about
			// touching a line. One map read tells whether this node had
			// children past the cap; an unknown answer counts as "may
			// have dropped data" — floor semantics, never a lie.
			if kids, known := ix.children(cur.pid); !known || len(kids) > 0 {
				st.Truncated = true
			}
			continue
		}
		kids, known := ix.children(cur.pid)
		if !known {
			// Mid-walk miss: silent collapse (see doc comment).
			continue
		}
		for _, kid := range kids {
			if visited[kid] {
				continue // cycle / pid-reuse guard
			}
			visited[kid] = true
			if len(collected) >= maxTreeChildren {
				st.Truncated = true
				break walk // G5: stop dead — capped means capped
			}
			info, ok := procInfoOf(kid)
			if !ok {
				continue // exited between enumeration and read — normal churn
			}
			collected = append(collected, ChildProc{
				PID:         kid,
				PPID:        cur.pid,
				Name:        info.name,
				RSSBytes:    info.rss,
				CPUPercent:  info.cpuPercent,
				CreatedAtMS: info.createdMS,
			})
			// SubtreeRSSBytes accumulates DESCENDANT RSS here; the caller
			// adds the main PID's own sample (it already holds it — one
			// read per PID, one owner per number).
			st.SubtreeRSSBytes += info.rss
			queue = append(queue, node{pid: kid, depth: cur.depth + 1})
		}
	}

	st.ChildCount = len(collected)
	sort.SliceStable(collected, func(i, j int) bool {
		return collected[i].RSSBytes > collected[j].RSSBytes
	})
	if len(collected) > defaultTopN {
		// DISPLAY slice only — NOT a walk cap. The undisplayed tail is not
		// dropped data: child_count above is the EXACT census and the
		// undisplayed count reaches the UI as "and M more"
		// (child_count - len(children)). children_truncated stays false
		// here (ARB-F1): it must mean a WALK cap only, else every
		// 11..511-child instance would falsely claim "counts are floors".
		st.Children = collected[:defaultTopN]
	} else {
		st.Children = collected
	}
	return st
}

// daemonPID is the current process's PID. Indirect for tests that need to
// pin the daemon-RSS signal's identity without re-execing.
func daemonPID() int { return os.Getpid() }
