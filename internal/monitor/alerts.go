package monitor

import (
	"math"
	"os"
	"strconv"
	"strings"
)

// Warning-signal evaluation (issue #111 P2, thresholds per design comment #4).
//
// The principle that pins every number below: 预警必须先行于裁决 — the
// warning must fire BEFORE oomd's verdict. systemd-oomd killed the mwbeta
// scope at "memory pressure > 50% for > 20s with reclaim activity"
// (journalctl evidence, issue #111), so the PSI amber sits at 30% — well
// ahead of the 50% kill line — and the red at 50% is the "oomd is already
// deciding right now" mirror, not a surprise.
//
// Zero-intervention: EvaluateAlerts is a PURE function of (samples,
// constants). It never signals, reaps, limits, or restarts anything; each
// alert's advice is a USER action (close tabs, stop an instance, clean
// /tmp, add swap) pointing at docs/PLAYBOOK-memory-alerts.md. myworktree
// detects; the user disposes.
const (
	// PSI memory some avg10 (%), /proc/pressure/memory. Amber 30%: the
	// early window where user action still changes the outcome. Red 50%:
	// oomd's own kill threshold (same source, so the warning and the
	// verdict ride the same signal — the warning is always first because
	// 30 < 50 and avg10 rises through 30 on its way to 50).
	PSIAmberPct = 30.0
	PSIRedPct   = 50.0

	// System available RAM below 15% of total (gopsutil VirtualMemory
	// Available/Total). Amber: the machine is heading toward the reclaim
	// wall; the incident machine ran 10.9G RSS against 22G with no swap.
	AvailableAmberPct = 15.0

	// One instance's subtree RSS above 40% of system RAM. Amber: a single
	// managed subtree owning two-fifths of the machine is the pattern the
	// monitor previously could not see at all ("children not counted").
	SubtreeRSSAmberPct = 40.0

	// One instance's descendant count above 50. Amber: the fake-serve
	// leak grew ~2 per test run to 230 — count is the cheapest leak
	// fingerprint, and 50 is far below the incident's 230 but far above
	// any healthy tool chain.
	ChildCountAmber = 50

	// Daemon's own RSS above 1 GiB. Amber: healthy mwbeta ran 300–700MB
	// steady (issue #111 evidence); 1GiB means "look at the daemon".
	DaemonRSSAmberBytes = 1 << 30
)

// Severity values.
const (
	SeverityAmber = "amber"
	SeverityRed   = "red"
)

// Alert is one active warning signal. Threshold and Current are pre-formatted
// strings with units (% / B / count) so the UI renders them verbatim; the
// machine-readable comparison lives in EvaluateAlerts, which is the single
// source for both the number and its display.
type Alert struct {
	ID       string `json:"id"`
	Severity string `json:"severity"` // SeverityAmber | SeverityRed
	Signal   string `json:"signal"`
	// Threshold is the crossed limit as displayed ("≥ 30.0%", "> 50", …).
	Threshold string `json:"threshold"`
	// Current is the sampled value as displayed ("47.4%", "3.1 GiB", …).
	Current string `json:"current"`
	// Advice is one sentence naming a USER action, pointing at a playbook
	// anchor (docs/PLAYBOOK-memory-alerts.md#<id>).
	Advice string `json:"advice"`
}

// InstanceTree is one running instance's subtree aggregation fed to
// EvaluateAlerts. ID/Name identify the instance the alert belongs to.
type InstanceTree struct {
	ID         string
	Name       string
	ChildCount int
	SubtreeRSS uint64
}

// AlertInput is the complete sample set EvaluateAlerts consumes. It is pure
// data — the tests build it literally, no processes, no clock.
//
// PSI semantics: HasPSI=false (non-Linux, or /proc/pressure unreadable)
// skips the PSI signal entirely — a missing signal must never fabricate an
// alert, and must never SUPPRESS the others either: on the incident machine
// the kernel could global-OOM without oomd acting at all (21:48 evidence),
// so available-RAM stays a first-class signal even when PSI is present.
type AlertInput struct {
	PSIsSomeAvg10 float64
	HasPSI        bool

	AvailableBytes uint64
	TotalBytes     uint64

	DaemonRSSBytes uint64

	Instances []InstanceTree
}

