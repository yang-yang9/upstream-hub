package newapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/worryzyy/upstream-hub/internal/connector"
)

func TestCheckAuthWithAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer persistent-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("Cookie"); got != "" {
			t.Fatalf("Cookie = %q, want empty", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	defer server.Close()

	err := New().CheckAuth(context.Background(), &connector.Channel{SiteURL: server.URL}, &connector.AuthSession{
		AccessToken: "persistent-token",
	})
	if err != nil {
		t.Fatalf("CheckAuth() error = %v", err)
	}
}

func TestCheckAuthRejectsEmptySession(t *testing.T) {
	err := New().CheckAuth(context.Background(), &connector.Channel{SiteURL: "https://example.com"}, &connector.AuthSession{})
	if err == nil {
		t.Fatal("CheckAuth() error = nil, want missing credential error")
	}
}
