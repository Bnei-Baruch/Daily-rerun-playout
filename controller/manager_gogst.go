//go:build gogst

package main

/*
#cgo pkg-config: gstreamer-1.0
#include <gst/gst.h>
#include <glib-object.h>
static gboolean set_active_pad(GstElement *sel, GstPad *pad) {
    if (pad == NULL) {
        return FALSE;
    }
    g_object_set(sel, "active-pad", pad, NULL);
    return TRUE;
}
*/
import "C"

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/tinyzimmer/go-gst/gst"
)

// goGstManager maintains a single persistent pipeline with an input-selector
// feeding a DeckLink chain. Sources (PNG, lessons) are bins that provide
// UYVY 1920x1080@25 progressive frames which are interlaced just before the
// DeckLink sink, matching the working deckplay test pipeline.
type goGstManager struct {
	audioMap AudioMap

	mu sync.Mutex

	pipeline *gst.Pipeline
	vsel     *gst.Element
	deckBin  *gst.Element

	pngBin     *gst.Bin
	pngPad     *gst.Pad
	pngSinkPad *gst.Pad

	lessonBin     *gst.Bin
	lessonPad     *gst.Pad
	lessonSinkPad *gst.Pad
	lessonDecode  *gst.Element

	currentMode    string // "png" or "lesson" or "hls" or "playlist"
	currentLesson  string
	currentStarted time.Time
	lessonLoop     bool
	trackMap       map[string]int
}

func NewGoGstManager(m AudioMap) *goGstManager {
	return &goGstManager{
		audioMap:    m,
		currentMode: "png", // Start with PNG
	}
}

func (m *goGstManager) BuildPipeline() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Build a simple PNG-only pipeline by default
	return m.buildPNGOnlyPipeline()
}

func (m *goGstManager) SwitchToLesson(path string, loop bool, trackMap map[string]int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if path == "" {
		return fmt.Errorf("path is required")
	}
	log.Printf("SwitchToLesson: requested path=%s loop=%t", path, loop)

	// Fully tear down any existing pipeline and build minimal lesson→decklink graph.
	if m.pipeline != nil {
		_ = m.pipeline.SetState(gst.StateNull)
	}
	m.pipeline = nil
	m.vsel = nil
	m.deckBin = nil
	m.pngBin = nil
	m.pngPad = nil
	m.pngSinkPad = nil
	m.lessonBin = nil
	m.lessonPad = nil
	m.lessonSinkPad = nil
	m.lessonDecode = nil

	// Set loop parameters BEFORE building pipeline so bus watch can access them
	m.currentMode = "lesson"
	m.currentLesson = path
	m.currentStarted = time.Now()
	m.lessonLoop = loop
	m.trackMap = trackMap

	if err := m.buildLessonOnlyPipeline(path); err != nil {
		return fmt.Errorf("build lesson-only pipeline: %w", err)
	}

	log.Printf("SwitchToLesson: now playing %s", path)
	return nil
}

func (m *goGstManager) SwitchToPNG() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	log.Printf("SwitchToPNG: requested")

	// Fully tear down any existing pipeline
	if m.pipeline != nil {
		_ = m.pipeline.SetState(gst.StateNull)
	}
	m.pipeline = nil
	m.vsel = nil
	m.deckBin = nil
	m.pngBin = nil
	m.pngPad = nil
	m.pngSinkPad = nil
	m.lessonBin = nil
	m.lessonPad = nil
	m.lessonSinkPad = nil
	m.lessonDecode = nil

	m.currentMode = "png"
	m.currentLesson = ""
	m.currentStarted = time.Now()
	m.lessonLoop = false

	if err := m.buildPNGOnlyPipeline(); err != nil {
		return fmt.Errorf("build PNG-only pipeline: %w", err)
	}

	log.Printf("SwitchToPNG: now displaying PNG")
	return nil
}

func (m *goGstManager) SwitchToClip(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if path == "" {
		return fmt.Errorf("path is required")
	}
	log.Printf("SwitchToClip: requested path=%s", path)

	if m.pipeline != nil {
		_ = m.pipeline.SetState(gst.StateNull)
	}
	m.pipeline = nil
	m.vsel = nil
	m.deckBin = nil
	m.pngBin = nil
	m.pngPad = nil
	m.pngSinkPad = nil
	m.lessonBin = nil
	m.lessonPad = nil
	m.lessonSinkPad = nil
	m.lessonDecode = nil

	m.currentMode = "clip"
	m.currentLesson = path
	m.currentStarted = time.Now()
	m.lessonLoop = true

	if err := m.buildClipOnlyPipeline(path); err != nil {
		return fmt.Errorf("build clip-only pipeline: %w", err)
	}

	log.Printf("SwitchToClip: now playing %s", path)
	return nil
}

func (m *goGstManager) SwitchToHLS(url string, gstCommand string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if url == "" {
		return fmt.Errorf("HLS URL is required")
	}
	log.Printf("SwitchToHLS: requested url=%s", url)

	// Fully tear down any existing pipeline
	if m.pipeline != nil {
		_ = m.pipeline.SetState(gst.StateNull)
	}
	m.pipeline = nil
	m.vsel = nil
	m.deckBin = nil
	m.pngBin = nil
	m.pngPad = nil
	m.pngSinkPad = nil
	m.lessonBin = nil
	m.lessonPad = nil
	m.lessonSinkPad = nil
	m.lessonDecode = nil

	m.currentMode = "hls"
	m.currentLesson = url
	m.currentStarted = time.Now()
	m.lessonLoop = false

	if err := m.buildHLSPipeline(url, gstCommand); err != nil {
		return fmt.Errorf("build HLS pipeline: %w", err)
	}

	log.Printf("SwitchToHLS: now playing %s", url)
	return nil
}

var (
	playlistPrepState      PlaylistPreparedState
	playlistPrepStateMu    sync.Mutex
	playlistPrerollPath    string
	playlistCurrentIndex   int
	playlistCurrentFile    string
	playlistCurrentStarted time.Time
	playlistFiles          []string
	playlistFilesMu        sync.Mutex
)

func init() {
	playlistPrerollPath = os.Getenv("PREROLL")
	if playlistPrerollPath == "" {
		playlistPrerollPath = "/opt/backup/preroll.mp4"
	}
}

func (m *goGstManager) PreparePlaylist(urls []string, label string) error {
	// Start async preparation
	go m.preparePlaylistAsync(urls, label)
	return nil
}

