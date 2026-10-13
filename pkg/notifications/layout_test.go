package notifications_test

import (
	"testing"

	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/stretchr/testify/assert"
)

func TestEmailRendersTheSameContentAsTextAndHTML(t *testing.T) {
	t.Parallel()
	text, html := notifications.Email{
		notifications.Greeting("Alex"),
		notifications.Paragraph("Fish & <Chips>"),
		notifications.AlbumList{Albums: []notifications.AlbumLine{{Title: `Coast "2026"`, Status: "new album", Counts: "2 photos"}}},
		notifications.Note{Label: "A note from your Curator", Text: "Line one\nLine two"},
		notifications.Button{Label: "See them on Memento", URL: "https://memento.example.test/albums"},
		notifications.Footer{Text: "You chose to get these emails. To stop them,", Link: "open this link and confirm", URL: "https://memento.example.test/unsubscribe?token=t"},
	}.Render()

	assert.Equal(t, `Hi Alex,

Fish & <Chips>

Coast "2026" (new album)
2 photos

A note from your Curator:
Line one
Line two

See them on Memento:
https://memento.example.test/albums

You chose to get these emails. To stop them, open this link and confirm:
https://memento.example.test/unsubscribe?token=t
`, text)
	assert.Contains(t, html, "Fish &amp; &lt;Chips&gt;")
	assert.Contains(t, html, `href="https://memento.example.test/unsubscribe?token=t"`)
	assert.Contains(t, html, "Coast &#34;2026&#34;")
	assert.Contains(t, html, "New album")
	assert.Contains(t, html, "Line one<br>Line two")
	assert.Contains(t, html, `>open this link and confirm</a>`)
	assert.NotContains(t, html, "<Chips>")
	assert.Contains(t, html, `src="cid:`+notifications.MarkContentID+`"`, "the mark is carried inside the email")
}
