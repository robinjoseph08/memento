package media

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// errInvalidToken covers every reason a signed media token is refused, so a
// caller cannot tell a forged token from an expired one.
var errInvalidToken = errors.New("invalid signed media token")

// tokenClaims name what one signed media URL may fetch, and for whom.
type tokenClaims struct {
	PersonID string
	EntryID  string
	Variant  string
}

// signToken authorizes claims until expiry. The token is its own payload
// followed by an HMAC over it, so verification needs no stored state.
func signToken(claims tokenClaims, expiry time.Time, key []byte) string {
	payload := strings.Join([]string{claims.PersonID, claims.EntryID, claims.Variant, strconv.FormatInt(expiry.Unix(), 10)}, ".")
	return payload + "." + base64.RawURLEncoding.EncodeToString(tokenMAC(payload, key))
}

// verifyToken returns the claims of an untampered token that has not reached
// its expiry at now.
func verifyToken(token string, key []byte, now time.Time) (tokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 5 || len(key) == 0 {
		return tokenClaims{}, errInvalidToken
	}
	mac, err := base64.RawURLEncoding.DecodeString(parts[4])
	if err != nil || !hmac.Equal(mac, tokenMAC(strings.Join(parts[:4], "."), key)) {
		return tokenClaims{}, errInvalidToken
	}
	expiry, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || !now.Before(time.Unix(expiry, 0)) {
		return tokenClaims{}, errInvalidToken
	}
	return tokenClaims{PersonID: parts[0], EntryID: parts[1], Variant: parts[2]}, nil
}

func tokenMAC(payload string, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}
