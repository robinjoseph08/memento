// Command emailpreview serves every email Memento sends, rendered from sample
// data, so their design can be reviewed without the app. With -send-to it
// instead sends each sample once through a real mail server, for checking in
// Gmail, Apple Mail, and other clients.
package main

import (
	"context"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/notifications"
)

const publicURL = "https://memento.example.com"

type sample struct {
	Name    string
	Title   string
	Message notifications.Message
}

func samples() []sample {
	update := func(name, title, person string, content notifications.UpdateContent) sample {
		subject, email := notifications.UpdateEmail(publicURL, person, content, "sample-unsubscribe-token-0123456789abcdef")
		text, html := email.Render()
		return sample{Name: name, Title: title, Message: notifications.Message{Kind: notifications.KindUpdate, Subject: subject, Body: text, HTML: html}}
	}
	album := func(id, title, status string, photos, videos int) notifications.NotificationAlbum {
		return notifications.NotificationAlbum{ID: id, Title: title, Status: status, PhotoCount: photos, VideoCount: videos}
	}
	return []sample{
		update("update-one-new", "Update: one new album with a note", "Grandma", notifications.UpdateContent{
			Albums: []notifications.NotificationAlbum{album("a1", "Lake Tahoe, Summer 2026", notifications.AlbumNew, 42, 3)},
			Note:   "Here are the photos from our trip! The sunset ones are my favorite.",
		}),
		update("update-one-updated", "Update: new photos in one album", "Alex", notifications.UpdateContent{
			Albums: []notifications.NotificationAlbum{album("a2", "Emma's First Year", notifications.AlbumUpdated, 12, 0)},
		}),
		update("update-several", "Update: several albums and albums to join", "Priya", notifications.UpdateContent{
			Albums: []notifications.NotificationAlbum{
				album("a1", "Lake Tahoe, Summer 2026", notifications.AlbumNew, 42, 3),
				album("a2", "Emma's First Year", notifications.AlbumUpdated, 12, 0),
				album("a3", "Thanksgiving", notifications.AlbumUpdated, 0, 2),
			},
			OfferedAlbums: []notifications.NotificationAlbum{
				album("o1", "Cousins Camping Trip", notifications.AlbumOffered, 87, 5),
				album("o2", "Dad's Retirement Party", notifications.AlbumOffered, 30, 1),
			},
			MoreOfferedAlbums: 3,
			Note:              "Lots of new things this week.\nLet me know if any photo should come down.",
		}),
		update("update-offered-one", "Update: one album to join", "Sam", notifications.UpdateContent{
			OfferedAlbums: []notifications.NotificationAlbum{album("o1", "Cousins Camping Trip", notifications.AlbumOffered, 87, 5)},
		}),
		update("update-long", "Update: long names", "Maria Fernanda de los Santos-Villanueva", notifications.UpdateContent{
			Albums: []notifications.NotificationAlbum{album("a4", "Wedding Weekend in the Napa Valley with the Whole Extended Family and Friends", notifications.AlbumNew, 1, 1)},
		}),
		{Name: "sign-in-code", Title: "Sign-in code", Message: identity.SignInCodeMessage("alex@example.com", "482913")},
		{Name: "invitation", Title: "Invitation", Message: identity.InvitationMessage(publicURL, "Grandma", "Robin", "grandma@example.com")},
		{Name: "join-request", Title: "Curator: someone asked to join", Message: identity.JoinRequestMessage(publicURL, "Jordan Lee", "jordan.lee@example.com")},
		{Name: "album-request", Title: "Curator: someone asked to see an album", Message: identity.AlbumRequestMessage(publicURL, "Alex", "Lake Tahoe, Summer 2026")},
	}
}

