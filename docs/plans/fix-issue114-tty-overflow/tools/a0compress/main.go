// Command a0compress is the Phase A-0 compression-ratio measurement tool for
// issue #114 (docs/plans/fix-issue114-tty-overflow/FIX-PLAN.md, decision 5a
// pin 1 / §四 Phase A-0).
//
// It is a measurement instrument, not product code. It lives in its own
// nested module (module a0compress, stdlib only) so the main repo's
// `go build ./...` never compiles it, and it performs no I/O beyond reading
// the sample file passed on the command line — it never contacts the daemon.
//
// Semantics, verbatim from FIX-PLAN 5a:
//   - one persistent compress/flate stream per connection, level BestSpeed;
//   - every frame is followed by Flush() (sync flush); the stream is NOT
//     closed between frames (Close would end the shared context and desync
//     the client's DecompressionStream('deflate-raw'));
//   - replay and live bytes share that single stream, so the measurement
//     feeds the whole capture in file order through one flate context.
//
// Frame models:
//
//	Scenario A (gate metric): 1024-byte frames = PTY read granularity.
//	Scenario B (secondary):   65536-byte frames = ReadSince granularity.
//
//	ratio = sum(raw bytes) / sum(compressed bytes)   // per scenario
//
// Gate: scenario A ratio >= 2.0 => PASS (proceed to 5a encoding);
// < 2.0 => FAIL (compression becomes a Phase C candidate per §四.0).
//
// Secondary output: a frame-size sweep (flush-tax sensitivity), per-window
// ratios (so the gate can be read as "worst window", not one blended mean),
// no-flush and DefaultCompression references, and the ANSI composition of
// the sample.
//
// Usage:
//
//	go run . -sample /tmp/a0-sample.bin
//	go run . -sample /tmp/a0-sample.bin -json
//	go run . -sample /tmp/a0-sample.bin -frameA 1024 -frameB 65536 -windows 8
//
// Exit status: 0 on PASS, 1 on FAIL, 2 on usage/IO error.
package main

