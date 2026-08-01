package tests

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type productionEvidence struct {
	path string
	test string
}

var matrixEvidence = map[matrixSurface][]productionEvidence{
	surfaceLibraryProjection: {
		{"pkg/library/service_integration_test.go", "TestRecipientLibraryPaginatesOnlyCurrentAuthorizedUnion"},
		{"pkg/events/loose_publications_integration_test.go", "TestLoosePublicationProjectsAuthorizedRecipientLibraryAndSearch"},
	},
	surfaceLibraryChronology: {{"pkg/library/service_integration_test.go", "TestChronologyProjectsTheCompleteCurrentAuthorizedDistinctLibraryAndDirectAnchors"}},
	surfaceEventDetail: {
		{"pkg/library/service_integration_test.go", "TestValidUnauthorizedIdentifiersAreIndistinguishableFromMissingContent"},
		{"pkg/events/loose_publications_integration_test.go", "TestLooseOnlyEntitlementDoesNotGrantEventDetailOrArchive"},
	},
	surfaceLooseItemDetail: {{"pkg/events/loose_publications_integration_test.go", "TestLoosePublicationProjectsAuthorizedRecipientLibraryAndSearch"}},
	surfacePeopleDirectory: {{"pkg/visibility/visibility_integration_test.go", "TestRecipientPeopleSearchAuthorizesBeforeMatchingAndRevealsOnlyTheDirectUnion"}},
	surfaceSearch: {
		{"pkg/events/search_integration_test.go", "TestSearchUsesOnlyAuthorizedCurrentPublicationAndDiscoverableAttendance"},
		{"pkg/events/loose_publications_integration_test.go", "TestLoosePublicationProjectsAuthorizedRecipientLibraryAndSearch"},
	},
	surfaceNewForYou: {
		{"pkg/library/service_integration_test.go", "TestRecipientAuthorizationMatrixRevalidatesReuseWithdrawalAndAvailability"},
		{"pkg/events/loose_publications_integration_test.go", "TestLoosePublicationProjectsAuthorizedRecipientLibraryAndSearch"},
	},
	surfaceThumbnail: {{"pkg/library/service_integration_test.go", "TestMediaRepresentationsRevalidateEveryAuthorizationBoundaryBeforeImmich"}},
	surfacePreview:   {{"pkg/library/service_integration_test.go", "TestThumbnailAndPreviewRoutesForwardValidatorsAndDispatch"}},
	surfaceVideo:     {{"pkg/library/service_integration_test.go", "TestVideoAndOriginalRoutesStreamSafeHeadersWithBoundedMemory"}},
	surfaceOriginal:  {{"pkg/library/service_integration_test.go", "TestVideoAndOriginalRoutesStreamSafeHeadersWithBoundedMemory"}},
	surfaceEventArchive: {
		{"pkg/archives/service_integration_test.go", "TestPlansCompleteEventAndRejectsIncompleteSubset"},
		{"pkg/events/loose_publications_integration_test.go", "TestLooseOnlyEntitlementDoesNotGrantEventDetailOrArchive"},
	},
	surfaceSubsetArchive: {{"pkg/archives/service_integration_test.go", "TestPlansCompleteEventAndRejectsIncompleteSubset"}},
	surfaceArchivePart:   {{"pkg/archives/service_integration_test.go", "TestEveryAccessLossBlocksAnUnstartedPart"}},
	surfaceComments:      {{"pkg/comments/comments_integration_test.go", "TestCommentsAuthorizeChronologyOwnershipAndModerationHistory"}},
	surfaceCommentWrite:  {{"pkg/comments/comments_integration_test.go", "TestCommentsAuthorizeChronologyOwnershipAndModerationHistory"}},
	surfaceCommentChange: {{"pkg/comments/comments_integration_test.go", "TestCommentsAuthorizeChronologyOwnershipAndModerationHistory"}},
	surfaceFavorites:     {{"pkg/comments/comments_integration_test.go", "TestFavoritesRemainPrivateAndPersistAcrossAccessLoss"}},
	surfaceFavoriteWrite: {{"pkg/comments/comments_integration_test.go", "TestFavoritesRemainPrivateAndPersistAcrossAccessLoss"}},
	surfaceImmediateEmail: {
		{"pkg/emaildelivery/immediate_integration_test.go", "TestImmediateEmailReauthorizesAndHandlesTerminalFailures"},
		{"pkg/emaildelivery/immediate_integration_test.go", "TestImmediateEmailHoldsAuthorizationLocksThroughSMTPAcceptance"},
		{"pkg/emaildelivery/immediate_integration_test.go", "TestImmediateEmailRecomputesSurvivorsAndStripsPreviewMetadata"},
	},
	surfaceWeeklyEmail:    {{"pkg/emaildelivery/weekly_integration_test.go", "TestWeeklyDigestUsesLocalBoundaryAndThreeSafeAuthorizedPreviews"}},
	surfaceCommentEmail:   {{"pkg/comments/comments_integration_test.go", "TestCommentHandoffReauthorizesAndWithdrawalAndRevocationDenyImmediately"}},
	surfacePushEnrollment: {{"pkg/push/immediate_integration_test.go", "TestPushRoutesEnforceSafeGETCSRFContentTypeAndPublicSessionPolicy"}},
	surfacePublicationPush: {
		{"pkg/push/immediate_integration_test.go", "TestPushReauthorizesWithdrawalImmediatelyBeforeSend"},
		{"pkg/push/immediate_integration_test.go", "TestPushHoldsSessionPreferenceLockThroughProviderAcceptance"},
		{"pkg/push/immediate_integration_test.go", "TestPushMatchesEmailSurvivorsAndTerminalOutcomeIsDeviceOnly"},
	},
	surfaceCommentPush: {{"pkg/push/immediate_integration_test.go", "TestPushMatchesEmailSurvivorsAndTerminalOutcomeIsDeviceOnly"}},
	surfacePreviewAsRecipient: {
		{"pkg/events/publications_integration_test.go", "TestPreviewRendersSavedEditableResultBeforePublication"},
		{"pkg/events/loose_publications_integration_test.go", "TestLoosePublicationSupportsEmptyAudienceAndGenerationAwarePreview"},
	},
}

func TestAuthorizationMatrixSurfacesHaveProductionEvidence(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Dir(filepath.Dir(currentFile))
	for _, surface := range matrixSurfaces {
		evidence := matrixEvidence[surface]
		require.NotEmptyf(t, evidence, "%s has no production evidence", surface)
		for _, item := range evidence {
			contents, err := os.ReadFile(filepath.Join(root, item.path))
			require.NoErrorf(t, err, "%s evidence file", surface)
			assert.Containsf(t, string(contents), "func "+item.test+"(", "%s evidence test %s is missing", surface, item.test)
		}
	}
	require.Len(t, matrixEvidence, len(matrixSurfaces))
	for surface := range matrixEvidence {
		assert.NotEmptyf(t, string(surface), "evidence contains an unnamed surface")
	}
}
