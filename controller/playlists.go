package main

// Keeps the editor's playlists (shidur.kab.info) ready for immediate playout. A
// watcher polls the feed and, for every playlist that changed, turns each in/out
// pair into a segment, stitches consecutive segments with the same audio layout
// into one HLS stream and builds the pipeline routing its audio to JACK. The
// routing rules come from Bnei-Baruch/media-streamer, which used to generate them
// as cmd_exec strings on a separate machine.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

const defaultPlaylistFeedURL = "https://shidur.kab.info/db/shidur/playlist"

// fadePrefix names the volume elements the end-of-playlist fade turns down.
const fadePrefix = "fadevol-"

// hlsLanguagePorts maps HLS LANGUAGE codes to JACK ports (media-streamer's
// HLSLanguageMap). Hebrew is stereo on 20/21 and feeds missing languages.
var hlsLanguagePorts = map[string]int{
	"null": 31, "he": 20, "en": 29, "ru": 22, "es": 30, "fr": 1, "it": 2, "de": 3,
	"tr": 4, "pt": 5, "bg": 6, "ka": 7, "ro": 8, "hu": 9, "sv": 10, "lt": 11,
	"hr": 12, "ja": 13, "sl": 14, "pl": 15, "no": 16, "lv": 17, "uk": 18, "nl": 19,
	"zh": 23, "am": 24, "hi": 25, "fa": 26, "ar": 27, "id": 28, "hy": 32, "da": 33,
	"et": 34, "el": 35, "tl": 36, "az": 37,
}

func playlistFeedURL() string {
	if u := os.Getenv("PLAYLIST_SOURCE_URL"); u != "" {
		return u
	}
	return defaultPlaylistFeedURL
}

func playlistPollInterval() time.Duration {
	if v := os.Getenv("PLAYLIST_POLL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 5*time.Second {
			return d
		}
		if n, err := strconv.Atoi(v); err == nil && n >= 5 {
			return time.Duration(n) * time.Second
		}
		log.Printf("warning: invalid PLAYLIST_POLL=%q, using 30s", v)
	}
	return 30 * time.Second
}

// The last feed that parsed, so playlists are ready right after a restart even when
// the feed is unreachable.
func playlistFeedPath() string {
	return filepath.Join(controllerStateDir(), "playlist_feed.json")
}

type feedItem struct {
	FilePath string    `json:"file_path"`
	HLSPath  string    `json:"hls_path"`
	IsHLS    bool      `json:"isHls"`
	InPoint  []float64 `json:"inpoint"`
	OutPoint []float64 `json:"outpoint"`
}

type feedPlaylist struct {
	Items []feedItem `json:"playlist"`
}

type segment struct {
	URL        string `json:"url"`
	FilePath   string `json:"file_path"`
	InMs       int64  `json:"in_ms"`
	OutMs      int64  `json:"out_ms"`
	DurationMs int64  `json:"duration_ms"`
}

type part struct {
	URL        string   `json:"url"`
	Segments   int      `json:"segments"`
	DurationMs int64    `json:"duration_ms"`
	Languages  []string `json:"languages"`
	Pipeline   string   `json:"pipeline"`
}

type preparedPlaylist struct {
	raw        json.RawMessage
	token      string // path of its stitched files under /hls-concat/
	Segments   []segment
	Parts      []part
	TotalMs    int64
	PreparedAt time.Time
	Err        string // last preparation error; an earlier version may still be playable
}

func (p *preparedPlaylist) jobs() []HLSJob {
	jobs := make([]HLSJob, len(p.Parts))
	for i, pt := range p.Parts {
		jobs[i] = HLSJob{URL: pt.URL, Pipeline: pt.Pipeline, DurationMs: pt.DurationMs}
	}
	return jobs
}

// date is the first file's YYYY-MM-DD, as Companion shows it on the buttons.
func (p *preparedPlaylist) date() string {
	if len(p.Segments) == 0 {
		return ""
	}
	return strings.ReplaceAll(path.Dir(p.Segments[0].FilePath), "/", "-")
}

var playlistStore = struct {
	sync.RWMutex
	byID   map[string]*preparedPlaylist
	files  map[string]map[string][]byte // token -> file name -> body
	tokens map[string][]string          // playlist id -> its tokens, newest last
	seq    int
}{
	byID:   map[string]*preparedPlaylist{},
	files:  map[string]map[string][]byte{},
	tokens: map[string][]string{},
}

// Stitched files of older versions are kept a little, so a playlist that changes
// while it plays can still rebuild into its next part.
const keepTokensPerPlaylist = 3

var playlistRefreshMu sync.Mutex