import (
	"bytes"
	"compress/flate"
	"crypto/md5"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// countingWriter counts every byte the flate writer pushes downstream, so
// "compressed bytes" is measured at the point the server would hand bytes to
// the socket (post-Flush). Nothing is buffered: an 8 MB sample compresses to
// ~1.4 MB and we do not need to keep it.
type countingWriter struct {
	n int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

// frameResult is one measurement run.
type frameResult struct {
	Label         string  `json:"label"`
	FrameSize     int     `json:"frameSize"`
	Frames        int     `json:"frames"`
	RawBytes      int64   `json:"rawBytes"`
	CompBytes     int64   `json:"compBytes"`
	Ratio         float64 `json:"ratio"`
	SavingsPct    float64 `json:"savingsPct"`
	MeanFrame     float64 `json:"meanFrameBytes"`
	MinFrame      int64   `json:"minFrameBytes"`
	MaxFrame      int64   `json:"maxFrameBytes"`
	P50Frame      int64   `json:"p50FrameBytes"`
	P99Frame      int64   `json:"p99FrameBytes"`
	StreamHeader  int64   `json:"streamHeaderBytes"`
	FlushTaxBytes int64   `json:"flushTaxBytes"`
}

// window is a contiguous slice of the sample measured under scenario A with a
// fresh stream, so the gate can be read as "worst window" instead of one
// blended average across the whole capture.
type window struct {
	Offset     int64   `json:"offsetBytes"`
	RawBytes   int64   `json:"rawBytes"`
	CompBytes  int64   `json:"compBytes"`
	Ratio      float64 `json:"ratio"`
	SeqPct     float64 `json:"escapeSequencePct"`
	FrameCount int     `json:"frames"`
}

// sampleStats holds the ANSI composition of the sample.
type sampleStats struct {
	RawBytes      int64    `json:"rawBytes"`
	ESCBytesTotal int64    `json:"escBytesTotal"` // every 0x1b byte, incl. ST terminators inside OSC
	ESCBytesPct   float64  `json:"escBytesPct"`   // escBytesTotal / rawBytes
	SeqCount      int64    `json:"escapeSequenceCount"`
	SeqBytes      int64    `json:"escapeSequenceBytes"`
	SeqPct        float64  `json:"escapeSequencePct"` // the plan's "ANSI 占比"
	SeqMeanLen    float64  `json:"escapeSequenceMeanLen"`
	SeqMaxLen     int64    `json:"escapeSequenceMaxLen"`
	SeqP99Len     int64    `json:"escapeSequenceP99Len"`
	CSI           int64    `json:"csiCount"`
	OSC           int64    `json:"oscCount"`
	TextBytes     int64    `json:"textBytes"`
	TextPct       float64  `json:"textPct"`
	HighBitBytes  int64    `json:"highBitBytes"` // >=0x80: UTF-8 payload
	HighBitPct    float64  `json:"highBitPct"`
	SGRResets     int64    `json:"sgrResets"` // [0m + [39m per-token styling churn
	CRs           int64    `json:"crBytes"`
	LFs           int64    `json:"lfBytes"`
	AlnumPct      float64  `json:"alnumPct"`
	Base64ishPct  float64  `json:"base64ishPct"`
	LongestRunB64 int64    `json:"longestBase64ishRun"`
	Windows       []window `json:"windows,omitempty"`
}

// report is the whole run.
type report struct {
	SampleFile    string        `json:"sampleFile"`
	SampleBytes   int64         `json:"sampleBytes"`
	SampleMD5     string        `json:"sampleMD5,omitempty"`
	Level         string        `json:"level"`
	Ansi          sampleStats   `json:"ansi"`
	ScenarioA     frameResult   `json:"scenarioA"`
	ScenarioB     frameResult   `json:"scenarioB"`
	Sweep         []frameResult `json:"sweep,omitempty"`
	NoFlushRef    *frameResult  `json:"noFlushReference,omitempty"`
	DefaultRef    *frameResult  `json:"defaultLevelFrameA,omitempty"`
	GateThreshold float64       `json:"gateThreshold"`
	Verdict       string        `json:"verdict"`
	VerdictNote   string        `json:"verdictNote"`
	RoundtripOK   *bool         `json:"roundtripOK,omitempty"`
}

func levelFromString(s string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "bestspeed", "1":
		return flate.BestSpeed, nil
	case "default", "defaultcompression", "-1":
		return flate.DefaultCompression, nil
	case "bestcompression", "9":
		return flate.BestCompression, nil
	case "huffmanonly", "0":
		return flate.HuffmanOnly, nil
	}
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n >= 0 && n <= 9 {
		return n, nil
	}
	return 0, fmt.Errorf("unknown -level %q (want bestspeed|default|bestcompression|huffmanonly|0-9)", s)
}

// measure feeds raw through ONE persistent flate writer in frames of
// frameSize bytes, Flush() after every frame. closeStream stays false for
// the gate runs (production never closes the stream mid-session); a
// persistent stream's only trailer is the one the final Close would emit,
// so counting stops at the last Flush and ratio is never diluted by it.
func measure(raw []byte, frameSize, level int, label string, closeStream bool) (*frameResult, error) {
	if frameSize <= 0 {
		return nil, errors.New("frame size must be > 0")
	}
	cw := &countingWriter{}
	zw, err := flate.NewWriter(cw, level)
	if err != nil {
		return nil, err
	}
	res := &frameResult{Label: label, FrameSize: frameSize, MinFrame: -1}
	perFrame := make([]int64, 0, len(raw)/frameSize+1)
	before := cw.n
	for off := 0; off < len(raw); off += frameSize {
		end := off + frameSize
		if end > len(raw) {
			end = len(raw)
		}
		if _, err := zw.Write(raw[off:end]); err != nil {
			return nil, fmt.Errorf("flate write at offset %d: %w", off, err)
		}
		if err := zw.Flush(); err != nil {
			return nil, fmt.Errorf("flate flush at offset %d: %w", off, err)
		}
		n := cw.n - before
		before = cw.n
		perFrame = append(perFrame, n)
		res.Frames++
		res.RawBytes += int64(end - off)
		if res.MinFrame < 0 || n < res.MinFrame {
			res.MinFrame = n
		}
		if n > res.MaxFrame {
			res.MaxFrame = n
		}
	}
	if closeStream {
		if err := zw.Close(); err != nil {
			return nil, fmt.Errorf("flate close: %w", err)
		}
	}
	res.CompBytes = cw.n
	res.StreamHeader = 2 // deflate-raw stream header (CMF+FLG), one-off per stream
	if len(perFrame) > 0 {
		res.MeanFrame = float64(res.CompBytes-res.StreamHeader) / float64(len(perFrame))
		s := append([]int64(nil), perFrame...)
		sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
		res.P50Frame = s[len(s)/2]
		res.P99Frame = s[int(float64(len(s)-1)*0.99)]
		// A sync flush closes the current block with an empty stored block
		// (5 bytes: 0x00 0x00 0xFF 0xFF plus the block header bit), so the
		// unavoidable per-frame cost is >=5 bytes. This is the "flush tax"
		// the 1KB PTY granularity pays and it is what scenario A measures.
		res.FlushTaxBytes = int64(res.Frames) * 5
	}
	if res.CompBytes > 0 && res.RawBytes > 0 {
		res.Ratio = float64(res.RawBytes) / float64(res.CompBytes)
		res.SavingsPct = (1 - float64(res.CompBytes)/float64(res.RawBytes)) * 100
	}
	return res, nil
}

// roundtrip verifies the scenario-A byte stream inflates back to the exact
// original bytes: the gate number is only meaningful if one persistent
// deflate-raw stream, flushed per frame, is actually decodable in order.
func roundtrip(raw []byte, frameSize, level int) (bool, error) {
	var buf bytes.Buffer
	zw, err := flate.NewWriter(&buf, level)
	if err != nil {
		return false, err
	}
	for off := 0; off < len(raw); off += frameSize {
		end := off + frameSize
		if end > len(raw) {
			end = len(raw)
		}
		if _, err := zw.Write(raw[off:end]); err != nil {
			return false, err
		}
		if err := zw.Flush(); err != nil {
			return false, err
		}
	}
	if err := zw.Close(); err != nil {
		return false, err
	}
	zr := flate.NewReader(bytes.NewReader(buf.Bytes()))
	got, err := io.ReadAll(zr)
	if cerr := zr.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, err
	}
	return bytes.Equal(got, raw), nil
}

