package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func TestLogRoutesRegisterWithoutConflicts(t *testing.T) {
	engine := gin.New()
	if err := router.RegisterAll(engine); err != nil {
		t.Fatal(err)
	}
	registered := make(map[string]bool)
	for _, route := range engine.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{
		"GET /api/v1/log/overview/stream",
		"GET /api/v1/log/request-body/:id",
		"GET /api/v1/log/response-body/:id",
		"GET /api/v1/log/:id/request-body",
		"GET /api/v1/log/:id/response-body",
		"POST /api/v1/log/stop/:id",
		"POST /api/v1/log/stop/:id/:round",
		"POST /api/v1/log/:id/:round/stop",
	} {
		if !registered[route] {
			t.Errorf("missing route %s", route)
		}
	}

	// Exercise parameter binding and handler dispatch for both old and new URLs.
	public := gin.New()
	for _, route := range engine.Routes() {
		switch route.Handler {
		case "github.com/bestruirui/octopus/internal/server/handlers.getRequestBody":
			public.Handle(route.Method, route.Path, getRequestBody)
		case "github.com/bestruirui/octopus/internal/server/handlers.getResponseBody":
			public.Handle(route.Method, route.Path, getResponseBody)
		case "github.com/bestruirui/octopus/internal/server/handlers.stopRequest":
			public.Handle(route.Method, route.Path, stopRequest)
		}
	}
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/v1/log/request-body/1", 200},
		{"GET", "/api/v1/log/response-body/1", 200},
		{"GET", "/api/v1/log/1/request-body", 200},
		{"GET", "/api/v1/log/1/response-body", 200},
		{"POST", "/api/v1/log/stop/1", 204},
		{"POST", "/api/v1/log/stop/1/2", 204},
		{"POST", "/api/v1/log/1/2/stop", 204},
		{"POST", "/api/v1/log/1/0/stop", http.StatusBadRequest},
		{"POST", "/api/v1/log/stop/1/invalid", http.StatusBadRequest},
		{"POST", "/api/v1/log/stop/invalid", http.StatusBadRequest},
	} {
		recorder := httptest.NewRecorder()
		public.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, nil))
		if recorder.Code != tc.status {
			t.Errorf("%s %s: status=%d want=%d", tc.method, tc.path, recorder.Code, tc.status)
		}
	}
}
