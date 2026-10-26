// Command acceptance is the TASK-D acceptance harness for issue #114.
//
// It is a THROWAWAY acceptance driver, not product code: it dials the tty
// WebSocket of a running beta daemon (built -tags ttymetrics) through the
// in-tree internal/ws client, exercises the caps matrix / slow-read /
// stop-read behaviours the §五 acceptance needs, and prints a single JSON
// observation blob to stdout for the acceptance report.
//
// Dependency surface: ONLY myworktree/internal/ws (per the arbiter's
// architecture constraint). Compression is inflated with stdlib compress/flate
// (raw deflate, the same format as the browser's
// DecompressionStream('deflate-raw')) fed through ONE persistent stream, so
// the cursor advances on inflated (logical) bytes exactly like the real
// client.
//
// The token is never taken from argv nor written anywhere: on loopback the
// daemon's withAuth bypasses auth entirely, so no credential is required.
package main

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	ws "myworktree/internal/ws"
)

const (
	opText   = 0x1
	opBinary = 0x2
	opClose  = 0x8
)

type notice struct {
	At            string `json:"at"`
	ElapsedMs     int64  `json:"elapsedMs"`
	DroppedBytes  int64  `json:"droppedBytes"`
	DroppedChunks int64  `json:"droppedChunks"`
}

type observation struct {
	Scenario        string   `json:"scenario"`
	Daemon          string   `json:"daemon"`
	InstanceID      string   `json:"instanceId"`
	Caps            string   `json:"caps"`
	Since           int64    `json:"since"`
	ReplayWindow    int64    `json:"replayWindow"`
	ReadyCompress   bool     `json:"readyCompress"`
	SyncStart       int64    `json:"syncStart"`   // opening {"type":"sync","start":true} offset (base)
	LastSyncEnd     int64    `json:"lastSyncEnd"` // closing {"type":"sync"} offset (end of replay)
	BinaryFrames    int      `json:"binaryFrames"`
	BinaryWireBytes int64    `json:"binaryWireBytes"` // bytes of binary frames as received (wire)
	InflatedBytes   int64    `json:"inflatedBytes"`   // logical bytes after inflate (== wire on plaintext)
	TextFrames      int      `json:"textFrames"`
	Pings           int      `json:"pings"`
	Notices         []notice `json:"notices"`
	NoticeMaxPerSec int      `json:"noticeMaxPerSec"`
	// CloseCode is the ws close-frame code IF the client received a close
	// frame. closeCode==0 means NO close frame was observed — the session ran
	// to its run-ms budget (or was locally closed), i.e. "跑满/未收 close",
	// NOT "server closed cleanly with code 0". A server-side write-deadline
	// reclaim may close the socket without a code the raw reader sees here.
	CloseCode  int    `json:"closeCode"`
	ElapsedMs  int64  `json:"elapsedMs"`
	InflateErr string `json:"inflateErr"`

	// AC3b end-to-end catch-up timing: wall clock from connect to the moment
	// the closing `sync` frame is received. This is the client's true
	// catch-up latency and, over netem, it is bounded by W_eff / linkRate —
	// unlike the server-side replayMs (which only measures the write into the
	// kernel send buffer, ~ms regardless of the shaped egress).
	E2ECatchupMs int64 `json:"e2eCatchupMs"`
	// AC3d per-frame cursor integrity, evaluated at the closing sync:
	// every binary frame inflates to logical bytes accumulated from the
	// opening-sync base; the closing sync's absolute offset must equal
	// base + replayInflated + droppedAnnouncedUpToHere.
	ClosingSyncSeen bool   `json:"closingSyncSeen"`
	ContinuityBase  int64  `json:"continuityBase"`
	ReplayInflated  int64  `json:"replayInflated"`
	CursorAtClosing int64  `json:"cursorAtClosing"`
	ContinuityOK    *bool  `json:"continuityOK"`
	Note            string `json:"note"`
}