func (m *goGstManager) preparePlaylistAsync(urls []string, label string) {
	playlistDir := os.Getenv("PLAYLIST_DIR")
	if playlistDir == "" {
		playlistDir = "/opt/backup/playlist"
	}

	playlistPrepStateMu.Lock()
	playlistPrepState = PlaylistPreparedState{
		Preparing:    true,
		Label:        label,
		URLs:         urls,
		URLCount:     len(urls),
		PlaylistDir:  playlistDir,
		StartedAtISO: time.Now().Format("2006-01-02 15:04:05"),
		UpdatedAtISO: time.Now().Format("2006-01-02 15:04:05"),
	}
	playlistPrepStateMu.Unlock()

	log.Printf("playlist prepare: starting label=%s urls=%d", label, len(urls))

	// Clean up previous files
	os.RemoveAll(playlistDir)
	os.MkdirAll(playlistDir, 0755)

	// Find ffmpeg and ffprobe
	ffmpegBin := findBinary("ffmpeg", "/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg")
	ffprobeBin := findBinary("ffprobe", "/opt/homebrew/bin/ffprobe", "/usr/local/bin/ffprobe")

	var items []PlaylistPreparedItem
	successCount := 0
	pngCount := 0

	for i, url := range urls {
		idx := i + 1
		filename := fmt.Sprintf("url_%d.mp4", idx)
		filePath := fmt.Sprintf("%s/%s", playlistDir, filename)
		pngPath := fmt.Sprintf("%s/last_frame-%d.png", playlistDir, idx)

		log.Printf("playlist prepare: downloading %d/%d: %s", idx, len(urls), url)

		// Download file
		cmd := exec.Command("curl", "-L", "-o", filePath, url)
		if err := cmd.Run(); err != nil {
			log.Printf("playlist prepare: download failed for %s: %v", url, err)
			continue
		}

		successCount++
		item := PlaylistPreparedItem{
			Index:      idx,
			URL:        url,
			File:       filePath,
			PNG:        pngPath,
			FileExists: true,
		}

		// Get final URL
		finalURLCmd := exec.Command("curl", "-Ls", "-o", "/dev/null", "-w", "%{url_effective}", url)
		if finalURLBytes, err := finalURLCmd.Output(); err == nil {
			item.FinalURL = strings.TrimSpace(string(finalURLBytes))
			if item.FinalURL != "" {
				urlParts := strings.Split(item.FinalURL, "?")
				item.OriginalName = filepath.Base(urlParts[0])
			}
		}

		// Extract last frame with ffmpeg
		if ffmpegBin != "" {
			log.Printf("playlist prepare: extracting last frame for %d", idx)
			ffmpegCmd := exec.Command(ffmpegBin, "-y", "-sseof", "-1", "-i", filePath, "-frames:v", "1", "-qscale:v", "2", pngPath)
			if err := ffmpegCmd.Run(); err == nil {
				item.PNGExists = true
				pngCount++
				log.Printf("playlist prepare: last frame extracted: %s", pngPath)
			} else {
				log.Printf("playlist prepare: ffmpeg last-frame failed for %s: %v", filePath, err)
			}
		}

		// Get duration with ffprobe
		if ffprobeBin != "" {
			probeCmd := exec.Command(ffprobeBin, "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", filePath)
			if durBytes, err := probeCmd.Output(); err == nil {
				durStr := strings.TrimSpace(string(durBytes))
				if dur, err := strconv.ParseFloat(durStr, 64); err == nil {
					sec := int(dur)
					h := sec / 3600
					m := (sec % 3600) / 60
					s := sec % 60
					item.DurationHMS = fmt.Sprintf("%02d:%02d:%02d", h, m, s)
				}
			}
		}

		// Get file mtime
		if stat, err := os.Stat(filePath); err == nil {
			item.FileMtimeISO = stat.ModTime().Format("2006-01-02 15:04:05")
		}

		items = append(items, item)

		// Update state
		playlistPrepStateMu.Lock()
		playlistPrepState.PreparedFiles = successCount
		playlistPrepState.PreparedPNGs = pngCount
		playlistPrepState.Items = items
		playlistPrepState.UpdatedAtISO = time.Now().Format("2006-01-02 15:04:05")
		playlistPrepStateMu.Unlock()
	}

	// Final state update
	playlistPrepStateMu.Lock()
	playlistPrepState.Preparing = false
	playlistPrepState.FileCount = successCount
	playlistPrepState.PreparedAtISO = time.Now().Format("2006-01-02 15:04:05")
	playlistPrepStateMu.Unlock()

	log.Printf("playlist prepare: completed label=%s files=%d pngs=%d", label, successCount, pngCount)
}

