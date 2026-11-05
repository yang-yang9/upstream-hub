package sub2api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/worryzyy/upstream-hub/internal/connector"
)

func TestRefreshTokenRotatesPair(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/refresh" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body["refresh_token"] != "old-refresh" {
			t.Errorf("wrong refresh token")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}}`))
	}))
	defer server.Close()
	access, refresh, ttl, err := New().RefreshToken(context.Background(), &connector.Channel{SiteURL: server.URL}, "old-refresh")
	if err != nil || access != "new-access" || refresh != "new-refresh" || ttl.Seconds() != 3600 {
		t.Fatalf("unexpected refresh result: %q %q %v %v", access, refresh, ttl, err)
	}
}

func TestRefreshTokenRejectsIncompletePair(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"new-access","expires_in":3600}}`))
	}))
	defer server.Close()
	_, _, _, err := New().RefreshToken(context.Background(), &connector.Channel{SiteURL: server.URL}, "old-refresh")
	if err == nil {
		t.Fatal("missing rotated refresh token must fail")
	}
}
