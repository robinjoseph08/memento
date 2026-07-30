package tests

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sessionType string
type sessionValidity string
type epochState string
type recipientState string
type generationState string
type audienceState string
type placementState string
type withdrawalState string
type sourceState string
type commentState string
type emailPreference string
type pushPreference string
type identifierAttempt string

const (
	trustedSession sessionType = "trusted"
	publicSession  sessionType = "public"

	validSession   sessionValidity = "valid"
	expiredSession sessionValidity = "expired"
	revokedSession sessionValidity = "revoked"

	currentEpoch epochState = "current"
	staleEpoch   epochState = "stale"

	pendingRecipient   recipientState = "pending"
	completedRecipient recipientState = "completed"
	suspendedRecipient recipientState = "suspended"
	revokedRecipient   recipientState = "revoked"

	currentGeneration generationState = "current"
	staleGeneration   generationState = "stale"

	entitledAudience     audienceState     = "entitled"
	unentitledAudience   audienceState     = "not_entitled"
	singlePlacement      placementState    = "single"
	reusedPlacement      placementState    = "reused"
	noWithdrawal         withdrawalState   = "none"
	primaryWithdrawal    withdrawalState   = "primary"
	completeWithdrawal   withdrawalState   = "all"
	currentSource        sourceState       = "current"
	missingSource        sourceState       = "source_missing"
	activeComment        commentState      = "active"
	deletedComment       commentState      = "deleted"
	moderatedComment     commentState      = "moderated"
	immediateEmail       emailPreference   = "immediate"
	weeklyEmail          emailPreference   = "weekly"
	noEmail              emailPreference   = "none"
	enabledPush          pushPreference    = "enabled"
	disabledPush         pushPreference    = "disabled"
	authorizedIdentifier identifierAttempt = "authorized"
	crossRecipientID     identifierAttempt = "cross_recipient"
	guessedIdentifier    identifierAttempt = "guessed"
)

type matrixState struct {
	sessionType       sessionType
	sessionValidity   sessionValidity
	epoch             epochState
	recipientState    recipientState
	generation        generationState
	audience          audienceState
	placement         placementState
	withdrawal        withdrawalState
	source            sourceState
	comment           commentState
	emailPreference   emailPreference
	pushPreference    pushPreference
	identifierAttempt identifierAttempt
}

type matrixSurface string

const (
	surfaceLibraryProjection  matrixSurface = "library_pages_order_counts_covers"
	surfaceEventDetail        matrixSurface = "event_detail"
	surfaceSearch             matrixSurface = "search"
	surfaceNewForYou          matrixSurface = "new_for_you"
	surfaceThumbnail          matrixSurface = "thumbnail"
	surfacePreview            matrixSurface = "preview"
	surfaceVideo              matrixSurface = "video"
	surfaceOriginal           matrixSurface = "original"
	surfaceEventArchive       matrixSurface = "event_archive"
	surfaceSubsetArchive      matrixSurface = "subset_archive"
	surfaceArchivePart        matrixSurface = "archive_part"
	surfaceComments           matrixSurface = "comments"
	surfaceCommentWrite       matrixSurface = "comment_write"
	surfaceCommentChange      matrixSurface = "comment_edit_delete"
	surfaceFavorites          matrixSurface = "favorites"
	surfaceFavoriteWrite      matrixSurface = "favorite_write"
	surfaceImmediateEmail     matrixSurface = "immediate_email"
	surfaceWeeklyEmail        matrixSurface = "weekly_email"
	surfaceCommentEmail       matrixSurface = "comment_email"
	surfacePushEnrollment     matrixSurface = "push_enrollment"
	surfacePublicationPush    matrixSurface = "publication_push"
	surfaceCommentPush        matrixSurface = "comment_push"
	surfacePreviewAsRecipient matrixSurface = "preview_as_recipient"
)

var matrixSurfaces = []matrixSurface{
	surfaceLibraryProjection, surfaceEventDetail, surfaceSearch, surfaceNewForYou,
	surfaceThumbnail, surfacePreview, surfaceVideo, surfaceOriginal,
	surfaceEventArchive, surfaceSubsetArchive, surfaceArchivePart,
	surfaceComments, surfaceCommentWrite, surfaceCommentChange,
	surfaceFavorites, surfaceFavoriteWrite,
	surfaceImmediateEmail, surfaceWeeklyEmail, surfaceCommentEmail,
	surfacePushEnrollment, surfacePublicationPush, surfaceCommentPush,
	surfacePreviewAsRecipient,
}

