//go:build gogst && deckplay

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	gst "github.com/tinyzimmer/go-gst/gst"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s /path/to/video [mode] [device]\n", os.Args[0])
		os.Exit(2)
	}

	// Build file:// URI
	uri := os.Args[1]
	if !filepath.IsAbs(uri) {
		if abs, err := filepath.Abs(uri); err == nil {
			uri = abs
		}
	}
	path := uri

	// DeckLink output mode (default 11 = 1080i50)
	mode := 11
	if len(os.Args) > 2 {
		if m, err := strconv.Atoi(os.Args[2]); err == nil {
			mode = m
		}
	}
	// DeckLink device-number (optional; default 0)
	device := 0
	if len(os.Args) > 3 {
		if d, err := strconv.Atoi(os.Args[3]); err == nil {
			device = d
		}
	}

	gst.Init(nil)

	// Minimal pipeline mimicking gst-launch with decodebin split for audio/video.
	desc := fmt.Sprintf(
		"playbin flags=video uri=file://%s video-sink=\"videoconvert ! interlace field-pattern=2:2 top-field-first=true ! videoconvert ! video/x-raw,format=UYVY,interlace-mode=interleaved,width=1920,height=1080,framerate=25/1 ! decklinkvideosink mode=%d device-number=%d sync=true\"",
		strings.ReplaceAll(path, "\"", "\\\""), mode, device,
	)

	pipe, err := gst.NewPipelineFromString(desc)
	must(err)

	if err := pipe.SetState(gst.StatePlaying); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: set PLAYING: %v\n", err)
		printInitialBusErrors(pipe)
		os.Exit(1)
	}

	// Wait for EOS/ERROR
	bus := pipe.GetBus()
	for {
		msg := bus.TimedPopFiltered(gst.ClockTimeNone, gst.MessageEOS|gst.MessageError|gst.MessageStateChanged)
		if msg == nil {
			continue
		}
		switch msg.Type() {
		case gst.MessageError:
			gerr := msg.ParseError()
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", gerr)
			_ = pipe.SetState(gst.StateNull)
			os.Exit(1)
		case gst.MessageEOS:
			_ = pipe.SetState(gst.StateNull)
			return
		}
	}
}

func printInitialBusErrors(pipe *gst.Pipeline) {
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
			gerr := msg.ParseError()
			fmt.Fprintf(os.Stderr, "BUS ERROR: %v\n", gerr)
		}
	}
}
