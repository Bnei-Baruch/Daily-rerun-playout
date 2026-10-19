package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Logging and crash bookkeeping.
//
// The controller used to run with its output attached to whatever terminal
// started it, so when the process was killed mid-switch the reason went
// nowhere. Two things are fixed here:
//
//  1. stdout/stderr are redirected into a log file at the *file descriptor*
//     level, so Go panic traces and cgo crash reports (a SIGSEGV inside
//     GStreamer, the JACK client or the DeckLink driver) land on disk.
//  2. A run marker records that the process is alive. If the marker survives
//     into the next run, the previous one never reached its shutdown path —
//     that is a crash, and it gets reported and tallied instead of vanishing.

const (
	defaultLogDir   = "/opt/backup/logs"
	defaultStateDir = "/opt/backup/state"

	logFileName    = "lesson_controller.log"
	crashFileName  = "lesson_controller.crashes.log"
	runMarkerName  = "controller_run.json"
	heartbeatEvery = 30 * time.Second

	// Roll the log over once it passes this size, keeping logRetain old files.
	logMaxBytes = 32 << 20
	logRetain   = 5
)

var (
	logMu   sync.Mutex
	logFile *os.File
	logPath string
)

func logDir() string {
	if d := os.Getenv("LOG_DIR"); d != "" {
		return d
	}
	return defaultLogDir
}

func controllerStateDir() string {
	if d := os.Getenv("STATE_DIR"); d != "" {
		return d
	}
	return defaultStateDir
}

// setupLogging sends stdout and stderr to the controller log file. It is the
// first thing main does, so that everything after it is captured.
//
// Set LOG_TO_STDERR=1 to keep output on the terminal instead (useful when
// running the controller by hand).
func setupLogging() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	if os.Getenv("LOG_TO_STDERR") == "1" {
		log.Printf("logging: LOG_TO_STDERR=1, staying on the terminal")
		return
	}

	path := os.Getenv("CONTROLLER_LOG_FILE")
	if path == "" {
		path = filepath.Join(logDir(), logFileName)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o775); err != nil {
		log.Printf("logging: cannot create %s: %v — staying on the terminal", filepath.Dir(path), err)
		return
	}
	rotateIfLarge(path)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o664)
	if err != nil {
		log.Printf("logging: cannot open %s: %v — staying on the terminal", path, err)
		return
	}

	// Tell whoever started us by hand where the output went, before it moves.
	fmt.Fprintf(os.Stderr, "lesson-controller: logging to %s (tail -f %s to follow)\n", path, path)

	if err := redirectStdio(f); err != nil {
		log.Printf("logging: %v — staying on the terminal", err)
		f.Close()
		return
	}

	logMu.Lock()
	logFile, logPath = f, path
	logMu.Unlock()

	// Written straight to the file so restarts are easy to spot in an
	// append-only log.
	fmt.Fprintf(f, "\n===== %s  lesson-controller start (pid %d) =====\n",
		time.Now().Format("2006-01-02 15:04:05"), os.Getpid())

	go rotateLoop()
}

// redirectStdio points file descriptors 1 and 2 at f.
//
// This has to happen at the descriptor level rather than by reassigning
// os.Stderr: the Go runtime writes panic traces and cgo crash reports straight
// to fd 2, and those are exactly the messages that were being lost.
func redirectStdio(f *os.File) error {
	fd := int(f.Fd())
	if err := syscall.Dup2(fd, int(os.Stdout.Fd())); err != nil {
		return fmt.Errorf("redirect stdout: %w", err)
	}
	if err := syscall.Dup2(fd, int(os.Stderr.Fd())); err != nil {
		return fmt.Errorf("redirect stderr: %w", err)
	}
	return nil
}

// rotateIfLarge shifts path to path.1, path.1 to path.2 and so on when the log
// has grown past logMaxBytes. Called at startup and periodically after that.
func rotateIfLarge(path string) {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() < logMaxBytes {
		return
	}
	os.Remove(fmt.Sprintf("%s.%d", path, logRetain))
	for i := logRetain - 1; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", path, i), fmt.Sprintf("%s.%d", path, i+1))
	}
	os.Rename(path, path+".1")
}