// ansiCounts parses escape sequences once and returns (seqCount, seqBytes,
// csi, osc, maxLen, p99Len). ESC is treated as: CSI (ESC '[' params final
// 0x40-0x7E), OSC/APC/DCS (ESC ']' | '_' | 'P' ... BEL or ST), everything
// else a two-byte escape.
func ansiCounts(raw []byte) (count, span, csi, osc, maxLen, p99Len int64) {
	lens := make([]int64, 0, 4096)
	for i := 0; i < len(raw); {
		if raw[i] != 0x1b {
			i++
			continue
		}
		j := i + 1
		count++
		if j < len(raw) {
			intro := raw[j]
			j++
			switch intro {
			case ']', '_', 'P':
				if intro == ']' {
					osc++
				}
				for j < len(raw) {
					b := raw[j]
					j++
					if b == 0x07 {
						break
					}
					if b == 0x1b && j < len(raw) && raw[j] == '\\' {
						j++
						break
					}
				}
			case '[':
				csi++
				for j < len(raw) {
					b := raw[j]
					j++
					if b >= 0x40 && b <= 0x7e {
						break
					}
				}
			default:
				// two-byte single-character escape
			}
		}
		l := int64(j - i)
		span += l
		if l > maxLen {
			maxLen = l
		}
		lens = append(lens, l)
		i = j
	}
	if len(lens) > 0 {
		sort.Slice(lens, func(i, j int) bool { return lens[i] < lens[j] })
		p99Len = lens[int(float64(len(lens)-1)*0.99)]
	}
	return count, span, csi, osc, maxLen, p99Len
}

