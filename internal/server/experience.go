package server

import (
	"context"
	"strconv"
	"strings"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// experienceView is one published role, as the transcript shows it.
type experienceView struct {
	content.Experience
	StartLabel, EndLabel string // "Mar 2026", "2019"; "" if unknown
}

// publishedExperience lists the published roles, newest first (their order).
func (s *Server) publishedExperience(ctx context.Context) ([]experienceView, error) {
	es := store.ExperienceOf(s.store)
	if es == nil {
		return nil, nil
	}
	all, err := es.Experience(ctx)
	if err != nil {
		return nil, err
	}
	var out []experienceView
	for _, e := range all {
		if !e.Published {
			continue
		}
		v := experienceView{Experience: e, StartLabel: monthLabel(e.Start)}
		if !e.Current {
			v.EndLabel = monthLabel(e.End)
		} else {
			v.End = ""
		}
		out = append(out, v)
	}
	return out, nil
}

var monthNames = [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

// monthLabel turns "2026-03" into "Mar 2026" and "2019" into "2019"
// (anything else into "").
func monthLabel(s string) string {
	if !content.ValidMonth(s) || s == "" {
		return ""
	}
	y, m, ok := strings.Cut(s, "-")
	if !ok {
		return y
	}
	n, _ := strconv.Atoi(m)
	return monthNames[n-1] + " " + y
}
