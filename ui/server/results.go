package server

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// ResultsView is the structured payload the results template renders.
// Built by walking a finished run's event stream.
type ResultsView struct {
	RunID         string         // for the apply form
	Mode          string         // self_check, cross_check, …
	SourcePath    string         // the folder that was scanned
	Status        RunStatus
	Cancelled     bool
	HasError      bool
	ErrorMessage  string
	Warnings      []ResultWarn
	Groups        []ResultGroup
	NumGroups     int
	NumFiles      int    // total candidate-to-quarantine files (sum of remove[] across groups)
	BytesReclaim  int64  // bytes reclaimable if the user accepts every default
	BytesHuman    string // formatted "3.4 GB"
	NumWarnings   int
	ApplyURL      string // "/api/self-check/apply" or "/api/cross-check/apply"

	// Populated from the run_end event when present.
	MovedCount   int
	DeletedCount int
	ManifestPath string
	QuarantineDir string // parent of ManifestPath, for the "Open in Finder" button
}

// ResultGroup is one duplicate cluster.
type ResultGroup struct {
	GroupID     int
	MatchReason string // md5, video_fast, …
	Hash        string
	Mode        string // "self_check" | "cross_check" — set by BuildResults from Run.Mode
	Keep        ResultFile
	Remove      []ResultFile

	// IsSimilar is true when the group came from a similarity match
	// (anything except md5). The template uses this to decide whether
	// to render thumbnails + per-file metadata.
	IsSimilar bool
}

// ResultFile is a single file inside a group (either the keeper or a
// remove-candidate).
type ResultFile struct {
	Path    string
	Name    string // basename, for display
	Size    int64
	SizeStr string // formatted "4.2 MB"
	MTime   int64

	// Video-only fields. Present when the source dup_group event included
	// per-side metadata (i.e., similarity matches with match_reason video_*).
	// Zero/empty for hash-exact clusters — the template uses HasMedia to
	// branch on whether to render the metadata strip.
	HasMedia      bool
	Duration      float64 // seconds
	DurationStr   string  // "3:21"
	Width         int
	Height        int
	DimensionsStr string // "1920x1080"
	FPS           float64
	FPSStr        string // "29.97 fps"
	Bitrate       int64  // bits per second
	BitrateStr    string // "5.0 Mbps"
}

// ResultWarn is a non-fatal warning surfaced at the top of the results panel.
type ResultWarn struct {
	Code   string
	Path   string
	Detail string
}

