package channel

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/worryzyy/upstream-hub/internal/config"
	"github.com/worryzyy/upstream-hub/internal/connector"
	"github.com/worryzyy/upstream-hub/internal/connector/sub2api"
	"github.com/worryzyy/upstream-hub/internal/crypto"
	"github.com/worryzyy/upstream-hub/internal/storage"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestJWTExpiryAdvisory(t *testing.T) {
	for _, tc := range []struct {
		token string
		want  bool
	}{
		{"opaque", false}, {"a.invalid.z", false},
		{"a." + base64.RawURLEncoding.EncodeToString([]byte(`{"exp":1}`)) + ".z", true},
		{"a." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, time.Now().Add(time.Hour).Unix()))) + ".z", false},
	} {
		if jwtExpiresSoon(tc.token, 5*time.Minute) != tc.want {
			t.Error("unexpected expiry decision")
		}
	}
}

// Opt-in integration test. All mutations are restricted to a transaction-local
// temporary table; production channel rows are never queried or modified.
func TestSub2APIRefreshPersistence(t *testing.T) {
	if os.Getenv("UPSTREAMHUB_TEST_REFRESH_DB") != "1" {
		t.Skip("set UPSTREAMHUB_TEST_REFRESH_DB=1 with database environment")
	}
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(postgres.Open(cfg.Database.ToStorageConfig().DSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("open test connection failed")
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	if err := tx.Exec("CREATE TEMP TABLE channels (LIKE public.channels INCLUDING DEFAULTS) ON COMMIT DROP").Error; err != nil {
		t.Fatal(err)
	}
	cipher, _ := crypto.NewCipher("test-only-key")
	repo := storage.NewChannels(tx)
	service := NewService(repo, nil, nil, nil, cipher)
	for _, tc := range []struct {
		name      string
		status    int
		refresh   bool
		wantErr   bool
		wantCalls int32
	}{
		{"concurrent rotation", 401, true, false, 1},
		{"server failure preserves pair", 503, true, true, 0},
		{"access only expiry", 401, false, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/auth/refresh" {
					calls.Add(1)
					fmt.Fprint(w, `{"code":0,"data":{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}}`)
					return
				}
				if r.Header.Get("Authorization") == "Bearer new-access" {
					fmt.Fprint(w, `{"code":0,"data":{}}`)
					return
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, `{"message":"test auth error"}`)
			}))
			defer server.Close()
			raw := `{"access_token":"old-access"}`
			if tc.refresh {
				raw = `{"access_token":"old-access","refresh_token":"old-refresh"}`
			}
			encrypted, _ := cipher.Encrypt(raw)
			row := &storage.Channel{ID: 1, Name: "test", Type: storage.ChannelTypeSub2API, SiteURL: server.URL, CredentialMode: storage.CredentialModeToken, PasswordCipher: encrypted}
			if err := tx.Exec("DELETE FROM pg_temp.channels").Error; err != nil {
				t.Fatal(err)
			}
			if err := repo.Create(row); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					session, err := service.EnsureSession(context.Background(), row, &connector.Channel{}, sub2api.New())
					if (err != nil) != tc.wantErr {
						t.Error("unexpected session outcome", err)
					}
					if !tc.wantErr && session.AccessToken != "new-access" {
						t.Error("stale access token")
					}
				}()
			}
			wg.Wait()
			if calls.Load() != tc.wantCalls {
				t.Errorf("refresh calls=%d want=%d", calls.Load(), tc.wantCalls)
			}
			saved, err := repo.FindByID(1)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(saved.PasswordCipher, "new-refresh") {
				t.Fatal("plaintext credential persisted")
			}
			cred, err := service.readSub2APICredential(saved)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantCalls == 1 && (cred.AccessToken != "new-access" || cred.RefreshToken != "new-refresh") {
				t.Fatal("rotated pair not persisted")
			}
			if tc.wantCalls == 0 && saved.PasswordCipher != encrypted {
				t.Fatal("credentials changed on failed authentication")
			}
			updated, err := repo.RotateTokenCredential(1, "stale-cipher", "replacement")
			if err != nil || updated {
				t.Fatal("stale credential update accepted")
			}
		})
	}
}
