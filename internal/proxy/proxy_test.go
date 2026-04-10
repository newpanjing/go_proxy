package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-proxy/internal/config"
)

func TestProxyServeHTTP_MockRouteSuccess(t *testing.T) {
	mgr := config.NewManager("")
	mgr.SetRoutes([]config.Route{
		{
			Path: "/mock/user",
			Type: config.RouteTypeMock,
			Mock: &config.MockConfig{
				Method:       http.MethodPost,
				Params:       map[string]string{"id": "42", "name": "demo"},
				StatusCode:   http.StatusCreated,
				ResponseType: "application/json",
				Headers:      map[string]string{"X-Custom": "test-value"},
				Data: map[string]interface{}{
					"user_id": "u-001",
				},
			},
		},
	})

	p := New(mgr)
	req := httptest.NewRequest(http.MethodPost, "/mock/user?id=42", strings.NewReader(`{"name":"demo"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	p.ServeHTTP(resp, req)

	if resp.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, resp.Code)
	}
	if got := resp.Header().Get("X-Go-Proxy-Mock"); got != "true" {
		t.Fatalf("expected mock header, got %q", got)
	}
	if got := resp.Header().Get("X-Custom"); got != "test-value" {
		t.Fatalf("expected custom header 'test-value', got %q", got)
	}
	if got := resp.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected content type application/json, got %q", got)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["user_id"] != "u-001" {
		t.Fatalf("unexpected payload: %#v", body)
	}
}

func TestProxyServeHTTP_MockRouteParamMismatch(t *testing.T) {
	mgr := config.NewManager("")
	mgr.SetRoutes([]config.Route{
		{
			Path: "/mock/check",
			Type: config.RouteTypeMock,
			Mock: &config.MockConfig{
				Method: http.MethodGet,
				Params: map[string]string{"token": "abc123"},
			},
		},
	})

	p := New(mgr)
	req := httptest.NewRequest(http.MethodGet, "/mock/check?token=wrong", nil)
	resp := httptest.NewRecorder()

	p.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, resp.Code)
	}

	var body struct {
		Code int `json:"code"`
		Data struct {
			Mismatch map[string]string `json:"mismatch"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != 40001 {
		t.Fatalf("expected business code 40001, got %d", body.Code)
	}
	if _, ok := body.Data.Mismatch["token"]; !ok {
		t.Fatalf("expected mismatch for token, got %#v", body.Data.Mismatch)
	}
}

func TestProxyServeHTTP_RoutePriorityWins(t *testing.T) {
	mgr := config.NewManager("")
	mgr.SetRoutes([]config.Route{
		{
			Path:     "/api/detail",
			Priority: 10,
			Type:     config.RouteTypeMock,
			Mock: &config.MockConfig{
				Data: map[string]interface{}{"source": "low-priority"},
			},
		},
		{
			Path:     "/api",
			Priority: 100,
			Type:     config.RouteTypeMock,
			Mock: &config.MockConfig{
				Data: map[string]interface{}{"source": "high-priority"},
			},
		},
	})

	p := New(mgr)
	req := httptest.NewRequest(http.MethodGet, "/api/detail/list", nil)
	resp := httptest.NewRecorder()

	p.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.Code)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["source"] != "high-priority" {
		t.Fatalf("expected high priority route to win, got %q", body["source"])
	}
}
