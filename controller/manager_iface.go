package main

type PipelineManager interface {
	BuildPipeline() error
	SwitchToPNG() error
	SwitchToLesson(path string, loop bool, trackMap map[string]int) error
	LoadPlaylist(p Playlist) error
	StartPlaylist() error
	NextItem() error
	Stop() error
	GetState() StateResponse
}

// StateResponse represents the current state of the controller
type StateResponse struct {
	Playout              PlayoutState           `json:"playout"`
	Jack                 JackState              `json:"jack"`
	Capture              CaptureState           `json:"capture"`
	PlaylistLastPrepared *PlaylistPreparedState `json:"playlist_last_prepared,omitempty"`
}

// PlayoutState represents the current playout state
type PlayoutState struct {
	Running        bool          `json:"running"`
	Mode           string        `json:"mode"` // "png", "lesson", or "playlist"
	Label          string        `json:"label,omitempty"`
	File           string        `json:"file,omitempty"`
	PID            int           `json:"pid"`
	Loop           bool          `json:"loop"`
	Overlay        string        `json:"overlay,omitempty"`
	Preroll        string        `json:"preroll,omitempty"`
	OverlayEnabled bool          `json:"overlay_enabled"`
	URL            string        `json:"url,omitempty"`
	Playlist       *PlaylistInfo `json:"playlist,omitempty"`
	StartedAtISO   string        `json:"started_at_iso,omitempty"`
	EndedAtISO     string        `json:"ended_at_iso,omitempty"`
	HeartbeatISO   string        `json:"heartbeat_iso"`
}

// PlaylistInfo contains current playlist information
type PlaylistInfo struct {
	ItemCount    int    `json:"item_count"`
	CurrentIndex int    `json:"current_index,omitempty"`
	CurrentFile  string `json:"current_file,omitempty"`
}

// JackState represents JACK server state
type JackState struct {
	Running  bool   `json:"running"`
	PID      int    `json:"pid"`
	Rate     int    `json:"rate"`
	Buffer   int    `json:"buffer"`
	NPeriods int    `json:"nperiods"`
	Source   string `json:"source"`
}

// CaptureState represents capture status
type CaptureState struct {
	Running      bool   `json:"running"`
	PID          int    `json:"pid"`
	Label        string `json:"label,omitempty"`
	File         string `json:"file,omitempty"`
	Mode         string `json:"mode,omitempty"`
	StartedAtISO string `json:"started_at_iso,omitempty"`
	EndedAtISO   string `json:"ended_at_iso,omitempty"`
	HeartbeatISO string `json:"heartbeat_iso,omitempty"`
}

// PlaylistPreparedState represents the last prepared playlist
type PlaylistPreparedState struct {
	Preparing     bool                   `json:"preparing"`
	Label         string                 `json:"label,omitempty"`
	URLs          []string               `json:"urls,omitempty"`
	URLCount      int                    `json:"url_count"`
	PlaylistDir   string                 `json:"playlist_dir"`
	FileCount     int                    `json:"file_count"`
	PreparedFiles int                    `json:"prepared_files"`
	PreparedPNGs  int                    `json:"prepared_pngs"`
	PreparedAtISO string                 `json:"prepared_at_iso,omitempty"`
	Items         []PlaylistPreparedItem `json:"items,omitempty"`
	StartedAtISO  string                 `json:"started_at_iso,omitempty"`
	UpdatedAtISO  string                 `json:"updated_at_iso,omitempty"`
}

// PlaylistPreparedItem represents a single prepared playlist item
type PlaylistPreparedItem struct {
	Index        int    `json:"index"`
	URL          string `json:"url"`
	File         string `json:"file"`
	PNG          string `json:"png,omitempty"`
	OriginalName string `json:"original_name,omitempty"`
	FinalURL     string `json:"final_url,omitempty"`
	DurationHMS  string `json:"duration_hms,omitempty"`
	FileExists   bool   `json:"file_exists"`
	PNGExists    bool   `json:"png_exists"`
	FileMtimeISO string `json:"file_mtime_iso,omitempty"`
}
