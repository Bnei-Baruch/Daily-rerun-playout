package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/mux"
)

// NOTE: This is a skeleton to keep existing repo untouched.
// Wire to GStreamer (go-gst) in a follow-up edit.

type OutputConfig struct {
	ID       string     `json:"id"`
	JackPort int        `json:"jack_port"`
	Source   SourceRule `json:"source"`
}

type SourceRule struct {
	Type    string `json:"type"`    // lang | track_index | silence
	Name    string `json:"name"`    // e.g., "he", "en"
	Channel string `json:"channel"` // L | R | mono
	Index   int    `json:"index"`   // for track_index
}

type AudioMap struct {
	Outputs  []OutputConfig `json:"outputs"`
	Fallback SourceRule     `json:"fallback"`
}

type Playlist struct {
	Items []PlaylistItem `json:"items"`
}

type PlaylistItem struct {
	Path         string         `json:"path"`
	Map          map[string]int `json:"map"`
	OriginalURL  string         `json:"original_url,omitempty"`
	OriginalName string         `json:"original_name,omitempty"`
	FinalURL     string         `json:"final_url,omitempty"`
}

var (
	audioMap AudioMap
	playlist Playlist
)

// Manager holds pipeline state (to be wired with go-gst in follow-up)
type Manager struct {
	audioMap AudioMap
	// TEMP: external gst-launch process while wiring full go-gst
	currentCmd *exec.Cmd
}

func NewManager(m AudioMap) *Manager {
	return &Manager{audioMap: m}
}

// BuildPipeline creates persistent pipeline with selectors and sinks
func (m *Manager) BuildPipeline() error {
	// TODO: gst.Init(nil); create pipeline, vsel, isel_* per outputs; jack sinks
	log.Printf("BuildPipeline: outputs=%d", len(m.audioMap.Outputs))
	return nil
}

func (m *Manager) startCmd(command string) error {
	log.Printf("starting gst: %s", command)
	parts := strings.Fields(command)
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	m.currentCmd = cmd
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

func (m *Manager) stopCurrent() error {
	if m.currentCmd == nil || m.currentCmd.Process == nil {
		return nil
	}
	if err := m.currentCmd.Process.Kill(); err != nil {
		return err
	}
	m.currentCmd = nil
	return nil
}

// SwitchToPNG switches video to PNG and audio to silence
func (m *Manager) SwitchToPNG() error {
	// TEMP: spawn gst-launch to SDI so you can see output while we wire go-gst
	if err := m.stopCurrent(); err != nil {
		return err
	}
	pngPath := os.Getenv("PNG_PATH")
	videoChain := "videotestsrc pattern=smpte ! videoconvert"
	if pngPath != "" {
		videoChain = fmt.Sprintf("filesrc location=%s ! pngdec ! imagefreeze ! videoscale ! videorate ! videoconvert", shellEscape(pngPath))
	}
	// Normalize and send to DeckLink; audio = silence to JACK null
	cmd := []string{
		"gst-launch-1.0", "-vvv",
		videoChain,
		"! video/x-raw,format=UYVY,width=1920,height=1080,framerate=25/1",
		"! interlace field-pattern=2:2 top-field-first=true",
		"! decklinkvideosink mode=1080i50 async=false",
		"audiotestsrc wave=silence",
		"! audio/x-raw,channels=1,rate=48000,format=F32LE",
		"! queue",
		"! jackaudiosink client-name=gst-null port-pattern=31 sync=false",
	}
	return m.startCmd(strings.Join(cmd, " "))
}

// SwitchToLesson loads/loops a lesson file and switches selectors
func (m *Manager) SwitchToLesson(path string, loop bool, trackMap map[string]int) error {
	// TEMP: spawn gst-launch playing the file to SDI (no switching yet)
	if err := m.stopCurrent(); err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("path is required")
	}
	// Build a simple pipeline: file → qtdemux → h264 → vtdec → normalize → decklink; audio silence for now
	cmd := []string{
		"gst-launch-1.0", "-vvv",
		fmt.Sprintf("filesrc location=%s ! qtdemux name=dem dem.video_0 ! queue ! h264parse ! vtdec ! videoconvert ! videoscale ! videorate ! video/x-raw,format=UYVY,width=1920,height=1080,framerate=25/1 ! interlace field-pattern=2:2 top-field-first=true ! decklinkvideosink mode=1080i50 async=false", shellEscape(path)),
		"audiotestsrc wave=silence ! audio/x-raw,channels=1,rate=48000,format=F32LE ! queue ! jackaudiosink client-name=gst-null port-pattern=31 sync=false",
	}
	// Looping not yet implemented in this temp path
	return m.startCmd(strings.Join(cmd, " "))
}

