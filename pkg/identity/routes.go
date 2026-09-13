package identity

import (
	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/config"
)

// RegisterRoutes registers public authentication routes. Protected features reuse the guards.
func RegisterRoutes(e *echo.Echo, cfg *config.Config, module UseCases) *Handlers {
	h := newHandlers(cfg, module)
	RegisterGoogleRoutes(e, cfg, module)
	e.GET("/api/identity/status", h.status)
	e.GET("/api/identity/me", h.me, h.RequirePerson)
	e.GET("/api/identity/profile", h.profile, h.RequirePerson)
	e.POST("/api/identity/profile", h.updateProfile, h.RequirePerson)
	e.GET("/api/identity/sessions", h.sessions, h.RequirePerson)
	e.POST("/api/identity/sign-out-everywhere", h.signOutEverywhere, h.RequirePerson)
	e.POST("/api/identity/identities/:identityID/unlink", h.unlinkIdentity, h.RequirePerson)
	e.POST("/api/identity/onboarding", h.completeOnboarding, h.RequirePerson)
	e.POST("/api/albums/:id/request-access", h.requestAlbumAccess, h.RequirePerson)
	e.GET("/api/access-requests", h.listAccessRequests, h.RequireCurator)
	e.POST("/api/access-requests/:id/approve", h.approveAccessRequest, h.RequireCurator)
	e.POST("/api/access-requests/:id/deny", h.denyAccessRequest, h.RequireCurator)
	e.POST("/api/access-requests/:id/reconsider", h.reconsiderAccessRequest, h.RequireCurator)
	e.GET("/api/people", h.listPeople, h.RequireCurator)
	e.POST("/api/people", h.createPerson, h.RequireCurator)
	e.POST("/api/people/from-face", h.createPersonFromFace, h.RequireCurator)
	e.POST("/api/people/:id/faces", h.linkFace, h.RequireCurator)
	e.POST("/api/people/:id/avatar", h.setPersonAvatar, h.RequireCurator)
	e.POST("/api/faces/:sourceID/ignore", h.ignoreFace, h.RequireCurator)
	e.GET("/api/people/:id", h.getPerson, h.RequireCurator)
	e.POST("/api/people/:id", h.updatePerson, h.RequireCurator)
	e.POST("/api/people/:id/preauthorizations", h.preauthorize, h.RequireCurator)
	e.POST("/api/people/:id/preauthorizations/:preauthorizationID/revoke", h.revokePreauthorization, h.RequireCurator)
	e.POST("/api/people/:id/invitations", h.sendInvitation, h.RequireCurator)
	e.POST("/api/people/:id/invitations/:invitationID/retry", h.retryInvitation, h.RequireCurator)
	e.POST("/api/people/:id/identities/:identityID/unlink", h.unlinkIdentity, h.RequireCurator)
	e.POST("/api/identity/sign-out", h.signOut)
	if cfg.AuthMode == "fake" && (cfg.AppEnv == "development" || cfg.AppEnv == "test") {
		e.POST("/api/identity/fake-sign-in", h.fakeSignIn)
	}
	return h
}
