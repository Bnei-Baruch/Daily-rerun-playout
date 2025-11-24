package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// CaptureManager manages ffmpeg-based capture operations
type CaptureManager struct {
	mu          sync.Mutex
	cmd         *exec.Cmd
	cancel      context.CancelFunc
	isRunning   bool
	label       string
	outputFile  string
	mode        string
	startTime   time.Time
	stateDir    string
	stateFile   string
	pidFile     string
	progressFile string
}

// NewCaptureManager creates a new capture manager
func NewCaptureManager() *CaptureManager {
	stateDir := os.Getenv("STATE_DIR")
	if stateDir == "" {
		stateDir = "/opt/backup/state"
	}
	
	// Ensure state directory exists
	os.MkdirAll(stateDir, 0755)
	
	return &CaptureManager{
		stateDir:     stateDir,
		stateFile:    filepath.Join(stateDir, "capture.json"),
		pidFile:      filepath.Join(stateDir, "capture.pid"),
		progressFile: filepath.Join(stateDir, "capture_progress.txt"),
	}
}

// Start begins a new capture
func (cm *CaptureManager) Start(label string, duration int, mode string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.isRunning {
		return fmt.Errorf("capture already running")
	}

	// Generate output filename
	outDir := os.Getenv("OUT_DIR")
	if outDir == "" {
		outDir = "/opt/backup"
	}
	os.MkdirAll(outDir, 0755)
	
	timestamp := time.Now().Format("20060102_150405")
	outputFile := filepath.Join(outDir, fmt.Sprintf("%s_%s.mp4", label, timestamp))

	// Build ffmpeg command
	ctx, cancel := context.WithCancel(context.Background())
	
	// Use ffmpeg-bb (custom build with big-endian support for Dante)
	ffmpegPath := os.Getenv("FFMPEG_BB_PATH")
	if ffmpegPath == "" {
		ffmpegPath = "/usr/local/bin/ffmpeg-bb"
	}

	args := cm.buildFFmpegArgs(outputFile, duration, mode)
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)

	// Set up logging
	logDir := os.Getenv("LOG_DIR")
	if logDir == "" {
		logDir = "/opt/backup/logs"
	}
	os.MkdirAll(logDir, 0755)
	
	outLog := filepath.Join(logDir, "capture_ffmpeg.out")
	errLog := filepath.Join(logDir, "capture_ffmpeg.err")
	
	outFile, err := os.OpenFile(outLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		cancel()
		return fmt.Errorf("failed to open output log: %w", err)
	}
	
	errFile, err := os.OpenFile(errLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		outFile.Close()
		cancel()
		return fmt.Errorf("failed to open error log: %w", err)
	}

	cmd.Stdout = outFile
	cmd.Stderr = errFile

	// Start the command
	if err := cmd.Start(); err != nil {
		outFile.Close()
		errFile.Close()
		cancel()
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Store state
	cm.cmd = cmd
	cm.cancel = cancel
	cm.isRunning = true
	cm.label = label
	cm.outputFile = outputFile
	cm.mode = mode
	cm.startTime = time.Now()

	// Write PID file
	os.WriteFile(cm.pidFile, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)

	// Write initial state
	cm.writeState(true, cmd.Process.Pid)

	log.Printf("capture: started ffmpeg pid=%d label=%s output=%s", cmd.Process.Pid, label, outputFile)

	// Start heartbeat goroutine
	go cm.heartbeatLoop(ctx)

	// Wait for process in background
	go func() {
		defer outFile.Close()
		defer errFile.Close()
		
		err := cmd.Wait()
		
		cm.mu.Lock()
		defer cm.mu.Unlock()
		
		if cm.isRunning {
			cm.isRunning = false
			cm.writeState(false, 0)
			
			if err != nil {
				log.Printf("capture: ffmpeg exited with error: %v", err)
			} else {
				log.Printf("capture: ffmpeg completed successfully")
			}
		}
	}()

	return nil
}