func nextPlaylistToken() string {
	playlistStore.Lock()
	defer playlistStore.Unlock()
	playlistStore.seq++
	return strconv.Itoa(playlistStore.seq)
}

func clipURL(hlsPath string, from, to int64) string {
	base := hlsPath
	if i := strings.Index(base, "/clipFrom/"); i != -1 {
		base = base[:i]
	} else {
		base = strings.TrimSuffix(base, "/master.m3u8")
	}
	return fmt.Sprintf("%s/clipFrom/%d/clipTo/%d/master.m3u8", base, from, to)
}

func formatHMS(ms int64) string {
	s := ms / 1000
	return fmt.Sprintf("%02d:%02d:%02d", s/3600, s%3600/60, s%60)
}

// hlsPipeline builds the pipeline description for an HLS stream with the given
// master: video to DeckLink, each audio rendition to its JACK port, Hebrew to
// every port whose language is missing (silence when there is no Hebrew).
// The mixer pad alpha and the volume elements named fadePrefix* are what the
// end-of-playlist fade ramps down; the mixer background is black for it.
func hlsPipeline(location string, mi masterInfo) (string, []string) {
	if len(mi.audio) == 0 {
		// Audio only muxed into the video variant: single stereo stream to Hebrew L/R
		return fmt.Sprintf("urisourcebin uri=%s parse-streams=true name=src src. ! queue ! capsfilter caps=video/x-h264 ! h264parse ! vtdec ! videoconvert ! videoscale ! video/x-raw,width=1920,height=1080 ! videomixer name=mix background=black ! videoconvert ! interlace field-pattern=2:2 top-field-first=true ! decklinkvideosink mode=1080i50 src. ! queue ! capsfilter caps=audio/mpeg ! aacparse ! fdkaacdec ! volume name=%s0 ! audioconvert ! audio/x-raw,channels=2 ! audioresample ! audio/x-raw,rate=48000 ! deinterleave name=d d.src_0 ! queue ! audioconvert ! jackaudiosink client-name=gst-heb-L port-pattern=20 d.src_1 ! queue ! audioconvert ! jackaudiosink client-name=gst-heb-R port-pattern=21", location, fadePrefix),
			[]string{"mono"}
	}

	pads := map[string]int{}
	for i, lang := range mi.audio {
		if _, dup := pads[lang]; lang != "" && !dup {
			pads[lang] = i + 1
		}
	}

	parts := []string{fmt.Sprintf("videomixer name=mix background=black ! videoconvert ! interlace field-pattern=2:2 top-field-first=true ! decklinkvideosink mode=11 souphttpsrc location=%s ! hlsdemux name=demux demux.src_0 ! tsdemux ! queue ! h264parse ! vtdec ! mix.", location)}
	var languages []string

	heIdx, hasHe := pads["he"]
	if hasHe {
		parts = append(parts,
			fmt.Sprintf("demux.src_%d ! tsdemux ! queue ! aacparse ! fdkaacdec ! volume name=%she ! tee name=t", heIdx, fadePrefix),
			"t. ! deinterleave name=d d.src_0 ! queue ! audioconvert ! audioresample ! jackaudiosink client-name=gst-heb-L port-pattern=20 d.src_1 ! queue ! audioconvert ! audioresample ! jackaudiosink client-name=gst-heb-R port-pattern=21")
		languages = append(languages, "he")
	}

	langs := make([]string, 0, len(hlsLanguagePorts))
	for lang := range hlsLanguagePorts {
		if lang != "he" {
			langs = append(langs, lang)
		}
	}
	sort.Slice(langs, func(i, j int) bool { return hlsLanguagePorts[langs[i]] < hlsLanguagePorts[langs[j]] })

	for _, lang := range langs {
		if idx, ok := pads[lang]; ok && lang != "null" {
			parts = append(parts, fmt.Sprintf("demux.src_%d ! tsdemux ! queue ! aacparse ! fdkaacdec ! volume name=%s%s ! audioconvert ! audioresample ! jackaudiosink client-name=gst-%s port-pattern=%d", idx, fadePrefix, lang, lang, hlsLanguagePorts[lang]))
			languages = append(languages, lang)
		}
	}
	for _, lang := range langs {
		if _, ok := pads[lang]; ok && lang != "null" {
			continue
		}
		if hasHe {
			parts = append(parts, fmt.Sprintf("t. ! audioconvert ! audioresample ! queue ! jackaudiosink client-name=gst-%s port-pattern=%d", lang, hlsLanguagePorts[lang]))
		} else {
			parts = append(parts, fmt.Sprintf("audiotestsrc wave=silence ! jackaudiosink client-name=gst-%s port-pattern=%d", lang, hlsLanguagePorts[lang]))
		}
		languages = append(languages, lang)
	}
	return strings.Join(parts, " "), languages
}

