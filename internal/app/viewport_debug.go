//go:build viewportdebug

package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The viewport diagnostics channel is deliberately narrow and ephemeral. It
// exists so a remote iPad can upload geometry without Safari Web Inspector;
// it is not a general-purpose log or terminal-content transport.
const (
	viewportDebugBodyLimit   = 64 * 1024
	viewportDebugSessionTTL  = 30 * time.Minute
	viewportDebugMaxSessions = 8
	// 600, per the product owner's floor of 500+ (given in the 2026-11-05
	// scheduling session, recorded off-repo): one A0 SOP round measures 300+
	// frames, and the real max a single session holds is 709 distinct events
	// (evidence/raw/p7-854069baf9dee9f6b85788e6.jsonl, seq 1..758 — the 975 of
	// evidence/raw/b90643be….jsonl is its line count over overlapping pulls, not
	// a distinct-event count), so a 120-frame window truncated the early
	// sequence of every long SOP run. A 600 window covers long sessions
	// (~5-10 min at 1 fps) but sessions longer than 600 are still truncated
	// (observed max 709). The window stays a RING (newest kept, oldest dropped)
	// and stays bounded: each event is one ≤64KiB report, measured at ~6.5KB
	// avg per frame on the current p9 probe (evidence/raw/b7460ddb….jsonl:
	// 6542B avg, 7683B peak).
	viewportDebugMaxEventsPerSes = 600
	// issue #116 contract v3.0: every PTY host is listed in a snapshot because a
	// stale bottom on a hidden host is the band the next switch produces. One
	// host per session, and sessions are bounded by the instance cap, so 64 is
	// generous — the limit exists to bound the body, not to hide a leak.
	viewportDebugMaxPtyHosts = 64
	// The unlock journal is a ring: enough frames to order a keyboard opening
	// against the geometry that followed it, small enough that a 1s capture never
	// crowds the 64KiB body limit.
	viewportDebugMaxTraceEntries = 16
)

var (
	viewportDebugSessionPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)
	viewportDebugKindPattern    = regexp.MustCompile(`^[a-z0-9_-]{0,32}$`)
	viewportDebugTagPattern     = regexp.MustCompile(`^[A-Za-z0-9]{0,32}$`)
	// The contract's own enums (PHASE1-CONTRACT B章 / frozen §5 / R6). A client
	// that invents a fifth ownership category or a fourth placement verdict is a
	// schema drift, caught at the door rather than averaged into a report.
	viewportDebugOwnershipPattern    = regexp.MustCompile(`^(HO-1|HO-2|HO-3|HO-4|unknown)$`)
	viewportDebugRolePattern         = regexp.MustCompile(`^[a-z][a-z-]{0,63}$`)
	viewportDebugSessionStatePattern = regexp.MustCompile(`^(active|recovering|geometryGrace|none|unknown)$`)
	viewportDebugPanelCornerPattern  = regexp.MustCompile(`^(tl|tr|bl|br|free|unknown|unavailable)$`)
	viewportDebugPanelSupportPattern = regexp.MustCompile(`^(supported|unsupported|unavailable)$`)
	// N-4: the entry types the client's eventTrace.note() actually journals
	// (click / touchstart / focusin / focusout), plus every name it may still COUNT
	// — so the whitelist is the client's vocabulary, not a guess at it. A type
	// outside this set means the channel is being used for something other than the
	// §5.5 unlock journal, and the door is where that gets caught.
	viewportDebugTraceTypePattern = regexp.MustCompile(`^(click|touchstart|focusin|focusout|resize|scroll|window-resize|orientationchange|pageshow|visibilitychange)$`)
	// m-5: the additive schema revisions this door accepts, spelled out. `schemaRev`
	// is optional so a pre-additive capture still uploads; when present it must be
	// a revision the server has declarations for, because a client that invents one
	// has added fields this struct would silently drop.
	viewportDebugSchemaRevPattern = regexp.MustCompile(`^p9-additive-ownership-panel-v1$`)
	viewportDebugReasons          = map[string]bool{
		"init": true, "resize": true, "scroll": true, "focusin": true,
		"focusout": true, "orientationchange": true, "window-resize": true,
		"visibilitychange": true, "pageshow": true, "settle": true,
		"manual": true, "magic-hint": true, "keyboard-hint": true, "periodic": true,
	}
)

