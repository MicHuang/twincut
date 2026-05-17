package server

import (
	"net/url"
	"testing"
)


func TestResolveSimilarVideo(t *testing.T) {
	cases := []struct {
		name       string
		form       url.Values
		hasVideos  bool
		wantOn     bool
		wantSizePct string
	}{
		{
			name:       "auto + has videos defaults on with 5%",
			form:       url.Values{"include_similar_video": {"auto"}},
			hasVideos:  true,
			wantOn:     true,
			wantSizePct: "5",
		},
		{
			name:       "auto + no videos stays off",
			form:       url.Values{"include_similar_video": {"auto"}},
			hasVideos:  false,
			wantOn:     false,
			wantSizePct: "",
		},
		{
			name:       "explicit on overrides empty folder",
			form:       url.Values{"include_similar_video": {"on"}},
			hasVideos:  false,
			wantOn:     true,
			wantSizePct: "5",
		},
		{
			name:       "explicit off overrides video presence",
			form:       url.Values{"include_similar_video": {"off"}},
			hasVideos:  true,
			wantOn:     false,
			wantSizePct: "",
		},
		{
			name: "off suppresses user-supplied size_pct (dead flag)",
			form: url.Values{
				"include_similar_video": {"off"},
				"size_pct":              {"3"},
			},
			hasVideos:   true,
			wantOn:      false,
			wantSizePct: "",
		},
		{
			name: "user size_pct override survives auto-default",
			form: url.Values{
				"include_similar_video": {"auto"},
				"size_pct":              {"2"},
			},
			hasVideos:   true,
			wantOn:      true,
			wantSizePct: "2",
		},
		{
			name:       "blank mode treated as auto (legacy form support)",
			form:       url.Values{},
			hasVideos:  true,
			wantOn:     true,
			wantSizePct: "5",
		},
		{
			name:       "legacy mode=1 treated as on",
			form:       url.Values{"include_similar_video": {"1"}},
			hasVideos:  false,
			wantOn:     true,
			wantSizePct: "5",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotOn, gotPct := resolveSimilarVideo(tc.form, func() bool { return tc.hasVideos })
			if gotOn != tc.wantOn || gotPct != tc.wantSizePct {
				t.Errorf("resolveSimilarVideo = (%v, %q); want (%v, %q)",
					gotOn, gotPct, tc.wantOn, tc.wantSizePct)
			}
		})
	}
}

