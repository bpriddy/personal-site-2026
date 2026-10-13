package server

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/go-pdf/fpdf"

	"github.com/bpriddy/personal-site-2026/internal/media"
	"github.com/bpriddy/personal-site-2026/web"
)

// resumePDF is /resume.pdf: the resume as a one- or two-page Letter PDF,
// made on request from the same data as /resume, so it's never out of date.
func (s *Server) resumePDF(w http.ResponseWriter, r *http.Request) {
	v, err := s.resumeView(r.Context())
	if err != nil {
		s.fail(w, "resume pdf", err)
		return
	}
	b, err := renderResumePDF(v, s.resumePhoto(r.Context(), v.Photo))
	if err != nil {
		s.fail(w, "resume pdf", err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/pdf")
	h.Set("Content-Disposition", `inline; filename="`+strings.ReplaceAll(v.Name, " ", "_")+`_Resume.pdf"`)
	h.Set("Cache-Control", "public, max-age=300")
	w.Write(b)
}

// pdfPhoto is the picture for the PDF: JPEG or PNG bytes (fpdf has no WebP).
type pdfPhoto struct {
	data []byte
	typ  string // "JPG" | "PNG"
}

// resumePhoto reads the resume photo for the PDF, from the embedded static
// files or the media store; nil (no photo, which is fine) if it isn't a
// JPEG or PNG or can't be read.
func (s *Server) resumePhoto(ctx context.Context, p string) *pdfPhoto {
	typ := map[string]string{".jpg": "JPG", ".jpeg": "JPG", ".png": "PNG"}[strings.ToLower(path.Ext(p))]
	if typ == "" {
		return nil
	}
	var data []byte
	if rest, ok := strings.CutPrefix(p, "/static/"); ok {
		data, _ = fs.ReadFile(web.FS, "static/"+rest)
	} else if name, ok := strings.CutPrefix(p, "/media/"); ok && s.media != nil {
		f, err := s.media.Open(ctx, name)
		if err != nil {
			return nil
		}
		defer f.Close()
		data, _ = io.ReadAll(io.LimitReader(f.Body, media.MaxBytes))
	}
	if len(data) == 0 {
		return nil
	}
	return &pdfPhoto{data, typ}
}

// the house palette, in print: ink on white, the accent for labels
var (
	pdfInk    = [3]int{21, 21, 19}
	pdfMuted  = [3]int{85, 82, 75}
	pdfAccent = [3]int{217, 64, 11}
	pdfRule   = [3]int{210, 206, 198}
)

func renderResumePDF(v resumeView, photo *pdfPhoto) ([]byte, error) {
	pdf := fpdf.New("P", "pt", "Letter", "")
	pdf.SetTitle(v.Name+" Resume", true)
	pdf.SetAuthor(v.Name, true)
	pdf.SetCreator("benpriddy.com", true)
	const margin = 44.0
	pdf.SetMargins(margin, margin, margin)
	pdf.SetAutoPageBreak(true, margin)
	pdf.AddPage()
	tr := pdf.UnicodeTranslatorFromDescriptor("") // UTF-8 → cp1252 for the core fonts
	t := func(s string) string { return tr(s) }
	pageW, _ := pdf.GetPageSize()
	width := pageW - 2*margin
	color := func(c [3]int) { pdf.SetTextColor(c[0], c[1], c[2]) }

	// the photo: a small circle at the right of the name, cropped to cover
	const photoD = 58.0
	textW := width
	if photo != nil {
		info := pdf.RegisterImageOptionsReader("photo", fpdf.ImageOptions{ImageType: photo.typ}, bytes.NewReader(photo.data))
		if pdf.Ok() && info != nil && info.Width() > 0 && info.Height() > 0 {
			iw, ih := photoD, photoD
			if r := info.Width() / info.Height(); r > 1 {
				iw = photoD * r
			} else {
				ih = photoD / r
			}
			cx, cy := margin+width-photoD/2, margin+photoD/2-4
			pdf.ClipCircle(cx, cy, photoD/2, false)
			pdf.ImageOptions("photo", cx-iw/2, cy-ih/2, iw, ih, false, fpdf.ImageOptions{ImageType: photo.typ}, 0, "")
			pdf.ClipEnd()
			textW = width - photoD - 16
		} else {
			pdf.ClearError() // a bad photo costs the photo, not the PDF
		}
	}

	// name and contact
	color(pdfInk)
	pdf.SetFont("Times", "", 28)
	pdf.CellFormat(textW, 30, t(v.Name), "", 1, "L", false, 0, "")
	var line []string
	if len(v.Groups) > 0 {
		line = append(line, v.Groups[0].Roles[0].Title+", "+v.Groups[0].Company)
	}
	for _, x := range []string{v.Location, v.Email, strings.TrimPrefix(strings.TrimPrefix(v.LinkedIn, "https://"), "www."), "benpriddy.com"} {
		if x != "" {
			line = append(line, x)
		}
	}
	color(pdfMuted)
	pdf.SetFont("Helvetica", "", 9)
	pdf.MultiCell(textW, 13, t(strings.Join(line, "  ·  ")), "", "L", false)
	pdf.Ln(4)
	if y := margin + photoD - 4; photo != nil && pdf.GetY() < y {
		pdf.SetY(y) // the first rule clears the photo
	}

	section := func(title string) {
		pdf.Ln(4)
		color(pdfAccent)
		pdf.SetFont("Helvetica", "B", 8)
		pdf.CellFormat(width, 12, strings.ToUpper(t(title)), "", 1, "L", false, 0, "")
		pdf.SetDrawColor(pdfRule[0], pdfRule[1], pdfRule[2])
		y := pdf.GetY() + 1
		pdf.Line(margin, y, margin+width, y)
		pdf.Ln(6)
	}
	body := func(text string, size float64, c [3]int) {
		color(c)
		pdf.SetFont("Helvetica", "", size)
		pdf.MultiCell(width, size*1.38, t(text), "", "L", false)
	}

	section("Profile")
	for i, p := range v.Profile {
		if i > 0 {
			pdf.Ln(3)
		}
		body(p, 9.5, pdfInk)
	}

	if len(v.Solving) > 0 {
		section("Currently solving")
		if v.SolvingTitle != "" {
			color(pdfInk)
			pdf.SetFont("Times", "I", 13)
			pdf.MultiCell(width, 16, t(v.SolvingTitle), "", "L", false)
			pdf.Ln(3)
		}
		for _, p := range v.Solving {
			body(p, 9.5, pdfInk)
		}
	}

	section("Experience")
	for gi, g := range v.Groups {
		if gi > 0 {
			pdf.Ln(5)
		}
		for _, role := range g.Roles {
			color(pdfInk)
			pdf.SetFont("Helvetica", "B", 10.5)
			title := t(role.Title + "  ·  " + g.Company)
			dates := t(role.Dates)
			pdf.SetFont("Helvetica", "", 9)
			dw := pdf.GetStringWidth(dates)
			pdf.SetFont("Helvetica", "B", 10.5)
			pdf.CellFormat(width-dw-6, 15, title, "", 0, "L", false, 0, "")
			color(pdfMuted)
			pdf.SetFont("Helvetica", "", 9)
			pdf.CellFormat(dw+6, 15, dates, "", 1, "R", false, 0, "")
		}
		for _, b := range g.Bullets {
			color(pdfInk)
			pdf.SetFont("Helvetica", "", 9.5)
			x := pdf.GetX()
			pdf.CellFormat(12, 12.5, t("•"), "", 0, "L", false, 0, "")
			pdf.MultiCell(width-12, 12.5, t(b), "", "L", false)
			pdf.SetX(x)
		}
	}

	if len(v.SkillsDo)+len(v.SkillsReview) > 0 {
		section("Technical skills")
		row := func(label string, items []string) {
			if len(items) == 0 {
				return
			}
			color(pdfInk)
			pdf.SetFont("Helvetica", "B", 9.5)
			pdf.CellFormat(110, 13.5, t(label), "", 0, "L", false, 0, "")
			pdf.SetFont("Helvetica", "", 9.5)
			pdf.MultiCell(width-110, 13.5, t(strings.Join(items, ", ")), "", "L", false)
			pdf.Ln(2)
		}
		row("What I can do", v.SkillsDo)
		row("What I can review", v.SkillsReview)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