func findBinary(name string, paths ...string) string {
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func (m *goGstManager) LoadPlaylist(Playlist) error { return nil }
func (m *goGstManager) StartPlaylist() error {
	return m.startPlaylistPlayback()
}
func (m *goGstManager) NextItem() error { return nil }

func (m *goGstManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pipeline != nil {
		_ = m.pipeline.SetState(gst.StateNull)
		m.pipeline = nil
	}
	return nil
}

func (m *goGstManager) GetState() StateResponse {
	m.mu.Lock()
	defer m.mu.Unlock()

	mode := m.currentMode
	if mode == "" {
		mode = "png" // Default to PNG if not set
	}
	running := m.pipeline != nil
	file := m.currentLesson
	loop := m.lessonLoop

	// For playlist mode, use current playing file
	playlistFilesMu.Lock()
	if mode == "playlist" && playlistCurrentFile != "" {
		file = playlistCurrentFile
	}
	playlistFilesMu.Unlock()

	rerunPNG := os.Getenv("RERUN_PNG")
	overlayEnabled := rerunPNG != "" && mode == "lesson" // Only show overlay for lessons

	// Get playlist state
	playlistPrepStateMu.Lock()
	var prepState *PlaylistPreparedState
	if playlistPrepState.URLCount > 0 {
		prepStateCopy := playlistPrepState
		prepState = &prepStateCopy
	}
	playlistPrepStateMu.Unlock()

	// Calculate started_at for current item
	var startedAtISO string
	if mode == "playlist" {
		playlistFilesMu.Lock()
		if !playlistCurrentStarted.IsZero() {
			startedAtISO = playlistCurrentStarted.Format("2006-01-02 15:04:05")
		}
		playlistFilesMu.Unlock()
	} else {
		// For lesson, HLS, PNG modes, use currentStarted
		if !m.currentStarted.IsZero() {
			startedAtISO = m.currentStarted.Format("2006-01-02 15:04:05")
		}
	}

	return StateResponse{
		Playout: PlayoutState{
			Running:        running,
			Mode:           mode,
			File:           file,
			PID:            os.Getpid(),
			Loop:           loop,
			Overlay:        rerunPNG,
			OverlayEnabled: overlayEnabled,
			StartedAtISO:   startedAtISO,
			HeartbeatISO:   time.Now().Format("2006-01-02 15:04:05"),
		},
		Jack: JackState{
			Running:  true, // Assume JACK is running if controller is running
			PID:      0,    // We don't track JACK PID
			Rate:     48000,
			Buffer:   256,
			NPeriods: 3,
			Source:   "assumed",
		},
		Capture: CaptureState{
			Running: false, // Capture is handled separately
		},
		PlaylistLastPrepared: prepState,
	}
}

// setActivePad selects the requested sink pad on the input-selector.
func setActivePad(vsel *gst.Element, sinkPad *gst.Pad) error {
	if vsel == nil || sinkPad == nil {
		return fmt.Errorf("selector or pad missing")
	}
	selPtr := (*C.GstElement)(unsafe.Pointer(vsel.Instance()))
	padPtr := (*C.GstPad)(unsafe.Pointer(sinkPad.Instance()))
	if C.set_active_pad(selPtr, padPtr) == 0 {
		return fmt.Errorf("input-selector refused pad %s", sinkPad.GetName())
	}
	return nil
}

// getCurrentActivePad returns the name of the currently active pad on the input-selector
func getCurrentActivePad(vsel *gst.Element) string {
	if vsel == nil {
		return "<nil>"
	}
	activePad, err := vsel.GetProperty("active-pad")
	if err != nil || activePad == nil {
		return "<none>"
	}
	if pad, ok := activePad.(*gst.Pad); ok {
		return pad.GetName()
	}
	return "<unknown>"
}

func logBusErrors(label string, pipe *gst.Pipeline) {
	if pipe == nil {
		return
	}
	bus := pipe.GetBus()
	if bus == nil {
		return
	}
	for {
		msg := bus.Pop()
		if msg == nil {
			break
		}
		if msg.Type() == gst.MessageError {
			err := msg.ParseError()
			if err != nil {
				log.Printf("%s bus error: %v", label, err)
			}
		}
	}
}

// buildDecklinkBin creates the videoconvert/interlace chain terminating in
// decklinkvideosink. The bin exposes a single "sink" ghost pad.
func buildDecklinkBin() (*gst.Bin, error) {
	modeName := decklinkModeString()
	modeID := decklinkModeEnum(modeName)
	sinkIdent := decklinkSinkIdentifier()

	var desc string
	if strings.Contains(strings.ToLower(modeName), "i") {
		// Interlaced path for 1080i50; interlace only here, no videorate/caps here (already in lesson bin)
		desc = fmt.Sprintf(
			"queue name=decklink_in max-size-buffers=0 max-size-bytes=0 max-size-time=0 leaky=2 ! interlace field-pattern=2:2 top-field-first=true ! decklinkvideosink mode=%d %s sync=true",
			modeID, sinkIdent,
		)
	} else {
		// Progressive path for 1080p25
		desc = fmt.Sprintf(
			"queue name=decklink_in max-size-buffers=0 max-size-bytes=0 max-size-time=0 leaky=2 ! identity ! decklinkvideosink mode=%d %s sync=true",
			modeID, sinkIdent,
		)
	}

	parsed, err := gst.NewBinFromString(desc, false)
	if err != nil {
		return nil, fmt.Errorf("parse decklink bin: %w", err)
	}

	deckBin := gst.NewBin("decklink_chain")
	if err := deckBin.Add(parsed.Element); err != nil {
		return nil, fmt.Errorf("embed decklink bin: %w", err)
	}

	queueElem, err := parsed.GetElementByName("decklink_in")
	if err != nil {
		return nil, fmt.Errorf("find decklink queue: %w", err)
	}
	queueSink := queueElem.GetStaticPad("sink")
	if queueSink == nil {
		return nil, fmt.Errorf("decklink queue missing sink pad")
	}
	ghost := gst.NewGhostPad("sink", queueSink)
	if ghost == nil || !deckBin.AddPad(ghost.Pad) {
		return nil, fmt.Errorf("add ghost pad")
	}

	return deckBin, nil
}

func capsString(elem *gst.Element) string {
	if elem == nil {
		return "<nil>"
	}
	pad := elem.GetStaticPad("src")
	if pad == nil {
		pad = elem.GetStaticPad("sink")
	}
	if pad == nil {
		return "<no pad>"
	}
	c := pad.GetCurrentCaps()
	if c == nil {
		return "<no caps>"
	}
	return c.String()
}

func padCaps(pad *gst.Pad) string {
	if pad == nil {
		return "<nil>"
	}
	c := pad.GetCurrentCaps()
	if c == nil {
		return "<no caps>"
	}
	return c.String()
}

func allowedCaps(pad *gst.Pad) string {
	if pad == nil {
		return "<nil>"
	}
	c := pad.GetAllowedCaps()
	if c == nil {
		return "<no allowed caps>"
	}
	return c.String()
}

func sinkTemplateCaps(deck *gst.Element) *gst.Caps {
	if deck == nil {
		return nil
	}
	templates := deck.GetPadTemplates()
	for _, tmpl := range templates {
		if tmpl.Direction() == gst.PadDirectionSink {
			return tmpl.Caps()
		}
	}
	return nil
}

func capsToString(c *gst.Caps) string {
	if c == nil {
		return "<nil>"
	}
	return c.String()
}

func capValueHint() *gst.Caps {
	return gst.NewCapsFromString("video/x-raw,format=UYVY,width=1920,height=1080,interlace-mode=interleaved,framerate=25/1")
}

func selectDecklinkCaps(deck *gst.Element, hint *gst.Caps) *gst.Caps {
	tmpl := sinkTemplateCaps(deck)
	if tmpl == nil && hint == nil {
		return nil
	}

	var chosen *gst.Caps
	switch {
	case tmpl == nil:
		chosen = hint.Copy()
	case hint == nil:
		chosen = tmpl.Copy()
	default:
		intersection := hint.Intersect(tmpl)
		if intersection != nil && !intersection.IsEmpty() {
			chosen = intersection
		} else {
			chosen = hint.Copy()
		}
	}

	if chosen == nil {
		return nil
	}

	return sanitizeDecklinkCaps(chosen.Fixate())
}

func sanitizeDecklinkCaps(c *gst.Caps) *gst.Caps {
	if c == nil {
		return nil
	}

	writable := c.MakeWritable()
	if writable == nil {
		return c
	}

	fieldsToDrop := []string{"chroma-site", "colorimetry", "pixel-aspect-ratio"}
	for i := 0; i < writable.GetSize(); i++ {
		st := writable.GetStructureAt(i)
		if st == nil {
			continue
		}
		for _, field := range fieldsToDrop {
			st.RemoveValue(field)
		}
	}

	return writable
}

func parseEnvInt(key string, def int) int {
	val := os.Getenv(key)
	if val == "" {
		return def
	}
	if n, err := strconv.Atoi(val); err == nil {
		return n
	}
	// Accept decklink mode strings like "1080i50" by mapping to known IDs
	switch strings.ToLower(val) {
	case "1080i50":
		return 11
	case "1080p25":
		return 8
	case "1080p30":
		return 9
	case "1080p50":
		return 10
	case "1080i5994":
		return 12
	case "1080p5994":
		return 13
	default:
		return def
	}
}

func decklinkModeString() string {
	val := os.Getenv("DECKLINK_MODE")
	if val == "" {
		return "1080i50"
	}
	return val
}

func decklinkDeviceNumber() int {
	devStr := os.Getenv("DECKLINK_DEVICE")
	if devStr == "" {
		return 0
	}
	dev, convErr := strconv.Atoi(devStr)
	if convErr != nil {
		log.Printf("warning: invalid DECKLINK_DEVICE=%q: %v", devStr, convErr)
		return 0
	}
	return dev
}

func toFileURI(path string) string {
	if strings.HasPrefix(path, "file://") {
		return path
	}
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	replacer := strings.NewReplacer(" ", "%20")
	return "file://" + replacer.Replace(path)
}

func buildPngBin() (*gst.Bin, *gst.Pad, error) {
	// Use PNG video file (same format as lesson files) for seamless switching
	// Check PNG_VIDEO_PATH first, then default location
	pngVideoPath := os.Getenv("PNG_VIDEO_PATH")
	if pngVideoPath == "" {
		pngVideoPath = "/opt/backup/png-video.mp4"
	}

	if _, err := os.Stat(pngVideoPath); os.IsNotExist(err) {
		log.Printf("PNG video file not found at %s, falling back to PNG image method", pngVideoPath)
		// Fallback to old PNG image method if video file doesn't exist
		return buildPngImageBin()
	}

	log.Printf("Using PNG video file: %s", pngVideoPath)
	// Build PNG bin using the video file (same as lesson files)
	return buildLessonExplicitBin(pngVideoPath)
}

func buildPngImageBin() (*gst.Bin, *gst.Pad, error) {
	bin := gst.NewBin("pngbin")

	pngPath := os.Getenv("PNG_PATH")
	if pngPath != "" {
		src, err := gst.NewElement("filesrc")
		if err != nil {
			return nil, nil, err
		}
		_ = src.SetProperty("location", pngPath)
		pngdec, err := gst.NewElement("pngdec")
		if err != nil {
			return nil, nil, err
		}
		freeze, _ := gst.NewElement("imagefreeze")
		vs, _ := gst.NewElement("videoscale")
		vr, _ := gst.NewElement("videorate")
		vc, _ := gst.NewElement("videoconvert")
		caps, err := gst.NewElement("capsfilter")
		if err != nil {
			return nil, nil, err
		}
		if caps != nil {
			c := gst.NewCapsFromString("video/x-raw,format=UYVY,width=1920,height=1080,framerate=25/1")
			caps.SetProperty("caps", c)
		}
		if err := bin.AddMany(src, pngdec, freeze, vs, vr, vc, caps); err != nil {
			return nil, nil, err
		}
		if err := gst.ElementLinkMany(src, pngdec, freeze, vs, vr, vc, caps); err != nil {
			return nil, nil, fmt.Errorf("link png chain: %w", err)
		}
		if p := caps.GetStaticPad("src"); p != nil {
			gp := gst.NewGhostPad("src", p)
			if gp == nil || !bin.AddPad(gp.Pad) {
				return nil, nil, fmt.Errorf("add png ghost pad")
			}
			return bin, gp.Pad, nil
		}
		return nil, nil, fmt.Errorf("png caps src pad missing")
	}

	vtest, err := gst.NewElement("videotestsrc")
	if err != nil {
		return nil, nil, err
	}
	_ = vtest.SetProperty("pattern", 1) // SMPTE bars
	vc, _ := gst.NewElement("videoconvert")
	caps, err := gst.NewElement("capsfilter")
	if err != nil {
		return nil, nil, err
	}
	if caps != nil {
		c := gst.NewCapsFromString("video/x-raw,format=UYVY,width=1920,height=1080,framerate=25/1")
		caps.SetProperty("caps", c)
	}
	if err := bin.AddMany(vtest, vc, caps); err != nil {
		return nil, nil, err
	}
	if err := gst.ElementLinkMany(vtest, vc, caps); err != nil {
		return nil, nil, fmt.Errorf("link png fallback: %w", err)
	}
	if p := caps.GetStaticPad("src"); p != nil {
		gp := gst.NewGhostPad("src", p)
		if gp == nil || !bin.AddPad(gp.Pad) {
			return nil, nil, fmt.Errorf("add fallback ghost pad")
		}
		return bin, gp.Pad, nil
	}
	return nil, nil, fmt.Errorf("png fallback src pad missing")
}

func buildFileBin() (*gst.Bin, *gst.Pad, *gst.Element, error) {
	bin := gst.NewBin("lessonbin")

	dec, err := gst.NewElement("uridecodebin")
	if err != nil {
		return nil, nil, nil, err
	}
	_ = dec.SetProperty("expose-all-streams", false)

	queue, err := gst.NewElement("queue")
	if err != nil {
		return nil, nil, nil, err
	}
	vc, err := gst.NewElement("videoconvert")
	if err != nil {
		return nil, nil, nil, err
	}
	vs, err := gst.NewElement("videoscale")
	if err != nil {
		return nil, nil, nil, err
	}
	vr, err := gst.NewElement("videorate")
	if err != nil {
		return nil, nil, nil, err
	}
	caps, err := gst.NewElement("capsfilter")
	if err != nil {
		return nil, nil, nil, err
	}
	if caps != nil {
		c := gst.NewCapsFromString("video/x-raw,format=UYVY,width=1920,height=1080,framerate=25/1")
		caps.SetProperty("caps", c)
	}

	if err := bin.AddMany(dec, queue, vc, vs, vr, caps); err != nil {
		return nil, nil, nil, err
	}

	dec.Connect("pad-added", func(self *gst.Element, pad *gst.Pad) {
		if pad == nil {
			return
		}
		caps := pad.GetCurrentCaps()
		if caps == nil {
			return
		}
		if caps.GetSize() == 0 {
			return
		}
		for i := 0; i < caps.GetSize(); i++ {
			st := caps.GetStructureAt(i)
			if st == nil {
				continue
			}
			if !strings.HasPrefix(st.Name(), "video/") {
				continue
			}
			log.Printf("lesson decodebin pad-added: %s", st.Name())
			if sink := queue.GetStaticPad("sink"); sink != nil {
				if pad.Link(sink) != gst.PadLinkOK {
					log.Printf("decodebin pad link failed: %s", st.Name())
				} else {
					log.Printf("lesson decodebin linked %s to queue", st.Name())
				}
			}
			break
		}
	})

	if err := gst.ElementLinkMany(queue, vc, vs, vr, caps); err != nil {
		return nil, nil, nil, fmt.Errorf("link lesson chain: %w", err)
	}
	if caps != nil {
		if sinkPad := caps.GetStaticPad("sink"); sinkPad != nil {
			sinkPad.AddProbe(gst.PadProbeTypeBuffer, func(p *gst.Pad, info *gst.PadProbeInfo) gst.PadProbeReturn {
				log.Printf("lesson capsfilter buffer caps=%s", padCaps(p))
				if peer := p.GetPeer(); peer != nil {
					log.Printf("lesson capsfilter peer caps=%s", padCaps(peer))
				}
				return gst.PadProbeRemove
			})
		}
	}

	if p := caps.GetStaticPad("src"); p != nil {
		gp := gst.NewGhostPad("src", p)
		if gp == nil || !bin.AddPad(gp.Pad) {
			return nil, nil, nil, fmt.Errorf("add lesson ghost pad failed")
		}
		return bin, gp.Pad, dec, nil
	}

	return nil, nil, nil, fmt.Errorf("lesson caps src pad missing")
}

// buildLessonExplicitBin builds an explicit MP4/H264 decoding chain:
// filesrc → qtdemux → queue → h264parse → avdec_h264 → videoconvert → videoscale → videorate → capsfilter
// Returns the bin and its ghost src pad.
func buildLessonExplicitBin(path string) (*gst.Bin, *gst.Pad, error) {
	// Use unique bin name to avoid conflicts when switching lessons
	binName := fmt.Sprintf("lessonbin_explicit_%d", time.Now().UnixNano())
	bin := gst.NewBin(binName)

	src, err := gst.NewElement("filesrc")
	if err != nil {
		return nil, nil, err
	}
	// filesrc expects a plain path, not a URI
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	_ = src.SetProperty("location", path)

	demux, err := gst.NewElement("qtdemux")
	if err != nil {
		return nil, nil, err
	}

	vq, err := gst.NewElement("queue")
	if err != nil {
		return nil, nil, err
	}
	h264p, err := gst.NewElement("h264parse")
	if err != nil {
		return nil, nil, err
	}
	// Try to push SPS/PPS regularly in case downstream needs it
	_ = h264p.SetProperty("config-interval", -1)

	dec, err := gst.NewElement("avdec_h264")
	if err != nil {
		return nil, nil, err
	}
	vc, err := gst.NewElement("videoconvert")
	if err != nil {
		return nil, nil, err
	}
	vs, err := gst.NewElement("videoscale")
	if err != nil {
		return nil, nil, err
	}
	vr, err := gst.NewElement("videorate")
	if err != nil {
		return nil, nil, err
	}

	// Add overlay if RERUN_PNG is set (only for lessons, not PNG video)
	rerunPNG := os.Getenv("RERUN_PNG")
	var overlay *gst.Element
	if rerunPNG != "" && !strings.Contains(path, "png-video.mp4") {
		overlay, err = gst.NewElement("gdkpixbufoverlay")
		if err == nil {
			_ = overlay.SetProperty("location", rerunPNG)
		} else {
			log.Printf("warning: could not create overlay element: %v", err)
		}
	}

	caps, err := gst.NewElement("capsfilter")
	if err != nil {
		return nil, nil, err
	}
	if caps != nil {
		c := gst.NewCapsFromString("video/x-raw,format=UYVY,width=1920,height=1080,interlace-mode=progressive,framerate=25/1")
		caps.SetProperty("caps", c)
	}

	elements := []*gst.Element{src, demux, vq, h264p, dec, vc, vs, vr, caps}
	if overlay != nil {
		elements = []*gst.Element{src, demux, vq, h264p, dec, vc, vs, vr, overlay, caps}
	}
	if err := bin.AddMany(elements...); err != nil {
		return nil, nil, err
	}
	if err := gst.ElementLinkMany(src, demux); err != nil {
		return nil, nil, err
	}

	// Dynamic pad from qtdemux → queue
	demux.Connect("pad-added", func(self *gst.Element, pad *gst.Pad) {
		if pad == nil {
			return
		}
		caps := pad.GetCurrentCaps()
		if caps == nil || caps.GetSize() == 0 {
			return
		}
		for i := 0; i < caps.GetSize(); i++ {
			st := caps.GetStructureAt(i)
			if st == nil {
				continue
			}
			if !strings.HasPrefix(st.Name(), "video/") && st.Name() != "video/x-h264" {
				continue
			}
			log.Printf("lesson demux pad-added: %s", st.Name())
			if sink := vq.GetStaticPad("sink"); sink != nil {
				if pad.Link(sink) != gst.PadLinkOK {
					log.Printf("qtdemux pad link failed: %s", st.Name())
				}
			}
			break
		}
	})

	// VIDEO-ONLY TEST: Skip audio setup for now
	log.Printf("VIDEO-ONLY MODE: Building lesson bin without audio sinks")

	// Link video chain (with or without overlay)
	if overlay != nil {
		if err := gst.ElementLinkMany(vq, h264p, dec, vc, vs, vr, overlay, caps); err != nil {
			return nil, nil, fmt.Errorf("link explicit lesson chain with overlay: %w", err)
		}
	} else {
		if err := gst.ElementLinkMany(vq, h264p, dec, vc, vs, vr, caps); err != nil {
			return nil, nil, fmt.Errorf("link explicit lesson chain: %w", err)
		}
	}
	if p := caps.GetStaticPad("src"); p != nil {
		// Log first buffer to confirm flow
		if sp := caps.GetStaticPad("sink"); sp != nil {
			sp.AddProbe(gst.PadProbeTypeBuffer, func(p *gst.Pad, info *gst.PadProbeInfo) gst.PadProbeReturn {
				log.Printf("lesson capsfilter buffer caps=%s", padCaps(p))
				if peer := p.GetPeer(); peer != nil {
					log.Printf("lesson capsfilter peer caps=%s", padCaps(peer))
				}
				return gst.PadProbeRemove
			})
		}
		gp := gst.NewGhostPad("src", p)
		if gp == nil || !bin.AddPad(gp.Pad) {
			return nil, nil, fmt.Errorf("add explicit lesson ghost pad failed")
		}
		return bin, gp.Pad, nil
	}
	return nil, nil, fmt.Errorf("explicit lesson caps src pad missing")
}

func newDecklinkSink() (*gst.Element, error) {
	mode := decklinkModeString()
	sink, err := gst.NewElement("decklinkvideosink")
	if err != nil {
		return nil, err
	}
	if mode != "" {
		if err := sink.SetProperty("mode", decklinkModeEnum(mode)); err != nil {
			return nil, fmt.Errorf("set decklink mode: %w", err)
		}
	}
	if devStr := os.Getenv("DECKLINK_DEVICE"); devStr != "" {
		if dev, err := strconv.Atoi(devStr); err == nil {
			if err := sink.SetProperty("device-number", dev); err != nil {
				return nil, fmt.Errorf("set decklink device: %w", err)
			}
		}
	}
	_ = sink.SetProperty("sync", true)
	return sink, nil
}

func decklinkModeEnum(name string) int {
	switch strings.ToLower(name) {
	case "ntsc":
		return 1
	case "ntsc2398":
		return 2
	case "pal":
		return 3
	case "ntsc-p":
		return 4
	case "pal-p":
		return 5
	case "1080p2398":
		return 6
	case "1080p24":
		return 7
	case "1080p25":
		return 8
	case "1080p2997":
		return 9
	case "1080p30":
		return 10
	case "1080i50":
		return 11
	case "1080i5994":
		return 12
	default:
		return 0
	}
}

func decklinkSinkIdentifier() string {
	if pid := strings.TrimSpace(os.Getenv("DECKLINK_PERSISTENT_ID")); pid != "" {
		if _, err := strconv.ParseUint(pid, 10, 64); err != nil {
			log.Printf("warning: invalid DECKLINK_PERSISTENT_ID=%q: %v", pid, err)
		} else {
			return fmt.Sprintf("persistent-id=%s", pid)
		}
	}
	return fmt.Sprintf("device-number=%d", decklinkDeviceNumber())
}

func (m *goGstManager) ensureLessonBin() error {
	if m.lessonBin != nil && m.lessonSinkPad != nil && m.lessonDecode != nil {
		return nil
	}

	lessonBin, lessonPad, lessonDecode, err := buildFileBin()
	if err != nil {
		return fmt.Errorf("build lesson bin: %w", err)
	}
	if lessonDecode == nil {
		return fmt.Errorf("lesson decode element missing")
	}
	if err := m.pipeline.Add(lessonBin.Element); err != nil {
		return fmt.Errorf("add lesson bin: %w", err)
	}

	lessonSinkPad := m.vsel.GetRequestPad("sink_%u")
	if lessonSinkPad == nil {
		return fmt.Errorf("input-selector lesson pad request failed")
	}

	sourcePad := lessonPad
	if sourcePad == nil {
		sourcePad = lessonBin.Element.GetStaticPad("src")
	}
	if sourcePad == nil {
		return fmt.Errorf("lesson pad missing")
	}

	if sourcePad.Link(lessonSinkPad) != gst.PadLinkOK {
		allowed := allowedCaps(lessonSinkPad)
		current := padCaps(lessonPad)
		return fmt.Errorf("link lesson to selector failed (lesson=%s selector allowed=%s)", current, allowed)
	}

	log.Printf("SwitchToLesson: lesson bin added, sink pad type=%s", lessonSinkPad.GetName())
	log.Printf("SwitchToLesson: lesson pad caps=%s selector allowed=%s", padCaps(sourcePad), allowedCaps(lessonSinkPad))

	m.lessonBin = lessonBin
	m.lessonPad = sourcePad
	m.lessonSinkPad = lessonSinkPad
	m.lessonDecode = lessonDecode

	return nil
}

func (m *goGstManager) resetLessonBranch() error {
	if m.lessonPad == nil {
		return fmt.Errorf("lesson pad missing")
	}

	log.Printf("SwitchToLesson: resetting stream on pad=%s", m.lessonPad.GetName())

	// Build fallback caps immediately (UYVY 1920x1080p25)
	caps := lessonFallbackCaps()
	log.Printf("SwitchToLesson: sending immediate caps for sticky events %s", caps.String())

	// Targets: input-selector lesson sink and decklink bin sink
	var targets []*gst.Pad
	// Prefer decklink sink first
	if m.deckBin != nil {
		if p := m.deckBin.GetStaticPad("sink"); p != nil {
			targets = append(targets, p)
		}
	}
	if m.lessonSinkPad != nil {
		targets = append(targets, m.lessonSinkPad)
	}
	if len(targets) == 0 {
		return fmt.Errorf("no targets to send sticky events")
	}

	succeeded := false
	for _, p := range targets {
		if err := sendStickySequence(p, caps); err != nil {
			log.Printf("SwitchToLesson: sticky sequence on %s failed: %v", p.GetName(), err)
			continue
		}
		succeeded = true
	}
	if !succeeded {
		return fmt.Errorf("sticky events failed on all targets")
	}

	log.Printf("SwitchToLesson: sticky events sent on at least one target")
	return nil
}

func sendStickySequence(p *gst.Pad, caps *gst.Caps) error {
	if p == nil {
		return fmt.Errorf("pad nil")
	}
	if !p.SendEvent(gst.NewFlushStartEvent()) {
		return fmt.Errorf("flush-start failed")
	}
	if !p.SendEvent(gst.NewFlushStopEvent(false)) {
		return fmt.Errorf("flush-stop failed")
	}
	streamID := fmt.Sprintf("lesson-%d", time.Now().UnixNano())
	if !p.SendEvent(gst.NewStreamStartEvent(streamID)) {
		return fmt.Errorf("stream-start failed")
	}
	if caps == nil {
		return fmt.Errorf("caps nil")
	}
	if !p.SendEvent(gst.NewCapsEvent(caps.Copy())) {
		return fmt.Errorf("caps event failed")
	}
	seg := gst.NewSegment()
	seg.Init(gst.FormatTime)
	if !p.SendEvent(gst.NewSegmentEvent(seg)) {
		return fmt.Errorf("segment event failed")
	}
	log.Printf("SwitchToLesson: sticky events sent on %s (stream-id=%s)", p.GetName(), streamID)
	return nil
}

func lessonFallbackCaps() *gst.Caps {
	return gst.NewCapsFromString("video/x-raw,format=UYVY,width=1920,height=1080,interlace-mode=progressive,framerate=25/1")
}

func waitForLessonCaps(pad *gst.Pad, timeout time.Duration) *gst.Caps {
	if pad == nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	nextLog := time.Now()
	for time.Now().Before(deadline) {
		if caps := pad.GetCurrentCaps(); caps != nil {
			return caps.Copy()
		}
		if time.Now().After(nextLog) {
			log.Printf("SwitchToLesson: waiting for negotiated caps on %s", pad.GetName())
			nextLog = time.Now().Add(50 * time.Millisecond)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func (m *goGstManager) buildLessonOnlyPipeline(path string) error {
	gst.Init(nil)

	pipe, err := gst.NewPipeline("lesson-only")
	if err != nil {
		return err
	}

	modeID := decklinkModeEnum(decklinkModeString())
	sinkIdent := decklinkSinkIdentifier()
	rerunPNG := os.Getenv("RERUN_PNG")

	// Build overlay part if RERUN_PNG is set
	overlayPart := ""
	if rerunPNG != "" {
		overlayPart = fmt.Sprintf("gdkpixbufoverlay location=%s ! ", shellEscape(rerunPNG))
	}

	// Mirror working video path and add full Dante JACK fan-out mapping
	descTemplate := `filesrc location=%s ! qtdemux name=dem
dem.video_0 ! queue ! h264parse config-interval=-1 ! avdec_h264 ! videoconvert ! videoscale ! videorate !
video/x-raw,format=UYVY,width=1920,height=1080,framerate=25/1 ! %sinterlace field-pattern=2:2 top-field-first=true !
decklinkvideosink mode=%d %s sync=true
dem.audio_0  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=2,format=F32LE,layout=interleaved ! deinterleave name=heb
heb.src_0    ! queue ! jackaudiosink client-name=gst-heb-L port-pattern=20 sync=false
heb.src_1    ! queue ! jackaudiosink client-name=gst-heb-R port-pattern=21 sync=false
dem.audio_1  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-en  port-pattern=29 sync=false
dem.audio_2  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-ru  port-pattern=22 sync=false
dem.audio_3  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-es  port-pattern=30 sync=false
dem.audio_4  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-fr  port-pattern=1  sync=false
dem.audio_5  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-it  port-pattern=2  sync=false
dem.audio_6  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-de  port-pattern=3  sync=false
dem.audio_7  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-tr  port-pattern=4  sync=false
dem.audio_8  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-pt  port-pattern=5  sync=false
dem.audio_9  ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-bg  port-pattern=6  sync=false
dem.audio_10 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-ka  port-pattern=7  sync=false
dem.audio_11 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-ro  port-pattern=8  sync=false
dem.audio_12 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-hu  port-pattern=9  sync=false
dem.audio_13 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-sv  port-pattern=10 sync=false
dem.audio_14 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-lt  port-pattern=11 sync=false
dem.audio_15 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-hr  port-pattern=12 sync=false
dem.audio_16 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-ja  port-pattern=13 sync=false
dem.audio_17 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-sl  port-pattern=14 sync=false
dem.audio_18 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-pl  port-pattern=15 sync=false
dem.audio_19 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-no  port-pattern=16 sync=false
dem.audio_20 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-lv  port-pattern=17 sync=false
dem.audio_21 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-uk  port-pattern=18 sync=false
dem.audio_22 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-nl  port-pattern=19 sync=false
dem.audio_23 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-zh  port-pattern=23 sync=false
dem.audio_24 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-am  port-pattern=24 sync=false
dem.audio_25 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-hi  port-pattern=25 sync=false
dem.audio_26 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-fa  port-pattern=26 sync=false
dem.audio_27 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-ar  port-pattern=27 sync=false
dem.audio_28 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-id  port-pattern=28 sync=false
dem.audio_29 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-hy  port-pattern=32 sync=false
dem.audio_30 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-da  port-pattern=33 sync=false
dem.audio_31 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-et  port-pattern=34 sync=false
dem.audio_32 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-el  port-pattern=35 sync=false
dem.audio_33 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-tl  port-pattern=36 sync=false
dem.audio_34 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-az  port-pattern=37 sync=false`
	desc := fmt.Sprintf(descTemplate, filepath.Clean(path), overlayPart, modeID, sinkIdent)

	parsed, err := gst.NewBinFromString(desc, false)
	if err != nil {
		return fmt.Errorf("parse lesson-only bin: %w", err)
	}
	if err := pipe.Add(parsed.Element); err != nil {
		return fmt.Errorf("add lesson-only bin: %w", err)
	}

	if err := pipe.SetState(gst.StatePlaying); err != nil {
		logBusErrors("pipeline", pipe)
		return fmt.Errorf("set PLAYING: %w", err)
	}

	// Add bus message handler for EOS and loop functionality
	bus := pipe.GetBus()
	if bus != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("bus watch recovered from panic: %v", r)
				}
			}()

			for {
				// Check if pipeline still exists BEFORE trying to pop messages
				m.mu.Lock()
				stillActive := m.pipeline == pipe
				m.mu.Unlock()
				if !stillActive {
					log.Printf("bus watch exiting: pipeline changed")
					return
				}

				msg := bus.TimedPop(100 * time.Millisecond)
				if msg == nil {
					continue
				}

				switch msg.Type() {
				case gst.MessageEOS:
					m.mu.Lock()
					shouldLoop := m.lessonLoop
					loopPath := m.currentLesson
					m.mu.Unlock()

					log.Printf("lesson EOS received, loop=%t, path=%s", shouldLoop, loopPath)
					if shouldLoop && loopPath != "" {
						log.Printf("restarting lesson in loop mode")
						time.Sleep(100 * time.Millisecond)
						// Call SwitchToLesson without holding any locks
						if err := m.SwitchToLesson(loopPath, true, nil); err != nil {
							log.Printf("lesson loop restart failed: %v", err)
						} else {
							log.Printf("lesson loop restarted successfully")
						}
						return // Exit this bus watch, new one will be created
					} else {
						log.Printf("lesson ended, not looping (loop=%t, path=%s)", shouldLoop, loopPath)
					}
				case gst.MessageError:
					err := msg.ParseError()
					log.Printf("pipeline bus error: %v", err)
				}
			}
		}()
	}

	m.pipeline = pipe
	m.vsel = nil
	m.deckBin = nil
	m.lessonBin = parsed
	m.lessonPad = nil
	m.lessonSinkPad = nil
	m.lessonDecode = nil
	m.pngBin = nil
	m.pngPad = nil
	m.pngSinkPad = nil

	return nil
}

