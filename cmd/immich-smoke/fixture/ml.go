package fixture

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"time"
)

// NASA's public-domain Eileen Collins portrait, distributed by scikit-image:
// https://github.com/scikit-image/scikit-image/blob/v0.19.3/skimage/data/astronaut.png
// Converted to JPEG with ffmpeg -q:v 3. Only the opt-in ML smoke uploads it.
//
//go:embed portrait.jpg
var mlPortrait []byte

// RunML uploads a known portrait and requests face detection.
// The separate ML smoke must enable Immich's ML service. Manual and EXIF faces
// cannot satisfy this check. Person assignment, geometry, and model scores are
// deliberately not assertions because they vary with model and configuration.
func (f *Library) RunML(ctx context.Context) error {
	id, err := f.owner.upload(ctx, "ml-portrait.jpg", mlPortrait)
	if err != nil {
		return err
	}
	if err := f.owner.json(ctx, http.MethodPost, "/assets/jobs", map[string]any{"assetIds": []string{id}, "name": "refresh-faces"}, nil); err != nil {
		return err
	}
	var lastErr error
	for {
		faces, err := f.Source().ListFaces(ctx, id)
		if err == nil {
			for _, face := range faces {
				if face.SourceType == "machine-learning" {
					return nil
				}
			}
			err = fmt.Errorf("no machine-learning face detected on the uploaded portrait")
		}
		lastErr = err
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("ML smoke: %w; last result: %w", ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
}