func cartesianMatrix(visit func(matrixState)) {
	for _, sessionType := range []sessionType{trustedSession, publicSession} {
		for _, sessionValidity := range []sessionValidity{validSession, expiredSession, revokedSession} {
			for _, epoch := range []epochState{currentEpoch, staleEpoch} {
				for _, recipientState := range []recipientState{pendingRecipient, completedRecipient, suspendedRecipient, revokedRecipient} {
					for _, generation := range []generationState{currentGeneration, staleGeneration} {
						for _, audience := range []audienceState{entitledAudience, unentitledAudience} {
							for _, placement := range []placementState{singlePlacement, reusedPlacement} {
								for _, withdrawal := range []withdrawalState{noWithdrawal, primaryWithdrawal, completeWithdrawal} {
									for _, source := range []sourceState{currentSource, missingSource} {
										for _, comment := range []commentState{activeComment, deletedComment, moderatedComment} {
											for _, email := range []emailPreference{immediateEmail, weeklyEmail, noEmail} {
												for _, push := range []pushPreference{enabledPush, disabledPush} {
													for _, identifier := range []identifierAttempt{authorizedIdentifier, crossRecipientID, guessedIdentifier} {
														visit(matrixState{
															sessionType: sessionType, sessionValidity: sessionValidity, epoch: epoch,
															recipientState: recipientState, generation: generation, audience: audience,
															placement: placement, withdrawal: withdrawal, source: source,
															comment: comment, emailPreference: email, pushPreference: push,
															identifierAttempt: identifier,
														})
													}
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
}

func (state matrixState) sessionUsable() bool {
	return state.sessionValidity == validSession && state.epoch == currentEpoch
}

func (state matrixState) generationUsable() bool {
	return state.recipientState == completedRecipient && state.generation == currentGeneration
}

func (state matrixState) hasLivePlacement() bool {
	if state.audience != entitledAudience || state.withdrawal == completeWithdrawal {
		return false
	}
	return state.withdrawal == noWithdrawal || state.placement == reusedPlacement
}

func (state matrixState) contentVisible() bool {
	return state.sessionUsable() && state.generationUsable() && state.hasLivePlacement()
}

func (state matrixState) resourceVisible() bool {
	return state.contentVisible() && state.identifierAttempt == authorizedIdentifier
}

func (state matrixState) deliveryEligible() bool {
	return state.generationUsable() && state.hasLivePlacement()
}

func (state matrixState) allows(surface matrixSurface) bool {
	switch surface {
	case surfaceLibraryProjection, surfaceSearch, surfaceNewForYou, surfaceFavorites:
		return state.contentVisible()
	case surfaceEventDetail, surfaceComments, surfaceCommentWrite, surfaceFavoriteWrite:
		return state.resourceVisible()
	case surfaceCommentChange:
		return state.resourceVisible() && state.comment == activeComment
	case surfaceThumbnail, surfacePreview, surfaceVideo, surfaceOriginal,
		surfaceEventArchive, surfaceSubsetArchive, surfaceArchivePart:
		return state.resourceVisible() && state.source == currentSource
	case surfaceImmediateEmail:
		return state.deliveryEligible() && state.emailPreference == immediateEmail
	case surfaceWeeklyEmail:
		return state.deliveryEligible() && state.emailPreference == weeklyEmail
	case surfaceCommentEmail:
		return state.deliveryEligible() && state.comment == activeComment && state.emailPreference != noEmail
	case surfacePushEnrollment:
		return state.sessionUsable() && state.generationUsable() && state.sessionType == trustedSession
	case surfacePublicationPush:
		return state.sessionUsable() && state.generationUsable() && state.hasLivePlacement() &&
			state.sessionType == trustedSession && state.pushPreference == enabledPush
	case surfaceCommentPush:
		return state.sessionUsable() && state.generationUsable() && state.hasLivePlacement() &&
			state.comment == activeComment && state.sessionType == trustedSession && state.pushPreference == enabledPush
	case surfacePreviewAsRecipient:
		eligibleRecipient := state.recipientState == pendingRecipient || state.recipientState == completedRecipient
		return eligibleRecipient && state.generation == currentGeneration && state.hasLivePlacement() && state.identifierAttempt == authorizedIdentifier
	default:
		panic(fmt.Sprintf("unclassified matrix surface %q", surface))
	}
}

func TestAuthorizationCapabilityMatrixCoversEveryCombination(t *testing.T) {
	allowCounts := make(map[matrixSurface]int, len(matrixSurfaces))
	denyCounts := make(map[matrixSurface]int, len(matrixSurfaces))
	cases := 0
	cartesianMatrix(func(state matrixState) {
		cases++
		for _, surface := range matrixSurfaces {
			if state.allows(surface) {
				allowCounts[surface]++
			} else {
				denyCounts[surface]++
			}
		}

		// Preferences are delivery choices, never content authority.
		content := state.contentVisible()
		for _, surface := range []matrixSurface{surfaceLibraryProjection, surfaceSearch, surfaceNewForYou, surfaceFavorites} {
			assert.Equal(t, content, state.allows(surface))
		}
		// Source missing preserves authorized metadata and interactions while blocking every byte-bearing surface.
		if state.source == missingSource && state.resourceVisible() {
			assert.True(t, state.allows(surfaceLibraryProjection))
			assert.True(t, state.allows(surfaceComments))
			assert.False(t, state.allows(surfaceThumbnail))
			assert.False(t, state.allows(surfaceOriginal))
			assert.False(t, state.allows(surfaceArchivePart))
		}
		// One withdrawn placement cannot hide a reused Media item with another live entitlement.
		if state.placement == reusedPlacement && state.withdrawal == primaryWithdrawal && state.audience == entitledAudience {
			assert.True(t, state.hasLivePlacement())
		}
		// Cross-Recipient and guessed identifiers are indistinguishable from absent content.
		if state.identifierAttempt != authorizedIdentifier {
			for _, surface := range []matrixSurface{
				surfaceEventDetail, surfaceThumbnail, surfaceOriginal, surfaceArchivePart,
				surfaceComments, surfaceCommentWrite, surfaceFavoriteWrite, surfacePreviewAsRecipient,
			} {
				assert.False(t, state.allows(surface))
			}
		}
	})

	require.Equal(t, 124416, cases)
	for _, surface := range matrixSurfaces {
		assert.Positivef(t, allowCounts[surface], "%s must have an allowed state", surface)
		assert.Positivef(t, denyCounts[surface], "%s must have a denied state", surface)
	}
}

func FuzzAuthorizationTransitions(f *testing.F) {
	f.Add([]byte{0, 3, 7, 12, 18, 25, 31, 39})
	f.Add([]byte{2, 11, 20, 29, 38, 47, 56, 65})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, transitions []byte) {
		if len(transitions) > 4096 {
			t.Skip()
		}
		state := matrixState{
			sessionType: trustedSession, sessionValidity: validSession, epoch: currentEpoch,
			recipientState: completedRecipient, generation: currentGeneration,
			audience: entitledAudience, placement: reusedPlacement, withdrawal: noWithdrawal,
			source: currentSource, comment: activeComment, emailPreference: immediateEmail,
			pushPreference: enabledPush, identifierAttempt: authorizedIdentifier,
		}
		for _, transition := range transitions {
			beforeContent := state.contentVisible()
			switch transition % 13 {
			case 0:
				state.sessionType = []sessionType{trustedSession, publicSession}[(transition/13)%2]
			case 1:
				state.sessionValidity = []sessionValidity{validSession, expiredSession, revokedSession}[(transition/13)%3]
			case 2:
				state.epoch = []epochState{currentEpoch, staleEpoch}[(transition/13)%2]
			case 3:
				state.recipientState = []recipientState{pendingRecipient, completedRecipient, suspendedRecipient, revokedRecipient}[(transition/13)%4]
			case 4:
				state.generation = []generationState{currentGeneration, staleGeneration}[(transition/13)%2]
			case 5:
				state.audience = []audienceState{entitledAudience, unentitledAudience}[(transition/13)%2]
			case 6:
				state.placement = []placementState{singlePlacement, reusedPlacement}[(transition/13)%2]
			case 7:
				state.withdrawal = []withdrawalState{noWithdrawal, primaryWithdrawal, completeWithdrawal}[(transition/13)%3]
			case 8:
				state.source = []sourceState{currentSource, missingSource}[(transition/13)%2]
			case 9:
				state.comment = []commentState{activeComment, deletedComment, moderatedComment}[(transition/13)%3]
			case 10:
				state.emailPreference = []emailPreference{immediateEmail, weeklyEmail, noEmail}[(transition/13)%3]
				assert.Equal(t, beforeContent, state.contentVisible(), "email preference must not alter access")
			case 11:
				state.pushPreference = []pushPreference{enabledPush, disabledPush}[(transition/13)%2]
				assert.Equal(t, beforeContent, state.contentVisible(), "push preference must not alter access")
			case 12:
				state.identifierAttempt = []identifierAttempt{authorizedIdentifier, crossRecipientID, guessedIdentifier}[(transition/13)%3]
			}

			if !state.sessionUsable() || !state.generationUsable() || !state.hasLivePlacement() {
				for _, surface := range []matrixSurface{
					surfaceLibraryProjection, surfaceEventDetail, surfaceSearch, surfaceThumbnail,
					surfaceOriginal, surfaceArchivePart, surfaceComments, surfaceFavorites,
				} {
					assert.False(t, state.allows(surface), "access loss must fail closed after transition %d", transition)
				}
			}
			if state.source == missingSource {
				assert.False(t, state.allows(surfaceThumbnail))
				assert.False(t, state.allows(surfaceOriginal))
				assert.False(t, state.allows(surfaceArchivePart))
			}
		}
	})
}
