package immich_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListFaces(t *testing.T) {
	t.Parallel()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/faces", r.URL.Path)
		assert.Equal(t, "asset/opaque ?#%", r.URL.Query().Get("id"))
		assert.Equal(t, "read-key", r.Header.Get("X-Api-Key"))
		_, _ = fmt.Fprint(w, `[
			{"id":"face-1","imageHeight":48,"imageWidth":64,"boundingBoxX1":5,"boundingBoxX2":25,"boundingBoxY1":7,"boundingBoxY2":31,"sourceType":"manual","person":{"id":"person-1","name":"Smoke person","birthDate":"1990-01-02","thumbnailPath":"upload/thumb.jpeg","isHidden":false,"updatedAt":"2024-07-02T07:00:00.123Z","isFavorite":true,"color":"#112233"},"future":"ignored"},
			{"id":"face-2","imageHeight":48,"imageWidth":64,"boundingBoxX1":30,"boundingBoxX2":40,"boundingBoxY1":10,"boundingBoxY2":20,"person":{"id":"person-1","name":"Smoke person","birthDate":"1990-01-02","thumbnailPath":"upload/thumb.jpeg","isHidden":false}},
			{"id":"face-3","imageHeight":48,"imageWidth":64,"boundingBoxX1":42,"boundingBoxX2":50,"boundingBoxY1":11,"boundingBoxY2":22,"person":null}
		]`)
	}))
	t.Cleanup(fixture.Close)

	faces, err := immich.New(fixture.URL, "read-key").ListFaces(t.Context(), "asset/opaque ?#%")
	require.NoError(t, err)
	require.Equal(t, []immich.Face{
		{FaceID: "face-1", ID: "person-1", Name: "Smoke person", ThumbnailPath: "upload/thumb.jpeg", UpdatedAt: "2024-07-02T07:00:00.123Z", Hidden: false, ImageHeight: 48, ImageWidth: 64, BoundingBoxX1: 5, BoundingBoxX2: 25, BoundingBoxY1: 7, BoundingBoxY2: 31, SourceType: "manual"},
		{FaceID: "face-2", ID: "person-1", Name: "Smoke person", ThumbnailPath: "upload/thumb.jpeg", Hidden: false, ImageHeight: 48, ImageWidth: 64, BoundingBoxX1: 30, BoundingBoxX2: 40, BoundingBoxY1: 10, BoundingBoxY2: 20},
		{FaceID: "face-3", ImageHeight: 48, ImageWidth: 64, BoundingBoxX1: 42, BoundingBoxX2: 50, BoundingBoxY1: 11, BoundingBoxY2: 22},
	}, faces)
}

func TestListFacesAcceptsEmptyListAndNullBirthDate(t *testing.T) {
	t.Parallel()
	for name, response := range map[string]string{
		"empty list":      `[]`,
		"null birth date": `[{"id":"face-1","imageHeight":48,"imageWidth":64,"boundingBoxX1":5,"boundingBoxX2":25,"boundingBoxY1":7,"boundingBoxY2":31,"person":{"id":"person-1","name":"Person","birthDate":null,"thumbnailPath":"","isHidden":false}}]`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, response) }))
			defer fixture.Close()
			faces, err := immich.New(fixture.URL, "read-key").ListFaces(t.Context(), "asset")
			require.NoError(t, err)
			require.NotNil(t, faces)
			if name == "null birth date" {
				require.Len(t, faces, 1)
				assert.Equal(t, "person-1", faces[0].ID)
			}
		})
	}
}