func main() {
	addr := flag.String("addr", "localhost:4410", "address to serve previews on")
	sendTo := flag.String("send-to", "", "send every sample to this address and exit")
	smtpURL := flag.String("smtp-url", os.Getenv("SMTP_URL"), "mail server for -send-to")
	smtpFrom := flag.String("smtp-from", os.Getenv("SMTP_FROM"), "sender for -send-to")
	flag.Parse()
	if *sendTo != "" {
		if err := send(*sendTo, *smtpURL, *smtpFrom); err != nil {
			log.Fatal(err)
		}
		return
	}
	server := &http.Server{Addr: *addr, Handler: routes(), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(server.ListenAndServe())
}

func send(to, smtpURL, from string) error {
	mailer, err := notifications.NewSMTPMailer(smtpURL, from)
	if err != nil {
		return fmt.Errorf("set -smtp-url and -smtp-from, or SMTP_URL and SMTP_FROM: %w", err)
	}
	for _, s := range samples() {
		message := s.Message
		message.To = to
		message.Subject = "[Preview] " + message.Subject
		if err := mailer.Send(context.Background(), message); err != nil {
			return fmt.Errorf("%s: %w", s.Name, err)
		}
		fmt.Printf("Sent %s\n", s.Name)
	}
	return nil
}

// routes renders every request from scratch, so a rebuilt binary shows
// changes on the next reload.
func routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/"+samples()[0].Name, http.StatusFound)
	})
	mux.HandleFunc("GET /{name}", func(w http.ResponseWriter, r *http.Request) {
		all := samples()
		for _, s := range all {
			if s.Name == r.PathValue("name") {
				_ = page.Execute(w, map[string]any{"Samples": all, "Current": s, "Dark": r.URL.Query().Has("dark")})
				return
			}
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /{name}/html", func(w http.ResponseWriter, r *http.Request) {
		for _, s := range samples() {
			if s.Name == r.PathValue("name") {
				body := s.Message.HTML
				// Pin the chosen scheme regardless of the viewer's system setting.
				scheme := "@media not all"
				if r.URL.Query().Has("dark") {
					scheme = "@media all"
				}
				body = strings.Replace(body, "@media (prefers-color-scheme:dark)", scheme, 1)
				// Browsers can't resolve cid: links to the email's own parts.
				body = strings.ReplaceAll(body, "cid:"+notifications.MarkContentID, "/mark.png")
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte(body))
				return
			}
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /mark.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(notifications.MarkPNG())
	})
	return mux
}

var page = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>{{.Current.Title}} · Email previews</title>
<style>
body{margin:0;display:flex;min-height:100vh;font:14px/1.5 -apple-system,BlinkMacSystemFont,sans-serif;background:#e6e9ea;color:#212629}
nav{width:260px;flex:none;padding:16px;background:#fff;border-right:1px solid #dce2e3}
nav a{display:block;padding:6px 10px;border-radius:6px;color:inherit;text-decoration:none}
nav a.on{background:#06b6d4;color:#083344;font-weight:600}
main{flex:1;padding:20px 24px;overflow:auto}
.subject{font-size:18px;font-weight:600;margin:0 0 4px}
.bar{margin:0 0 16px;color:#657077}
.bar a{color:#08788d}
.panes{display:flex;gap:20px;align-items:flex-start;flex-wrap:wrap}
.pane h3{margin:0 0 6px;font-size:12px;text-transform:uppercase;letter-spacing:.06em;color:#657077}
iframe{border:1px solid #c9d0d2;border-radius:8px;background:#fff;height:900px}
pre{margin:0;width:420px;padding:16px;background:#fff;border:1px solid #c9d0d2;border-radius:8px;white-space:pre-wrap;word-break:break-word;font:13px/1.5 ui-monospace,Menlo,monospace}
</style></head><body>
<nav><strong>Emails</strong>{{$c := .Current}}{{$dark := .Dark}}{{range .Samples}}
<a href="/{{.Name}}{{if $dark}}?dark{{end}}"{{if eq .Name $c.Name}} class="on"{{end}}>{{.Title}}</a>{{end}}</nav>
<main>
<p class="subject">{{.Current.Message.Subject}}</p>
<p class="bar">{{if .Dark}}Dark · <a href="/{{.Current.Name}}">Show light</a>{{else}}Light · <a href="/{{.Current.Name}}?dark">Show dark</a>{{end}} · <a href="/{{.Current.Name}}/html{{if .Dark}}?dark{{end}}" target="_blank">Open HTML alone</a></p>
<div class="panes">
<div class="pane"><h3>Phone</h3><iframe src="/{{.Current.Name}}/html{{if .Dark}}?dark{{end}}" width="375"></iframe></div>
<div class="pane"><h3>Desktop</h3><iframe src="/{{.Current.Name}}/html{{if .Dark}}?dark{{end}}" width="680"></iframe></div>
<div class="pane"><h3>Plain text</h3><pre>{{.Current.Message.Body}}</pre></div>
</div></main></body></html>`))