// Playlist controls
func (m *Manager) LoadPlaylist(p Playlist) error {
	playlist = p
	log.Printf("LoadPlaylist: %d items", len(playlist.Items))
	return nil
}

func (m *Manager) StartPlaylist() error {
	// TODO: create first bin, preroll next, switch
	log.Printf("StartPlaylist: starting")
	return nil
}

func (m *Manager) NextItem() error {
	// TODO: switch selectors to next item atomically
	log.Printf("NextItem: switching")
	return nil
}

var manager PipelineManager
var captureManager *CaptureManager

func loadAudioMap(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &audioMap)
}

func loadPlaylist(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &playlist)
}

func handleSourcePNG(w http.ResponseWriter, r *http.Request) {
	if err := manager.SwitchToPNG(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func handleSourceLesson(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string         `json:"path"`
		Loop bool           `json:"loop"`
		Map  map[string]int `json:"map"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	if err := manager.SwitchToLesson(req.Path, req.Loop, req.Map); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func handleSourceHLS(w http.ResponseWriter, r *http.Request) {
	// Check if playlist is in URL path
	vars := mux.Vars(r)
	playlistFromPath := vars["playlist"]

	var req struct {
		Playlist string `json:"playlist"` // Playlist ID (e.g. "01")
		URL      string `json:"url"`      // Or direct HLS URL
	}

	// Try to decode JSON body (optional if playlist is in path)
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	// Prefer playlist from URL path, then from JSON body
	playlist := playlistFromPath
	if playlist == "" {
		playlist = req.Playlist
	}

	var hlsURL, gstCommand string

	// If playlist ID provided, fetch from API
	if playlist != "" {
		url := fmt.Sprintf("http://10.66.1.105:8081/api/playlist/list")

		// Try Go HTTP client first
		resp, err := http.Get(url)
		var playlists map[string]struct {
			Playlist []struct {
				HLSPath string `json:"hls_path"`
				CmdExec string `json:"cmd_exec"`
			} `json:"playlist"`
		}

		if err != nil {
			// Fallback to curl if Go HTTP client fails
			log.Printf("HLS: Go HTTP client failed (%v), trying curl fallback", err)
			cmd := exec.Command("curl", "-s", url)
			output, curlErr := cmd.Output()
			if curlErr != nil {
				http.Error(w, fmt.Sprintf("failed to fetch playlist (curl): %v", curlErr), http.StatusInternalServerError)
				return
			}
			if err := json.Unmarshal(output, &playlists); err != nil {
				http.Error(w, fmt.Sprintf("failed to parse playlist (curl): %v", err), http.StatusInternalServerError)
				return
			}
			log.Printf("HLS: curl fallback succeeded")
		} else {
			defer resp.Body.Close()
			if err := json.NewDecoder(resp.Body).Decode(&playlists); err != nil {
				http.Error(w, fmt.Sprintf("failed to parse playlist: %v", err), http.StatusInternalServerError)
				return
			}
		}

		playlistData, ok := playlists[playlist]
		if !ok || len(playlistData.Playlist) == 0 {
			http.Error(w, fmt.Sprintf("playlist %s not found", playlist), http.StatusNotFound)
			return
		}

		hlsURL = playlistData.Playlist[0].HLSPath
		gstCommand = playlistData.Playlist[0].CmdExec
		log.Printf("HLS: Using playlist %s, URL=%s", playlist, hlsURL)
	} else if req.URL != "" {
		hlsURL = req.URL
		log.Printf("HLS: Using direct URL=%s", hlsURL)
	} else {
		http.Error(w, "either playlist or url is required", http.StatusBadRequest)
		return
	}

	// Type assertion to access HLS method
	if hlsManager, ok := manager.(interface{ SwitchToHLS(string, string) error }); ok {
		if err := hlsManager.SwitchToHLS(hlsURL, gstCommand); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "HLS not supported by this manager", http.StatusNotImplemented)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func handleOverlayTimerShow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SeekMs int64 `json:"seek_ms"` // Not used for now, but kept for API compatibility
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if timerManager, ok := manager.(interface{ ShowTimerOverlay(int64) error }); ok {
		if err := timerManager.ShowTimerOverlay(req.SeekMs); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "timer overlay not supported by this manager", http.StatusNotImplemented)
		return
	}

	log.Printf("timer: showing overlay")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"message": "timer overlay shown",
	})
}

func handleOverlayTimerHide(w http.ResponseWriter, r *http.Request) {
	if timerManager, ok := manager.(interface{ HideTimerOverlay() error }); ok {
		if err := timerManager.HideTimerOverlay(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "timer overlay not supported by this manager", http.StatusNotImplemented)
		return
	}

	log.Printf("timer: hiding overlay")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"message": "timer overlay hidden",
	})
}

func handlePlaylistPrepare(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URLs  []string `json:"urls"`
		Label string   `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.URLs) == 0 {
		http.Error(w, "urls array is required", http.StatusBadRequest)
		return
	}
	if req.Label == "" {
		req.Label = "playlist-prepare"
	}

	// Type assertion to access PreparePlaylist method
	if prepManager, ok := manager.(interface {
		PreparePlaylist(urls []string, label string) error
	}); ok {
		if err := prepManager.PreparePlaylist(req.URLs, req.Label); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "playlist prepare not supported by this manager", http.StatusNotImplemented)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "preparing",
		"count":   len(req.URLs),
		"label":   req.Label,
		"message": "playlist preparation started in background",
	})
}