type viewportDebugRect struct {
	Top    float64 `json:"top"`
	Bottom float64 `json:"bottom"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type viewportDebugStyledRect struct {
	viewportDebugRect
	Background string `json:"background"`
}

type viewportDebugVV struct {
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
	OffsetTop  float64 `json:"offsetTop"`
	OffsetLeft float64 `json:"offsetLeft"`
	Scale      float64 `json:"scale"`
}

type viewportDebugApp struct {
	viewportDebugRect
	ComputedHeight string `json:"computedHeight"`
	// issue #116 contract v2.2: the shell's computed position is part of the
	// structural claim ("fixed", paired with two clipped root boxes). Adding a
	// client field without its server twin makes every upload 400 — see the
	// pageTop incident; the schema test's payload is the guard.
	ComputedPosition string `json:"computedPosition,omitempty"`
	InlineHeight     string `json:"inlineHeight"`
	// issue #116 contract v3.0: the shell is FULL HEIGHT in every state, so the
	// number that describes it is clientHeight − app.bottom (~0 always), NOT
	// vv.height − app.bottom. v2.2 pinned the shell to vv.height and let that
	// gap go negative — the "crossing" reading is retired, because a shell that
	// extends under the keyboard is exactly what v3.0 wants.
	LayoutGap     float64 `json:"layoutGap"`
	PaddingTop    string  `json:"paddingTop"`
	PaddingRight  string  `json:"paddingRight"`
	PaddingBottom string  `json:"paddingBottom"`
	PaddingLeft   string  `json:"paddingLeft"`
	Background    string  `json:"background"`
}

type viewportDebugUI struct {
	Collapsed        bool   `json:"collapsed"`
	QuickKeysOpen    bool   `json:"quickKeysOpen"`
	Kind             string `json:"kind"`
	Status           string `json:"status"`
	ActiveElementTag string `json:"activeElementTag"`
}

type viewportDebugDevice struct {
	UserAgent    string  `json:"userAgent"`
	Platform     string  `json:"platform"`
	TouchPoints  float64 `json:"touchPoints"`
	Orientation  string  `json:"orientation"`
	ScreenWidth  float64 `json:"screenWidth"`
	ScreenHeight float64 `json:"screenHeight"`
	Coarse       bool    `json:"coarse"`
}

type viewportDebugDocument struct {
	BodyBackground string `json:"bodyBackground"`
	HTMLBackground string `json:"htmlBackground"`
	// issue #116: scroll/pan discriminators. These exist so a report can say
	// whether the page SCROLLED or whether iOS panned the VISUAL viewport — the
	// two look identical on screen but have different fixes. Pointers, not
	// float64, because the client sends null when the API is unavailable
	// (elementFromPoint/visualViewport-less environments) and "absent" must stay
	// distinguishable from "zero".
	PageTop                      *float64 `json:"pageTop"`
	ScrollTop                    *float64 `json:"scrollTop"`
	ScrollingElementScrollTop    *float64 `json:"scrollingElementScrollTop"`
	ScrollingElementScrollHeight *float64 `json:"scrollingElementScrollHeight"`
	BodyScrollTop                *float64 `json:"bodyScrollTop"`
	// issue #116 contract v2.2: the structural half of the claim, reported by
	// the engine instead of inferred from the stylesheet. HTMLOverflow /
	// BodyOverflow are the computed `overflow` of the two root boxes ("clip"
	// under this contract, "hidden" on an engine that never learned it), and
	// RootScrollHeight against the snapshot's clientHeight is the scroll RANGE:
	// no range means nothing to scroll. ScrollTop stays a POINTER to evidence,
	// never an input — this build never writes it.
	HTMLOverflow     string   `json:"htmlOverflow,omitempty"`
	BodyOverflow     string   `json:"bodyOverflow,omitempty"`
	RootScrollHeight *float64 `json:"rootScrollHeight"`
}

// viewportDebugProbe answers "what is painted at the bottom edge" as data rather
// than inference: the hit-tested element at the last visible pixel of the VISUAL
// viewport and of the LAYOUT viewport. They disagree exactly when the visual
// viewport is panned, and a BODY/HTML hit means the root canvas is exposed
// (a real band) whereas the terminal host means the pixels are the app's own ink.
type viewportDebugProbe struct {
	VisualBottom *viewportDebugProbeHit `json:"visualBottom,omitempty"`
	LayoutBottom *viewportDebugProbeHit `json:"layoutBottom,omitempty"`
	// PageTop accompanies the two hits because elementFromPoint is a CLIENT-space
	// API: without it a reader cannot tell whether a hit was taken in client or
	// layout coordinates, and an earlier probe mixed the two (50 of 120 frames
	// reported a phantom "BODY at the bottom" purely from that mistake).
	PageTop *float64 `json:"pageTop,omitempty"`
}

type viewportDebugProbeHit struct {
	Y          float64 `json:"y"`
	Tag        string  `json:"tag"`
	ID         string  `json:"id"`
	ClassName  string  `json:"className"`
	Background string  `json:"background"`
}

// viewportDebugPtyHost is one PTY host inside #terminal-container, reported by
// the p7 probe. Contract v3.0 moves NOTHING but these boxes: the shell, the
// sidebar, the header and #workspace keep their full height in every keyboard
// state and each host lifts its OWN bottom by the occlusion. So every host is
// listed — hidden and inactive ones included, because a stale inline bottom on a
// host the user switches TO is the band the next switch produces — and each
// carries the rect it resolved to (client space), the value the applier wrote
// (inlineBottom) and whether it is on screen right now (visible).
type viewportDebugPtyHost struct {
	Index          int     `json:"index"`
	Top            float64 `json:"top"`
	Bottom         float64 `json:"bottom"`
	Height         float64 `json:"height"`
	InlineBottom   string  `json:"inlineBottom,omitempty"`
	ComputedBottom string  `json:"computedBottom,omitempty"`
	Visible        bool    `json:"visible"`
}

// viewportDebugOverlay is one of the two absolutely positioned overlay children
// of #workspace that the p7 probe reports: #terminal-controls (the quick-keys
// entry / panel column) and #status-bar (the transport strip). #workspace keeps
// its full height in every keyboard state, so these two are covered by the
// keyboard unless THEY move too — and the requirement is that quick-keys stays
// reachable, so they are written by the same avoidance pass as the hosts, each
// from its OWN authored bottom (20px for the column, 0 for the strip). Reported
// with the same three numbers a host is reported with: the rect it resolved to,
// the value the applier wrote, and whether it is on screen. Missing element
// means the client sent null, and a null is "not observed", never a pass.
type viewportDebugOverlay struct {
	viewportDebugRect
	InlineBottom   string `json:"inlineBottom,omitempty"`
	ComputedBottom string `json:"computedBottom,omitempty"`
	Visible        bool   `json:"visible"`
}

// viewportDebugQuickKeys is the quick-keys panel. maxHeight is the COMPUTED
// value, so the capture states what the percentage resolved to instead of what
// the stylesheet hoped for: the percentage resolves against #terminal-controls'
// height, so when the column lifts by 20 + occlusion this is the number that has
// to shrink, and scrollHeight against clientHeight is the internal scroll that
// keeps the rest of the key set reachable inside the shortened box.
type viewportDebugQuickKeys struct {
	viewportDebugRect
	MaxHeight    string  `json:"maxHeight,omitempty"`
	ClientHeight float64 `json:"clientHeight"`
	ScrollHeight float64 `json:"scrollHeight"`
	Open         bool    `json:"open"`
}

// ── issue #116 Phase 2 task 1/2: the ADDITIVE field groups ────────────────────
// PHASE1-CONTRACT「验收前置条件（工具依赖，不得绕过）」 makes the ownership
// category, the focused host's identity + visible client rect and the keyboard
// session marker pre-conditions for whole classes of verdicts, and D1 names the
// third: the sub-frame order of events against the geometry change. The decoder
// uses DisallowUnknownFields, so every one of those client fields needs a twin
// here — a twin missing is not a dropped field, it is every upload answered 400
// and a panel that only ever says "upload failed; retrying" (the pageTop incident).
//
// Nothing here can carry content: identity fields are TAGS and BOOLEAN role
// markers, rects are numbers, and the trace is explicitly `advisoryOnly`.

// viewportDebugClientRect is a rect in CLIENT space with the two horizontal edges
// too: reachability is "does this box fall inside [0, vv.height] / [0, vv.width]",
// which top/bottom/width/height alone cannot answer.
type viewportDebugClientRect struct {
	viewportDebugRect
	Left  float64 `json:"left"`
	Right float64 `json:"right"`
}

// viewportDebugOwnership is the contract's B章 answer: the category, the branch
// that decided it, and — when nothing could be decided — the reason. `unknown` is
// a legal category and an empty unavailableReason is the only proof it was decided.
type viewportDebugOwnership struct {
	Category          string `json:"category"`
	Basis             string `json:"basis"`
	UnavailableReason string `json:"unavailableReason"`
}

type viewportDebugActiveInput struct {
	Observed                bool   `json:"observed"`
	Tag                     string `json:"tag"`
	Editable                bool   `json:"editable"`
	Role                    string `json:"role"`
	XtermMarker             bool   `json:"xtermMarker"`
	HelperMarker            bool   `json:"helperMarker"`
	InsideTerminalContainer bool   `json:"insideTerminalContainer"`
	// POINTERS on the three-state reads: "not determinable" is a different claim
	// from "determinable and false", and the verifier answers them differently.
	InsideTerminalHost    *bool                    `json:"insideTerminalHost"`
	InsideQuickKeysPanel  bool                     `json:"insideQuickKeysPanel"`
	HostIsForeground      *bool                    `json:"hostIsForeground"`
	HostVisible           *bool                    `json:"hostVisible"`
	AncestryReachedRoot   bool                     `json:"ancestryReachedRoot"`
	Rect                  *viewportDebugClientRect `json:"rect,omitempty"`
	RectUnavailableReason string                   `json:"rectUnavailableReason"`
	InsideVisibleViewport *bool                    `json:"insideVisibleViewport"`
	UnavailableReason     string                   `json:"unavailableReason"`
}

// viewportDebugKeyboardSession is the applier's mirrored occlusion plus the
// probe's DERIVED label over it. `rawShortfall` is a MEASUREMENT (clientHeight −
// vv.height) and never an input: the occlusion the shell owes stays in
// `snapshot.occlusion`, read off window.__mwTerminalOcclusion.
//
// B-3: the label is named `derivedState` and carries `derived:true`, because it is
// NOT an acceptance state. The applier keeps `terminalAvoidanceActive` /
// `...Recovering` / `...GeometryGrace` module-local, so every word here is an
// inference across the frames the probe happened to sample. The AUTHORITY for a
// session stays the frozen judge (evidence/verify-sop.mjs makeKeyboardSessionJudge,
// which replays §5 from measurements) next to this block's measurement fields
// (`occlusion` on the snapshot, `mirrorOcclusion`, `rawShortfall`). A label that
// cannot be derived is `unknown` PLUS a non-empty `unavailableReason`, and the two
// numbers the inference was made across (`previousMirrorOcclusion`,
// `unlockTick`/`previousUnlockTick`) travel with it so a reader can re-check the
// inference instead of trusting the word.
type viewportDebugKeyboardSession struct {
	Derived                      bool     `json:"derived"`
	DerivedState                 string   `json:"derivedState"`
	Basis                        string   `json:"basis"`
	Evidence                     string   `json:"evidence"`
	UnavailableReason            string   `json:"unavailableReason"`
	MirrorExposed                bool     `json:"mirrorExposed"`
	MirrorOcclusion              float64  `json:"mirrorOcclusion"`
	RawShortfall                 float64  `json:"rawShortfall"`
	LayoutHeight                 float64  `json:"layoutHeight"`
	VVHeight                     float64  `json:"vvHeight"`
	VVWidth                      float64  `json:"vvWidth"`
	VVOffsetTop                  float64  `json:"vvOffsetTop"`
	Scale                        float64  `json:"scale"`
	Zoomed                       bool     `json:"zoomed"`
	VVObserved                   bool     `json:"vvObserved"`
	FocusEditable                bool     `json:"focusEditable"`
	GeometryChangedSincePrevious *bool    `json:"geometryChangedSincePrevious,omitempty"`
	PreviousMirrorOcclusion      *float64 `json:"previousMirrorOcclusion,omitempty"`
	// The tick span the derived label was inferred across (B-3). An unlock landing
	// inside it means `recovering` was NOT derivable — the frame says so in
	// `unavailableReason`, and these two numbers are the proof it says so on.
	UnlockTick         *int `json:"unlockTick,omitempty"`
	PreviousUnlockTick *int `json:"previousUnlockTick,omitempty"`
}

// viewportDebugTraceCounts is the COUNTER half of the journal. M-6 / N-1 are why
// the set is split the way it is: `vvResize` counts ONLY the visualViewport's
// `resize` (the keyboard-shaped signal) and `windowResize` counts the window's, so
// one can never be read as the other; both keep counting after the entry ring rolls
// (or, for the geometry events, instead of entering it).
type viewportDebugTraceCounts struct {
	Click               int `json:"click"`
	ClickOnTerminalHost int `json:"clickOnTerminalHost"`
	Touchstart          int `json:"touchstart"`
	Focusin             int `json:"focusin"`
	Focusout            int `json:"focusout"`
	VVResize            int `json:"vvResize"`
	VVScroll            int `json:"vvScroll"`
	WindowResize        int `json:"windowResize"`
	Other               int `json:"other"`
}

// viewportDebugUnlockTrace is §5.5 evidence, RECORD ONLY: `click` and `touchstart`
// are kept apart because only click unlocks the recovery latch, and the frozen
// verifier could never see a click frame at all (GI-2 seq 373–377). Each entry
// carries the geometry visible at the instant the handler ran, which is what makes
// the sub-frame order (D1 ③) derivable next to the frame's captureTick.
// viewportDebugUnlockTrace is §5.5 evidence, RECORD ONLY: `click` and `touchstart`
// are kept apart because only click unlocks the recovery latch, and the frozen
// verifier could never see a click frame at all (GI-2 seq 373–377). Each entry
// carries the geometry visible at the instant the handler ran, which is what makes
// the sub-frame order (D1 ③) derivable next to the frame's captureTick.
//
// M-6: `entries` is the DECISION ring — click / touchstart / focusin / focusout —
// and `geometryCountedOnly:true` states on the frame that the high-frequency
// geometry events (`resize` / `scroll` / `window-resize`) live in `counts` and never
// enter the ring. A continuous pan emits dozens of geometry events per second; with
// one shared ring that flood evicted the single click §5.5 asks for, and the
// journal reported its own blindness as "no click happened".
type viewportDebugUnlockTrace struct {
	AdvisoryOnly        bool                      `json:"advisoryOnly"`
	CaptureTick         int                       `json:"captureTick"`
	GeometryCountedOnly bool                      `json:"geometryCountedOnly"`
	Counts              *viewportDebugTraceCounts `json:"counts"`
	Entries             []viewportDebugTraceEntry `json:"entries"`
	UnavailableReason   string                    `json:"unavailableReason,omitempty"`
}

type viewportDebugTraceEntry struct {
	Tick           int      `json:"tick"`
	Type           string   `json:"type"`
	Tag            string   `json:"tag"`
	Editable       bool     `json:"editable"`
	OnTerminalHost *bool    `json:"onTerminalHost"`
	XtermMarker    bool     `json:"xtermMarker"`
	VVHeight       *float64 `json:"vvHeight"`
	VVOffsetTop    *float64 `json:"vvOffsetTop"`
	// N-3: the horizontal and layout sides of the same instant, so §5.7's
	// `layoutHeight` / `vvWidth` geometry-change comparison closes INSIDE the
	// journal — a reader no longer needs a second frame next to it to check the
	// claim that geometry changed when the handler ran.
	VVWidth         *float64 `json:"vvWidth"`
	LayoutHeight    *float64 `json:"layoutHeight"`
	Scale           *float64 `json:"scale"`
	MirrorOcclusion *float64 `json:"mirrorOcclusion"`
}

// viewportDebugPanel is R6 / GI-9: the instrument reporting its own box. A panel
// the user cannot reach makes every verdict built on it INCONCLUSIVE, so the
// capture states the corner it authored, whether that corner is supported in this
// keyboard/pan state, and how much of it the keyboard covers.
type viewportDebugPanel struct {
	Exists                bool                     `json:"exists"`
	Corner                string                   `json:"corner"`
	Support               string                   `json:"support"`
	UnsupportedReason     string                   `json:"unsupportedReason"`
	Lifted                *bool                    `json:"lifted"`
	PanCorrected          *bool                    `json:"panCorrected"`
	NominalHeight         *float64                 `json:"nominalHeight"`
	Rect                  *viewportDebugClientRect `json:"rect,omitempty"`
	RectUnavailableReason string                   `json:"rectUnavailableReason"`
	KeyboardOverlap       *float64                 `json:"keyboardOverlap"`
	InsideVisibleViewport *bool                    `json:"insideVisibleViewport"`
}

type viewportDebugSnapshot struct {
	// issue #116: which build of the instrument produced this snapshot. Two
	// verification rounds were spent on reports that turned out to describe a
	// page still running the previous build; a capture now identifies itself.
	ProbeVersion string                   `json:"probeVersion,omitempty"`
	Probe        *viewportDebugProbe      `json:"probe,omitempty"`
	CapturedAt   string                   `json:"capturedAt"`
	Reason       string                   `json:"reason"`
	InnerWidth   float64                  `json:"innerWidth"`
	InnerHeight  float64                  `json:"innerHeight"`
	ClientWidth  float64                  `json:"clientWidth"`
	ClientHeight float64                  `json:"clientHeight"`
	DPR          float64                  `json:"dpr"`
	Device       viewportDebugDevice      `json:"device"`
	Document     viewportDebugDocument    `json:"document"`
	VV           *viewportDebugVV         `json:"vv"`
	App          viewportDebugApp         `json:"app"`
	Main         viewportDebugRect        `json:"main"`
	Workspace    viewportDebugStyledRect  `json:"workspace"`
	Terminal     *viewportDebugStyledRect `json:"terminal,omitempty"`
	// issue #116 contract v3.0: the occlusion the shell owes the boxes it moves,
	// and those boxes' own geometry. The hosts plus the two overlay children of
	// #workspace are exactly the set v3.0 moves, so they are the numbers a verdict
	// is taken from now that the shell no longer resizes; `ptyHosts` is capped by
	// viewportDebugMaxPtyHosts.
	// NO `omitempty` ON `ptyHosts`: THE EMPTY LIST IS A MEASUREMENT, NOT AN ABSENCE.
	// The probe sends `ptyHosts` in EVERY frame — `ptyHostsOf` answers `[]` when
	// `#terminal-container` has no host yet (the `init` frame of a fresh session,
	// and every web-kind page) — and it is the DOWNLOADED capture the SOP is read
	// from. With `omitempty` Go dropped that observed empty list from the response,
	// so the frame came back with the field ABSENT, and the verifier's
	// `!Array.isArray(s.ptyHosts)` read it as "an older instrument that cannot speak
	// about the local avoidance": real evidence of a hostless page was thrown away
	// as evidence of an outdated build. A nil list still encodes `null`, which is
	// the honest encoding of a frame that never carried the field at all, so
	// "not observed" stays distinguishable from "observed, zero hosts" — the
	// verifier fails or answers INCONCLUSIVE on both, never PASS. Every other
	// nullable field keeps its pointer/`omitempty` semantics untouched.
	Occlusion   float64                  `json:"occlusion"`
	PtyHosts    []viewportDebugPtyHost   `json:"ptyHosts"`
	Controls    *viewportDebugOverlay    `json:"controls,omitempty"`
	StatusBar   *viewportDebugOverlay    `json:"statusBar,omitempty"`
	QuickKeys   *viewportDebugQuickKeys  `json:"quickKeys,omitempty"`
	WebPanel    *viewportDebugStyledRect `json:"webPanel,omitempty"`
	XtermScreen *viewportDebugRect       `json:"xtermScreen,omitempty"`
	XtermRows   *viewportDebugRect       `json:"xtermRows,omitempty"`
	UI          viewportDebugUI          `json:"ui"`
	// issue #116 Phase 2 task 1 / task 2, ADDITIVE: the p9 fields above are
	// untouched, these are the ones PHASE1-CONTRACT lists as pre-conditions
	// (ownership category, focused host + its client rect in the same frame, the
	// keyboard session marker, the §5.5 unlock evidence) plus the instrument's own
	// box (R6 / GI-9). `schemaRev` marks the additive field set without bumping
	// the frozen PROBE_VERSION, which the verifier's stamp check depends on.
	SchemaRev string `json:"schemaRev,omitempty"`
	// m-2: the degraded frame. When the probe's own read of the live page throws,
	// the frame still goes out marked — every measurement field absent (null), the
	// marker stating it — instead of an uncaught exception surfacing on the iPad
	// that is being measured. Absence stays absence; nothing here is zero-filled
	// into a number a verifier could measure.
	ProbeReadFailed       bool                          `json:"probeReadFailed,omitempty"`
	ProbeReadFailedReason string                        `json:"probeReadFailedReason,omitempty"`
	Ownership             *viewportDebugOwnership       `json:"ownership,omitempty"`
	ActiveInput           *viewportDebugActiveInput     `json:"activeInput,omitempty"`
	KeyboardSession       *viewportDebugKeyboardSession `json:"keyboardSession,omitempty"`
	UnlockTrace           *viewportDebugUnlockTrace     `json:"unlockTrace,omitempty"`
	Panel                 *viewportDebugPanel           `json:"panel,omitempty"`
}

