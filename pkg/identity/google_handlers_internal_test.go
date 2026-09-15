package identity

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreGoogleTransactionEvictsOldestAtCapacity(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	transactions := make(map[string]googleTransaction, maxGoogleLoginTransactions)
	for i := range maxGoogleLoginTransactions {
		transactions[fmt.Sprintf("pending-%04d", i)] = googleTransaction{expires: now.Add(time.Duration(i+1) * time.Second)}
	}
	added := googleTransaction{expires: now.Add(googleLoginLifetime)}

	storeGoogleTransaction(transactions, "new", added, "", now)

	require.Len(t, transactions, maxGoogleLoginTransactions)
	assert.NotContains(t, transactions, "pending-0000")
	assert.Equal(t, added, transactions["new"])
}