func (m *goGstManager) buildClipOnlyPipeline(path string) error {
	gst.Init(nil)

	pipe, err := gst.NewPipeline("clip-only")
	if err != nil {
		return err
	}

	modeID := decklinkModeEnum(decklinkModeString())
	sinkIdent := decklinkSinkIdentifier()

	// Video: same decode chain as lesson.
	// Audio: single stereo track decoded once, tee'd into:
	//   - deinterleave → Hebrew L (port 20) and R (port 21)
	//   - audioconvert downmix → mono tee → all 34 language ports
	descTemplate := `filesrc location=%s ! qtdemux name=dem
dem.video_0 ! queue ! h264parse config-interval=-1 ! avdec_h264 ! videoconvert ! videoscale ! videorate !
video/x-raw,format=UYVY,width=1920,height=1080,framerate=25/1 ! interlace field-pattern=2:2 top-field-first=true !
decklinkvideosink mode=%d %s sync=true
dem.audio_0 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample !
audio/x-raw,rate=48000,channels=2,format=F32LE,layout=interleaved ! tee name=stereo_tee
stereo_tee. ! queue ! deinterleave name=heb
heb.src_0 ! queue ! jackaudiosink client-name=gst-heb-L port-pattern=20 sync=false
heb.src_1 ! queue ! jackaudiosink client-name=gst-heb-R port-pattern=21 sync=false
stereo_tee. ! queue ! audioconvert ! audio/x-raw,rate=48000,channels=1,format=F32LE ! tee name=mono_tee
mono_tee. ! queue ! jackaudiosink client-name=gst-fr  port-pattern=1  sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-it  port-pattern=2  sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-de  port-pattern=3  sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-tr  port-pattern=4  sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-pt  port-pattern=5  sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-bg  port-pattern=6  sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-ka  port-pattern=7  sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-ro  port-pattern=8  sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-hu  port-pattern=9  sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-sv  port-pattern=10 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-lt  port-pattern=11 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-hr  port-pattern=12 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-ja  port-pattern=13 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-sl  port-pattern=14 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-pl  port-pattern=15 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-no  port-pattern=16 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-lv  port-pattern=17 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-uk  port-pattern=18 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-nl  port-pattern=19 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-ru  port-pattern=22 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-zh  port-pattern=23 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-am  port-pattern=24 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-hi  port-pattern=25 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-fa  port-pattern=26 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-ar  port-pattern=27 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-id  port-pattern=28 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-en  port-pattern=29 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-es  port-pattern=30 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-hy  port-pattern=32 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-da  port-pattern=33 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-et  port-pattern=34 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-el  port-pattern=35 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-tl  port-pattern=36 sync=false
mono_tee. ! queue ! jackaudiosink client-name=gst-az  port-pattern=37 sync=false`

	desc := fmt.Sprintf(descTemplate, filepath.Clean(path), modeID, sinkIdent)

	parsed, err := gst.NewBinFromString(desc, false)
	if err != nil {
		return fmt.Errorf("parse clip-only bin: %w", err)
	}
	if err := pipe.Add(parsed.Element); err != nil {
		return fmt.Errorf("add clip-only bin: %w", err)
	}

	if err := pipe.SetState(gst.StatePlaying); err != nil {
		logBusErrors("pipeline", pipe)
		return fmt.Errorf("set PLAYING: %w", err)
	}

	bus := pipe.GetBus()
	if bus != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("clip bus watch recovered from panic: %v", r)
				}
			}()

			plays := 0 // incremented on each EOS; seek on first, switch to PNG on second

			for {
				m.mu.Lock()
				stillActive := m.pipeline == pipe
				m.mu.Unlock()
				if !stillActive {
					log.Printf("clip bus watch exiting: pipeline changed")
					return
				}

				msg := bus.TimedPop(100 * time.Millisecond)
				if msg == nil {
					continue
				}

				switch msg.Type() {
				case gst.MessageEOS:
					m.mu.Lock()
					stillClip := m.pipeline == pipe && m.currentMode == "clip"
					m.mu.Unlock()

					if !stillClip {
						return
					}

					plays++
					if plays < 2 {
						log.Printf("clip EOS play %d/2, seeking to start", plays)
						seekEvent := gst.NewSeekEvent(
							1.0,
							gst.FormatTime,
							gst.SeekFlagFlush|gst.SeekFlagKeyUnit,
							gst.SeekTypeSet, 0,
							gst.SeekTypeNone, -1,
						)
						if !pipe.SendEvent(seekEvent) {
							log.Printf("clip seek failed")
						}
					} else {
						log.Printf("clip finished 2 plays, switching to PNG")
						if err := m.SwitchToPNG(); err != nil {
							log.Printf("clip→PNG switch failed: %v", err)
						}
						return
					}
				case gst.MessageError:
					err := msg.ParseError()
					log.Printf("clip pipeline bus error: %v", err)
				}
			}
		}()
	}

	m.pipeline = pipe
	m.vsel = nil
	m.deckBin = nil
	m.lessonBin = parsed
	m.lessonPad = nil
	m.lessonSinkPad = nil
	m.lessonDecode = nil
	m.pngBin = nil
	m.pngPad = nil
	m.pngSinkPad = nil

	return nil
}