// EvaluateAlerts applies every threshold to the sample set and returns the
// active alerts, one entry per signal, at the highest severity crossed
// (the rule from the task contract: 同一信号取最高严重级一条).
//
// Ordering: reds first, then ambers, each group in signal-stable order, so
// the UI's first row is always the worst thing known.
//
// Guards worth naming: TotalBytes==0 (mem unreadable) skips every ratio
// signal — dividing by an untrusted zero to invent a percentage is worse
// than silence. child-count is NOT a ratio: it is an absolute count read
// straight off the process table, so a failed VirtualMemory read must not
// swallow the leak fingerprint (review G1) — it stays outside the guard.
// An instance with ChildCount==0 and SubtreeRSS==0 (tree never walked, e.g.
// dead pid tolerated by the walk) emits nothing.
func EvaluateAlerts(in AlertInput) []Alert {
	var alerts []Alert

	// PSI memory pressure (some avg10 — the only tiered signal; its red
	// mirrors oomd's kill line so it is the "verdict is imminent" light).
	if in.HasPSI {
		switch {
		case in.PSIsSomeAvg10 >= PSIRedPct:
			alerts = append(alerts, alert(SeverityRed, "psi-memory",
				"PSI memory some avg10",
				"≥ "+pct(PSIRedPct),
				pct(in.PSIsSomeAvg10),
				"oomd acts at this level (>50% for >20s) — stop an instance or close tabs NOW: docs/PLAYBOOK-memory-alerts.md#psi-memory"))
		case in.PSIsSomeAvg10 >= PSIAmberPct:
			alerts = append(alerts, alert(SeverityAmber, "psi-memory",
				"PSI memory some avg10",
				"≥ "+pct(PSIAmberPct),
				pct(in.PSIsSomeAvg10),
				"memory reclaim is stalling tasks — see docs/PLAYBOOK-memory-alerts.md#psi-memory"))
		}
	}

	if in.TotalBytes > 0 {
		availPct := float64(in.AvailableBytes) / float64(in.TotalBytes) * 100
		if availPct < AvailableAmberPct {
			alerts = append(alerts, alert(SeverityAmber, "available-ram",
				"system available RAM",
				"< "+pct(AvailableAmberPct),
				pct(availPct)+" ("+formatBytes(in.AvailableBytes)+" of "+formatBytes(in.TotalBytes)+")",
				"free memory before the reclaim wall — see docs/PLAYBOOK-memory-alerts.md#available-ram"))
		}
	}

	// One loop over the instances, NOT nested inside the TotalBytes guard:
	// subtree-rss is a ratio and stays guarded by its denominator, but
	// child-count is an absolute count — the leak fingerprint the incident
	// grew to 230 — and a failed VirtualMemory read (TotalBytes==0) must
	// not silence it (review G1). Order per instance is unchanged
	// (subtree-rss before child-count), so the reds-first/insertion-order
	// contract holds either way.
	for _, inst := range in.Instances {
		if inst.SubtreeRSS > 0 && in.TotalBytes > 0 {
			rssPct := float64(inst.SubtreeRSS) / float64(in.TotalBytes) * 100
			if rssPct > SubtreeRSSAmberPct {
				alerts = append(alerts, alert(SeverityAmber, "subtree-rss:"+inst.ID,
					"instance subtree RSS · "+inst.Name,
					"> "+pct(SubtreeRSSAmberPct),
					pct(rssPct)+" ("+formatBytes(inst.SubtreeRSS)+")",
					"inspect this instance's children in the table below, then stop it if the growth is not yours to keep: docs/PLAYBOOK-memory-alerts.md#subtree-rss"))
			}
		}
		if inst.ChildCount > ChildCountAmber {
			alerts = append(alerts, alert(SeverityAmber, "child-count:"+inst.ID,
				"instance child processes · "+inst.Name,
				"> "+strconv.Itoa(ChildCountAmber),
				strconv.Itoa(inst.ChildCount),
				"a growing descendant count is a leak fingerprint — inspect and stop the leaker yourself: docs/PLAYBOOK-memory-alerts.md#child-count"))
		}
	}

	if in.DaemonRSSBytes > DaemonRSSAmberBytes {
		alerts = append(alerts, alert(SeverityAmber, "daemon-rss",
			"mw daemon RSS",
			"> 1 GiB",
			formatBytes(in.DaemonRSSBytes),
			"the daemon itself is over its healthy band — see docs/PLAYBOOK-memory-alerts.md#daemon-rss"))
	}

	sortAlerts(alerts)
	return alerts
}

