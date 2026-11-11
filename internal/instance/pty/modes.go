package pty

import (
	"strconv"
	"strings"
	"sync"
)

// modeTracker is a deliberately small, write-only scanner for the DEC
// private modes a fresh xterm needs when it attaches to an already-running
// PTY. It is NOT a terminal emulator: it keeps no screen, no cursor and no
// scrollback. It only remembers the mode bits that decide where a wheel
// event goes and how the next bytes are interpreted.
//
// The tracker consumes the same redacted byte stream that goes into the
// ring buffer, so a preamble and the tail that follows it describe the
// same terminal history.
type modeTracker struct {
	mu sync.Mutex

	state parserState
	csi   []byte

	altScreen             bool
	altMode               int
	mouseProtocol         mouseProtocol
	mouseEncoding         mouseEncoding
	focusReporting        bool
	bracketedPaste        bool
	autowrap              bool
	cursorVisible         bool
	applicationCursorKeys bool
	applicationKeypad     bool
}

type mouseProtocol uint8

const (
	mouseNone mouseProtocol = iota
	mouseX10
	mouseVT200
	mouseDrag
	mouseAny
)

type mouseEncoding uint8

const (
	encodingDefault mouseEncoding = iota
	encodingSGR
	encodingSGRPixels
)

type parserState uint8

const (
	stateGround parserState = iota
	stateEsc
	stateCSI
	stateOSC
	stateOSCEsc
	stateDCS
	stateDCSEsc
)

// csiMax bounds the parameter bytes retained for one CSI sequence. Real
// DECSET/DECRST sequences are tiny; the cap keeps a malformed stream from
// growing memory. Once the cap is hit the sequence is still consumed to
// its final byte, it is just no longer interpreted.
const csiMax = 64

func newModeTracker() *modeTracker {
	return &modeTracker{
		autowrap:      true,
		cursorVisible: true,
	}
}

// Write consumes one chunk of PTY output. The parser state is preserved
// across calls, so a sequence split over two reads is handled correctly.
func (t *modeTracker) Write(chunk string) {
	if t == nil || chunk == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := 0; i < len(chunk); i++ {
		t.consume(chunk[i])
	}
}

func (t *modeTracker) consume(b byte) {
	switch t.state {
	case stateGround:
		if b == 0x1b {
			t.state = stateEsc
		}
	case stateEsc:
		switch b {
		case '[':
			t.state = stateCSI
			t.csi = t.csi[:0]
		case ']':
			t.state = stateOSC
		case 'P', '_', '^', 'X':
			// DCS / APC / PM / SOS: control strings whose contents must
			// never be mistaken for mode sequences.
			t.state = stateDCS
		case 'c':
			// RIS resets the terminal. The screen is the client's problem;
			// the mode bits are ours.
			t.reset()
			t.state = stateGround
		default:
			t.state = stateGround
		}
	case stateCSI:
		if b >= 0x40 && b <= 0x7e {
			t.applyCSI(string(t.csi), b)
			t.csi = t.csi[:0]
			t.state = stateGround
			return
		}
		if len(t.csi) < csiMax {
			t.csi = append(t.csi, b)
		}
	case stateOSC:
		switch b {
		case 0x07:
			t.state = stateGround
		case 0x1b:
			t.state = stateOSCEsc
		}
	case stateOSCEsc:
		switch b {
		case '\\':
			t.state = stateGround
		case 0x1b:
			// Stay in the escape tail; a later backslash still terminates.
		default:
			t.state = stateOSC
		}
	case stateDCS:
		if b == 0x1b {
			t.state = stateDCSEsc
		}
	case stateDCSEsc:
		switch b {
		case '\\':
			t.state = stateGround
		case 0x1b:
			// Stay; see stateOSCEsc.
		default:
			t.state = stateDCS
		}
	}
}

// applyCSI handles only DECSET/DECRST (CSI ? params h/l). Every other CSI
// is ignored, including the DECRQM query (which ends in $p).
func (t *modeTracker) applyCSI(params string, final byte) {
	if final != 'h' && final != 'l' {
		return
	}
	if !strings.HasPrefix(params, "?") {
		return
	}
	set := final == 'h'
	for _, raw := range strings.Split(params[1:], ";") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			continue
		}
		t.applyMode(n, set)
	}
}