// SwitchToPNG switches to PNG source using persistent pipeline (no tear-down).
func (m *goGstManager) buildPNGOnlyPipeline() error {
	gst.Init(nil)

	pipe, err := gst.NewPipeline("png-only")
	if err != nil {
		return err
	}

	modeID := decklinkModeEnum(decklinkModeString())
	sinkIdent := decklinkSinkIdentifier()
	pngPath := os.Getenv("PNG_PATH")
	if pngPath == "" {
		pngPath = "/tmp/test.png" // Fallback to test pattern if no PNG specified
	}

	// Simple PNG pipeline: filesrc -> pngdec -> imagefreeze -> videoconvert -> videoscale -> videorate -> caps -> interlace -> decklink
	// Plus silence audio to JACK
	descTemplate := `filesrc location=%s ! pngdec ! imagefreeze ! videoconvert ! videoscale ! videorate !
video/x-raw,format=UYVY,width=1920,height=1080,framerate=25/1 ! interlace field-pattern=2:2 top-field-first=true !
decklinkvideosink mode=%d %s sync=true
audiotestsrc wave=silence ! audio/x-raw,rate=48000,channels=1,format=F32LE ! jackaudiosink client-name=gst-silence port-pattern=31 sync=false`

	desc := fmt.Sprintf(descTemplate, filepath.Clean(pngPath), modeID, sinkIdent)

	parsed, err := gst.NewBinFromString(desc, false)
	if err != nil {
		return fmt.Errorf("parse png-only bin: %w", err)
	}
	if err := pipe.Add(parsed.Element); err != nil {
		return fmt.Errorf("add png-only bin: %w", err)
	}

	if err := pipe.SetState(gst.StatePlaying); err != nil {
		logBusErrors("pipeline", pipe)
		return fmt.Errorf("set PLAYING: %w", err)
	}

	m.pipeline = pipe
	m.vsel = nil
	m.deckBin = nil
	m.lessonBin = nil
	m.lessonPad = nil
	m.lessonSinkPad = nil
	m.lessonDecode = nil
	m.pngBin = parsed
	m.pngPad = nil
	m.pngSinkPad = nil

	return nil
}