// Stop stops the current capture
func (cm *CaptureManager) Stop() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if !cm.isRunning {
		return fmt.Errorf("no capture running")
	}

	log.Printf("capture: stopping ffmpeg pid=%d", cm.cmd.Process.Pid)

	// Cancel context to stop ffmpeg gracefully
	if cm.cancel != nil {
		cm.cancel()
	}

	// Give it a moment to stop gracefully
	time.Sleep(500 * time.Millisecond)

	// Force kill if still running
	if cm.cmd != nil && cm.cmd.Process != nil {
		cm.cmd.Process.Kill()
	}

	cm.isRunning = false
	cm.writeState(false, 0)

	log.Printf("capture: stopped")

	return nil
}

// GetState returns the current capture state
func (cm *CaptureManager) GetState() CaptureState {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	state := CaptureState{
		Running: cm.isRunning,
	}

	if cm.isRunning && cm.cmd != nil && cm.cmd.Process != nil {
		state.PID = cm.cmd.Process.Pid
		state.Label = cm.label
		state.File = cm.outputFile
		state.Mode = cm.mode
		state.StartedAtISO = cm.startTime.Format("2006-01-02 15:04:05")
		state.HeartbeatISO = time.Now().Format("2006-01-02 15:04:05")
	} else {
		// Read from state file if not running
		data, err := os.ReadFile(cm.stateFile)
		if err == nil {
			var stateData map[string]interface{}
			if err := json.Unmarshal(data, &stateData); err == nil {
				if label, ok := stateData["label"].(string); ok {
					state.Label = label
				}
				if file, ok := stateData["file"].(string); ok {
					state.File = file
				}
				if mode, ok := stateData["mode"].(string); ok {
					state.Mode = mode
				}
				if startedAt, ok := stateData["started_at"].(float64); ok && startedAt > 0 {
					state.StartedAtISO = time.Unix(int64(startedAt), 0).Format("2006-01-02 15:04:05")
				}
				if endedAt, ok := stateData["ended_at"].(float64); ok && endedAt > 0 {
					state.EndedAtISO = time.Unix(int64(endedAt), 0).Format("2006-01-02 15:04:05")
				}
			}
		}
	}

	return state
}

// writeState writes the capture state to the state file
func (cm *CaptureManager) writeState(running bool, pid int) {
	now := time.Now().Unix()
	
	stateData := map[string]interface{}{
		"running":   running,
		"mode":      "capture",
		"label":     cm.label,
		"file":      cm.outputFile,
		"pid":       pid,
		"heartbeat": now,
	}

	if running {
		stateData["started_at"] = cm.startTime.Unix()
	} else {
		stateData["ended_at"] = now
		stateData["pid"] = 0
	}

	data, err := json.MarshalIndent(stateData, "", "  ")
	if err != nil {
		log.Printf("capture: failed to marshal state: %v", err)
		return
	}

	if err := os.WriteFile(cm.stateFile, data, 0644); err != nil {
		log.Printf("capture: failed to write state file: %v", err)
	}
}

// heartbeatLoop updates the heartbeat in the state file
func (cm *CaptureManager) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cm.mu.Lock()
			if cm.isRunning {
				// Update heartbeat
				data, err := os.ReadFile(cm.stateFile)
				if err == nil {
					var stateData map[string]interface{}
					if err := json.Unmarshal(data, &stateData); err == nil {
						stateData["heartbeat"] = time.Now().Unix()
						if newData, err := json.MarshalIndent(stateData, "", "  "); err == nil {
							os.WriteFile(cm.stateFile, newData, 0644)
						}
					}
				}
			}
			cm.mu.Unlock()
		}
	}
}

