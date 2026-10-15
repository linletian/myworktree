package monitor

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// Full table-driven coverage of EvaluateAlerts (issue #111 P2 contract):
// every signal, below / at / above each threshold, severity tiers, the
// one-alert-per-signal rule, and the guards. Pure data in, pure data out —
// no processes, no clock, no /proc.

func TestEvaluateAlertsTable(t *testing.T) {
	const total = uint64(100 << 30) // 100 GiB for clean percentages
	cases := []struct {
		name    string
		in      AlertInput
		wantIDs []string // ordered: reds first, then amber
		// wantSeverity pins the TIER each psi-memory row must carry
		// ("" = the signal must stay silent). The ≤50 boundary is the
		// oomd kill line: 49.99 is "verdict approaching", 50.0 is
		// "verdict here", and a `>` instead of `>=` at either line
		// silently drops the tier — F-m5.
		wantSeverity string
	}{
		{
			name:    "all clear",
			in:      AlertInput{HasPSI: true, PSIsSomeAvg10: 1.0, TotalBytes: total, AvailableBytes: 60 << 30},
			wantIDs: nil,
		},
		{
			name:         "psi amber just at 30",
			in:           AlertInput{HasPSI: true, PSIsSomeAvg10: 30.0, TotalBytes: total, AvailableBytes: 60 << 30},
			wantIDs:      []string{"psi-memory"},
			wantSeverity: SeverityAmber,
		},
		{
			name:    "psi below amber stays silent at 29.99",
			in:      AlertInput{HasPSI: true, PSIsSomeAvg10: 29.99, TotalBytes: total, AvailableBytes: 60 << 30},
			wantIDs: nil,
		},
		{
			name:         "psi red just at 50 collapses the amber tier to one entry",
			in:           AlertInput{HasPSI: true, PSIsSomeAvg10: 50.0, TotalBytes: total, AvailableBytes: 60 << 30},
			wantIDs:      []string{"psi-memory"},
			wantSeverity: SeverityRed,
		},
		{
			name:         "psi amber at 49.99 — the kill line not yet crossed",
			in:           AlertInput{HasPSI: true, PSIsSomeAvg10: 49.99, TotalBytes: total, AvailableBytes: 60 << 30},
			wantIDs:      []string{"psi-memory"},
			wantSeverity: SeverityAmber,
		},
		{
			name:    "psi missing emits nothing and suppresses nothing else",
			in:      AlertInput{HasPSI: false, PSIsSomeAvg10: 99.0, TotalBytes: total, AvailableBytes: 60 << 30},
			wantIDs: nil,
		},
		{
			name:    "available exactly at 15% is not low",
			in:      AlertInput{TotalBytes: total, AvailableBytes: 15 << 30},
			wantIDs: nil,
		},
		{
			name:    "available below 15%",
			in:      AlertInput{TotalBytes: total, AvailableBytes: 14<<30 - 1},
			wantIDs: []string{"available-ram"},
		},
		{
			name: "subtree ratio above 40% flags the instance",
			in: AlertInput{
				TotalBytes:     total,
				AvailableBytes: 60 << 30,
				Instances:      []InstanceTree{{ID: "inst-a", Name: "agent", SubtreeRSS: 41 << 30}},
			},
			wantIDs: []string{"subtree-rss:inst-a"},
		},
		{
			name: "subtree exactly 40% is not over",
			in: AlertInput{
				TotalBytes:     total,
				AvailableBytes: 60 << 30,
				Instances:      []InstanceTree{{ID: "inst-a", Name: "agent", SubtreeRSS: 40 << 30}},
			},
			wantIDs: nil,
		},
		{
			name: "child count above 50 flags the instance",
			in: AlertInput{
				TotalBytes:     total,
				AvailableBytes: 60 << 30,
				Instances:      []InstanceTree{{ID: "inst-a", Name: "agent", ChildCount: 51}},
			},
			wantIDs: []string{"child-count:inst-a"},
		},
		{
			name: "child count exactly 50 is not over",
			in: AlertInput{
				TotalBytes:     total,
				AvailableBytes: 60 << 30,
				Instances:      []InstanceTree{{ID: "inst-a", Name: "agent", ChildCount: 50}},
			},
			wantIDs: nil,
		},
		{
			name:    "daemon over 1GiB",
			in:      AlertInput{TotalBytes: total, AvailableBytes: 60 << 30, DaemonRSSBytes: (1 << 30) + 1},
			wantIDs: []string{"daemon-rss"},
		},
		{
			name:    "daemon exactly 1GiB is not over",
			in:      AlertInput{TotalBytes: total, AvailableBytes: 60 << 30, DaemonRSSBytes: 1 << 30},
			wantIDs: nil,
		},
		{
			name:    "zero total RAM skips all ratio signals",
			in:      AlertInput{HasPSI: false, TotalBytes: 0, DaemonRSSBytes: 5 << 30}, // daemon signal has no RAM ratio — it still fires
			wantIDs: []string{"daemon-rss"},
		},
		{
			// Review G1: child-count is an ABSOLUTE count, not a ratio. A
			// failed VirtualMemory read must swallow the subtree ratio
			// (no denominator to trust) but never the leak fingerprint —
			// before the fix this whole instance loop sat behind the
			// TotalBytes guard and this case came back empty.
			name: "zero total RAM keeps child-count (absolute count, not a ratio)",
			in: AlertInput{
				HasPSI:     false,
				TotalBytes: 0, // VirtualMemory read failed
				Instances: []InstanceTree{
					{ID: "inst-a", Name: "agent", ChildCount: 60, SubtreeRSS: 50 << 30}, // subtree ratio skipped: no denominator
				},
			},
			wantIDs: []string{"child-count:inst-a"},
		},
		{
			name: "everything at once: red leads, instances keep name order",
			in: AlertInput{
				HasPSI:         true,
				PSIsSomeAvg10:  55.0,
				TotalBytes:     total,
				AvailableBytes: 10 << 30,
				DaemonRSSBytes: 2 << 30,
				Instances: []InstanceTree{
					{ID: "i1", Name: "one", SubtreeRSS: 50 << 30, ChildCount: 60},
					{ID: "i2", Name: "two", SubtreeRSS: 45 << 30, ChildCount: 55},
				},
			},
			wantIDs: []string{
				"psi-memory", // red first
				"available-ram",
				"subtree-rss:i1",
				"child-count:i1",
				"subtree-rss:i2",
				"child-count:i2",
				"daemon-rss",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluateAlerts(tc.in)
			if len(got) != len(tc.wantIDs) {
				t.Fatalf("alerts = %d %v, want %d %v", len(got), alertIDs(got), len(tc.wantIDs), tc.wantIDs)
			}
			for i := range got {
				if got[i].ID != tc.wantIDs[i] {
					t.Errorf("alert[%d].ID = %q, want %q (order = reds first, then insertion)", i, got[i].ID, tc.wantIDs[i])
				}
				if got[i].Severity != SeverityRed && got[i].Severity != SeverityAmber {
					t.Errorf("alert %q severity %q", got[i].ID, got[i].Severity)
				}
				// Contract: every alert carries a display triple and a
				// playbook-pointing advice.
				if got[i].Signal == "" || got[i].Threshold == "" || got[i].Current == "" {
					t.Errorf("alert %q missing display fields", got[i].ID)
				}
				if !strings.Contains(got[i].Advice, "PLAYBOOK-memory-alerts.md#") {
					t.Errorf("alert %q advice must point at a playbook anchor: %q", got[i].ID, got[i].Advice)
				}
			}
			// Severity sanity per position: red only ever psi-memory at ≥50.
			for _, a := range got {
				if a.Severity == SeverityRed && a.ID != "psi-memory" {
					t.Errorf("non-PSI alert %q is red — red is reserved for the oomd kill-line mirror", a.ID)
				}
			}
			// F-m5: the table's wantSeverity column. A PSI row must carry
			// EXACTLY the tier the boundary promises (49.99 amber / 50.0
			// red / 30.0 amber), and a case that expects NO alerts at all
			// while PSI is present is the "below the amber line" boundary:
			// the signal must stay silent. `>` instead of `>=` at either
			// line flips one of these rows RED.
			if tc.wantSeverity != "" {
				sev := ""
				for _, a := range got {
					if a.ID == "psi-memory" {
						sev = a.Severity
					}
				}
				if sev != tc.wantSeverity {
					t.Errorf("psi-memory severity = %q, want %q (boundary %v)", sev, tc.wantSeverity, tc.in.PSIsSomeAvg10)
				}
			} else if len(tc.wantIDs) == 0 && tc.in.HasPSI {
				for _, a := range got {
					if a.ID == "psi-memory" {
						t.Errorf("psi-memory fired at %v%% — the signal must stay silent below the amber line", tc.in.PSIsSomeAvg10)
					}
				}
			}
		})
	}
}

