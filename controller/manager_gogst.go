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
	"path/filepath"
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

	currentLesson string
	lessonLoop    bool
	trackMap      map[string]int
}

func NewGoGstManager(m AudioMap) *goGstManager {
	return &goGstManager{audioMap: m}
}

func (m *goGstManager) BuildPipeline() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.buildPipelineLocked()
}

func (m *goGstManager) buildPipelineLocked() error {
	if m.pipeline != nil {
		return nil
	}

	gst.Init(nil)

	pipe, err := gst.NewPipeline("lesson-pipeline")
	if err != nil {
		return err
	}

	vsel, err := gst.NewElement("input-selector")
	if err != nil {
		return err
	}
	if setErr := vsel.SetProperty("sync-streams", false); setErr != nil {
		log.Printf("warning: could not disable input-selector sync-streams: %v", setErr)
	}
	if setErr := vsel.SetProperty("drop-backwards", true); setErr != nil {
		log.Printf("warning: could not set input-selector drop-backwards: %v", setErr)
	}
	if err := pipe.Add(vsel); err != nil {
		return err
	}

	deckChain, err := buildDecklinkBin()
	if err != nil {
		return err
	}
	if err := pipe.Add(deckChain.Element); err != nil {
		return fmt.Errorf("add decklink chain: %w", err)
	}
	if err := gst.ElementLinkMany(vsel, deckChain.Element); err != nil {
		return fmt.Errorf("link selector to decklink: %w", err)
	}

	pngBin, pngPad, err := buildPngBin()
	if err != nil {
		return fmt.Errorf("build png bin: %w", err)
	}
	if err := pipe.Add(pngBin.Element); err != nil {
		return err
	}
	pngSinkPad := vsel.GetRequestPad("sink_%u")
	if pngSinkPad == nil {
		return fmt.Errorf("input-selector png pad request failed")
	}
	if pngPad == nil || pngPad.Link(pngSinkPad) != gst.PadLinkOK {
		return fmt.Errorf("link png to selector failed")
	}

	if err := pipe.SetState(gst.StatePlaying); err != nil {
		logBusErrors("pipeline", pipe)
		return fmt.Errorf("set pipeline PLAYING: %w", err)
	}
	if err := setActivePad(vsel, pngSinkPad); err != nil {
		log.Printf("warning: default to PNG failed: %v", err)
	}

	m.pipeline = pipe
	m.vsel = vsel

	m.pngBin = pngBin
	m.pngPad = pngPad
	m.pngSinkPad = pngSinkPad
	m.lessonBin = nil
	m.lessonPad = nil
	m.lessonSinkPad = nil
	m.lessonDecode = nil
	m.deckBin = deckChain.Element

	return nil
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

	if err := m.buildLessonOnlyPipeline(path); err != nil {
		return fmt.Errorf("build lesson-only pipeline: %w", err)
	}

	m.currentLesson = path
	m.lessonLoop = loop
	m.trackMap = trackMap
	log.Printf("SwitchToLesson: now playing %s", path)
	return nil
}

func (m *goGstManager) LoadPlaylist(Playlist) error { return nil }
func (m *goGstManager) StartPlaylist() error        { return nil }
func (m *goGstManager) NextItem() error             { return nil }

