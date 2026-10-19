package main

// HLS helpers: reading nginx-vod master/media playlists and stitching several clips
// (EXT-X-DISCONTINUITY between them) into one stream the controller serves, so a
// single pipeline plays them back to back.

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

func listenPort() string {
	if port := os.Getenv("PORT"); port != "" {
		return port
	}
	return "8788"
}

var hlsHTTPClient = &http.Client{Timeout: 20 * time.Second}

func fetchText(u string) (string, error) {
	resp, err := hlsHTTPClient.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func resolveURL(base, ref string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}

// renditionKey names a rendition by its file name (e.g. "index-a1.m3u8"), which is
// identical across clips of the same file.
func renditionKey(u string) string {
	if p, err := url.Parse(u); err == nil {
		return path.Base(p.Path)
	}
	return path.Base(u)
}

func lineURI(line string) (string, bool) {
	const attr = `URI="`
	i := strings.Index(line, attr)
	if i == -1 {
		return "", false
	}
	rest := line[i+len(attr):]
	j := strings.Index(rest, `"`)
	if j == -1 {
		return "", false
	}
	return rest[:j], true
}

// HLSJob is one pipeline run: an HLS URL and the pipeline description that plays it.
// DurationMs (0 when unknown) lets the last run fade out before it ends.
type HLSJob struct {
	URL        string
	Pipeline   string
	DurationMs int64
}

type masterInfo struct {
	lines []string          // raw master lines
	order []string          // rendition keys in master order
	urls  map[string]string // rendition key -> absolute media playlist URL
	langs map[string]string // rendition key -> LANGUAGE (audio renditions)
	audio []string          // LANGUAGE of each audio rendition in order: hlsdemux src_1..N
}

// layout identifies the rendition order and languages. cmd_exec maps demux pads to
// JACK ports by this order, so only clips with the same layout can share a pipeline.
func (mi masterInfo) layout() string {
	parts := make([]string, len(mi.order))
	for i, key := range mi.order {
		parts[i] = key + "=" + mi.langs[key]
	}
	return strings.Join(parts, ",")
}

func lineAttr(line, name string) string {
	attr := name + `="`
	i := strings.Index(line, attr)
	if i == -1 {
		return ""
	}
	rest := line[i+len(attr):]
	if j := strings.Index(rest, `"`); j != -1 {
		return rest[:j]
	}
	return ""
}

func parseMaster(masterURL, body string) masterInfo {
	mi := masterInfo{urls: map[string]string{}, langs: map[string]string{}}
	add := func(ref string) {
		abs := resolveURL(masterURL, ref)
		key := renditionKey(abs)
		if _, ok := mi.urls[key]; !ok {
			mi.order = append(mi.order, key)
		}
		mi.urls[key] = abs
	}
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		mi.lines = append(mi.lines, line)
		t := strings.TrimSpace(line)
		switch {
		case t == "":
		case strings.HasPrefix(t, "#EXT-X-I-FRAME-STREAM-INF"):
		case strings.HasPrefix(t, "#EXT-X-MEDIA:"):
			if u, ok := lineURI(t); ok {
				add(u)
				mi.langs[renditionKey(resolveURL(masterURL, u))] = lineAttr(t, "LANGUAGE")
				if strings.Contains(t, "TYPE=AUDIO") {
					mi.audio = append(mi.audio, lineAttr(t, "LANGUAGE"))
				}
			}
		case !strings.HasPrefix(t, "#"):
			add(t)
		}
	}
	return mi
}

// rewriteMaster points every rendition of item[0]'s master at the local stitched
// playlists, keeping the original order (cmd_exec maps demux pads by that order).
func rewriteMaster(mi masterInfo, localBase string) string {
	var out []string
	for _, line := range mi.lines {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "#EXT-X-I-FRAME-STREAM-INF"):
			continue
		case strings.HasPrefix(t, "#EXT-X-MEDIA:"):
			if u, ok := lineURI(t); ok {
				t = strings.Replace(t, `URI="`+u+`"`, `URI="`+localBase+renditionKey(u)+`"`, 1)
			}
		case t != "" && !strings.HasPrefix(t, "#"):
			t = localBase + renditionKey(t)
		}
		out = append(out, t)
	}
	return strings.Join(out, "\n") + "\n"
}

// mediaEntries returns the segment lines (EXTINF, URLs, other per-segment tags) of a
// VOD media playlist with absolute URLs, plus its target duration.
func mediaEntries(mediaURL, body string) ([]string, int) {
	var entries []string
	target := 0
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "":
		case strings.HasPrefix(t, "#EXT-X-TARGETDURATION:"):
			target, _ = strconv.Atoi(strings.TrimPrefix(t, "#EXT-X-TARGETDURATION:"))
		case t == "#EXTM3U",
			strings.HasPrefix(t, "#EXT-X-VERSION"),
			strings.HasPrefix(t, "#EXT-X-PLAYLIST-TYPE"),
			strings.HasPrefix(t, "#EXT-X-MEDIA-SEQUENCE"),
			strings.HasPrefix(t, "#EXT-X-ALLOW-CACHE"),
			strings.HasPrefix(t, "#EXT-X-ENDLIST"):
		case strings.HasPrefix(t, "#"):
			entries = append(entries, t)
		default:
			entries = append(entries, resolveURL(mediaURL, t))
		}
	}
	return entries, target
}

// stitchRun fetches the media playlists of clips sharing one layout and writes the
// combined playlists into files, named with prefix.
func stitchRun(urls []string, masters []masterInfo, localBase, prefix string, files map[string][]byte) error {
	// Fetch all media playlists in parallel: [rendition][item].
	type media struct {
		entries []string
		target  int
		err     error
	}
	keys := masters[0].order
	fetched := make([][]media, len(keys))
	var wg sync.WaitGroup
	for k, key := range keys {
		fetched[k] = make([]media, len(urls))
		for i := range urls {
			wg.Add(1)
			go func(k, i int, u string) {
				defer wg.Done()
				body, err := fetchText(u)
				if err != nil {
					fetched[k][i].err = err
					return
				}
				fetched[k][i].entries, fetched[k][i].target = mediaEntries(u, body)
			}(k, i, masters[i].urls[key])
		}
	}
	wg.Wait()

	for k, key := range keys {
		target := 1
		for i := range urls {
			if fetched[k][i].err != nil {
				return fmt.Errorf("%s %s: %w", urls[i], key, fetched[k][i].err)
			}
			if fetched[k][i].target > target {
				target = fetched[k][i].target
			}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:1\n", target)
		for i := range urls {
			if i > 0 {
				b.WriteString("#EXT-X-DISCONTINUITY\n")
			}
			for _, e := range fetched[k][i].entries {
				b.WriteString(e)
				b.WriteString("\n")
			}
		}
		b.WriteString("#EXT-X-ENDLIST\n")
		files[prefix+key] = []byte(b.String())
	}
	files[prefix+"master.m3u8"] = []byte(rewriteMaster(masters[0], localBase))
	return nil
}
