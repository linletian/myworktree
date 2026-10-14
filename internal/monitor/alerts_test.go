package monitor

import (
	"os"
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
	}{
		{
			name:    "all clear",
			in:      AlertInput{HasPSI: true, PSIsSomeAvg10: 1.0, TotalBytes: total, AvailableBytes: 60 << 30},
			wantIDs: nil,
		},
		{
			name:    "psi amber just at 30",
			in:      AlertInput{HasPSI: true, PSIsSomeAvg10: 30.0, TotalBytes: total, AvailableBytes: 60 << 30},
			wantIDs: []string{"psi-memory"},
		},
		{
			name:    "psi below amber stays silent at 29.9",
			in:      AlertInput{HasPSI: true, PSIsSomeAvg10: 29.9, TotalBytes: total, AvailableBytes: 60 << 30},
			wantIDs: nil,
		},
		{
			name:    "psi red just at 50 collapses the amber tier to one entry",
			in:      AlertInput{HasPSI: true, PSIsSomeAvg10: 50.0, TotalBytes: total, AvailableBytes: 60 << 30},
			wantIDs: []string{"psi-memory"},
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