func (m *goGstManager) buildHLSPipeline(url string, gstCommand string) error {
	gst.Init(nil)

	pipe, err := gst.NewPipeline("hls-pipeline")
	if err != nil {
		return err
	}

	var desc string

	// If we have a gst-launch command from the API, parse it
	if gstCommand != "" {
		log.Printf("HLS: Using pipeline from API cmd_exec")
		// Extract the pipeline description from gst-launch command
		// Format: "gst-launch-1.0 -vvv <pipeline>"
		desc = extractPipelineFromCommand(gstCommand)
		log.Printf("HLS: Extracted pipeline (first 200 chars): %s...", truncate(desc, 200))
	} else {
		// Fallback to generic HLS pipeline
		log.Printf("HLS: Using generic fallback pipeline")
		modeName := decklinkModeString()
		modeID := decklinkModeEnum(modeName)
		sinkIdent := decklinkSinkIdentifier()

		descTemplate := `
videomixer name=mix ! videoconvert ! interlace field-pattern=2:2 top-field-first=true ! decklinkvideosink mode=%d %s
souphttpsrc location=%s ! hlsdemux name=demux
demux.src_0 ! tsdemux ! queue ! h264parse ! vtdec ! mix.
demux.src_1 ! tsdemux ! queue ! aacparse ! fdkaacdec ! tee name=t
t. ! deinterleave name=d
d.src_0 ! queue ! audioconvert ! audioresample ! jackaudiosink client-name=gst-heb-L port-pattern=20 sync=false
d.src_1 ! queue ! audioconvert ! audioresample ! jackaudiosink client-name=gst-heb-R port-pattern=21 sync=false
demux.src_2 ! tsdemux ! queue ! aacparse ! fdkaacdec ! audioconvert ! audioresample ! jackaudiosink client-name=gst-en port-pattern=29 sync=false
demux.src_3 ! tsdemux ! queue ! aacparse ! fdkaacdec ! audioconvert ! audioresample ! jackaudiosink client-name=gst-ru port-pattern=22 sync=false
demux.src_4 ! tsdemux ! queue ! aacparse ! fdkaacdec ! audioconvert ! audioresample ! jackaudiosink client-name=gst-es port-pattern=30 sync=false
demux.src_5 ! tsdemux ! queue ! aacparse ! fdkaacdec ! audioconvert ! audioresample ! jackaudiosink client-name=gst-fr port-pattern=1 sync=false
demux.src_6 ! tsdemux ! queue ! aacparse ! fdkaacdec ! audioconvert ! audioresample ! jackaudiosink client-name=gst-it port-pattern=2 sync=false
demux.src_7 ! tsdemux ! queue ! aacparse ! fdkaacdec ! audioconvert ! audioresample ! jackaudiosink client-name=gst-de port-pattern=3 sync=false
demux.src_8 ! tsdemux ! queue ! aacparse ! fdkaacdec ! audioconvert ! audioresample ! jackaudiosink client-name=gst-pt port-pattern=5 sync=false
demux.src_9 ! tsdemux ! queue ! aacparse ! fdkaacdec ! audioconvert ! audioresample ! jackaudiosink client-name=gst-tr port-pattern=4 sync=false
t. ! audioconvert ! audioresample ! queue ! jackaudiosink client-name=gst-ar port-pattern=27 sync=false
t. ! audioconvert ! audioresample ! queue ! jackaudiosink client-name=gst-id port-pattern=28 sync=false
t. ! audioconvert ! audioresample ! queue ! jackaudiosink client-name=gst-null port-pattern=31 sync=false
t. ! audioconvert ! audioresample ! queue ! jackaudiosink client-name=gst-hy port-pattern=32 sync=false`

		desc = fmt.Sprintf(descTemplate, modeID, sinkIdent, url)
	}

	parsed, err := gst.NewBinFromString(desc, false)
	if err != nil {
		return fmt.Errorf("parse HLS bin: %w", err)
	}
	if err := pipe.Add(parsed.Element); err != nil {
		return fmt.Errorf("add HLS bin: %w", err)
	}

	if err := pipe.SetState(gst.StatePlaying); err != nil {
		logBusErrors("HLS pipeline", pipe)
		return fmt.Errorf("set HLS pipeline PLAYING: %w", err)
	}

	m.pipeline = pipe

	// Add bus message handler for EOS - switch back to PNG when HLS stream ends
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("HLS bus watch recovered from panic: %v", r)
			}
		}()

		bus := pipe.GetBus()
		if bus == nil {
			log.Printf("HLS: no bus available")
			return
		}

		for {
			msg := bus.TimedPop(100 * time.Millisecond)
			if msg == nil {
				// Check if pipeline still exists
				m.mu.Lock()
				stillActive := m.pipeline == pipe && m.currentMode == "hls"
				m.mu.Unlock()

				if !stillActive {
					log.Printf("HLS bus watch exiting: pipeline changed")
					return
				}
				continue
			}

			switch msg.Type() {
			case gst.MessageEOS:
				log.Printf("HLS stream finished (EOS), switching back to PNG")

				// Switch back to PNG
				go func() {
					if err := m.SwitchToPNG(); err != nil {
						log.Printf("HLS EOS: failed to switch to PNG: %v", err)
					} else {
						log.Printf("HLS EOS: switched to PNG successfully")
					}
				}()
				return

			case gst.MessageError:
				err := msg.ParseError()
				log.Printf("HLS pipeline error: %s", err.Error())

				// On error, also switch back to PNG
				log.Printf("HLS error detected, switching back to PNG")
				go func() {
					if err := m.SwitchToPNG(); err != nil {
						log.Printf("HLS error: failed to switch to PNG: %v", err)
					}
				}()
				return
			}
		}
	}()

	log.Printf("HLS pipeline started for %s", url)
	return nil
}