type viewportDebugReport struct {
	Session  string                `json:"session"`
	Seq      int                   `json:"seq"`
	Snapshot viewportDebugSnapshot `json:"snapshot"`
}

type viewportDebugEvent struct {
	Seq        int                   `json:"seq"`
	ReceivedAt time.Time             `json:"receivedAt"`
	Snapshot   viewportDebugSnapshot `json:"snapshot"`
}

type viewportDebugSession struct {
	UpdatedAt time.Time
	Events    []viewportDebugEvent
}

type viewportDebugStore struct {
	mu       sync.Mutex
	now      func() time.Time
	sessions map[string]*viewportDebugSession
}

func newViewportDebugStore() *viewportDebugStore {
	return &viewportDebugStore{now: time.Now, sessions: map[string]*viewportDebugSession{}}
}

func (s *Server) handleViewportDebugReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	s.viewportDebugOnce.Do(func() {
		if s.viewportDebug == nil {
			// Tests and embedders may construct Server directly instead of New().
			s.viewportDebug = newViewportDebugStore()
		}
	})
	switch r.Method {
	case http.MethodPost:
		s.handleViewportDebugUpload(w, r)
	case http.MethodGet:
		s.handleViewportDebugDownload(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeErr(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
	}
}

func (s *Server) handleViewportDebugUpload(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeErr(w, http.StatusUnsupportedMediaType, errors.New("application/json is required"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, viewportDebugBodyLimit+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if len(body) > viewportDebugBodyLimit {
		writeErr(w, http.StatusRequestEntityTooLarge, errors.New("diagnostic report exceeds 64KiB"))
		return
	}
	var report viewportDebugReport
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&report); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid diagnostic report: %w", err))
		return
	}
	if dec.More() {
		writeErr(w, http.StatusBadRequest, errors.New("exactly one diagnostic report is required"))
		return
	}
	if err := validateViewportDebugReport(report); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	accepted, latest := s.viewportDebug.put(report)
	writeJSON(w, http.StatusOK, map[string]any{"accepted": accepted, "latestSeq": latest})
}

func (s *Server) handleViewportDebugDownload(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	if !viewportDebugSessionPattern.MatchString(sessionID) {
		writeErr(w, http.StatusBadRequest, errors.New("invalid diagnostic session"))
		return
	}
	after := 0
	if raw := r.URL.Query().Get("after"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, errors.New("after must be a non-negative integer"))
			return
		}
		after = n
	}
	session, ok := s.viewportDebug.get(sessionID)
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("diagnostic session not found or expired"))
		return
	}
	events := make([]viewportDebugEvent, 0, len(session.Events))
	latest := 0
	for _, event := range session.Events {
		if event.Seq > latest {
			latest = event.Seq
		}
		if event.Seq > after {
			events = append(events, event)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session":   sessionID,
		"updatedAt": session.UpdatedAt,
		"latestSeq": latest,
		"events":    events,
	})
}