func TestEvaluateAlertsPSIRedIsOneEntry(t *testing.T) {
	// 55 crosses BOTH tiers; the rule is one entry at the highest.
	got := EvaluateAlerts(AlertInput{HasPSI: true, PSIsSomeAvg10: 55, TotalBytes: 100 << 30, AvailableBytes: 50 << 30})
	if len(got) != 1 || got[0].ID != "psi-memory" || got[0].Severity != SeverityRed {
		t.Fatalf("got %+v", got)
	}
}

// TestEvaluateAlertsPartialTreeRendersLowerBound pins the display half of
// F-A2m1 + F-N6: a row whose subtree could not be ENUMERATED (Unreadable —
// what the collector feeds for a tree_unavailable row) still fires
// subtree-rss off its MEASURED main-PID RSS, and a row whose walk was
// CAPPED (children_truncated) fires off everything the walk did measure.
// Both numbers are LOWER BOUNDS on the real subtree (true subtree = measured
// + the descendants that were never read), so Current must lead with "≥" and
// name the reason; a readable, uncapped row keeps the exact pre-existing
// shape byte-for-byte. F-N5 adds the other half: an unreadable row's advice
// must name the /proc walk, not a children list that row does not render.
func TestEvaluateAlertsPartialTreeRendersLowerBound(t *testing.T) {
	const total = uint64(1 << 20) // 1 MiB: any RSS is past the 40% line
	const rss = uint64(1 << 19)   // 512 KiB = 50% — over the amber line

	unreadable := EvaluateAlerts(AlertInput{
		TotalBytes:     total,
		AvailableBytes: total,
		Instances: []InstanceTree{
			{ID: "inst-p", Name: "partial", SubtreeRSS: rss, ChildCount: 0, Unreadable: true},
		},
	})
	if len(unreadable) != 1 || unreadable[0].ID != "subtree-rss:inst-p" {
		t.Fatalf("an unreadable row's main-PID RSS is MEASURED and over the line — it must fire subtree-rss: %+v", unreadable)
	}
	cur := unreadable[0].Current
	if !strings.HasPrefix(cur, "≥") {
		t.Errorf("Current must lead with a lower-bound sign, got %q", cur)
	}
	if !strings.Contains(cur, "≥ "+formatBytes(rss)) {
		t.Errorf("the bytes must carry the same bound: %q (want ≥ %s)", cur, formatBytes(rss))
	}
	for _, want := range []string{"main PID measured", "children unenumerated", "actual is higher"} {
		if !strings.Contains(cur, want) {
			t.Errorf("Current must say %q — an unexplained bound reads as a census: %q", want, cur)
		}
	}
	// F-N5: the row renders "Tree unavailable", so "inspect the children
	// below" is a dead end. The advice must name the walk itself and still
	// point at the playbook anchor.
	if strings.Contains(unreadable[0].Advice, "inspect this instance's children") {
		t.Errorf("an UNREADABLE row has no children to inspect — the advice is a dead end: %q", unreadable[0].Advice)
	}
	for _, want := range []string{"main PID", "/proc walk failed", "permissions", "hidepid", "docs/PLAYBOOK-memory-alerts.md#subtree-rss"} {
		if !strings.Contains(unreadable[0].Advice, want) {
			t.Errorf("the unreadable advice must say %q: %q", want, unreadable[0].Advice)
		}
	}
	// The descendants are UNKNOWN, so child-count stays silent on an
	// unreadable row: 0 is never a measured census.
	for _, a := range unreadable {
		if strings.HasPrefix(a.ID, "child-count:") {
			t.Errorf("child-count fired on an unenumerated tree: %q", a.ID)
		}
	}

	// F-N6, the capped half: the same value on a row the walk capped is a
	// lower bound for a DIFFERENT reason — descendants were dropped — and
	// the child-count floor it does carry renders "≥ N".
	capped := EvaluateAlerts(AlertInput{
		TotalBytes:     total,
		AvailableBytes: total,
		Instances: []InstanceTree{
			{ID: "inst-c", Name: "capped", SubtreeRSS: rss, ChildCount: maxTreeChildren, Capped: true},
		},
	})
	if len(capped) != 2 || capped[0].ID != "subtree-rss:inst-c" || capped[1].ID != "child-count:inst-c" {
		t.Fatalf("a capped row fires both subtree-rss and its child-count floor: %+v", capped)
	}
	cur = capped[0].Current
	if !strings.HasPrefix(cur, "≥") {
		t.Errorf("a capped row's RSS is a lower bound too — Current must lead with ≥, got %q", cur)
	}
	if !strings.Contains(cur, "≥ "+formatBytes(rss)) {
		t.Errorf("the capped bytes must carry the bound: %q", cur)
	}
	for _, want := range []string{"walk capped", "descendants not fully counted", "actual is higher"} {
		if !strings.Contains(cur, want) {
			t.Errorf("the capped bound must say %q: %q", want, cur)
		}
	}
	if strings.Contains(cur, "unenumerated") {
		t.Errorf("a capped row is not an unenumerated row — the reasons must not be conflated: %q", cur)
	}
	if want := "≥ " + strconv.Itoa(maxTreeChildren); capped[1].Current != want {
		t.Errorf("capped child-count Current = %q, want the floor form %q", capped[1].Current, want)
	}
	// Capped rows DO render top-N + "and M more", so the original
	// inspect-the-children advice is still actionable and must survive.
	if !strings.Contains(capped[0].Advice, "inspect this instance's children") {
		t.Errorf("a capped row keeps the actionable advice: %q", capped[0].Advice)
	}

	// The READABLE + UNcapped half of the same pin: identical input minus the
	// markers renders the old exact form — no ≥, no footnote, no "≥" on the
	// child count. This is what makes the assertions above non-vacuous: the
	// branches CHANGE the rendering, they are not decoration on top of it.
	exact := EvaluateAlerts(AlertInput{
		TotalBytes:     total,
		AvailableBytes: total,
		Instances: []InstanceTree{
			{ID: "inst-e", Name: "exact", SubtreeRSS: rss, ChildCount: maxTreeChildren, Unreadable: false, Capped: false},
		},
	})
	if len(exact) != 2 || exact[0].ID != "subtree-rss:inst-e" || exact[1].ID != "child-count:inst-e" {
		t.Fatalf("readable + uncapped row must still fire both signals: %+v", exact)
	}
	if want := pct(50.0) + " (" + formatBytes(rss) + ")"; exact[0].Current != want {
		t.Errorf("readable Current = %q, want the unchanged exact form %q", exact[0].Current, want)
	}
	if want := strconv.Itoa(maxTreeChildren); exact[1].Current != want {
		t.Errorf("readable child-count Current = %q, want the exact census %q", exact[1].Current, want)
	}
	if !strings.Contains(exact[0].Advice, "inspect this instance's children") {
		t.Errorf("a readable row keeps the inspect-the-children advice: %q", exact[0].Advice)
	}
}

