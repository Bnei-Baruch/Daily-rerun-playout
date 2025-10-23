package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// gstLaunchManager: temporary implementation using gst-launch
type gstLaunchManager struct {
	audioMap   AudioMap
	currentCmd *exec.Cmd
}

func NewGstLaunchManager(m AudioMap) *gstLaunchManager {
	return &gstLaunchManager{audioMap: m}
}

func (m *gstLaunchManager) BuildPipeline() error {
	log.Printf("[gst-launch] stub BuildPipeline (outputs=%d)", len(m.audioMap.Outputs))
	return nil
}

func (m *gstLaunchManager) startCmd(command string) error {
	log.Printf("[gst-launch] starting: %s", command)
	parts := strings.Fields(command)
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	m.currentCmd = cmd
	go func() { _ = cmd.Wait() }()
	return nil
}

func (m *gstLaunchManager) stopCurrent() error {
	if m.currentCmd == nil || m.currentCmd.Process == nil {
		return nil
	}
	if err := m.currentCmd.Process.Kill(); err != nil {
		return err
	}
	m.currentCmd = nil
	return nil
}

func (m *gstLaunchManager) SwitchToPNG() error {
	if err := m.stopCurrent(); err != nil {
		return err
	}
	pngPath := os.Getenv("PNG_PATH")
	deckDev := os.Getenv("DECKLINK_DEVICE")
	videoChain := "videotestsrc pattern=smpte ! videoconvert"
	if pngPath != "" {
		videoChain = fmt.Sprintf("filesrc location=%s ! pngdec ! imagefreeze ! videoscale ! videorate ! videoconvert", shellEscape(pngPath))
	}
	deckSink := "decklinkvideosink mode=1080i50 async=false"
	if deckDev != "" {
		deckSink = fmt.Sprintf("decklinkvideosink mode=1080i50 async=false device-number=%s", deckDev)
	}
	cmd := []string{
		"gst-launch-1.0", "-vvv",
		videoChain,
		"! video/x-raw,format=UYVY,width=1920,height=1080,framerate=25/1",
		"! interlace field-pattern=2:2 top-field-first=true",
		"! " + deckSink,
		"audiotestsrc wave=silence",
		"! audio/x-raw,channels=1,rate=48000,format=F32LE",
		"! queue",
		"! jackaudiosink client-name=gst-null port-pattern=31 sync=false",
	}
	return m.startCmd(strings.Join(cmd, " "))
}

func (m *gstLaunchManager) SwitchToLesson(path string, loop bool, trackMap map[string]int) error {
	if err := m.stopCurrent(); err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("path is required")
	}
	deckDev := os.Getenv("DECKLINK_DEVICE")
	deckSink := "decklinkvideosink mode=1080i50 async=false"
	if deckDev != "" {
		deckSink = fmt.Sprintf("decklinkvideosink mode=1080i50 async=false device-number=%s", deckDev)
	}
	// If an HLS URL is provided, use souphttpsrc → hlsdemux → tsdemux for video.
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		cmd := []string{
			"gst-launch-1.0", "-vvv",
			fmt.Sprintf("souphttpsrc location=%s user-agent=sviva-playout keep-alive=true timeout=0 is-live=false do-timestamp=true blocksize=32768 ", shellEscape(path)) +
				"! queue2 use-buffering=true max-size-time=9000000000 max-size-bytes=0 max-size-buffers=0 low-watermark=0.1 high-watermark=0.8 " +
				"! hlsdemux name=demux demux.src_0 ! tsdemux ! queue2 use-buffering=true max-size-time=9000000000 max-size-bytes=0 max-size-buffers=0 " +
				"! h264parse ! vtdec ! videoconvert ! videoscale ! videorate " +
				"! video/x-raw,format=UYVY,width=1920,height=1080,framerate=25/1 " +
				"! interlace field-pattern=2:2 top-field-first=true ! " + deckSink,
			"audiotestsrc wave=silence ! audio/x-raw,channels=1,rate=48000,format=F32LE ! queue ! jackaudiosink client-name=gst-null port-pattern=31 sync=false",
		}
		return m.startCmd(strings.Join(cmd, " "))
	}

	// Local file fallback (MP4/MOV): qtdemux path
	cmd := []string{
		"gst-launch-1.0", "-vvv",
		fmt.Sprintf("filesrc location=%s ! qtdemux name=dem dem.video_0 ! queue ! h264parse ! vtdec ! videoconvert ! videoscale ! videorate ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=25/1 ! interlace field-pattern=2:2 top-field-first=true ! %s", shellEscape(path), deckSink),
		"audiotestsrc wave=silence ! audio/x-raw,channels=1,rate=48000,format=F32LE ! queue ! jackaudiosink client-name=gst-null port-pattern=31 sync=false",
	}
	return m.startCmd(strings.Join(cmd, " "))
}

func (m *gstLaunchManager) LoadPlaylist(p Playlist) error { playlist = p; return nil }
func (m *gstLaunchManager) StartPlaylist() error          { return nil }
func (m *gstLaunchManager) NextItem() error               { return nil }
func (m *gstLaunchManager) Stop() error                   { return m.stopCurrent() }
func (m *gstLaunchManager) GetState() StateResponse {
	return StateResponse{
		Playout: PlayoutState{
			Running:      false,
			Mode:         "unknown",
			PID:          os.Getpid(),
			HeartbeatISO: time.Now().Format("2006-01-02 15:04:05"),
		},
		Jack: JackState{
			Running: false,
			Source:  "unavailable",
		},
	}
}