func (s *viewportDebugStore) put(report viewportDebugReport) (bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	session := s.sessions[report.Session]
	if session == nil {
		if len(s.sessions) >= viewportDebugMaxSessions {
			s.evictOldestLocked()
		}
		session = &viewportDebugSession{Events: make([]viewportDebugEvent, 0, 16)}
		s.sessions[report.Session] = session
	}
	latest := 0
	if len(session.Events) > 0 {
		latest = session.Events[len(session.Events)-1].Seq
	}
	// Retries are idempotent and an older retry cannot overwrite a newer
	// observation. Sequence gaps are allowed because offline captures may drop.
	if report.Seq <= latest {
		return false, latest
	}
	session.Events = append(session.Events, viewportDebugEvent{
		Seq: report.Seq, ReceivedAt: s.now(), Snapshot: report.Snapshot,
	})
	if len(session.Events) > viewportDebugMaxEventsPerSes {
		drop := len(session.Events) - viewportDebugMaxEventsPerSes
		session.Events = append([]viewportDebugEvent(nil), session.Events[drop:]...)
	}
	session.UpdatedAt = s.now()
	return true, report.Seq
}

func (s *viewportDebugStore) get(sessionID string) (viewportDebugSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	session := s.sessions[sessionID]
	if session == nil {
		return viewportDebugSession{}, false
	}
	// Return a copy so the HTTP encoder never walks the live mutable slice.
	events := append([]viewportDebugEvent(nil), session.Events...)
	return viewportDebugSession{UpdatedAt: session.UpdatedAt, Events: events}, true
}

