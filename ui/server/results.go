package server

import (
	"encoding/json"
	"fmt"
	"path/filepath"
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

	// Populated from the run_end event when present.
	MovedCount   int
	DeletedCount int
	ManifestPath string
}

// ResultGroup is one duplicate cluster.
type ResultGroup struct {
	GroupID     int
	MatchReason string // md5, video_fast, …
	Hash        string
	Keep        ResultFile
	Remove      []ResultFile
}

// ResultFile is a single file inside a group (either the keeper or a
// remove-candidate).
type ResultFile struct {
	Path     string
	Name     string // basename, for display
	Size     int64
	SizeStr  string // formatted "4.2 MB"
	MTime    int64
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

	view.NumGroups = len(view.Groups)
	view.NumWarnings = len(view.Warnings)
	view.BytesHuman = humanBytes(view.BytesReclaim)
	return view, nil
}

// decodeGroup handles both the cross-check shape (single remove_path field)
// and the self-check shape (remove[] array). Cross-check emits one group per
// match while iterating source files; self-check emits one group per hash
// cluster.
func decodeGroup(raw json.RawMessage) (ResultGroup, error) {
	var p struct {
		GroupID     int    `json:"group_id"`
		MatchReason string `json:"match_reason"`
		Hash        string `json:"hash"`

		KeepPath  string `json:"keep_path"`
		KeepSize  int64  `json:"keep_size"`
		KeepMTime int64  `json:"keep_mtime"`

		// Self-check shape:
		Remove []struct {
			Path  string `json:"path"`
			Size  int64  `json:"size"`
			MTime int64  `json:"mtime"`
		} `json:"remove"`

		// Cross-check shape:
		RemovePath  string `json:"remove_path"`
		RemoveSize  int64  `json:"remove_size"`
		RemoveMTime int64  `json:"remove_mtime"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return ResultGroup{}, err
	}

	g := ResultGroup{
		GroupID:     p.GroupID,
		MatchReason: p.MatchReason,
		Hash:        p.Hash,
		Keep: ResultFile{
			Path:    p.KeepPath,
			Name:    filepath.Base(p.KeepPath),
			Size:    p.KeepSize,
			SizeStr: humanBytes(p.KeepSize),
			MTime:   p.KeepMTime,
		},
	}

	if len(p.Remove) > 0 {
		for _, r := range p.Remove {
			g.Remove = append(g.Remove, ResultFile{
				Path:    r.Path,
				Name:    filepath.Base(r.Path),
				Size:    r.Size,
				SizeStr: humanBytes(r.Size),
				MTime:   r.MTime,
			})
		}
	} else if p.RemovePath != "" {
		g.Remove = append(g.Remove, ResultFile{
			Path:    p.RemovePath,
			Name:    filepath.Base(p.RemovePath),
			Size:    p.RemoveSize,
			SizeStr: humanBytes(p.RemoveSize),
			MTime:   p.RemoveMTime,
		})
	}

	return g, nil
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