func fetchMaster(u string) (masterInfo, error) {
	body, err := fetchText(u)
	if err != nil {
		return masterInfo{}, err
	}
	mi := parseMaster(u, body)
	if len(mi.order) == 0 {
		return masterInfo{}, fmt.Errorf("%s: master has no renditions", u)
	}
	return mi, nil
}

// directHLSJob plays a single HLS URL that is not from the editor's feed.
func directHLSJob(u string) (HLSJob, error) {
	mi, err := fetchMaster(u)
	if err != nil {
		return HLSJob{}, err
	}
	pipeline, _ := hlsPipeline(u, mi)
	return HLSJob{URL: u, Pipeline: pipeline}, nil
}

// preparePlaylist turns one feed entry into segments and playable parts. It
// returns the stitched files to serve under the playlist's token.
func preparePlaylist(id string, raw json.RawMessage) (*preparedPlaylist, map[string][]byte, error) {
	var fp feedPlaylist
	if err := json.Unmarshal(raw, &fp); err != nil {
		return nil, nil, fmt.Errorf("invalid playlist: %v", err)
	}
	if len(fp.Items) == 0 {
		return nil, nil, fmt.Errorf("playlist is empty")
	}

	p := &preparedPlaylist{raw: raw, token: nextPlaylistToken(), PreparedAt: time.Now()}
	for idx, it := range fp.Items {
		if !it.IsHLS || it.HLSPath == "" {
			return nil, nil, fmt.Errorf("item %d is not HLS (only HLS items are supported)", idx)
		}
		if len(it.InPoint) == 0 || len(it.InPoint) != len(it.OutPoint) {
			log.Printf("playlist %s: item %d has no usable in/out points, playing %s as sent (its length is unknown)", id, idx, it.HLSPath)
			p.Segments = append(p.Segments, segment{URL: it.HLSPath, FilePath: it.FilePath})
			continue
		}
		for i := range it.InPoint {
			in, out := int64(it.InPoint[i]), int64(it.OutPoint[i])
			if out <= in {
				return nil, nil, fmt.Errorf("item %d pair %d: out %d is not after in %d", idx, i, out, in)
			}
			p.Segments = append(p.Segments, segment{URL: clipURL(it.HLSPath, in, out), FilePath: it.FilePath, InMs: in, OutMs: out, DurationMs: out - in})
			p.TotalMs += out - in
		}
	}

	masters := make([]masterInfo, len(p.Segments))
	errs := make([]error, len(p.Segments))
	var wg sync.WaitGroup
	for i, seg := range p.Segments {
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			masters[i], errs[i] = fetchMaster(u)
		}(i, seg.URL)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			return nil, nil, fmt.Errorf("segment %d: %v", i+1, err)
		}
	}

	// cmd_exec routes demux pads by rendition order, so only consecutive segments
	// with the same layout share a pipeline; a layout change starts a new part,
	// played after a pipeline rebuild.
	files := map[string][]byte{}
	for start := 0; start < len(p.Segments); {
		end := start + 1
		for end < len(p.Segments) && masters[end].layout() == masters[start].layout() {
			end++
		}
		location := p.Segments[start].URL
		if end-start > 1 {
			prefix := fmt.Sprintf("r%d-", len(p.Parts)+1)
			localBase := fmt.Sprintf("http://127.0.0.1:%s/hls-concat/%s/%s", listenPort(), p.token, prefix)
			urls := make([]string, 0, end-start)
			for _, seg := range p.Segments[start:end] {
				urls = append(urls, seg.URL)
			}
			if err := stitchRun(urls, masters[start:end], localBase, prefix, files); err != nil {
				return nil, nil, err
			}
			location = localBase + "master.m3u8"
		}
		pipeline, languages := hlsPipeline(location, masters[start])
		var durMs int64
		for _, seg := range p.Segments[start:end] {
			if seg.DurationMs == 0 {
				durMs = 0 // a segment of unknown length: no fade for this part
				break
			}
			durMs += seg.DurationMs
		}
		p.Parts = append(p.Parts, part{URL: location, Segments: end - start, DurationMs: durMs, Languages: languages, Pipeline: pipeline})
		start = end
	}
	return p, files, nil
}

type refreshResult struct {
	Playlists int               `json:"playlists"`
	Added     []string          `json:"added"`
	Changed   []string          `json:"changed"`
	Removed   []string          `json:"removed"`
	Failed    map[string]string `json:"failed"`
}