func (s *viewportDebugStore) expireLocked() {
	now := s.now()
	for id, session := range s.sessions {
		if now.Sub(session.UpdatedAt) > viewportDebugSessionTTL {
			delete(s.sessions, id)
		}
	}
}

func (s *viewportDebugStore) evictOldestLocked() {
	var oldestID string
	var oldest time.Time
	for id, session := range s.sessions {
		if oldestID == "" || session.UpdatedAt.Before(oldest) {
			oldestID, oldest = id, session.UpdatedAt
		}
	}
	delete(s.sessions, oldestID)
}

func validateViewportDebugReport(report viewportDebugReport) error {
	if !viewportDebugSessionPattern.MatchString(report.Session) {
		return errors.New("session must be 16-64 ASCII letters, digits, '_' or '-'")
	}
	if report.Seq <= 0 {
		return errors.New("seq must be positive")
	}
	s := report.Snapshot
	if !viewportDebugReasons[s.Reason] {
		return errors.New("invalid capture reason")
	}
	if _, err := time.Parse(time.RFC3339Nano, s.CapturedAt); err != nil {
		return errors.New("capturedAt must be RFC3339Nano")
	}
	for _, n := range []float64{s.InnerWidth, s.InnerHeight, s.ClientWidth, s.ClientHeight, s.DPR} {
		if err := viewportDebugNumber(n); err != nil {
			return err
		}
	}
	if s.InnerWidth < 0 || s.InnerHeight < 0 || s.ClientWidth < 0 || s.ClientHeight < 0 || s.DPR <= 0 {
		return errors.New("invalid viewport or DPR dimensions")
	}
	if len(s.Device.UserAgent) > 256 || len(s.Device.Platform) > 64 || len(s.Device.Orientation) > 64 {
		return errors.New("device diagnostic value is too long")
	}
	for _, n := range []float64{s.Device.TouchPoints, s.Device.ScreenWidth, s.Device.ScreenHeight} {
		if err := viewportDebugNumber(n); err != nil {
			return err
		}
	}
	if s.Device.TouchPoints < 0 || s.Device.ScreenWidth < 0 || s.Device.ScreenHeight < 0 {
		return errors.New("invalid device touch/screen dimensions")
	}
	if s.VV != nil {
		for _, n := range []float64{s.VV.Width, s.VV.Height, s.VV.OffsetTop, s.VV.OffsetLeft, s.VV.Scale} {
			if err := viewportDebugNumber(n); err != nil {
				return err
			}
		}
		if s.VV.Width < 0 || s.VV.Height < 0 || s.VV.Scale <= 0 {
			return errors.New("invalid visual viewport dimensions or scale")
		}
	}
	if err := validateViewportDebugRect(s.App.viewportDebugRect); err != nil {
		return err
	}
	if err := validateViewportDebugRect(s.Main); err != nil {
		return err
	}
	if err := validateViewportDebugRect(s.Workspace.viewportDebugRect); err != nil {
		return err
	}
	for _, value := range []string{s.App.ComputedHeight, s.App.ComputedPosition, s.App.InlineHeight, s.App.PaddingTop, s.App.PaddingRight, s.App.PaddingBottom, s.App.PaddingLeft, s.App.Background, s.Workspace.Background, s.Document.BodyBackground, s.Document.HTMLBackground, s.Document.HTMLOverflow, s.Document.BodyOverflow} {
		if len(value) > 160 {
			return errors.New("CSS diagnostic value is too long")
		}
	}
	// issue #116 contract v3.0: the occlusion is the height v3.0 owes every PTY
	// host, and each host reports the value it was given. Both are bounded and
	// finite for the same reason every other diagnostic is: this channel carries
	// numbers a verdict is taken from, so a NaN or a 1e12 must be rejected at the
	// door instead of being averaged into a report.
	if err := viewportDebugNumber(s.Occlusion); err != nil {
		return err
	}
	if s.Occlusion < 0 {
		return errors.New("occlusion must be non-negative")
	}
	if len(s.PtyHosts) > viewportDebugMaxPtyHosts {
		return errors.New("too many PTY hosts in snapshot")
	}
	for i, host := range s.PtyHosts {
		if host.Index != i {
			return errors.New("PTY host index must be sequential")
		}
		if err := viewportDebugNumber(host.Top); err != nil {
			return err
		}
		if err := viewportDebugNumber(host.Bottom); err != nil {
			return err
		}
		if err := viewportDebugNumber(host.Height); err != nil {
			return err
		}
		if host.Height < 0 {
			return errors.New("PTY host height must be non-negative")
		}
		if len(host.InlineBottom) > 160 || len(host.ComputedBottom) > 160 {
			return errors.New("CSS diagnostic value is too long")
		}
	}
	for _, rect := range []*viewportDebugStyledRect{s.Terminal, s.WebPanel} {
		if rect != nil {
			if err := validateViewportDebugRect(rect.viewportDebugRect); err != nil {
				return err
			}
			if len(rect.Background) > 160 {
				return errors.New("CSS diagnostic value is too long")
			}
		}
	}
	// issue #116 contract v3.0, the overlay half: #terminal-controls and
	// #status-bar are moved by the same occlusion, and #quick-keys-panel's
	// computed max-height is the number that proves the column really got
	// shorter. All three are OPTIONAL pointers — a login gate has none of them,
	// and "absent" must stay distinguishable from "at zero", so a nil is accepted
	// here and answered INCONCLUSIVE by the verifier, never silently passed.
	for _, overlay := range []*viewportDebugOverlay{s.Controls, s.StatusBar} {
		if overlay == nil {
			continue
		}
		if err := validateViewportDebugRect(overlay.viewportDebugRect); err != nil {
			return err
		}
		if len(overlay.InlineBottom) > 160 || len(overlay.ComputedBottom) > 160 {
			return errors.New("CSS diagnostic value is too long")
		}
	}
	if qk := s.QuickKeys; qk != nil {
		if err := validateViewportDebugRect(qk.viewportDebugRect); err != nil {
			return err
		}
		if len(qk.MaxHeight) > 160 {
			return errors.New("CSS diagnostic value is too long")
		}
		if err := viewportDebugNumber(qk.ClientHeight); err != nil {
			return err
		}
		if err := viewportDebugNumber(qk.ScrollHeight); err != nil {
			return err
		}
		if qk.ClientHeight < 0 || qk.ScrollHeight < 0 {
			return errors.New("quick-keys heights must be non-negative")
		}
	}
	for _, rect := range []*viewportDebugRect{s.XtermScreen, s.XtermRows} {
		if rect != nil {
			if err := validateViewportDebugRect(*rect); err != nil {
				return err
			}
		}
	}
	if !viewportDebugKindPattern.MatchString(s.UI.Kind) || !viewportDebugKindPattern.MatchString(s.UI.Status) {
		return errors.New("invalid instance kind/status")
	}
	if !viewportDebugTagPattern.MatchString(s.UI.ActiveElementTag) {
		return errors.New("invalid active element tag")
	}
	if err := validateViewportDebugAdditive(s); err != nil {
		return err
	}
	return nil
}