func alertIDs(as []Alert) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.ID)
	}
	return out
}

// TestSortAlertsUnknownSeverityRanksLast pins ARB-F13: an unrecognized
// severity must sort LAST, never as red. The old map-zero-value read any
// severity not literally "red"/"amber" as rank 0 == red, so "reds first"
// misfired on a future or typo'd severity. EvaluateAlerts emits only
// red/amber today, so this drives sortAlerts directly to pin the latent rule.
func TestSortAlertsUnknownSeverityRanksLast(t *testing.T) {
	alerts := []Alert{
		{ID: "mystery", Severity: "teal"}, // unknown
		{ID: "psi", Severity: SeverityRed},
		{ID: "avail", Severity: SeverityAmber},
	}
	sortAlerts(alerts)
	got := alertIDs(alerts)
	want := []string{"psi", "avail", "mystery"} // red, amber, unknown-last
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v (red, amber, unknown-last)", got, want)
		}
	}
}

// TestPlaybookAdviceAnchorsAreReachable pins ARB-F2: every advice string
// quotes a docs/PLAYBOOK-memory-alerts.md anchor, and a quoted anchor with no
// matching <a id> is a 404 from the one affordance that turns a warning into
// a disposal action. Build an input that trips ALL five contract signals,
// pull each quoted anchor straight out of the RUNTIME advice strings, and
// assert the playbook carries an explicit <a id="..."> for it — a mechanism
// that resolves on GitHub + raw + offline (no fragile Chinese-heading-slug
// replication). This goes RED if any quoted anchor or any playbook <a id>
// drifts, and it is what makes the CHANGELOG's pin claim true.
func TestPlaybookAdviceAnchorsAreReachable(t *testing.T) {
	in := AlertInput{
		HasPSI:         true,
		PSIsSomeAvg10:  75.0,      // red tier — still one psi-memory alert
		TotalBytes:     100 << 30, // 100 GiB
		AvailableBytes: 1 << 30,   // 10% < 15% → available-ram amber
		DaemonRSSBytes: 2 << 30,   // >1 GiB → daemon-rss amber
		Instances: []InstanceTree{
			{ID: "inst-a", Name: "a", SubtreeRSS: 50 << 30, ChildCount: 60}, // subtree-rss:inst-a + child-count:inst-a
		},
	}
	alerts := EvaluateAlerts(in)
	if len(alerts) == 0 {
		t.Fatal("no alerts emitted — the test input must trip every signal")
	}
	pb, err := os.ReadFile("../../docs/PLAYBOOK-memory-alerts.md")
	if err != nil {
		t.Fatalf("read playbook: %v", err)
	}
	playbook := string(pb)

	const marker = "PLAYBOOK-memory-alerts.md#"
	seen := map[string]bool{}
	for _, a := range alerts {
		idx := strings.Index(a.Advice, marker)
		if idx < 0 {
			t.Fatalf("alert %q advice has no playbook anchor: %q", a.ID, a.Advice)
		}
		anchor := a.Advice[idx+len(marker):]
		if anchor == "" {
			t.Fatalf("alert %q advice quotes an empty anchor", a.ID)
		}
		seen[anchor] = true
		want := `<a id="` + anchor + `"></a>`
		if !strings.Contains(playbook, want) {
			t.Errorf("playbook has no anchor %q for alert %q advice — every quoted anchor must resolve", anchor, a.ID)
		}
	}
	// The five contract signals each contribute at least one reachable anchor.
	for _, want := range []string{"psi-memory", "available-ram", "subtree-rss", "child-count", "daemon-rss"} {
		if !seen[want] {
			t.Errorf("no alert quoted the %q anchor — the contract's five signals must all be reachable", want)
		}
	}
}