// BuildResults walks the run's event history and produces a ResultsView.
// Safe to call on a finished run; if called on a still-running run, it
// renders whatever has accumulated so far.
func BuildResults(run *Run) (ResultsView, error) {
	snap := run.Snapshot()
	view := ResultsView{
		RunID:     run.ID,
		Mode:      snap.Mode,
		Status:    snap.Status,
	}

	// Canonical workflow mode for templates. Strip _preview/_apply suffix
	// from Run.Mode (which is "self_check_preview" / "self_check_apply" /
	// "cross_check_preview" / "cross_check_apply" depending on the call site).
	workflow := snap.Mode
	switch {
	case strings.HasPrefix(workflow, "cross_check"):
		workflow = "cross_check"
		view.ApplyURL = "/api/cross-check/apply"
	case strings.HasPrefix(workflow, "self_check"):
		workflow = "self_check"
		view.ApplyURL = "/api/self-check/apply"
	default:
		view.ApplyURL = "/api/self-check/apply" // safe fallback
	}

	for _, ev := range run.EventsSince(0) {
		switch ev.Type {
		case EventRunStart:
			var p struct {
				Mode   string `json:"mode"`
				Source string `json:"source"`
			}
			if err := json.Unmarshal(ev.Raw, &p); err == nil {
				if p.Mode != "" {
					view.Mode = p.Mode
				}
				view.SourcePath = p.Source
			}
		case EventDupGroup:
			g, err := decodeGroup(ev.Raw)
			if err != nil {
				return view, fmt.Errorf("decode dup_group seq=%d: %w", ev.Seq, err)
			}
			view.Groups = append(view.Groups, g)
			view.NumFiles += len(g.Remove)
			for _, r := range g.Remove {
				view.BytesReclaim += r.Size
			}
		case EventWarn:
			var p struct {
				Code   string `json:"code"`
				Path   string `json:"path"`
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(ev.Raw, &p); err == nil {
				view.Warnings = append(view.Warnings, ResultWarn{
					Code:   p.Code,
					Path:   p.Path,
					Detail: p.Detail,
				})
			}
		case EventError:
			view.HasError = true
			var p struct {
				Code   string `json:"code"`
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(ev.Raw, &p); err == nil {
				view.ErrorMessage = fmt.Sprintf("%s: %s", p.Code, p.Detail)
			}
		case EventRunEnd:
			var p struct {
				Cancelled    bool   `json:"cancelled"`
				Moved        int    `json:"moved"`
				Deleted      int    `json:"deleted"`
				ManifestPath string `json:"manifest_path"`
			}
			if err := json.Unmarshal(ev.Raw, &p); err == nil {
				view.Cancelled = p.Cancelled
				view.MovedCount = p.Moved
				view.DeletedCount = p.Deleted
				view.ManifestPath = p.ManifestPath
			}
		}
	}

	// Twincut maintains a separate group_id counter per match family
	// (md5 source-self, similar-video, etc.), so two clusters can both
	// arrive with group_id=1. The UI uses GroupID as the form key for
	// per-cluster controls — collisions would cross-wire the radios.
	// Re-number to a single sequence for the page. Also stamp the
	// canonical workflow mode on each group for template branching.
	for i := range view.Groups {
		view.Groups[i].GroupID = i + 1
		view.Groups[i].Mode = workflow
	}

	view.NumGroups = len(view.Groups)
	view.NumWarnings = len(view.Warnings)
	view.BytesHuman = humanBytes(view.BytesReclaim)
	if view.ManifestPath != "" {
		view.QuarantineDir = filepath.Dir(view.ManifestPath)
	}
	return view, nil
}

// decodeGroup handles both the cross-check shape (single remove_path field)
// and the self-check shape (remove[] array). Cross-check emits one group per
// match while iterating source files; self-check emits one group per hash
// cluster. Similar-video matches additionally carry per-side video metadata
// (duration / dims / fps / bitrate) which we surface via ResultFile.
func decodeGroup(raw json.RawMessage) (ResultGroup, error) {
	var p struct {
		GroupID     int    `json:"group_id"`
		MatchReason string `json:"match_reason"`
		Hash        string `json:"hash"`

		KeepPath     string  `json:"keep_path"`
		KeepSize     int64   `json:"keep_size"`
		KeepMTime    int64   `json:"keep_mtime"`
		KeepDuration float64 `json:"keep_duration"`
		KeepWidth    int     `json:"keep_width"`
		KeepHeight   int     `json:"keep_height"`
		KeepFPS      float64 `json:"keep_fps"`
		KeepBitrate  int64   `json:"keep_bitrate"`

		// Self-check shape:
		Remove []struct {
			Path     string  `json:"path"`
			Size     int64   `json:"size"`
			MTime    int64   `json:"mtime"`
			Duration float64 `json:"duration"`
			Width    int     `json:"width"`
			Height   int     `json:"height"`
			FPS      float64 `json:"fps"`
			Bitrate  int64   `json:"bitrate"`
		} `json:"remove"`

		// Cross-check + similar-video shape (single removed file):
		RemovePath     string  `json:"remove_path"`
		RemoveSize     int64   `json:"remove_size"`
		RemoveMTime    int64   `json:"remove_mtime"`
		RemoveDuration float64 `json:"remove_duration"`
		RemoveWidth    int     `json:"remove_width"`
		RemoveHeight   int     `json:"remove_height"`
		RemoveFPS      float64 `json:"remove_fps"`
		RemoveBitrate  int64   `json:"remove_bitrate"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return ResultGroup{}, err
	}

	g := ResultGroup{
		GroupID:     p.GroupID,
		MatchReason: p.MatchReason,
		Hash:        p.Hash,
		IsSimilar:   p.MatchReason != "" && p.MatchReason != "md5",
		Keep: newResultFile(p.KeepPath, p.KeepSize, p.KeepMTime,
			p.KeepDuration, p.KeepWidth, p.KeepHeight, p.KeepFPS, p.KeepBitrate),
	}

	if len(p.Remove) > 0 {
		for _, r := range p.Remove {
			g.Remove = append(g.Remove, newResultFile(r.Path, r.Size, r.MTime,
				r.Duration, r.Width, r.Height, r.FPS, r.Bitrate))
		}
	} else if p.RemovePath != "" {
		g.Remove = append(g.Remove, newResultFile(p.RemovePath, p.RemoveSize, p.RemoveMTime,
			p.RemoveDuration, p.RemoveWidth, p.RemoveHeight, p.RemoveFPS, p.RemoveBitrate))
	}

	return g, nil
}

func newResultFile(path string, size, mtime int64, dur float64, w, h int, fps float64, bps int64) ResultFile {
	rf := ResultFile{
		Path:    path,
		Name:    filepath.Base(path),
		Size:    size,
		SizeStr: humanBytes(size),
		MTime:   mtime,
	}
	if dur > 0 || w > 0 || h > 0 || fps > 0 || bps > 0 {
		rf.HasMedia = true
		rf.Duration = dur
		rf.DurationStr = formatDuration(dur)
		rf.Width = w
		rf.Height = h
		if w > 0 && h > 0 {
			rf.DimensionsStr = fmt.Sprintf("%dx%d", w, h)
		}
		rf.FPS = fps
		if fps > 0 {
			rf.FPSStr = fmt.Sprintf("%.2f fps", fps)
		}
		rf.Bitrate = bps
		rf.BitrateStr = formatBitrate(bps)
	}
	return rf
}

// formatDuration renders seconds as "M:SS" (or "H:MM:SS" past one hour).
// Sub-second clips fall back to "Ns" / "Nms" so we never print "0:00".
func formatDuration(sec float64) string {
	if sec <= 0 {
		return ""
	}
	if sec < 1 {
		return fmt.Sprintf("%dms", int(sec*1000))
	}
	total := int(sec + 0.5)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// formatBitrate renders bps as "5.0 Mbps" / "320 kbps".
func formatBitrate(bps int64) string {
	if bps <= 0 {
		return ""
	}
	switch {
	case bps >= 1_000_000:
		return fmt.Sprintf("%.1f Mbps", float64(bps)/1_000_000)
	case bps >= 1_000:
		return fmt.Sprintf("%d kbps", bps/1_000)
	default:
		return fmt.Sprintf("%d bps", bps)
	}
}

// humanBytes renders a byte count as a short, human-readable string. We
// intentionally avoid going past TB — collections that large will have
// their own problems.
func humanBytes(b int64) string {
	const (
		_         = iota
		kb int64 = 1 << (10 * iota)
		mb
		gb
		tb
	)
	switch {
	case b >= tb:
		return fmt.Sprintf("%.1f TB", float64(b)/float64(tb))
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