// viewportDebugAdditive guards the Phase 2 field groups through the same door as
// everything else a verdict is read from: an enum outside its set, a NaN, a tag
// carrying markup or an unbounded journal is a broken capture, not a measurement.
// The enums are the contract's own words (PHASE1-CONTRACT B章 HO-1..HO-4, the
// frozen §5 session states, R6's supported/unsupported), so a client cannot quietly
// invent a fifth ownership category or a fourth placement verdict.
func validateViewportDebugAdditive(s viewportDebugSnapshot) error {
	if o := s.Ownership; o != nil {
		if !viewportDebugOwnershipPattern.MatchString(o.Category) {
			return errors.New("invalid ownership category")
		}
		if len(o.Basis) > 64 || len(o.UnavailableReason) > 64 {
			return errors.New("ownership diagnostic text is too long")
		}
		// "缺字段一律 INCONCLUSIVE": a decided category must not smuggle an
		// unavailable reason, and an undecided one must state which.
		if o.Category != "unknown" && o.UnavailableReason != "" {
			return errors.New("a decided ownership must not carry an unavailable reason")
		}
		if o.Category == "unknown" && o.UnavailableReason == "" {
			return errors.New("an unknown ownership must state why it is unobtainable")
		}
	}
	if a := s.ActiveInput; a != nil {
		if !viewportDebugTagPattern.MatchString(a.Tag) {
			return errors.New("invalid active input tag")
		}
		if !viewportDebugRolePattern.MatchString(a.Role) {
			return errors.New("invalid active input role")
		}
		if len(a.UnavailableReason) > 64 || len(a.RectUnavailableReason) > 64 {
			return errors.New("active input diagnostic text is too long")
		}
		if a.Rect != nil {
			if err := validateViewportDebugRect(a.Rect.viewportDebugRect); err != nil {
				return err
			}
			if err := viewportDebugNumber(a.Rect.Left); err != nil {
				return err
			}
			if err := viewportDebugNumber(a.Rect.Right); err != nil {
				return err
			}
		} else if a.RectUnavailableReason == "" {
			return errors.New("a missing client rect must state why it is unobtainable")
		}
	}
	if k := s.KeyboardSession; k != nil {
		// B-3: the label is DERIVED and the door enforces that it says so. A client
		// that drops `derived:true` is publishing a hint as a verdict, which is the
		// second source of truth the frozen §5 judge must not have to argue with.
		if !k.Derived {
			return errors.New("keyboardSession.derivedState is derived by contract and must say so")
		}
		if !viewportDebugSessionStatePattern.MatchString(k.DerivedState) {
			return errors.New("invalid derived keyboard session state")
		}
		// An asymmetry, on purpose (B-3): `unknown` must always explain itself, while
		// a label that WAS derivable may still carry the limit of what it covers —
		// `recovering` legitimately ships "recovery-latch-not-published-by-applier".
		if k.DerivedState == "unknown" && k.UnavailableReason == "" {
			return errors.New("an unknown derived state must state why it is undecidable")
		}
		if len(k.Basis) > 64 || len(k.Evidence) > 64 || len(k.UnavailableReason) > 64 {
			return errors.New("keyboard session diagnostic text is too long")
		}
		for _, n := range []float64{k.MirrorOcclusion, k.RawShortfall, k.LayoutHeight, k.VVHeight, k.VVWidth, k.VVOffsetTop, k.Scale} {
			if err := viewportDebugNumber(n); err != nil {
				return err
			}
		}
		if k.MirrorOcclusion < 0 || k.RawShortfall < 0 || k.LayoutHeight < 0 || k.VVHeight < 0 || k.VVWidth < 0 {
			return errors.New("keyboard session heights must be non-negative")
		}
		if k.Scale <= 0 {
			return errors.New("invalid keyboard session scale")
		}
		if k.PreviousMirrorOcclusion != nil {
			if err := viewportDebugNumber(*k.PreviousMirrorOcclusion); err != nil {
				return err
			}
		}
		if k.UnlockTick != nil && *k.UnlockTick < 0 {
			return errors.New("unlock tick must be non-negative")
		}
		if k.PreviousUnlockTick != nil && *k.PreviousUnlockTick < 0 {
			return errors.New("previous unlock tick must be non-negative")
		}
	}
	// m-2 / m-5: the two self-describing markers of the additive block. A frame
	// that says it could not read must carry the marker's fixed text and must not
	// smuggle a measurement next to it; a `schemaRev` outside the accepted set is
	// schema drift caught at the door, not averaged into a report.
	if s.ProbeReadFailed {
		if s.ProbeReadFailedReason != "probe-read-failed" {
			return errors.New("a failed probe read must carry the fixed probe-read-failed marker")
		}
	} else if s.ProbeReadFailedReason != "" {
		return errors.New("probe-read-failed marker without probeReadFailed")
	}
	if s.SchemaRev != "" && !viewportDebugSchemaRevPattern.MatchString(s.SchemaRev) {
		return errors.New("unknown diagnostic schemaRev")
	}
	if t := s.UnlockTrace; t != nil {
		if !t.AdvisoryOnly {
			return errors.New("the unlock trace is advisory by contract and must stay recorded-only")
		}
		if t.CaptureTick < 0 {
			return errors.New("captureTick must be non-negative")
		}
		if len(t.Entries) > viewportDebugMaxTraceEntries {
			return errors.New("too many unlock trace entries in snapshot")
		}
		// M-6: the ring is bounded, the counts are not, and `geometryCountedOnly` is
		// the frame's own word for "the geometry flood lives in counts, so no click
		// was ever evicted by it" — checked, not assumed.
		if !t.GeometryCountedOnly {
			return errors.New("the unlock trace rings decision events by contract; geometry events must be count-only")
		}
		if t.Counts != nil {
			for _, n := range []int{t.Counts.Click, t.Counts.ClickOnTerminalHost, t.Counts.Touchstart,
				t.Counts.Focusin, t.Counts.Focusout, t.Counts.VVResize, t.Counts.VVScroll,
				t.Counts.WindowResize, t.Counts.Other} {
				if n < 0 {
					return errors.New("unlock trace counts must be non-negative")
				}
			}
		}
		for _, entry := range t.Entries {
			if entry.Tick < 0 {
				return errors.New("unlock trace tick must be non-negative")
			}
			// N-4: `type` gets the same character-set discipline `tag` always had. The
			// enum is the set the client's note() actually journals; an entry typed
			// with anything else is either a client drift or a channel being used as a
			// general text transport, and both belong at the door, not in a report.
			if !viewportDebugTraceTypePattern.MatchString(entry.Type) {
				return errors.New("invalid unlock trace entry type")
			}
			if !viewportDebugTagPattern.MatchString(entry.Tag) {
				return errors.New("invalid unlock trace entry tag")
			}
			for _, n := range []*float64{entry.VVHeight, entry.VVOffsetTop, entry.VVWidth,
				entry.LayoutHeight, entry.Scale, entry.MirrorOcclusion} {
				if n == nil {
					continue
				}
				if err := viewportDebugNumber(*n); err != nil {
					return err
				}
			}
		}
	}
	if p := s.Panel; p != nil {
		if !viewportDebugPanelCornerPattern.MatchString(p.Corner) {
			return errors.New("invalid diagnostic panel corner")
		}
		if !viewportDebugPanelSupportPattern.MatchString(p.Support) {
			return errors.New("invalid diagnostic panel support")
		}
		if len(p.UnsupportedReason) > 64 || len(p.RectUnavailableReason) > 64 {
			return errors.New("diagnostic panel diagnostic text is too long")
		}
		// B-2: ONE vocabulary on both sides — supported / unsupported / unavailable —
		// and every corner that is NOT reachable states why, whichever word it uses.
		// A panel that exists but whose dataset was never written reports
		// `support:"unavailable"` + a reason; inventing a fourth word there (the
		// previous client's "unknown") was a 400 on EVERY upload, which is how a
		// diagnostic loses a whole capture round.
		if p.Support != "supported" && p.UnsupportedReason == "" {
			return errors.New("a corner that is not supported must state why (R6: never silently unreachable)")
		}
		if p.Support == "supported" && p.UnsupportedReason != "" {
			return errors.New("a supported corner must not carry an unsupported reason")
		}
		if p.Rect != nil {
			if err := validateViewportDebugRect(p.Rect.viewportDebugRect); err != nil {
				return err
			}
			if err := viewportDebugNumber(p.Rect.Left); err != nil {
				return err
			}
			if err := viewportDebugNumber(p.Rect.Right); err != nil {
				return err
			}
		} else if p.Exists && p.RectUnavailableReason == "" {
			return errors.New("a panel without a rect must state why it is unobtainable")
		}
		if p.KeyboardOverlap != nil {
			if err := viewportDebugNumber(*p.KeyboardOverlap); err != nil {
				return err
			}
			if *p.KeyboardOverlap < 0 {
				return errors.New("keyboard overlap must be non-negative")
			}
		}
		if p.NominalHeight != nil {
			if err := viewportDebugNumber(*p.NominalHeight); err != nil {
				return err
			}
			if *p.NominalHeight < 0 {
				return errors.New("panel nominal height must be non-negative")
			}
		}
	}
	return nil
}

func validateViewportDebugRect(rect viewportDebugRect) error {
	for _, n := range []float64{rect.Top, rect.Bottom, rect.Width, rect.Height} {
		if err := viewportDebugNumber(n); err != nil {
			return err
		}
	}
	if rect.Width < 0 || rect.Height < 0 {
		return errors.New("rect width/height must be non-negative")
	}
	return nil
}

func viewportDebugNumber(n float64) error {
	if math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > 10_000_000 {
		return errors.New("non-finite or unreasonable diagnostic number")
	}
	return nil
}