func rotateLoop() {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for range t.C {
		logMu.Lock()
		if logFile != nil && logPath != "" {
			if fi, err := os.Stat(logPath); err == nil && fi.Size() >= logMaxBytes {
				old := logFile
				rotateIfLarge(logPath)
				if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o664); err == nil {
					if err := redirectStdio(f); err == nil {
						logFile = f
						old.Close()
					} else {
						f.Close()
					}
				}
			}
		}
		logMu.Unlock()
	}
}

// runMarker is the "I am alive" file. LastSeen is refreshed on a heartbeat, so
// after an unclean exit it tells us roughly when the process died and what was
// on air at the time.
type runMarker struct {
	PID      int    `json:"pid"`
	Started  string `json:"started"`
	LastSeen string `json:"last_seen"`
	Mode     string `json:"mode,omitempty"`
	File     string `json:"file,omitempty"`
	URL      string `json:"url,omitempty"`
	Loop     bool   `json:"loop,omitempty"`
}

func runMarkerPath() string { return filepath.Join(controllerStateDir(), runMarkerName) }

// reportPreviousRun looks for a marker left behind by the last run. A marker
// that is still there means the process never got to its shutdown path: it was
// killed rather than stopped.
func reportPreviousRun() {
	path := runMarkerPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return // no marker: previous run shut down cleanly (or this is a first run)
	}
	var prev runMarker
	if err := json.Unmarshal(data, &prev); err != nil {
		log.Printf("WARNING: unreadable run marker %s: %v", path, err)
		os.Remove(path)
		return
	}

	what := prev.Mode
	if prev.File != "" {
		what = fmt.Sprintf("%s %s", prev.Mode, filepath.Base(prev.File))
	} else if prev.URL != "" {
		what = fmt.Sprintf("%s %s", prev.Mode, prev.URL)
	}

	log.Printf("WARNING: previous run (pid %d, started %s) did not shut down cleanly — "+
		"last alive %s, on air: %s", prev.PID, prev.Started, prev.LastSeen, what)
	log.Printf("WARNING: that is a crash or a kill, not a stop. Check the macOS crash report: " +
		"ls -lt ~/Library/Logs/DiagnosticReports/ | head")

	// Keep a one-line-per-crash tally, so "it happens a few times a week" can
	// be counted rather than remembered.
	crashPath := filepath.Join(logDir(), crashFileName)
	if f, err := os.OpenFile(crashPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o664); err == nil {
		fmt.Fprintf(f, "%s unclean exit: pid=%d started=%s last_seen=%s on_air=%q\n",
			time.Now().Format(time.RFC3339), prev.PID, prev.Started, prev.LastSeen, what)
		f.Close()
	}

	os.Remove(path)
}

// startRunMarker writes the marker for this run and keeps it fresh.
func startRunMarker() {
	if err := os.MkdirAll(controllerStateDir(), 0o775); err != nil {
		log.Printf("run marker: cannot create %s: %v", controllerStateDir(), err)
		return
	}
	started := time.Now().Format(time.RFC3339)
	writeRunMarker(started)

	go func() {
		t := time.NewTicker(heartbeatEvery)
		defer t.Stop()
		for range t.C {
			writeRunMarker(started)
		}
	}()
}

func writeRunMarker(started string) {
	m := runMarker{
		PID:      os.Getpid(),
		Started:  started,
		LastSeen: time.Now().Format(time.RFC3339),
	}

	// Best effort: GetState takes the pipeline lock, and a wedged pipeline
	// should not stop the heartbeat from recording what it can.
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("run marker: recovered while reading state: %v", r)
			}
		}()
		if manager != nil {
			st := manager.GetState()
			m.Mode, m.File, m.URL, m.Loop = st.Playout.Mode, st.Playout.File, st.Playout.URL, st.Playout.Loop
		}
	}()

	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	tmp := runMarkerPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o664); err != nil {
		return
	}
	os.Rename(tmp, runMarkerPath())
}

// clearRunMarker is called on graceful shutdown. Its absence on the next start
// is what marks the previous run as clean.
func clearRunMarker() {
	if err := os.Remove(runMarkerPath()); err != nil && !os.IsNotExist(err) {
		log.Printf("run marker: could not remove %s: %v", runMarkerPath(), err)
	}
}
