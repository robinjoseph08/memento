package media

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSignedTokenVerifiesUntilItExpiresAndRejectsTampering(t *testing.T) {
	t.Parallel()
	key := []byte("0123456789abcdef0123456789abcdef")
	minted := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	expiry := minted.Add(6 * time.Hour)
	claims := tokenClaims{PersonID: "0198c1f0-0000-7000-8000-000000000001", EntryID: "0198c1f0-0000-7000-8000-000000000002", Variant: "playback"}
	token := signToken(claims, expiry, key)

	verified, err := verifyToken(token, key, minted)
	require.NoError(t, err)
	require.Equal(t, claims, verified)
	_, err = verifyToken(token, key, expiry.Add(-time.Second))
	require.NoError(t, err)
	_, err = verifyToken(token, key, expiry)
	require.ErrorIs(t, err, errInvalidToken, "a token stops working at its expiry")

	_, err = verifyToken(token, []byte("another key of the very same len"), minted)
	require.ErrorIs(t, err, errInvalidToken)
	_, err = verifyToken(strings.Replace(token, "playback", "preview", 1), key, minted)
	require.ErrorIs(t, err, errInvalidToken, "the variant is covered by the signature")
	_, err = verifyToken(strings.Replace(token, claims.EntryID, "0198c1f0-0000-7000-8000-000000000003", 1), key, minted)
	require.ErrorIs(t, err, errInvalidToken)
	later := signToken(claims, expiry.Add(time.Hour), key)
	forged := later[:strings.LastIndex(later, ".")] + token[strings.LastIndex(token, "."):]
	_, err = verifyToken(forged, key, minted)
	require.ErrorIs(t, err, errInvalidToken, "the expiry is covered by the signature")
	for _, malformed := range []string{"", "a.b.c", token + ".extra", token[:len(token)-2]} {
		_, err = verifyToken(malformed, key, minted)
		require.ErrorIs(t, err, errInvalidToken, malformed)
	}
	_, err = verifyToken(signToken(claims, expiry, nil), nil, minted)
	require.ErrorIs(t, err, errInvalidToken, "an installation without a key signs nothing")
}