func ansiStats(raw []byte, frameSize, level int, windows int) sampleStats {
	s := sampleStats{RawBytes: int64(len(raw))}
	s.ESCBytesTotal = int64(bytes.Count(raw, []byte{0x1b}))
	n, span, csi, osc, maxLen, p99 := ansiCounts(raw)
	s.SeqCount, s.SeqBytes, s.CSI, s.OSC, s.SeqMaxLen, s.SeqP99Len = n, span, csi, osc, maxLen, p99
	s.SGRResets = int64(bytes.Count(raw, []byte("\x1b[0m")) + bytes.Count(raw, []byte("\x1b[39m")))
	s.CRs = int64(bytes.Count(raw, []byte{'\r'}))
	s.LFs = int64(bytes.Count(raw, []byte{'\n'}))
	for _, c := range raw {
		if c >= 0x80 {
			s.HighBitBytes++
		}
	}
	var alnum, b64, run, longest int64
	for _, c := range raw {
		isAlnum := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if isAlnum {
			alnum++
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
		if isAlnum || c == '+' || c == '/' || c == '=' || c == '-' || c == '_' {
			b64++
		}
	}
	if s.RawBytes > 0 {
		s.TextBytes = s.RawBytes - s.SeqBytes
		s.ESCBytesPct = float64(s.ESCBytesTotal) / float64(s.RawBytes) * 100
		s.SeqPct = float64(s.SeqBytes) / float64(s.RawBytes) * 100
		s.TextPct = float64(s.TextBytes) / float64(s.RawBytes) * 100
		s.HighBitPct = float64(s.HighBitBytes) / float64(s.RawBytes) * 100
		s.AlnumPct = float64(alnum) / float64(s.RawBytes) * 100
		s.Base64ishPct = float64(b64) / float64(s.RawBytes) * 100
		s.LongestRunB64 = longest
	}
	if s.SeqCount > 0 {
		s.SeqMeanLen = float64(s.SeqBytes) / float64(s.SeqCount)
	}
	if windows > 1 && len(raw) >= windows*frameSize {
		wsz := (len(raw) / windows) / frameSize * frameSize
		for k := 0; k < windows; k++ {
			off := k * wsz
			end := off + wsz
			if k == windows-1 {
				end = len(raw)
			}
			if end <= off {
				continue
			}
			chunk := raw[off:end]
			r, err := measure(chunk, frameSize, level, fmt.Sprintf("window%d", k), false)
			if err != nil {
				continue
			}
			_, wspan, _, _, _, _ := ansiCounts(chunk)
			s.Windows = append(s.Windows, window{
				Offset:     int64(off),
				RawBytes:   r.RawBytes,
				CompBytes:  r.CompBytes,
				Ratio:      r.Ratio,
				SeqPct:     float64(wspan) / float64(len(chunk)) * 100,
				FrameCount: r.Frames,
			})
		}
	}
	return s
}

func parseSizes(spec string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("bad -sweep entry %q", part)
		}
		out = append(out, n)
	}
	return out, nil
}