func (t *modeTracker) applyMode(n int, set bool) {
	switch n {
	case 1:
		t.applicationCursorKeys = set
	case 7:
		t.autowrap = set
	case 9:
		// xterm's DECRST for every mouse protocol (9/1000/1002/1003)
		// sets the active protocol to NONE; it does not restore the
		// previously active one.
		if set {
			t.mouseProtocol = mouseX10
		} else {
			t.mouseProtocol = mouseNone
		}
	case 25:
		t.cursorVisible = set
	case 47, 1047, 1049:
		if set {
			t.altScreen = true
			t.altMode = n
		} else {
			t.altScreen = false
			t.altMode = 0
		}
	case 66:
		t.applicationKeypad = set
	case 1000:
		if set {
			t.mouseProtocol = mouseVT200
		} else {
			t.mouseProtocol = mouseNone
		}
	case 1002:
		if set {
			t.mouseProtocol = mouseDrag
		} else {
			t.mouseProtocol = mouseNone
		}
	case 1003:
		if set {
			t.mouseProtocol = mouseAny
		} else {
			t.mouseProtocol = mouseNone
		}
	case 1004:
		t.focusReporting = set
	case 1006:
		if set {
			t.mouseEncoding = encodingSGR
		} else {
			t.mouseEncoding = encodingDefault
		}
	case 1016:
		if set {
			t.mouseEncoding = encodingSGRPixels
		} else {
			t.mouseEncoding = encodingDefault
		}
	case 2004:
		t.bracketedPaste = set
	}
	// Deliberately ignored:
	//   1007 alternate scroll (the client strips it; re-enabling would
	//        turn wheels into arrow keys in a shell),
	//   2026 synchronized output (the preamble always releases it; the
	//        app re-arms it on its next frame),
	//   2031 color-scheme notifications (not needed for viewport
	//        ownership).
}

func (t *modeTracker) reset() {
	t.altScreen = false
	t.altMode = 0
	t.mouseProtocol = mouseNone
	t.mouseEncoding = encodingDefault
	t.focusReporting = false
	t.bracketedPaste = false
	t.autowrap = true
	t.cursorVisible = true
	t.applicationCursorKeys = false
	t.applicationKeypad = false
}

// Preamble returns a synthetic escape sequence that puts a fresh xterm in
// the mode state the running application expects. It is deterministic and
// idempotent: it first releases synchronized output, switches to the
// correct buffer, resets every viewport-affecting mode, then re-applies
// the modes that are actually active.
//
// It deliberately does NOT try to restore screen cells, scrollback,
// cursor position, scroll regions or character sets. Those are the parts
// only a state snapshot (or a full replay) can supply.
func (t *modeTracker) Preamble() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	var b strings.Builder
	// A client that attaches while the app holds synchronized output
	// would otherwise render nothing until the app's next 2026l. Release
	// it; the app re-arms it on its next frame.
	b.WriteString("\x1b[?2026l")
	// Start from a known pen so stale attributes from a previous screen
	// cannot tint the tail that follows.
	b.WriteString("\x1b[0m")

	if t.altScreen {
		switch t.altMode {
		case 47:
			b.WriteString("\x1b[?47h")
		case 1047:
			b.WriteString("\x1b[?1047h")
		default:
			b.WriteString("\x1b[?1049h")
		}
	}

	// Reset the modes that decide input routing and key encoding. xterm's
	// DECRST semantics are used: resetting any mouse protocol/encoding
	// returns to NONE/DEFAULT, not to a previous value.
	b.WriteString("\x1b[?9l\x1b[?1000l\x1b[?1002l\x1b[?1003l")
	b.WriteString("\x1b[?1006l\x1b[?1016l")
	b.WriteString("\x1b[?1004l\x1b[?2004l\x1b[?1l\x1b[?66l")

	if t.autowrap {
		b.WriteString("\x1b[?7h")
	} else {
		b.WriteString("\x1b[?7l")
	}
	if t.cursorVisible {
		b.WriteString("\x1b[?25h")
	} else {
		b.WriteString("\x1b[?25l")
	}

	switch t.mouseProtocol {
	case mouseX10:
		b.WriteString("\x1b[?9h")
	case mouseVT200:
		b.WriteString("\x1b[?1000h")
	case mouseDrag:
		b.WriteString("\x1b[?1002h")
	case mouseAny:
		b.WriteString("\x1b[?1003h")
	}
	switch t.mouseEncoding {
	case encodingSGR:
		b.WriteString("\x1b[?1006h")
	case encodingSGRPixels:
		b.WriteString("\x1b[?1016h")
	}
	if t.focusReporting {
		b.WriteString("\x1b[?1004h")
	}
	if t.bracketedPaste {
		b.WriteString("\x1b[?2004h")
	}
	if t.applicationCursorKeys {
		b.WriteString("\x1b[?1h")
	}
	if t.applicationKeypad {
		b.WriteString("\x1b[?66h")
	}
	return b.String()
}
