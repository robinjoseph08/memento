package server

import (
	"errors"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
)

var browserMutationMethods = []string{
	http.MethodPost,
	http.MethodPut,
	http.MethodPatch,
	http.MethodDelete,
}

var errInvalidPublicOrigin = errors.New("initialize browser security: invalid public origin")

const (
	allowedCORSMethods = "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS"
	allowedCORSHeaders = "Content-Type, X-Memento-CSRF, Idempotency-Key, If-Match, Range, If-Range"
)

// browserSecurity denies browser requests outside the configured origin and
// prevents cookie-authenticated mutations from using CORS-simple body types.
func browserSecurity(publicOrigin string) (echo.MiddlewareFunc, error) {
	allowedOrigin, err := canonicalOrigin(publicOrigin)
	if err != nil {
		return nil, errInvalidPublicOrigin
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			request := c.Request()
			origin := request.Header.Get(echo.HeaderOrigin)
			if origin != "" {
				requestOrigin, originErr := canonicalOrigin(origin)
				if originErr != nil || requestOrigin != allowedOrigin {
					return errcodes.Forbidden("Cross-origin requests")
				}
				headers := c.Response().Header()
				headers.Set(echo.HeaderVary, echo.HeaderOrigin)
				headers.Set(echo.HeaderAccessControlAllowOrigin, allowedOrigin)
				headers.Set(echo.HeaderAccessControlAllowCredentials, "true")
				if request.Method == http.MethodOptions && request.Header.Get(echo.HeaderAccessControlRequestMethod) != "" {
					requestedMethod := request.Header.Get(echo.HeaderAccessControlRequestMethod)
					if requestedMethod != http.MethodGet && requestedMethod != http.MethodHead && !slices.Contains(browserMutationMethods, requestedMethod) {
						return errcodes.Forbidden("Cross-origin requests")
					}
					headers.Set(echo.HeaderAccessControlAllowMethods, allowedCORSMethods)
					headers.Set(echo.HeaderAccessControlAllowHeaders, allowedCORSHeaders)
					headers.Add(echo.HeaderVary, echo.HeaderAccessControlRequestMethod)
					headers.Add(echo.HeaderVary, echo.HeaderAccessControlRequestHeaders)
					return c.NoContent(http.StatusNoContent)
				}
			}

			if slices.Contains(browserMutationMethods, request.Method) && !tokenPreferenceForm(request) {
				contentType, _, parseErr := mime.ParseMediaType(request.Header.Get(echo.HeaderContentType))
				if parseErr == nil && slices.Contains([]string{
					echo.MIMEApplicationForm,
					echo.MIMEMultipartForm,
					echo.MIMETextPlain,
				}, contentType) {
					return errcodes.UnsupportedMediaType()
				}
			}
			return next(c)
		}
	}, nil
}

func canonicalOrigin(raw string) (string, error) {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errInvalidPublicOrigin
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errInvalidPublicOrigin
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return "", errInvalidPublicOrigin
	}
	port := parsed.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	host := hostname
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	return (&url.URL{Scheme: scheme, Host: host}).String(), nil
}

func tokenPreferenceForm(request *http.Request) bool {
	return request.Method == http.MethodPost && request.URL.Path == "/api/email/preferences/unsubscribe"
}