func main() {
	var (
		sample  = flag.String("sample", "/tmp/a0-sample.bin", "raw captured sample bytes (read-only)")
		frameA  = flag.Int("frameA", 1024, "scenario A frame size: live PTY read granularity")
		frameB  = flag.Int("frameB", 65536, "scenario B frame size: ReadSince granularity")
		level   = flag.String("level", "bestspeed", "flate level for the gate scenarios (5a pins BestSpeed)")
		sweep   = flag.String("sweep", "256,512,1024,2048,4096,8192,16384,65536", "frame sizes for the flush-tax sweep")
		windows = flag.Int("windows", 8, "split the sample into N fresh-stream windows (0 = skip)")
		round   = flag.Bool("roundtrip", true, "verify scenario A inflates back to the original bytes")
		noRefs  = flag.Bool("noRefs", false, "skip the no-flush / DefaultCompression reference runs")
		asJSON  = flag.Bool("json", false, "emit JSON instead of the text report")
	)
	flag.Parse()

	raw, err := os.ReadFile(*sample)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read sample: %v\n", err)
		os.Exit(2)
	}
	if len(raw) == 0 {
		fmt.Fprintln(os.Stderr, "sample is empty")
		os.Exit(2)
	}
	if *frameA <= 0 || *frameB <= 0 {
		fmt.Fprintln(os.Stderr, "-frameA and -frameB must be > 0")
		os.Exit(2)
	}
	lvl, err := levelFromString(*level)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}
	sizes, err := parseSizes(*sweep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}

	rep := report{SampleFile: *sample, SampleBytes: int64(len(raw)), Level: *level}
	if md5, err := md5File(*sample); err == nil {
		rep.SampleMD5 = md5
	}
	rep.Ansi = ansiStats(raw, *frameA, lvl, *windows)

	a, err := measure(raw, *frameA, lvl, "A live 1KB sync flush", false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scenario A: %v\n", err)
		os.Exit(2)
	}
	b, err := measure(raw, *frameB, lvl, "B replay 64KB sync flush", false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scenario B: %v\n", err)
		os.Exit(2)
	}
	rep.ScenarioA, rep.ScenarioB = *a, *b

	for _, sz := range sizes {
		if sz == *frameA || sz == *frameB || sz > len(raw) {
			continue
		}
		r, err := measure(raw, sz, lvl, fmt.Sprintf("sweep %dB frame", sz), false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sweep %d: %v\n", sz, err)
			continue
		}
		rep.Sweep = append(rep.Sweep, *r)
	}
	if !*noRefs {
		// Upper bound: the same stream with no per-frame flush cost.
		if nf, err := measure(raw, len(raw), lvl, "ref: no per-frame flush", false); err == nil {
			rep.NoFlushRef = nf
		}
		if df, err := measure(raw, *frameA, flate.DefaultCompression, "ref: DefaultCompression 1KB", false); err == nil {
			rep.DefaultRef = df
		}
	}
	if *round {
		ok, err := roundtrip(raw, *frameA, lvl)
		if err != nil {
			fmt.Fprintf(os.Stderr, "roundtrip: %v\n", err)
			os.Exit(2)
		}
		rep.RoundtripOK = &ok
	}

	rep.GateThreshold = 2.0
	if a.Ratio >= rep.GateThreshold {
		rep.Verdict = "PASS"
		rep.VerdictNote = fmt.Sprintf("scenario A ratio %.3fx >= %.1fx: proceed to 5a encoding", a.Ratio, rep.GateThreshold)
		if n := len(rep.Ansi.Windows); n > 0 {
			worst := rep.Ansi.Windows[0]
			for _, w := range rep.Ansi.Windows {
				if w.Ratio < worst.Ratio {
					worst = w
				}
			}
			rep.VerdictNote += fmt.Sprintf("; worst of %d windows %.3fx at offset %d", n, worst.Ratio, worst.Offset)
		}
	} else {
		rep.Verdict = "FAIL"
		rep.VerdictNote = fmt.Sprintf("scenario A ratio %.3fx < %.1fx: compression drops to a Phase C candidate; recompute W/queue params for no-compression per FIX-PLAN §四.0", a.Ratio, rep.GateThreshold)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintf(os.Stderr, "encode json: %v\n", err)
			os.Exit(2)
		}
	} else {
		fmt.Print(render(rep))
	}
	if rep.Verdict == "FAIL" {
		os.Exit(1)
	}
}

