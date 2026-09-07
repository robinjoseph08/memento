package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestImmichFixture(t *testing.T) {
	t.Parallel()
	fixture := newImmichFixture(true)
	request := func(method, path, body, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("x-api-key", key)
		response := httptest.NewRecorder()
		fixture.ServeHTTP(response, req)
		return response
	}
	assert.Equal(t, http.StatusServiceUnavailable, request("GET", "/api/server/version", "", "").Code)
	assert.Equal(t, http.StatusOK, request("POST", "/__fixture/state", `{"available":true}`, "").Code)
	version := request("GET", "/api/server/version", "", "")
	assert.Equal(t, http.StatusOK, version.Code)
	assert.JSONEq(t, `{"major":2,"minor":7,"patch":0}`, version.Body.String())
	assert.Equal(t, http.StatusUnauthorized, request("GET", "/api/users/me", "", "wrong").Code)
	owner := request("GET", "/api/users/me", "", "fixture-only-key")
	assert.Equal(t, http.StatusOK, owner.Code)
	assert.JSONEq(t, `{"id":"fixture-owner"}`, owner.Body.String())
	assert.Equal(t, http.StatusOK, request("POST", "/__fixture/state", `{"available":true,"unauthorized":true}`, "").Code)
	assert.Equal(t, http.StatusUnauthorized, request("GET", "/api/users/me", "", "fixture-only-key").Code)
	assert.Equal(t, http.StatusMethodNotAllowed, request("POST", "/api/users/me", `{}`, "fixture-only-key").Code)
	assert.Equal(t, http.StatusNotFound, request("GET", "/api/albums", "", "fixture-only-key").Code)
	assert.Equal(t, http.StatusBadRequest, request("POST", "/__fixture/state", `{"unauthorized":true}`, "").Code)
	req := httptest.NewRequest("POST", "/__fixture/state", strings.NewReader(`{"available":true}`))
	req.Header.Set("Origin", "https://example.com")
	response := httptest.NewRecorder()
	fixture.ServeHTTP(response, req)
	assert.Equal(t, http.StatusForbidden, response.Code)
}