// refreshPlaylists prepares every playlist in feed whose entry changed (or failed
// last time) and drops the ones no longer in it.
func refreshPlaylists(feed []byte) (refreshResult, error) {
	playlistRefreshMu.Lock()
	defer playlistRefreshMu.Unlock()

	res := refreshResult{Added: []string{}, Changed: []string{}, Removed: []string{}, Failed: map[string]string{}}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(feed, &entries); err != nil {
		return res, fmt.Errorf("invalid playlist feed: %v", err)
	}

	playlistStore.RLock()
	old := playlistStore.byID
	playlistStore.RUnlock()

	type result struct {
		p     *preparedPlaylist
		files map[string][]byte
		err   error
	}
	results := map[string]*result{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for id, raw := range entries {
		if prev := old[id]; prev != nil && prev.Err == "" && bytes.Equal(prev.raw, raw) {
			continue
		}
		wg.Add(1)
		go func(id string, raw json.RawMessage) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			p, files, err := preparePlaylist(id, raw)
			mu.Lock()
			results[id] = &result{p, files, err}
			mu.Unlock()
		}(id, raw)
	}
	wg.Wait()

	byID := make(map[string]*preparedPlaylist, len(entries))
	for id := range entries {
		prev := old[id]
		r, redone := results[id]
		switch {
		case !redone:
			byID[id] = prev
		case r.err != nil:
			res.Failed[id] = r.err.Error()
			if prev != nil {
				kept := *prev
				kept.Err = r.err.Error()
				byID[id] = &kept
			} else {
				byID[id] = &preparedPlaylist{raw: entries[id], Err: r.err.Error(), PreparedAt: time.Now()}
			}
		default:
			byID[id] = r.p
			if prev == nil {
				res.Added = append(res.Added, id)
			} else if !bytes.Equal(prev.raw, entries[id]) || prev.Err != "" {
				res.Changed = append(res.Changed, id)
			}
		}
	}
	for id := range old {
		if _, ok := entries[id]; !ok {
			res.Removed = append(res.Removed, id)
		}
	}
	res.Playlists = len(byID)
	sort.Strings(res.Added)
	sort.Strings(res.Changed)
	sort.Strings(res.Removed)

	playlistStore.Lock()
	playlistStore.byID = byID
	for id, r := range results {
		if r.err != nil {
			continue
		}
		playlistStore.files[r.p.token] = r.files
		playlistStore.tokens[id] = append(playlistStore.tokens[id], r.p.token)
		if n := len(playlistStore.tokens[id]); n > keepTokensPerPlaylist {
			for _, t := range playlistStore.tokens[id][:n-keepTokensPerPlaylist] {
				delete(playlistStore.files, t)
			}
			playlistStore.tokens[id] = playlistStore.tokens[id][n-keepTokensPerPlaylist:]
		}
	}
	for _, id := range res.Removed {
		for _, t := range playlistStore.tokens[id] {
			delete(playlistStore.files, t)
		}
		delete(playlistStore.tokens, id)
	}
	playlistStore.Unlock()

	for _, id := range append(append([]string{}, res.Added...), res.Changed...) {
		p := byID[id]
		log.Printf("playlist %s ready: %d segments, %d parts, total %s", id, len(p.Segments), len(p.Parts), formatHMS(p.TotalMs))
	}
	for _, id := range res.Removed {
		log.Printf("playlist %s removed from feed", id)
	}
	for id, e := range res.Failed {
		log.Printf("playlist %s failed to prepare: %s", id, e)
	}
	return res, nil
}

