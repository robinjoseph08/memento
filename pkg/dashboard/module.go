// Package dashboard composes the Curator's pending work from the modules that
// own it. It reads their public projections and adds no status of its own.
package dashboard

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/robinjoseph08/memento/pkg/publishing"
)

// Requests is the consumer-owned view of Identity.
type Requests interface {
	PendingAccessRequests(context.Context) (int, error)
}

// Albums is the consumer-owned view of Publishing.
type Albums interface {
	ListAlbums(context.Context, string) ([]publishing.Album, error)
	ChapterFailures(context.Context) ([]publishing.ChapterFailure, error)
}

// Mail is the consumer-owned view of Notifications. The preview is computed
// on request and changes nothing; its row count is the unannounced people.
// It resolves every eligible Person's visible content, which is fine at
// household scale but is the heaviest part of a poll.
type Mail interface {
	OpenDeliveries(context.Context) ([]notifications.DeliveryWork, error)
	PreviewUpdates(context.Context) (notifications.Preview, error)
}

type Module struct {
	requests Requests
	albums   Albums
	mail     Mail
}

func New(requests Requests, albums Albums, mail Mail) *Module {
	return &Module{requests: requests, albums: albums, mail: mail}
}

// Overview sorts every module's open work into the two groups. Failed and
// interrupted imports need a decision; queued and processing ones only keep
// the page polling.
func (m *Module) Overview(ctx context.Context) (Dashboard, error) {
	result := Dashboard{
		NeedsAttention: Attention{Imports: []AlbumWork{}, Deliveries: []Delivery{}, Chapters: []ChapterWork{}},
		Ready:          Ready{Unpublished: []AlbumWork{}},
	}
	pending, err := m.requests.PendingAccessRequests(ctx)
	if err != nil {
		return result, err
	}
	result.NeedsAttention.PendingRequests = pending
	albums, err := m.albums.ListAlbums(ctx, "")
	if err != nil {
		return result, err
	}
	for _, album := range albums {
		work := AlbumWork{ID: album.ID, Title: album.Title, Status: album.Status, Message: album.Message, Ready: album.Ready}
		switch album.Status {
		case "failed", "interrupted":
			result.NeedsAttention.Imports = append(result.NeedsAttention.Imports, work)
		case "queued", "processing":
			result.Active = true
		default:
			if !album.Published {
				result.Ready.Unpublished = append(result.Ready.Unpublished, work)
			}
		}
	}
	chapters, err := m.albums.ChapterFailures(ctx)
	if err != nil {
		return result, err
	}
	for _, failure := range chapters {
		result.NeedsAttention.Chapters = append(result.NeedsAttention.Chapters, ChapterWork(failure))
	}
	deliveries, err := m.mail.OpenDeliveries(ctx)
	if err != nil {
		return result, err
	}
	for _, work := range deliveries {
		switch work.Status {
		case notifications.StatusFailed, notifications.StatusUncertain:
			result.NeedsAttention.Deliveries = append(result.NeedsAttention.Deliveries, Delivery{ID: work.ID, Kind: work.Kind, Recipient: work.Recipient, Status: work.Status, Message: work.Message,
				Attempts: work.Attempts, UpdatedAt: work.UpdatedAt, PersonID: work.PersonID, PersonName: work.PersonName, InvitationID: work.InvitationID})
		default:
			result.Active = true
		}
	}
	preview, err := m.mail.PreviewUpdates(ctx)
	if err != nil {
		return result, err
	}
	result.Ready.UnannouncedPeople = len(preview.People)
	return result, nil
}