func handlePlaylistLoad(w http.ResponseWriter, r *http.Request) {
	var req struct {
		File string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := loadPlaylist(req.File); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func urlsToPlaylistItems(urls []string) []PlaylistItem {
	items := make([]PlaylistItem, len(urls))
	for i, url := range urls {
		items[i] = PlaylistItem{Path: url, Map: nil}
	}
	return items
}

func handlePlaylistStart(w http.ResponseWriter, r *http.Request) {
	if err := manager.StartPlaylist(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func handlePlaylistNext(w http.ResponseWriter, r *http.Request) {
	if err := manager.NextItem(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func handleState(w http.ResponseWriter, r *http.Request) {
	state := manager.GetState()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(state)
}

func handleCaptureStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Label    string `json:"label"`
		Duration int    `json:"duration"`
		Mode     string `json:"mode"` // "sdi" for embedded stereo
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Allow empty body
		req.Label = "capture"
	}
	if req.Label == "" {
		req.Label = "capture"
	}

	// Start capture using the capture manager
	if err := captureManager.Start(req.Label, req.Duration, req.Mode); err != nil {
		http.Error(w, fmt.Sprintf("failed to start capture: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("capture: started label=%s duration=%d mode=%s", req.Label, req.Duration, req.Mode)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"label":   req.Label,
		"message": "capture started",
	})
}

func handleCaptureStop(w http.ResponseWriter, r *http.Request) {
	// Stop capture using the capture manager
	if err := captureManager.Stop(); err != nil {
		http.Error(w, fmt.Sprintf("failed to stop capture: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("capture: stopped")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"message": "capture stopped",
	})
}

func handleCaptureStatus(w http.ResponseWriter, r *http.Request) {
	// Get capture status from the capture manager
	state := captureManager.GetState()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(state)
}

func main() {
	log.Printf("lesson-controller starting (PID=%d)", os.Getpid())
	// Optional: read map from env or default sample
	mapPath := os.Getenv("AUDIO_MAP_PATH")
	if mapPath == "" {
		// Prefer local path when running from controller dir
		if _, err := os.Stat("audio_map.json.sample"); err == nil {
			mapPath = "audio_map.json.sample"
		} else if _, err := os.Stat("controller/audio_map.json.sample"); err == nil {
			mapPath = "controller/audio_map.json.sample"
		} else {
			mapPath = "controller/audio_map.json.sample"
		}
	}
	if err := loadAudioMap(mapPath); err != nil {
		log.Printf("warning: could not load audio map %s: %v", mapPath, err)
	}

	// Initialize capture manager
	captureManager = NewCaptureManager()
	log.Printf("capture manager initialized")

	// Manager
	// Manager selection: try go-gst when GO_GST=1, else fallback to gst-launch
	mgr, err := tryNewGoGst(audioMap)
	if err != nil {
		log.Fatalf("go-gst manager not available: %v", err)
	}
	manager = mgr
	if err := manager.BuildPipeline(); err != nil {
		log.Printf("warning: pipeline not built yet: %v", err)
	} else {
		log.Printf("pipeline ready: defaulting to PNG; use /source endpoints to switch")
	}

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	r := mux.NewRouter()
	r.HandleFunc("/source/png", handleSourcePNG).Methods(http.MethodPost)
	r.HandleFunc("/source/lesson", handleSourceLesson).Methods(http.MethodPost)
	r.HandleFunc("/source/hls", handleSourceHLS).Methods(http.MethodPost)
	r.HandleFunc("/source/hls/{playlist}", handleSourceHLS).Methods(http.MethodPost)
	r.HandleFunc("/overlay/timer/show", handleOverlayTimerShow).Methods(http.MethodPost)
	r.HandleFunc("/overlay/timer/hide", handleOverlayTimerHide).Methods(http.MethodPost)
	r.HandleFunc("/playlist/prepare", handlePlaylistPrepare).Methods(http.MethodPost)
	r.HandleFunc("/playlist/load", handlePlaylistLoad).Methods(http.MethodPost)
	r.HandleFunc("/playlist/start", handlePlaylistStart).Methods(http.MethodPost)
	r.HandleFunc("/playlist/next", handlePlaylistNext).Methods(http.MethodPost)
	r.HandleFunc("/v1/state", handleState).Methods(http.MethodGet)
	r.HandleFunc("/v1/capture/start", handleCaptureStart).Methods(http.MethodPost)
	r.HandleFunc("/v1/capture/stop", handleCaptureStop).Methods(http.MethodPost)
	r.HandleFunc("/v1/capture", handleCaptureStatus).Methods(http.MethodGet)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8788"
	}
	addr := ":" + port

	// Create HTTP server with graceful shutdown support
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("lesson-controller listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	sig := <-sigChan
	log.Printf("received signal: %v, shutting down gracefully...", sig)

	// Clean up pipeline and JACK connections
	log.Printf("stopping pipeline and cleaning up JACK connections...")
	if err := manager.Stop(); err != nil {
		log.Printf("error stopping manager: %v", err)
	}

	// Gracefully shutdown HTTP server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}

	log.Printf("shutdown complete")
}

// minimal shell escape for file paths used in gst location=
func shellEscape(path string) string {
	if strings.ContainsAny(path, " \"'") {
		return fmt.Sprintf("'%s'", strings.ReplaceAll(path, "'", "'\\''"))
	}
	return path
}
