package notifications

import (
	_ "embed"
	"fmt"
	"html"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Email is the content of one message as a list of blocks. Render writes it
// as both the plain-text and the HTML body, so the two always say the same
// thing. A Footer renders last, wherever it appears in the list.
type Email []Block

// Block is one piece of an Email. Every block ends its text with a blank line.
type Block interface {
	text(b *strings.Builder)
	html(b *strings.Builder)
}

// Greeting opens an email by name: "Hi Alex,".
type Greeting string

// Paragraph is ordinary body copy. Line breaks are kept.
type Paragraph string

// Code is a short value the reader types elsewhere, such as a sign-in code.
type Code string

// Button is the one action the email asks for. Text shows "Label:" over the URL.
type Button struct {
	Label string
	URL   string
}

// AlbumList names Albums with their counts. The optional Heading introduces
// the list and More closes it, for Albums left out.
type AlbumList struct {
	Heading string
	Albums  []AlbumLine
	More    string
}

// AlbumLine is one Album. Status is a short lowercase label such as "new
// album".
type AlbumLine struct {
	Title  string
	Status string
	Counts string
}

// Note quotes the Curator's own words under a label.
type Note struct {
	Label string
	Text  string
}

// Footer is small print below the card. Text reads "Text Link:" over the URL,
// while HTML links the Link words.
type Footer struct {
	Text string
	Link string
	URL  string
}

// Light colors follow the app's light theme and dark colors its default
// theme. Clients that support prefers-color-scheme switch with the reader;
// Gmail's apps and Outlook on Windows darken the light design on their own.
const (
	colorPage      = "#f0f2f2"
	colorCard      = "#ffffff"
	colorBorder    = "#dce2e3"
	colorText      = "#212629"
	colorMuted     = "#657077"
	colorLink      = "#08788d"
	colorPrimary   = "#06b6d4"
	colorOnPrimary = "#083344"
	colorInset     = "#f0f2f2"
	colorPill      = "#e1e6e7"
	fontBody       = "Montserrat,-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif"
	fontHeading    = "'Slabo 13px',Georgia,'Times New Roman',serif"
)

// styles carries the dark theme and the phone layout. The m- classes exist
// only for these rules; inline styles hold the light design.
const styles = `:root{color-scheme:light dark;supported-color-schemes:light dark}
@media (prefers-color-scheme:dark){
.m-page{background:#101112!important}
.m-card{background:#191b1c!important;border-color:#2d3032!important}
.m-text{color:#edeeed!important}
.m-muted{color:#a8aaa9!important}
.m-link{color:#4ac2d3!important}
.m-rule{border-color:#2d3032!important}
.m-inset{background:#25282a!important}
.m-pill{background:#25282a!important;color:#4ac2d3!important}
}
@media (max-width:480px){
.m-outer{padding:20px 10px!important}
.m-card{padding:26px 20px 4px!important}
}`

// Render returns the plain-text and HTML bodies.
func (e Email) Render() (text, html string) {
	var t, footerText, card, footer strings.Builder
	for _, block := range e {
		if _, ok := block.(Footer); ok {
			block.text(&footerText)
			block.html(&footer)
		} else {
			block.text(&t)
			block.html(&card)
		}
	}
	t.WriteString(footerText.String())
	var h strings.Builder
	h.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light dark"><meta name="supported-color-schemes" content="light dark"><title></title><style>`)
	h.WriteString(styles)
	fmt.Fprintf(&h, `</style></head><body class="m-page" style="margin:0;padding:0;background:%s;">`, colorPage)
	// Inbox lists preview the first text they find; without this they would
	// show the mark's alt text.
	if preview := e.preview(); preview != "" {
		fmt.Fprintf(&h, `<div style="display:none;max-height:0;overflow:hidden;mso-hide:all;">%s%s</div>`, escape(preview), strings.Repeat("&#847;&zwnj;&nbsp;", 40))
	}
	fmt.Fprintf(&h, `<table role="presentation" class="m-page" width="100%%" cellpadding="0" cellspacing="0" border="0" style="background:%s;"><tr><td class="m-outer" align="center" style="padding:32px 16px;">`, colorPage)
	h.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;">`)
	h.WriteString(`<tr><td style="padding:0 4px 20px;">` + mark + `</td></tr>`)
	fmt.Fprintf(&h, `<tr><td class="m-card m-text" style="background:%s;border:1px solid %s;border-radius:12px;padding:32px 28px 8px;font-family:%s;font-size:16px;line-height:1.55;color:%s;">`, colorCard, colorBorder, fontBody, colorText)
	h.WriteString(card.String())
	h.WriteString(`</td></tr>`)
	if footer.Len() > 0 {
		fmt.Fprintf(&h, `<tr><td class="m-muted" style="padding:20px 4px 0;font-family:%s;font-size:13px;line-height:1.5;color:%s;">%s</td></tr>`, fontBody, colorMuted, footer.String())
	}
	h.WriteString(`</table></td></tr></table></body></html>`)
	return strings.TrimRight(t.String(), "\n") + "\n", h.String()
}

// preview is the first Paragraph, which inbox lists show beside the subject.
func (e Email) preview() string {
	for _, block := range e {
		if p, ok := block.(Paragraph); ok {
			return strings.ReplaceAll(string(p), "\n", " ")
		}
	}
	return ""
}

// The header is the Frames mark alone, carried inside the email so it shows
// without loading anything remote. Its cyan reads on light and on the dark
// backgrounds Gmail and Outlook substitute, so it needs no dark variant.
//
//go:embed mark.png
var markPNG []byte

// MarkContentID names the mark's part of the email; HTML shows it as
// cid:MarkContentID.
const MarkContentID = "mark@memento"

// MarkPNG returns the mark's image, for previews that cannot resolve cid:.
func MarkPNG() []byte { return markPNG }

const mark = `<img src="cid:` + MarkContentID + `" alt="Memento" width="40" height="40" style="display:block;border:0;">`

// escape makes text safe for HTML and keeps its line breaks.
func escape(s string) string {
	return strings.ReplaceAll(html.EscapeString(s), "\n", "<br>")
}

func capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

func (g Greeting) text(b *strings.Builder) { fmt.Fprintf(b, "Hi %s,\n\n", string(g)) }
func (g Greeting) html(b *strings.Builder) {
	fmt.Fprintf(b, `<h1 class="m-text" style="margin:0 0 16px;font-family:%s;font-size:26px;line-height:1.25;font-weight:normal;color:%s;">Hi %s,</h1>`, fontHeading, colorText, escape(string(g)))
}

func (p Paragraph) text(b *strings.Builder) { b.WriteString(string(p) + "\n\n") }
func (p Paragraph) html(b *strings.Builder) {
	fmt.Fprintf(b, `<p style="margin:0 0 20px;">%s</p>`, escape(string(p)))
}

func (c Code) text(b *strings.Builder) { b.WriteString(string(c) + "\n\n") }
func (c Code) html(b *strings.Builder) {
	fmt.Fprintf(b, `<div class="m-inset m-text" style="margin:0 0 20px;padding:18px 12px 18px 20px;background:%s;border-radius:10px;text-align:center;font-size:34px;line-height:1.2;font-weight:500;letter-spacing:8px;color:%s;">%s</div>`, colorInset, colorText, escape(string(c)))
}

func (a Button) text(b *strings.Builder) { fmt.Fprintf(b, "%s:\n%s\n\n", a.Label, a.URL) }
func (a Button) html(b *strings.Builder) {
	fmt.Fprintf(b, `<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin:4px 0 24px;"><tr><td style="background:%[1]s;border-radius:8px;">`+
		`<a href="%[2]s" style="display:inline-block;padding:13px 24px;font-family:%[3]s;font-size:16px;font-weight:500;line-height:1.2;color:%[4]s;text-decoration:none;border-radius:8px;">%[5]s</a>`+
		`</td></tr></table>`, colorPrimary, html.EscapeString(a.URL), fontBody, colorOnPrimary, escape(a.Label))
}

func (l AlbumList) text(b *strings.Builder) {
	if l.Heading != "" {
		b.WriteString(l.Heading + ":\n\n")
	}
	for _, album := range l.Albums {
		if album.Status == "" {
			fmt.Fprintf(b, "%s\n%s\n\n", album.Title, album.Counts)
		} else {
			fmt.Fprintf(b, "%s (%s)\n%s\n\n", album.Title, album.Status, album.Counts)
		}
	}
	if l.More != "" {
		b.WriteString(l.More + "\n\n")
	}
}

func (l AlbumList) html(b *strings.Builder) {
	if l.Heading != "" {
		fmt.Fprintf(b, `<h2 class="m-text" style="margin:8px 0 4px;font-family:%s;font-size:19px;line-height:1.3;font-weight:normal;color:%s;">%s</h2>`, fontHeading, colorText, escape(l.Heading))
	}
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:0 0 20px;">`)
	for i, album := range l.Albums {
		rule := "border-top:1px solid " + colorBorder + ";"
		if i == 0 {
			rule = ""
		}
		title := escape(album.Title)
		fmt.Fprintf(b, `<tr><td class="m-rule" style="padding:12px 0;%s"><div style="font-weight:500;">%s`, rule, title)
		if album.Status != "" {
			fmt.Fprintf(b, ` <span class="m-pill" style="display:inline-block;padding:1px 8px;border-radius:999px;background:%s;color:%s;font-size:12px;line-height:1.6;font-weight:500;vertical-align:2px;">%s</span>`, colorPill, colorLink, escape(capitalize(album.Status)))
		}
		fmt.Fprintf(b, `</div><div class="m-muted" style="font-size:14px;color:%s;">%s</div></td></tr>`, colorMuted, escape(album.Counts))
	}
	if l.More != "" {
		fmt.Fprintf(b, `<tr><td class="m-rule m-muted" style="padding:12px 0;border-top:1px solid %s;font-size:14px;color:%s;">%s</td></tr>`, colorBorder, colorMuted, escape(l.More))
	}
	b.WriteString(`</table>`)
}

func (n Note) text(b *strings.Builder) { fmt.Fprintf(b, "%s:\n%s\n\n", n.Label, n.Text) }
func (n Note) html(b *strings.Builder) {
	fmt.Fprintf(b, `<div class="m-inset" style="margin:0 0 24px;padding:14px 16px;background:%s;border-left:3px solid %s;border-radius:4px 10px 10px 4px;">`+
		`<div class="m-muted" style="margin:0 0 4px;font-size:13px;font-weight:500;color:%s;">%s</div><div>%s</div></div>`,
		colorInset, colorPrimary, colorMuted, escape(n.Label), escape(n.Text))
}

func (f Footer) text(b *strings.Builder) { fmt.Fprintf(b, "%s %s:\n%s\n\n", f.Text, f.Link, f.URL) }
func (f Footer) html(b *strings.Builder) {
	fmt.Fprintf(b, `%s <a class="m-link" href="%s" style="color:%s;">%s</a>.`, escape(f.Text), html.EscapeString(f.URL), colorLink, escape(f.Link))
}