// fetchAndRefreshPlaylists reads the feed, prepares what changed and saves the
// feed for the next start.
func fetchAndRefreshPlaylists() (refreshResult, error) {
	resp, err := hlsHTTPClient.Get(playlistFeedURL())
	if err != nil {
		return refreshResult{}, fmt.Errorf("fetch feed: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return refreshResult{}, fmt.Errorf("read feed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return refreshResult{}, fmt.Errorf("fetch feed: %s", resp.Status)
	}

	res, err := refreshPlaylists(body)
	if err != nil {
		return res, err
	}
	if prev, _ := os.ReadFile(playlistFeedPath()); !bytes.Equal(prev, body) {
		tmp := playlistFeedPath() + ".tmp"
		if err := os.WriteFile(tmp, body, 0o664); err == nil {
			err = os.Rename(tmp, playlistFeedPath())
		}
		if err != nil {
			log.Printf("warning: saving playlist feed: %v", err)
		}
	}
	return res, nil
}

// startPlaylistWatcher prepares the saved feed, then polls the live one.
func startPlaylistWatcher() {
	go func() {
		if feed, err := os.ReadFile(playlistFeedPath()); err == nil {
			if res, err := refreshPlaylists(feed); err != nil {
				log.Printf("saved playlist feed: %v", err)
			} else {
				log.Printf("saved playlist feed: %d playlists prepared", res.Playlists)
			}
		}

		interval := playlistPollInterval()
		log.Printf("playlist watcher: polling %s every %s", playlistFeedURL(), interval)
		lastErr := ""
		for {
			_, err := fetchAndRefreshPlaylists()
			switch {
			case err != nil && err.Error() != lastErr:
				log.Printf("playlist watcher: %v (keeping the last good playlists)", err)
				lastErr = err.Error()
			case err == nil && lastErr != "":
				log.Printf("playlist watcher: feed reachable again")
				lastErr = ""
			}
			time.Sleep(interval)
		}
	}()
}

// playlistJobs returns the parts to play for id. A playlist the watcher has not
// seen yet (just added in the editor) triggers an immediate refresh.
func playlistJobs(id string) ([]HLSJob, int, error) {
	get := func() *preparedPlaylist {
		playlistStore.RLock()
		defer playlistStore.RUnlock()
		return playlistStore.byID[id]
	}
	p := get()
	if p == nil {
		if _, err := fetchAndRefreshPlaylists(); err != nil {
			log.Printf("playlist %s not prepared and refresh failed: %v", id, err)
		}
		p = get()
	}
	if p == nil {
		return nil, http.StatusNotFound, fmt.Errorf("playlist %s not found in %s", id, playlistFeedURL())
	}
	if len(p.Parts) == 0 {
		return nil, http.StatusBadGateway, fmt.Errorf("playlist %s could not be prepared: %s", id, p.Err)
	}
	if p.Err != "" {
		log.Printf("playlist %s: latest version failed (%s), playing the previous one", id, p.Err)
	}
	return p.jobs(), http.StatusOK, nil
}

type playlistSummary struct {
	TotalMs    int64     `json:"total_ms"`
	Total      string    `json:"total"`
	Date       string    `json:"date"`
	FilePath   string    `json:"file_path"`
	Segments   int       `json:"segments"`
	Parts      int       `json:"parts"`
	PreparedAt time.Time `json:"prepared_at"`
	Error      string    `json:"error,omitempty"`
}

func summarize(p *preparedPlaylist) playlistSummary {
	s := playlistSummary{TotalMs: p.TotalMs, Total: formatHMS(p.TotalMs), Date: p.date(), Segments: len(p.Segments), Parts: len(p.Parts), PreparedAt: p.PreparedAt, Error: p.Err}
	if len(p.Segments) > 0 {
		s.FilePath = p.Segments[0].FilePath
	}
	return s
}

// GET /api/playlists — every playlist keyed by id, with its total time for Companion.
func handlePlaylists(w http.ResponseWriter, r *http.Request) {
	playlistStore.RLock()
	out := make(map[string]playlistSummary, len(playlistStore.byID))
	for id, p := range playlistStore.byID {
		out[id] = summarize(p)
	}
	playlistStore.RUnlock()
	writeJSON(w, http.StatusOK, out)
}

// GET /api/playlists/{id} — segments, parts and pipelines of one playlist.
func handlePlaylist(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	playlistStore.RLock()
	p := playlistStore.byID[id]
	playlistStore.RUnlock()
	if p == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("playlist %s not found", id)})
		return
	}
	writeJSON(w, http.StatusOK, struct {
		playlistSummary
		SegmentList []segment `json:"segment_list"`
		PartList    []part    `json:"part_list"`
	}{summarize(p), p.Segments, p.Parts})
}

// GET /api/playlist/load/{id} — the playlist as the editor sent it, in
// media-streamer's response shape, for Companion's button labels.
func handleFeedPlaylistLoad(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	playlistStore.RLock()
	p := playlistStore.byID[id]
	playlistStore.RUnlock()
	if p == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": fmt.Sprintf("playlist %s not found", id)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "playlist": p.raw})
}

// POST /api/playlists/refresh — read the feed now instead of at the next poll.
func handlePlaylistsRefresh(w http.ResponseWriter, r *http.Request) {
	res, err := fetchAndRefreshPlaylists()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// GET /hls-concat/{token}/{file} — stitched playlists for the pipeline.
func handleHLSConcatFile(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	playlistStore.RLock()
	body, ok := playlistStore.files[vars["token"]][vars["file"]]
	playlistStore.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	_, _ = w.Write(body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