func main() {
	daemon := flag.String("daemon", "", "host:port of the daemon (required)")
	id := flag.String("id", "", "instance id (required)")
	caps := flag.String("caps", "", "comma-separated caps")
	since := flag.Int64("since", -1, "cursor since (-1 = first connect tail)")
	replayWindow := flag.Int64("replay-window", 0, "append &replay-window=<bytes> when >0")
	mode := flag.String("mode", "normal", "normal|slow|stop")
	readDelayMs := flag.Int("read-delay-ms", 0, "slow mode: sleep between reads")
	runMs := flag.Int("run-ms", 4000, "how long to keep the session")
	stopBlockMs := flag.Int("stop-block-ms", 16000, "stop mode: block reads this long after warmup")
	warmupMs := flag.Int("warmup-ms", 500, "stop mode: read normally this long first")
	readDeadlineMs := flag.Int("read-deadline-ms", 30000, "per-read socket deadline; widened to 30s so a netem-shaped frame is never aborted mid-frame (parser desync root cause)")
	flag.Parse()

	if *daemon == "" || *id == "" {
		fmt.Fprintln(os.Stderr, "acceptance: -daemon and -id are required")
		os.Exit(2)
	}

	path := "/api/instances/tty/ws?id=" + *id
	if *caps != "" {
		path += "&caps=" + *caps
	}
	if *since >= 0 {
		path += fmt.Sprintf("&since=%d", *since)
	}
	if *replayWindow > 0 {
		path += fmt.Sprintf("&replay-window=%d", *replayWindow)
	}

	dbg := os.Getenv("ACC_DEBUG") != ""
	obs := &observation{
		Scenario: *mode, Daemon: *daemon, InstanceID: *id, Caps: *caps,
		Since: *since, ReplayWindow: *replayWindow, Notices: []notice{},
	}
	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*runMs+5000)*time.Millisecond)
	defer cancel()

	conn, err := ws.Dial(ctx, *daemon, path, http.Header{})
	if err != nil {
		obs.Note = "dial failed: " + err.Error()
		emit(obs)
		os.Exit(1)
	}
	defer conn.Close()

	// The server's Step-3 handshake (replay + live subscribe) waits for the
	// FIRST resize frame (or a 5s timer). A headless reader that never sends
	// one gets only the ready frame, so we must announce a size like the real
	// browser does to pull the replay and open the live subscription.
	if err := conn.WriteText([]byte(`{"type":"resize","cols":80,"rows":24}`)); err != nil {
		obs.Note = "resize write: " + err.Error()
	}

	// Persistent inflate stream: every BINARY frame is written into the pipe;
	// a single flate.Reader decodes the continuous raw-deflate stream. The
	// inflated byte counter is the client's logical cursor.
	var (
		pipeW       *io.PipeWriter
		inflated    int64
		inflatedMu  sync.Mutex
		inflateDone = make(chan struct{})
	)
	if capsHas(*caps, "compress") {
		pipeR, pw := io.Pipe()
		pipeW = pw
		go func() {
			defer close(inflateDone)
			fr := flate.NewReader(pipeR)
			buf := make([]byte, 32*1024)
			for {
				n, rerr := fr.Read(buf)
				if n > 0 {
					inflatedMu.Lock()
					inflated += int64(n)
					inflatedMu.Unlock()
				}
				if rerr != nil {
					if rerr != io.EOF {
						inflatedMu.Lock()
						if obs.InflateErr == "" {
							obs.InflateErr = rerr.Error()
						}
						inflatedMu.Unlock()
					}
					return
				}
			}
		}()
	}
	// inflatedNow reads the logical cursor under lock.
	inflatedNow := func() int64 {
		inflatedMu.Lock()
		defer inflatedMu.Unlock()
		return inflated
	}
	// drainInflate lets the async inflate goroutine catch up with the frames
	// already fed to the pipe before we take a cursor snapshot: poll until the
	// counter is stable for two consecutive samples (or a short ceiling). This
	// closes the read-loop-vs-inflate-goroutine lag so the closing-sync cursor
	// is exact rather than one frame short.
	drainInflate := func() {
		prev, stable := inflatedNow(), 0
		// Hard ceiling: under a live flood the counter keeps growing and would
		// never look "stable"; cap the wait so the closing-sync handler can
		// never spin forever. On a real replay end the counter settles well
		// inside this window.
		deadline := time.Now().Add(400 * time.Millisecond)
		for stable < 2 && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
			cur := inflatedNow()
			if cur == prev {
				stable++
			} else {
				stable = 0
			}
			prev = cur
		}
	}

	// second-bucket notice counting for the <=4/s rate check.
	noticeBucket := map[int64]int{}

	// Cursor-integrity state (AC3d): base is the opening sync offset; the
	// closing sync must equal base + replayInflated + drops announced so far.
	var (
		replayOpen    bool
		replayClosed  bool
		droppedSoFar  int64
		cursorFrames  int
		firstCursorAt int64
	)

	sessionEnd := start.Add(time.Duration(*runMs) * time.Millisecond)
	if *mode == "stop" {
		sessionEnd = start.Add(time.Duration(*warmupMs) * time.Millisecond)
	}

	for {
		now := time.Now()
		if !now.Before(sessionEnd) {
			break
		}
		// A generous deadline, NOT a short one: over netem the largest single
		// frame (the 64 KB handshake replay at 800 kbit ~ 1 s, and under real
		// multi-instance load even longer as the shaper queues backlog) must
		// fully arrive before the deadline. A short deadline aborts mid-frame
		// and desyncs the ws frame parser on the next read ("data frame before
		// the final continuation") — this abort/desync is the ROOT CAUSE of the
		// earlier "5-path live" failures; 30 s lets every shaped frame land whole.
		_ = conn.SetReadDeadline(now.Add(time.Duration(*readDeadlineMs) * time.Millisecond))
		op, payload, rerr := conn.ReadMessage()
		if rerr != nil {
			if ne, ok := rerr.(interface{ Timeout() bool }); ok && ne.Timeout() {
				if dbg {
					fmt.Fprintf(os.Stderr, "[dbg] t=%dms TIMEOUT no-frame\n", time.Since(start).Milliseconds())
				}
				continue // no frame this second; keep session open
			}
			if dbg {
				fmt.Fprintf(os.Stderr, "[dbg] t=%dms READ-ERR %v\n", time.Since(start).Milliseconds(), rerr)
			}
			break // real read error / close -> record and stop
		}
		if dbg {
			fmt.Fprintf(os.Stderr, "[dbg] t=%dms frame op=%d len=%d\n", time.Since(start).Milliseconds(), op, len(payload))
		}
		switch op {
		case opText:
			obs.TextFrames++
			var ctl struct {
				Type          string `json:"type"`
				Compress      bool   `json:"compress"`
				Offset        int64  `json:"offset"`
				Start         bool   `json:"start"`
				DroppedBytes  int64  `json:"droppedBytes"`
				DroppedChunks int64  `json:"droppedChunks"`
			}
			if json.Unmarshal(payload, &ctl) == nil {
				switch ctl.Type {
				case "ready":
					obs.ReadyCompress = ctl.Compress
				case "sync":
					if ctl.Start {
						// Opening sync: the replay's real (clamped) start offset.
						// This is the cursor base for AC3d.
						obs.SyncStart = ctl.Offset
						replayOpen = true
					} else if replayOpen && !replayClosed {
						// Closing sync: end of the replay window. Freeze the
						// cursor, publish the absolute end offset, and check that
						// base + replayInflated + drops == this absolute value.
						drainInflate()
						replayClosed = true
						obs.ClosingSyncSeen = true
						obs.LastSyncEnd = ctl.Offset
						obs.E2ECatchupMs = time.Since(start).Milliseconds()
						obs.ContinuityBase = obs.SyncStart
						obs.ReplayInflated = inflatedNow()
						obs.CursorAtClosing = obs.SyncStart + obs.ReplayInflated + droppedSoFar
						ok := obs.CursorAtClosing == ctl.Offset
						obs.ContinuityOK = &ok
						if dbg {
							fmt.Fprintf(os.Stderr, "[dbg] closing sync offset=%d base=%d replayInflated=%d dropped=%d cursor=%d ok=%v e2e=%dms\n",
								ctl.Offset, obs.SyncStart, obs.ReplayInflated, droppedSoFar, obs.CursorAtClosing, ok, obs.E2ECatchupMs)
						}
					} else {
						// A cursor-less tail closing sync (no opening sync): still
						// record the absolute end for reference.
						obs.LastSyncEnd = ctl.Offset
					}
				case "ping":
					obs.Pings++
				case "overflow":
					elapsed := time.Since(start).Milliseconds()
					obs.Notices = append(obs.Notices, notice{
						At: time.Now().Format("15:04:05.000"), ElapsedMs: elapsed,
						DroppedBytes: ctl.DroppedBytes, DroppedChunks: ctl.DroppedChunks,
					})
					droppedSoFar += ctl.DroppedBytes
					noticeBucket[elapsed/1000]++
				}
			}
		case opBinary:
			obs.BinaryFrames++
			obs.BinaryWireBytes += int64(len(payload))
			// Per-frame cursor advance: on plaintext the frame IS the logical
			// stream; on compress the inflate goroutine advances it. Count the
			// frames that belong to the replay window (after base, before close).
			if replayOpen && !replayClosed {
				cursorFrames++
				if cursorFrames == 1 {
					firstCursorAt = time.Since(start).Milliseconds()
				}
			}
			if pipeW != nil {
				if _, werr := pipeW.Write(payload); werr != nil {
					obs.Note = "pipe write: " + werr.Error()
				}
			} else {
				inflatedMu.Lock()
				inflated += int64(len(payload))
				inflatedMu.Unlock()
			}
		case opClose:
			if len(payload) >= 2 {
				obs.CloseCode = int(payload[0])<<8 | int(payload[1])
			}
		default:
			// ping/pong handled inside the ws client.
		}
		if *mode == "slow" && *readDelayMs > 0 {
			time.Sleep(time.Duration(*readDelayMs) * time.Millisecond)
		}
	}

	// stop mode: hold the socket open WITHOUT reading for stopBlockMs so the
	// server's write deadline reclaims it (AC7), then close.
	if *mode == "stop" {
		time.Sleep(time.Duration(*stopBlockMs) * time.Millisecond)
	}

	if pipeW != nil {
		_ = pipeW.Close()
		select {
		case <-inflateDone:
		case <-time.After(2 * time.Second):
		}
	}
	obs.InflatedBytes = inflatedNow()

	for _, c := range noticeBucket {
		if c > obs.NoticeMaxPerSec {
			obs.NoticeMaxPerSec = c
		}
	}
	obs.ElapsedMs = time.Since(start).Milliseconds()
	// firstCursorAt is diagnostic only; reuse a field-free debug line.
	if dbg {
		fmt.Fprintf(os.Stderr, "[dbg] replayFrames=%d firstCursorAt=%dms inflated=%d\n", cursorFrames, firstCursorAt, obs.InflatedBytes)
	}
	emit(obs)
}

func capsHas(caps, name string) bool {
	for _, tok := range bytes.Split([]byte(caps), []byte(",")) {
		if string(bytes.TrimSpace(tok)) == name {
			return true
		}
	}
	return false
}

func emit(o *observation) {
	b, _ := json.Marshal(o)
	fmt.Println(string(b))
}