func TestListFacesRejectsUnreadableResponses(t *testing.T) {
	t.Parallel()
	validFace := `{"id":"face-1","imageHeight":48,"imageWidth":64,"boundingBoxX1":5,"boundingBoxX2":25,"boundingBoxY1":7,"boundingBoxY2":31,"sourceType":"manual","person":null}`
	for name, response := range map[string]string{
		"null list":                 `null`,
		"object list":               `{}`,
		"missing face field":        `[{"id":"face-1","imageHeight":48,"imageWidth":64,"boundingBoxX1":5,"boundingBoxX2":25,"boundingBoxY1":7,"person":null}]`,
		"null required face field":  `[{"id":"face-1","imageHeight":48,"imageWidth":64,"boundingBoxX1":5,"boundingBoxX2":25,"boundingBoxY1":7,"boundingBoxY2":null,"person":null}]`,
		"empty face identity":       `[{"id":"","imageHeight":48,"imageWidth":64,"boundingBoxX1":5,"boundingBoxX2":25,"boundingBoxY1":7,"boundingBoxY2":31,"person":null}]`,
		"negative image size":       `[{"id":"face-1","imageHeight":-1,"imageWidth":64,"boundingBoxX1":5,"boundingBoxX2":25,"boundingBoxY1":7,"boundingBoxY2":31,"person":null}]`,
		"unsafe image size":         `[{"id":"face-1","imageHeight":48,"imageWidth":9007199254740992,"boundingBoxX1":5,"boundingBoxX2":25,"boundingBoxY1":7,"boundingBoxY2":31,"person":null}]`,
		"unsafe coordinate":         `[{"id":"face-1","imageHeight":48,"imageWidth":64,"boundingBoxX1":9007199254740992,"boundingBoxX2":25,"boundingBoxY1":7,"boundingBoxY2":31,"person":null}]`,
		"invalid source type":       `[{"id":"face-1","imageHeight":48,"imageWidth":64,"boundingBoxX1":5,"boundingBoxX2":25,"boundingBoxY1":7,"boundingBoxY2":31,"sourceType":"private-key","person":null}]`,
		"null source type":          `[{"id":"face-1","imageHeight":48,"imageWidth":64,"boundingBoxX1":5,"boundingBoxX2":25,"boundingBoxY1":7,"boundingBoxY2":31,"sourceType":null,"person":null}]`,
		"missing person field":      `[{"id":"face-1","imageHeight":48,"imageWidth":64,"boundingBoxX1":5,"boundingBoxX2":25,"boundingBoxY1":7,"boundingBoxY2":31}]`,
		"missing person field data": `[{"id":"face-1","imageHeight":48,"imageWidth":64,"boundingBoxX1":5,"boundingBoxX2":25,"boundingBoxY1":7,"boundingBoxY2":31,"person":{"id":"person-1","name":"Person","birthDate":null,"thumbnailPath":"thumb.jpg"}}]`,
		"empty person identity":     `[{"id":"face-1","imageHeight":48,"imageWidth":64,"boundingBoxX1":5,"boundingBoxX2":25,"boundingBoxY1":7,"boundingBoxY2":31,"person":{"id":"","name":"Person","birthDate":null,"thumbnailPath":"thumb.jpg","isHidden":false}}]`,
		"invalid person birth date": `[{"id":"face-1","imageHeight":48,"imageWidth":64,"boundingBoxX1":5,"boundingBoxX2":25,"boundingBoxY1":7,"boundingBoxY2":31,"person":{"id":"person-1","name":"Person","birthDate":"private-key","thumbnailPath":"thumb.jpg","isHidden":false}}]`,
		"invalid person update":     `[{"id":"face-1","imageHeight":48,"imageWidth":64,"boundingBoxX1":5,"boundingBoxX2":25,"boundingBoxY1":7,"boundingBoxY2":31,"person":{"id":"person-1","name":"Person","birthDate":null,"thumbnailPath":"thumb.jpg","isHidden":false,"updatedAt":"private-key"}}]`,
		"null optional person bool": `[{"id":"face-1","imageHeight":48,"imageWidth":64,"boundingBoxX1":5,"boundingBoxX2":25,"boundingBoxY1":7,"boundingBoxY2":31,"person":{"id":"person-1","name":"Person","birthDate":null,"thumbnailPath":"thumb.jpg","isHidden":false,"isFavorite":null}}]`,
		"duplicate face identity":   `[` + validFace + `,` + validFace + `]`,
		"valid followed by junk":    `[` + validFace + `] private-key`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, response) }))
			defer fixture.Close()
			_, err := immich.New(fixture.URL, "private-key").ListFaces(t.Context(), "asset")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unreadable")
			assert.NotContains(t, fmt.Sprintf("%+v", err), "private-key")
			assert.Equal(t, 1, stackCount(err))
		})
	}
}