// extractPipelineFromCommand extracts the pipeline description from a gst-launch command
// Input: "gst-launch-1.0 -vvv videomixer name=mix ! ..."
// Output: "videomixer name=mix ! ..."
func extractPipelineFromCommand(cmd string) string {
	// Remove "gst-launch-1.0" and flags like "-vvv"
	parts := strings.Fields(cmd)
	var pipelineParts []string
	skipNext := false

	for i, part := range parts {
		if i == 0 && strings.Contains(part, "gst-launch") {
			continue // Skip gst-launch-1.0
		}
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(part, "-") {
			// Skip flags like -vvv, -e, etc.
			// Check if it's a flag with a value (like --gst-debug=2)
			if strings.Contains(part, "=") {
				continue
			}
			// Otherwise, skip this and potentially the next token
			continue
		}
		pipelineParts = append(pipelineParts, part)
	}

	return strings.Join(pipelineParts, " ")
}

// truncate returns the first n characters of s
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// startPlaylistPlayback plays all prepared files with preroll and PNG transitions
func (m *goGstManager) startPlaylistPlayback() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	playlistDir := os.Getenv("PLAYLIST_DIR")
	if playlistDir == "" {
		playlistDir = "/opt/backup/playlist"
	}

	// Find all MP4 files in playlist directory
	files, err := filepath.Glob(filepath.Join(playlistDir, "url_*.mp4"))
	if err != nil || len(files) == 0 {
		return fmt.Errorf("no playlist files found in %s", playlistDir)
	}

	// Sort files by name (url_1.mp4, url_2.mp4, etc.)
	sort.Strings(files)

	log.Printf("playlist playback: found %d files", len(files))

	// Tear down any existing pipeline
	if m.pipeline != nil {
		_ = m.pipeline.SetState(gst.StateNull)
	}
	m.pipeline = nil

	m.currentMode = "playlist"
	playlistFiles = files
	playlistCurrentIndex = 0

	if err := m.buildPlaylistPipeline(files); err != nil {
		return fmt.Errorf("build playlist pipeline: %w", err)
	}

	log.Printf("playlist playback: started with %d files", len(files))
	return nil
}

