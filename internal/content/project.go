package content

import (
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Project is a piece of client work, shown at /work/<slug> (migration 0005).
// Text fields are plain text; Body's paragraphs are separated by blank lines.
type Project struct {
	Slug         string
	Title        string
	Client       string
	Agency       string
	Year         string // as written ("2020", "2019-2020")
	Tags         []string
	Roles        []string
	Summary      string
	Contribution string // what Ben did on it
	Body         string
	Link         string   // the live work (http(s)), or ""
	Palette      []string // hex colours, "#rrggbb"
	YouTube      string   // a YouTube video id, or ""
	Media        []Media
	Order        int // featured order: lower first
	Published    bool
	UpdatedAt    time.Time
}

// Media is one still or loop. Src and Poster are paths on the site's own
// origins ("/media/projects/<slug>/hero.jpg"), served by both services from
// FRONTENDS_BUCKET (objects media/...) or MEDIA_DIR; never external URLs.
type Media struct {
	Kind   string `json:"kind"`             // MediaImage or MediaLoop
	Src    string `json:"src"`              // the image, or the loop's MP4
	Poster string `json:"poster,omitempty"` // a loop's still
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Alt    string `json:"alt,omitempty"`
}

// Media kinds.
const (
	MediaImage = "image" // a still
	MediaLoop  = "loop"  // a short silent video, played muted on a loop
)

// MediaPrefix is the URL path every media file lives under.
const MediaPrefix = "/media/"

var (
	mediaSeg  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	mediaExts = []string{".mp4", ".webm", ".jpg", ".jpeg", ".png", ".gif", ".webp"}
)

// MediaName validates a media file name (the URL path after /media/, e.g.
// "projects/qledecode/loop-1.mp4"): 1-8 segments of [A-Za-z0-9._-] that
// don't start with a dot, a known image or video extension, at most 200
// bytes. It is the only check between a URL and a bucket object or file.
func MediaName(name string) bool {
	if name == "" || len(name) > 200 || path.Clean(name) != name {
		return false
	}
	segs := strings.Split(name, "/")
	if len(segs) > 8 {
		return false
	}
	for _, s := range segs {
		if !mediaSeg.MatchString(s) {
			return false
		}
	}
	return slices.Contains(mediaExts, strings.ToLower(path.Ext(name)))
}

// MediaPath reports whether p is a servable media URL path ("/media/<name>").
func MediaPath(p string) bool {
	name, ok := strings.CutPrefix(p, MediaPrefix)
	return ok && MediaName(name)
}

// Valid reports whether m can be served: a known kind, a media path, and for
// a loop an optional media-path poster.
func (m Media) Valid() bool {
	if m.Kind != MediaImage && m.Kind != MediaLoop {
		return false
	}
	if !MediaPath(m.Src) || (m.Poster != "" && !MediaPath(m.Poster)) {
		return false
	}
	return m.Width >= 0 && m.Height >= 0 && m.Width <= 20000 && m.Height <= 20000
}

var (
	slugPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)
	youtubePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	hexPattern     = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// ValidSlug reports whether s is a project or experiment slug.
func ValidSlug(s string) bool { return slugPattern.MatchString(s) }

// ValidYouTube reports whether id looks like a YouTube video id.
func ValidYouTube(id string) bool { return youtubePattern.MatchString(id) }

// ValidHex reports whether c is a "#rrggbb" colour.
func ValidHex(c string) bool { return hexPattern.MatchString(c) }

// ValidLink reports whether u is an absolute http(s) URL (or "").
func ValidLink(u string) bool {
	if u == "" {
		return true
	}
	if len(u) > 2000 || strings.ContainsAny(u, " \t\r\n\"'<>\\") {
		return false
	}
	scheme, rest, ok := strings.Cut(u, "://")
	scheme = strings.ToLower(scheme)
	return ok && (scheme == "http" || scheme == "https") && rest != "" && !strings.HasPrefix(rest, "/")
}

// YouTubeURL is the watch page for a video id.
func YouTubeURL(id string) string { return "https://www.youtube.com/watch?v=" + id }