func alert(sev, id, signal, threshold, current, advice string) Alert {
	return Alert{ID: id, Severity: sev, Signal: signal, Threshold: threshold, Current: current, Advice: advice}
}

// sortAlerts puts red before amber, preserving insertion order inside each
// group. Manual stable partition: n is tiny (≤ dozens) and the group count is
// fixed, so the explicit loop is the whole algorithm — a sort import and
// comparator would say the same thing with less prose.
//
// ARB-F13: an UNKNOWN severity ranks LAST. The old shape ranked via a map
// whose zero-value (0) meant "red", so any severity not literally "red" or
// "amber" would sort first as if it were red — "reds first" misfiring on a
// future or typo'd severity. Explicit ranks (red 0 / amber 1 / else 2) keep
// the order honest; only red and amber are emitted today.
func sortAlerts(alerts []Alert) {
	rank := func(sev string) int {
		switch sev {
		case SeverityRed:
			return 0
		case SeverityAmber:
			return 1
		default:
			return 2
		}
	}
	out := make([]Alert, 0, len(alerts))
	for _, want := range []int{0, 1, 2} {
		for _, a := range alerts {
			if rank(a.Severity) == want {
				out = append(out, a)
			}
		}
	}
	copy(alerts, out)
}

// formatBytes renders a byte count with binary units, one decimal below
// 1 GiB. Unit spelling (KB/MB/GB) AND rounding mirror the UI's formatBytes
// EXACTLY — the alert strings and the table cells sit side by side in the
// modal and must not disagree on what the same number is called.
//
// The rounding is HALF-UP, like the UI's toFixed. strconv.FormatFloat is
// ties-to-even, so the un-rounded shape disagreed with the page on every
// exact .x5 / .xx5 value (1.25 MiB → Go "1.2 MB" vs UI "1.3 MB"; 3.125 GiB
// → Go "3.12 GB" vs UI "3.13 GB"). Round first, format second: the number
// is then computed the same way on both sides of the seam, so an alert row
// and the modal cell beside it can never print two different values for
// the same bytes (F-M1; UI targets measured, pinned in alerts_test.go).
func formatBytes(b uint64) string {
	kib := float64(1024)
	if b < uint64(kib) {
		return strconv.FormatUint(b, 10) + " B"
	}
	if b < uint64(kib*kib) {
		return strconv.FormatFloat(math.Round(float64(b)/kib*10)/10, 'f', 1, 64) + " KB"
	}
	if b < uint64(kib*kib*kib) {
		return strconv.FormatFloat(math.Round(float64(b)/(kib*kib)*10)/10, 'f', 1, 64) + " MB"
	}
	return strconv.FormatFloat(math.Round(float64(b)/(kib*kib*kib)*100)/100, 'f', 2, 64) + " GB"
}

// pct formats a percentage with one decimal, trimming "-0".
func pct(v float64) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	if s == "-0.0" {
		s = "0.0"
	}
	return s + "%"
}

// readPSIMemory reads /proc/pressure/memory. The path is fixed because the
// signal is Linux-specific by definition (oomd/PSI); on other platforms the
// read fails and HasPSI=false, which skips the signal honestly.
var readPSIMemory = func() (string, error) {
	b, err := os.ReadFile("/proc/pressure/memory")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// parsePSIsomeAvg10 extracts the "some avg10=" value from the raw
// /proc/pressure/memory content.
//
// Format (kernel Documentation/accounting/psi.rst): one line per stall
// ("some", "full"), each carrying "avg10=NN.NN avg60=… avg300=… total=…".
// We take SOME avg10 — "at least one task was stalled by memory some of the
// last 10s", the same family of number systemd-oomd consumes.
//
// Pure and injectable-tested: pass content as a string, no filesystem.
func parsePSIsomeAvg10(content string) (float64, bool) {
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		// Whole-token match: "some" the stall kind, not "someone" — the
		// kernel emits exactly two lines ("some", "full"), but a parser
		// that prefix-matches would happily read a stranger's numbers.
		if len(fields) == 0 || fields[0] != "some" {
			continue
		}
		for _, f := range fields[1:] {
			v, ok := strings.CutPrefix(f, "avg10=")
			if !ok {
				continue
			}
			fv, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return 0, false
			}
			return fv, true
		}
		// A "some" line without avg10 is malformed input — treat as absent.
		return 0, false
	}
	return 0, false
}