// buildPlaylistPipeline creates a concat-based pipeline for playlist playback
// Structure: [preroll -> file -> png] repeated for each file
func (m *goGstManager) buildPlaylistPipeline(files []string) error {
	gst.Init(nil)

	pipe, err := gst.NewPipeline("playlist-pipeline")
	if err != nil {
		return err
	}

	modeID := decklinkModeEnum(decklinkModeString())
	sinkIdent := decklinkSinkIdentifier()
	fps := 25
	pngDurationSec := 5
	pngFrames := fps * pngDurationSec
	pngAudioBuf := 100 * pngDurationSec

	playlistDir := os.Getenv("PLAYLIST_DIR")
	if playlistDir == "" {
		playlistDir = "/opt/backup/playlist"
	}

	// Start building the pipeline description
	// Video concat -> interlace -> decklink
	// Audio concat -> tee -> 36 JACK sinks
	desc := fmt.Sprintf(`concat name=vcat ! videoconvert ! videoscale ! videorate ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=%d/1 ! interlace field-pattern=2:2 top-field-first=true ! decklinkvideosink mode=%d %s sync=true`, fps, modeID, sinkIdent)

	// Audio tee to all JACK ports
	desc += ` concat name=acat ! tee name=audio_tee`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-heb-L port-pattern=20 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-heb-R port-pattern=21 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-en port-pattern=29 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-ru port-pattern=22 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-es port-pattern=30 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-fr port-pattern=1 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-it port-pattern=2 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-de port-pattern=3 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-tr port-pattern=4 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-pt port-pattern=5 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-bg port-pattern=6 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-ka port-pattern=7 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-ro port-pattern=8 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-hu port-pattern=9 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-sv port-pattern=10 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-lt port-pattern=11 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-hr port-pattern=12 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-ja port-pattern=13 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-sl port-pattern=14 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-pl port-pattern=15 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-no port-pattern=16 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-lv port-pattern=17 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-uk port-pattern=18 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-nl port-pattern=19 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-zh port-pattern=23 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-am port-pattern=24 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-hi port-pattern=25 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-fa port-pattern=26 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-ar port-pattern=27 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-id port-pattern=28 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-hy port-pattern=32 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-da port-pattern=33 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-et port-pattern=34 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-el port-pattern=35 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-tl port-pattern=36 sync=false`
	desc += ` audio_tee. ! queue ! jackaudiosink client-name=gst-az port-pattern=37 sync=false`

	sinkIndex := 0

	// For each file: add preroll (if exists), then file, then PNG
	for i, file := range files {
		itemNum := i + 1
		log.Printf("playlist: adding segments for item %d: %s", itemNum, file)

		// 1. Preroll video (if exists)
		if playlistPrerollPath != "" {
			if _, err := os.Stat(playlistPrerollPath); err == nil {
				desc += fmt.Sprintf(` filesrc location="%s" ! qtdemux name=p%d`, playlistPrerollPath, itemNum)
				desc += fmt.Sprintf(` p%d.video_0 ! queue ! h264parse ! avdec_h264 ! videoconvert ! videoscale ! videorate ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=%d/1 ! vcat.sink_%d`, itemNum, fps, sinkIndex)
				desc += fmt.Sprintf(` p%d.audio_0 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=2,format=F32LE,layout=interleaved ! acat.sink_%d`, itemNum, sinkIndex)
				sinkIndex++
			}
		}

		// 2. Main file
		desc += fmt.Sprintf(` filesrc location="%s" ! qtdemux name=u%d`, file, itemNum)
		desc += fmt.Sprintf(` u%d.video_0 ! queue ! h264parse ! avdec_h264 ! videoconvert ! videoscale ! videorate ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=%d/1 ! vcat.sink_%d`, itemNum, fps, sinkIndex)
		desc += fmt.Sprintf(` u%d.audio_0 ! queue ! aacparse ! avdec_aac ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=2,format=F32LE,layout=interleaved ! acat.sink_%d`, itemNum, sinkIndex)
		sinkIndex++

		// 3. PNG last frame (if exists)
		pngPath := fmt.Sprintf("%s/last_frame-%d.png", playlistDir, itemNum)
		if _, err := os.Stat(pngPath); err == nil {
			desc += fmt.Sprintf(` filesrc location="%s" ! decodebin ! queue ! imagefreeze ! videoscale ! videoconvert ! videorate ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=%d/1 ! identity eos-after=%d ! vcat.sink_%d`, pngPath, fps, pngFrames, sinkIndex)
			desc += fmt.Sprintf(` audiotestsrc wave=silence samplesperbuffer=480 num-buffers=%d ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=2,format=F32LE,layout=interleaved ! acat.sink_%d`, pngAudioBuf, sinkIndex)
			sinkIndex++
		} else {
			log.Printf("playlist: warning - PNG not found for item %d: %s", itemNum, pngPath)
		}
	}

	log.Printf("playlist: pipeline description length: %d chars", len(desc))

	parsed, err := gst.NewBinFromString(desc, false)
	if err != nil {
		return fmt.Errorf("parse playlist bin: %w", err)
	}
	if err := pipe.Add(parsed.Element); err != nil {
		return fmt.Errorf("add playlist bin: %w", err)
	}

	if err := pipe.SetState(gst.StatePlaying); err != nil {
		logBusErrors("playlist pipeline", pipe)
		return fmt.Errorf("set playlist pipeline PLAYING: %w", err)
	}

	m.pipeline = pipe

	// Set initial file and start time
	playlistFilesMu.Lock()
	if len(files) > 0 {
		playlistCurrentFile = filepath.Base(files[0])
		playlistCurrentStarted = time.Now()
		playlistCurrentIndex = 0
	}
	playlistFilesMu.Unlock()

	// Add bus message handler for EOS
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("playlist bus watch recovered from panic: %v", r)
			}
		}()

		bus := pipe.GetBus()
		if bus == nil {
			log.Printf("playlist: no bus available")
			return
		}

		for {
			msg := bus.TimedPop(100 * time.Millisecond)
			if msg == nil {
				// Check if pipeline still exists
				m.mu.Lock()
				stillActive := m.pipeline == pipe && m.currentMode == "playlist"
				m.mu.Unlock()

				if !stillActive {
					log.Printf("playlist bus watch exiting: pipeline changed")
					return
				}
				continue
			}

			switch msg.Type() {
			case gst.MessageEOS:
				log.Printf("playlist finished (EOS), starting lesson-1.mp4 with loop")
				go func() {
					// Find lesson-1.mp4 in LATEST_DIR
					latestDir := os.Getenv("LATEST_DIR")
					if latestDir == "" {
						latestDir = "/opt/backup/latest"
					}
					lessonPath := filepath.Join(latestDir, "lesson-1.mp4")

					if _, err := os.Stat(lessonPath); err == nil {
						if err := m.SwitchToLesson(lessonPath, true, nil); err != nil {
							log.Printf("playlist EOS: failed to start lesson-1: %v", err)
							// Fallback to PNG if lesson fails
							if err := m.SwitchToPNG(); err != nil {
								log.Printf("playlist EOS: failed to switch to PNG: %v", err)
							}
						} else {
							log.Printf("playlist EOS: started lesson-1.mp4 with loop")
						}
					} else {
						log.Printf("playlist EOS: lesson-1.mp4 not found at %s, switching to PNG", lessonPath)
						if err := m.SwitchToPNG(); err != nil {
							log.Printf("playlist EOS: failed to switch to PNG: %v", err)
						}
					}
				}()
				return

			case gst.MessageError:
				err := msg.ParseError()
				log.Printf("playlist pipeline error: %s", err.Error())
				go func() {
					if err := m.SwitchToPNG(); err != nil {
						log.Printf("playlist error: failed to switch to PNG: %v", err)
					}
				}()
				return
			}
		}
	}()

	log.Printf("playlist pipeline started")
	return nil
}
