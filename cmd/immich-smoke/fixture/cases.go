package fixture

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/url"

	"github.com/robinjoseph08/memento/internal/testmedia"
)

// SetupCases adds a separate album after the original three-album snapshot checks.
// The Live Photo motion asset is linked through the public API, not album membership.
func (f *Library) SetupCases(ctx context.Context) error {
	if f.CasesAlbum.ID != "" {
		return fmt.Errorf("additional fixture cases already exist")
	}
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := range 48 {
		for x := range 64 {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 3), G: uint8(y * 4), B: 173, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		return err
	}
	f.PNGBytes = encoded.Bytes()
	f.PNG = Asset{Filename: "generated.png", CapturedAt: "2020-01-01T00:00:00Z"}
	var err error
	if f.PNG.ID, err = f.owner.upload(ctx, f.PNG.Filename, f.PNGBytes); err != nil {
		return err
	}
	// An EBML Void element keeps the motion file playable but distinct from PlainVideo.
	motion := append(append([]byte{}, testmedia.Plain...), 0xec, 0x81, 0x00)
	f.LiveMotion = Video{Filename: "live-motion.webm", Bytes: motion}
	if f.LiveMotion.ID, err = f.owner.upload(ctx, f.LiveMotion.Filename, motion); err != nil {
		return err
	}
	for i, name := range []string{"live-still.jpg", "stack-primary.jpg", "stack-secondary.jpg"} {
		asset := Asset{Filename: name, CapturedAt: "2024-07-04T12:00:00-07:00"}
		data, err := JPEG(asset.Photo, i+10)
		if err != nil {
			return err
		}
		if asset.ID, err = f.owner.upload(ctx, name, data); err != nil {
			return err
		}
		if i == 0 {
			f.LivePhoto = asset
		} else {
			f.Stack = append(f.Stack, asset)
		}
	}
	if err := f.owner.json(ctx, http.MethodPut, "/assets/"+url.PathEscape(f.LivePhoto.ID), map[string]string{"livePhotoVideoId": f.LiveMotion.ID}, nil); err != nil {
		return err
	}
	var stack struct {
		ID string `json:"id"`
	}
	if err := f.owner.json(ctx, http.MethodPost, "/stacks", map[string]any{"assetIds": []string{f.Stack[0].ID, f.Stack[1].ID}}, &stack); err != nil {
		return err
	}
	if stack.ID == "" {
		return fmt.Errorf("fixture stack has no ID")
	}
	f.StackID = stack.ID
	f.PlainVideo = Video{Filename: "unchaptered.webm", Bytes: testmedia.Plain}
	if f.PlainVideo.ID, err = f.owner.upload(ctx, f.PlainVideo.Filename, f.PlainVideo.Bytes); err != nil {
		return err
	}
	f.CasesAlbum = Album{Name: "Smoke extra cases", Description: "PNG, Live Photo, flattened stack, and unchaptered video", AssetIDs: []string{f.PNG.ID, f.LivePhoto.ID, f.Stack[0].ID, f.Stack[1].ID, f.PlainVideo.ID}}
	var album struct {
		ID string `json:"id"`
	}
	if err := f.owner.json(ctx, http.MethodPost, "/albums", map[string]any{"albumName": f.CasesAlbum.Name, "description": f.CasesAlbum.Description, "assetIds": f.CasesAlbum.AssetIDs}, &album); err != nil {
		return err
	}
	if album.ID == "" {
		return fmt.Errorf("fixture cases album has no ID")
	}
	f.CasesAlbum.ID = album.ID
	return nil
}