// TestFormatBytesTiesRoundHalfUpToMatchUI pins F-M1: the Go formatter must
// round HALF-UP, like the UI's toFixed, so an alert row and the modal cell
// beside it never print two different values for the same bytes.
// strconv.FormatFloat is ties-to-even, so the un-rounded shape disagreed
// with the page on every exact .x5 / .xx5 value. The values below are the
// measured UI targets — they are the authority, and each one goes RED if the
// math.Round is dropped (or replaced by a truncation).
func TestFormatBytesTiesRoundHalfUpToMatchUI(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{1310720, "1.3 MB"},     // 1.25 MiB: half-up 1.3 (ties-to-even said 1.2)
		{3355443200, "3.13 GB"}, // 3.125 GiB: half-up 3.13 (ties-to-even said 3.12)
		{1280, "1.3 KB"},        // the same .x5 tie in the KB band
	}
	for _, c := range cases {
		if got := formatBytes(c.in); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q (half-up, like the UI's toFixed)", c.in, got, c.want)
		}
	}

	// Band boundaries and non-tie values keep their old truth: a value below
	// 1 MiB stays in the KB band (524288 = 512 KiB renders as "512.0 KB",
	// never a fractional MB), and the existing non-tie tables in
	// TestFormatBytesAndPct keep passing unchanged.
	if got := formatBytes(524288); got != "512.0 KB" {
		t.Errorf("formatBytes(524288) = %q, want 512.0 KB (below 1 MiB stays KB)", got)
	}
}

func TestThresholdConstantsMatchContract(t *testing.T) {
	// The numbers are the contract (issue #111 comment #4). If a constant
	// drifts, this test is the loud failure — the thresholds must not be
	// "adjusted" silently.
	if PSIAmberPct != 30.0 || PSIRedPct != 50.0 {
		t.Errorf("PSI tiers = %v/%v, want 30/50", PSIAmberPct, PSIRedPct)
	}
	if AvailableAmberPct != 15.0 {
		t.Errorf("available amber = %v, want 15", AvailableAmberPct)
	}
	if SubtreeRSSAmberPct != 40.0 {
		t.Errorf("subtree amber = %v, want 40", SubtreeRSSAmberPct)
	}
	if ChildCountAmber != 50 {
		t.Errorf("child count amber = %v, want 50", ChildCountAmber)
	}
	if DaemonRSSAmberBytes != 1<<30 {
		t.Errorf("daemon amber = %d, want 1GiB", DaemonRSSAmberBytes)
	}
}