func md5File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func render(r report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Phase A-0 compression gate (issue #114, FIX-PLAN 5a pin 1)\n")
	fmt.Fprintf(&b, "sample: %s  bytes=%d  md5=%s  level=%s\n\n", r.SampleFile, r.SampleBytes, r.SampleMD5, r.Level)
	fmt.Fprintf(&b, "ANSI composition\n")
	fmt.Fprintf(&b, "  ESC bytes               %10d  (%.3f%% of raw)\n", r.Ansi.ESCBytesTotal, r.Ansi.ESCBytesPct)
	fmt.Fprintf(&b, "  escape sequences      %10d  CSI=%d OSC=%d\n", r.Ansi.SeqCount, r.Ansi.CSI, r.Ansi.OSC)
	fmt.Fprintf(&b, "  sequence bytes        %10d  (%.3f%%)  len mean %.2f p99 %d max %d\n",
		r.Ansi.SeqBytes, r.Ansi.SeqPct, r.Ansi.SeqMeanLen, r.Ansi.SeqP99Len, r.Ansi.SeqMaxLen)
	fmt.Fprintf(&b, "  text bytes            %10d  (%.3f%%)\n", r.Ansi.TextBytes, r.Ansi.TextPct)
	fmt.Fprintf(&b, "  high-bit (UTF-8)      %10d  (%.3f%%)\n", r.Ansi.HighBitBytes, r.Ansi.HighBitPct)
	fmt.Fprintf(&b, "  SGR resets ([0m+[39m) %10d\n", r.Ansi.SGRResets)
	fmt.Fprintf(&b, "  CR / LF               %10d / %d\n", r.Ansi.CRs, r.Ansi.LFs)
	fmt.Fprintf(&b, "  alnum / base64-ish    %.2f%% / %.2f%%   longest base64-ish run %d\n\n",
		r.Ansi.AlnumPct, r.Ansi.Base64ishPct, r.Ansi.LongestRunB64)
	fmt.Fprintf(&b, "%-30s %8s %11s %11s %8s %8s %9s\n",
		"scenario", "frames", "raw bytes", "comp bytes", "ratio", "savings", "mean/fr")
	row := func(f frameResult) {
		fmt.Fprintf(&b, "%-30s %8d %11d %11d %7.3fx %7.2f%% %9.1f\n",
			f.Label, f.Frames, f.RawBytes, f.CompBytes, f.Ratio, f.SavingsPct, f.MeanFrame)
	}
	row(r.ScenarioA)
	row(r.ScenarioB)
	for _, f := range r.Sweep {
		row(f)
	}
	if r.NoFlushRef != nil {
		row(*r.NoFlushRef)
	}
	if r.DefaultRef != nil {
		row(*r.DefaultRef)
	}
	fmt.Fprintf(&b, "\nscenario A per-frame compressed size: min %d p50 %d p99 %d max %d"+
		" (deflate-raw header %d B, sync-flush floor ~%d B)\n",
		r.ScenarioA.MinFrame, r.ScenarioA.P50Frame, r.ScenarioA.P99Frame, r.ScenarioA.MaxFrame,
		r.ScenarioA.StreamHeader, r.ScenarioA.FlushTaxBytes)
	if n := len(r.Ansi.Windows); n > 0 {
		worst, best := r.Ansi.Windows[0], r.Ansi.Windows[0]
		for _, w := range r.Ansi.Windows {
			if w.Ratio < worst.Ratio {
				worst = w
			}
			if w.Ratio > best.Ratio {
				best = w
			}
		}
		fmt.Fprintf(&b, "scenario A fresh-stream windows (n=%d): min %.3fx (offset %d)  max %.3fx (offset %d)\n",
			n, worst.Ratio, worst.Offset, best.Ratio, best.Offset)
	}
	if r.RoundtripOK != nil {
		fmt.Fprintf(&b, "roundtrip, one persistent deflate-raw stream flushed per frame: ok=%v\n", *r.RoundtripOK)
	}
	fmt.Fprintf(&b, "\nGATE (scenario A >= %.1fx): %s\n%s\n", r.GateThreshold, r.Verdict, r.VerdictNote)
	return b.String()
}