// buildFFmpegArgs builds the ffmpeg command arguments
func (cm *CaptureManager) buildFFmpegArgs(outputFile string, duration int, mode string) []string {
	args := []string{
		"-hide_banner",
		"-y",
		"-progress", cm.progressFile,
		"-thread_queue_size", "512",
	}

	// Audio source selection
	if mode == "sdi" {
		// SDI embedded audio
		args = append(args,
			"-format_code", "Hi50",
			"-f", "decklink",
			"-i", "UltraStudio Recorder 3G",
		)
	} else {
		// Dante audio (default)
		args = append(args,
			"-big_endian", "1",
			"-f", "audiotoolbox",
			"-i", "AUDINATE-DVS-UID",
			"-thread_queue_size", "512",
			"-format_code", "Hi50",
			"-f", "decklink",
			"-i", "UltraStudio Recorder 3G",
		)
	}

	// Video codec
	args = append(args,
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "23",
		"-pix_fmt", "yuv420p",
		"-r", "25",
	)

	// Audio codec and mapping
	if mode == "sdi" {
		// Map video and build filter_complex for SDI stereo split
		args = append(args, "-map", "0:v")
		
		// Build filter_complex for splitting stereo into 36 tracks
		filterComplex := "[0:a:0]asplit=36[s0]"
		for i := 1; i <= 34; i++ {
			filterComplex += fmt.Sprintf("[m%d]", i)
		}
		filterComplex += "[s35];"
		filterComplex += "[s0]aformat=fltp,pan=stereo|c0=c0|c1=c1,volume=3dB[out0];"
		for i := 1; i <= 34; i++ {
			filterComplex += fmt.Sprintf("[m%d]pan=mono|c0=0.5*c0+0.5*c1,volume=9dB[out%d];", i, i)
		}
		filterComplex += "[s35]aformat=fltp,pan=stereo|c0=c0|c1=c1,volume=9dB[out35]"
		
		args = append(args, "-filter_complex", filterComplex)
		
		// Map all 36 audio outputs
		for i := 0; i <= 35; i++ {
			args = append(args, "-map", fmt.Sprintf("[out%d]", i))
		}
	} else {
		// Dante: map video from input 1, audio from input 0
		args = append(args, "-map", "1:v")
		for i := 0; i < 36; i++ {
			args = append(args, "-map", "0:a")
		}
		
		// Audio filters for Dante
		args = append(args, "-filter:1", "pan=stereo|c0=c0|c1=c1,volume=3dB")
		for i := 2; i <= 29; i++ {
			args = append(args, fmt.Sprintf("-filter:%d", i), fmt.Sprintf("pan=mono|c0=c%d,volume=9dB", i))
		}
		args = append(args, "-filter:30", "pan=mono|c0=c32,volume=9dB")
		args = append(args, "-filter:31", "pan=mono|c0=c33,volume=9dB")
		args = append(args, "-filter:32", "pan=mono|c0=c34,volume=9dB")
		args = append(args, "-filter:33", "pan=mono|c0=c35,volume=9dB")
		args = append(args, "-filter:34", "pan=mono|c0=c36,volume=9dB")
		args = append(args, "-filter:35", "pan=mono|c0=c37,volume=9dB")
		args = append(args, "-filter:36", "pan=stereo|c0=c30|c1=c31,volume=9dB")
	}

	// Audio codec settings
	args = append(args, "-c:a", "libfdk_aac")
	args = append(args, "-g", "12")
	
	// Audio bitrates
	args = append(args, "-ac:a:0", "2", "-b:a:0", "256k")
	for i := 1; i <= 34; i++ {
		args = append(args, fmt.Sprintf("-ac:a:%d", i), "1", fmt.Sprintf("-b:a:%d", i), "128k")
	}
	args = append(args, "-ac:a:35", "2", "-b:a:35", "256k")

	// Metadata
	languages := []string{
		"heb", "eng", "rus", "spa", "fre", "ita", "ger", "tur", "por", "bul",
		"geo", "ron", "hun", "swe", "lit", "hrv", "jpn", "slv", "pol", "nor",
		"lav", "ukr", "tur", "amh", "ara", "ind", "arm", "dan", "est", "gre",
		"tgl", "aze",
	}
	
	args = append(args, "-metadata", "network=archiveb")
	args = append(args, "-metadata", fmt.Sprintf("comment=c%d", time.Now().Unix()))
	
	for i, lang := range languages {
		if i < 36 {
			args = append(args, fmt.Sprintf("-metadata:s:a:%d", i), fmt.Sprintf("language=%s", lang))
		}
	}

	// Duration limit if specified
	if duration > 0 {
		args = append(args, "-t", fmt.Sprintf("%d", duration))
	}

	// Output
	args = append(args, "-f", "mp4", outputFile)

	return args
}