func (m *goGstManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pipeline != nil {
		_ = m.pipeline.SetState(gst.StateNull)
		m.pipeline = nil
	}
	return nil
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
	bin := gst.NewBin("lessonbin_explicit")

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
	caps, err := gst.NewElement("capsfilter")
	if err != nil {
		return nil, nil, err
	}
	if caps != nil {
		c := gst.NewCapsFromString("video/x-raw,format=UYVY,width=1920,height=1080,interlace-mode=progressive,framerate=25/1")
		caps.SetProperty("caps", c)
	}

	if err := bin.AddMany(src, demux, vq, h264p, dec, vc, vs, vr, caps); err != nil {
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

	if err := gst.ElementLinkMany(vq, h264p, dec, vc, vs, vr, caps); err != nil {
		return nil, nil, fmt.Errorf("link explicit lesson chain: %w", err)
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

func (m *goGstManager) refreshDecklinkChain() error {
	if m.pipeline == nil {
		return fmt.Errorf("pipeline not built")
	}

	if m.deckBin != nil {
		_ = m.deckBin.SetState(gst.StateNull)
		if err := m.pipeline.Remove(m.deckBin); err != nil {
			return fmt.Errorf("remove old decklink bin: %w", err)
		}
		m.deckBin = nil
	}

	deckChain, err := buildDecklinkBin()
	if err != nil {
		return fmt.Errorf("build decklink bin: %w", err)
	}

	log.Printf("refreshDecklink: adding new decklink chain")

	if err := m.pipeline.Add(deckChain.Element); err != nil {
		return fmt.Errorf("add decklink bin: %w", err)
	}

	if err := gst.ElementLinkMany(m.vsel, deckChain.Element); err != nil {
		return fmt.Errorf("link selector to decklink: %w", err)
	}

	deckChain.Element.SetState(gst.StateReady)
	deckChain.Element.SyncStateWithParent()

	sinkPad := deckChain.Element.GetStaticPad("sink")
	if sinkPad != nil {
		sinkPad.AddProbe(gst.PadProbeTypeBuffer, func(p *gst.Pad, info *gst.PadProbeInfo) gst.PadProbeReturn {
			log.Printf("decklink queue buffer caps=%s", padCaps(p))
			return gst.PadProbeRemove
		})
	}

	m.deckBin = deckChain.Element
	log.Printf("refreshDecklink: decklink chain ready")
	log.Printf("refreshDecklink: decklink caps=%s", capsString(m.deckBin))
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
	// Mirror working video path and add full Dante JACK fan-out mapping
	descTemplate := `filesrc location=%s ! qtdemux name=dem
dem.video_0 ! queue ! h264parse config-interval=-1 ! avdec_h264 ! videoconvert ! videoscale ! videorate !
video/x-raw,format=UYVY,width=1920,height=1080,framerate=25/1 ! interlace field-pattern=2:2 top-field-first=true !
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
	desc := fmt.Sprintf(descTemplate, filepath.Clean(path), modeID, sinkIdent)

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

	// Start bus watch to handle EOS for looping
	go m.busWatchLesson()

	return nil
}

// SwitchToPNG tears down current pipeline and restores default PNG→selector→decklink graph.
func (m *goGstManager) SwitchToPNG() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pipeline != nil {
		_ = m.pipeline.SetState(gst.StateNull)
	}
	m.pipeline = nil
	m.vsel = nil
	m.deckBin = nil
	m.lessonBin = nil
	m.lessonPad = nil
	m.lessonSinkPad = nil
	m.lessonDecode = nil
	m.pngBin = nil
	m.pngPad = nil
	m.pngSinkPad = nil
	return m.buildPipelineLocked()
}

// busWatchLesson monitors the lesson-only pipeline bus for EOS and handles looping
func (m *goGstManager) busWatchLesson() {
	for {
		m.mu.Lock()
		pipe := m.pipeline
		loop := m.lessonLoop
		path := m.currentLesson
		m.mu.Unlock()

		if pipe == nil {
			return
		}

		bus := pipe.GetBus()
		if bus == nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		msg := bus.TimedPop(100 * time.Millisecond)
		if msg == nil {
			continue
		}

		switch msg.Type() {
		case gst.MessageEOS:
			log.Printf("busWatchLesson: EOS received")
			if loop && path != "" {
				log.Printf("busWatchLesson: restarting lesson (loop enabled)")
				// Restart the same lesson by calling SwitchToLesson again
				if err := m.SwitchToLesson(path, loop, nil); err != nil {
					log.Printf("busWatchLesson: restart failed: %v", err)
				}
			} else {
				log.Printf("busWatchLesson: switching to PNG (no loop)")
				if err := m.SwitchToPNG(); err != nil {
					log.Printf("busWatchLesson: switch to PNG failed: %v", err)
				}
			}
			return
		case gst.MessageError:
			err := msg.ParseError()
			if err != nil {
				log.Printf("busWatchLesson: pipeline error: %v", err)
				log.Printf("busWatchLesson: error debug: %s", err.DebugString())
				log.Printf("busWatchLesson: error source: %s", msg.Source())
			}
		}
	}
}
