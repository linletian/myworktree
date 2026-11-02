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
	viewportDebugBodyLimit       = 64 * 1024
	viewportDebugSessionTTL      = 30 * time.Minute
	viewportDebugMaxSessions     = 8
	viewportDebugMaxEventsPerSes = 120
)

var (
	viewportDebugSessionPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)
	viewportDebugKindPattern    = regexp.MustCompile(`^[a-z0-9_-]{0,32}$`)
	viewportDebugTagPattern     = regexp.MustCompile(`^[A-Za-z0-9]{0,32}$`)
	viewportDebugReasons        = map[string]bool{
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
	InlineHeight   string `json:"inlineHeight"`
	PaddingTop     string `json:"paddingTop"`
	PaddingRight   string `json:"paddingRight"`
	PaddingBottom  string `json:"paddingBottom"`
	PaddingLeft    string `json:"paddingLeft"`
	Background     string `json:"background"`
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
}

// viewportDebugProbe answers "what is painted at the bottom edge" as data rather
// than inference: the hit-tested element at the last visible pixel of the VISUAL
// viewport and of the LAYOUT viewport. They disagree exactly when the visual
// viewport is panned, and a BODY/HTML hit means the root canvas is exposed
// (a real band) whereas the terminal host means the pixels are the app's own ink.
type viewportDebugProbe struct {
	VisualBottom *viewportDebugProbeHit `json:"visualBottom,omitempty"`
	LayoutBottom *viewportDebugProbeHit `json:"layoutBottom,omitempty"`
}

type viewportDebugProbeHit struct {
	Y          float64 `json:"y"`
	Tag        string  `json:"tag"`
	ID         string  `json:"id"`
	ClassName  string  `json:"className"`
	Background string  `json:"background"`
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
	WebPanel     *viewportDebugStyledRect `json:"webPanel,omitempty"`
	XtermScreen  *viewportDebugRect       `json:"xtermScreen,omitempty"`
	XtermRows    *viewportDebugRect       `json:"xtermRows,omitempty"`
	UI           viewportDebugUI          `json:"ui"`
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
	for _, value := range []string{s.App.ComputedHeight, s.App.InlineHeight, s.App.PaddingTop, s.App.PaddingRight, s.App.PaddingBottom, s.App.PaddingLeft, s.App.Background, s.Workspace.Background, s.Document.BodyBackground, s.Document.HTMLBackground} {
		if len(value) > 160 {
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
