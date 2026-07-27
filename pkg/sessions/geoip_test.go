package sessions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalGeoIPIsOptionalAndNeverFallsBackToANetworkLookup(t *testing.T) {
	resolver, err := OpenLocalGeoIP("")
	require.NoError(t, err)
	assert.Nil(t, resolver)
	resolver, err = OpenLocalGeoIP("testdata/does-not-exist.mmdb")
	assert.Nil(t, resolver)
	assert.ErrorContains(t, err, "open local GeoIP database")
}
