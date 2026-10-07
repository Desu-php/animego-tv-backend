package httptransport_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httptransport "animego/backend/internal/transport/http"
	"github.com/gin-gonic/gin"
)

func documentationRouter() *gin.Engine {
	// Documentation must work without calling a catalog or playback provider.
	return httptransport.NewRouter(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), httptransport.WithPlayback(nil))
}

func TestEmbeddedSwaggerAssets(t *testing.T) {
	router := documentationRouter()
	for _, asset := range []struct{ path, contentType string }{
		{"/swagger/", "text/html"},
		{"/swagger/index.html", "text/html"},
		{"/swagger/swagger-ui.css", "text/css"},
		{"/swagger/swagger-ui-bundle.js", "application/javascript"},
		{"/swagger/swagger-initializer.js", "application/javascript"},
		{"/openapi.json", "application/json"},
		{"/swagger/openapi.json", "application/json"},
	} {
		t.Run(asset.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, asset.path, nil))
			if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), asset.contentType) || w.Body.Len() == 0 {
				t.Fatalf("asset unavailable: status=%d content-type=%q bytes=%d", w.Code, w.Header().Get("Content-Type"), w.Body.Len())
			}
		})
	}
	redirect := httptest.NewRecorder()
	router.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, "/swagger", nil))
	if redirect.Code != http.StatusTemporaryRedirect || redirect.Header().Get("Location") != "/swagger/index.html" {
		t.Fatalf("unexpected Swagger redirect: %d %q", redirect.Code, redirect.Header().Get("Location"))
	}
}

func TestOpenAPITracksRegisteredEndpoints(t *testing.T) {
	router := documentationRouter()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	var contract struct {
		OpenAPI string                                `json:"openapi"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &contract); err != nil {
		t.Fatal(err)
	}
	if contract.OpenAPI != "3.0.3" {
		t.Fatalf("unexpected OpenAPI version: %q", contract.OpenAPI)
	}
	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		if route.Path != "/health" && !strings.HasPrefix(route.Path, "/api/") {
			continue
		}
		parts := strings.Split(route.Path, "/")
		for i, part := range parts {
			if strings.HasPrefix(part, ":") {
				parts[i] = "{" + strings.TrimPrefix(part, ":") + "}"
			}
		}
		path, method := strings.Join(parts, "/"), strings.ToLower(route.Method)
		registered[method+" "+path] = true
		if _, exists := contract.Paths[path][method]; !exists {
			t.Errorf("registered endpoint missing from OpenAPI: %s %s", method, path)
		}
	}
	for path, methods := range contract.Paths {
		for method := range methods {
			if !registered[method+" "+path] {
				t.Errorf("OpenAPI endpoint not registered: %s %s", method, path)
			}
		}
	}
}
