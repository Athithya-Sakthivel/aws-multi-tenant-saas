package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAuthE2E(t *testing.T) {
	baseURL := os.Getenv("AUTH_BASE_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080"
	}

	waitForReady(t, baseURL)

	tenant := fmt.Sprintf("tenant_%d", time.Now().UnixNano())
	email := fmt.Sprintf("e2e-%d@test.local", time.Now().UnixNano())
	password := "Passw0rd!1234"

	createTenant := map[string]string{
		"tenant": tenant,
	}
	code, body := postJSON(t, baseURL+"/tenants", createTenant)
	if code != http.StatusOK {
		t.Fatalf("create tenant failed: status=%d body=%s", code, body)
	}

	code, body = postJSON(t, baseURL+"/tenants", createTenant)
	if code != http.StatusOK {
		t.Fatalf("create tenant idempotency failed: status=%d body=%s", code, body)
	}

	registerReq := map[string]string{
		"tenant":   tenant,
		"email":    email,
		"password": password,
	}

	code, body = postJSON(t, baseURL+"/register", registerReq)
	if code != http.StatusCreated {
		t.Fatalf("register failed: status=%d body=%s", code, body)
	}

	code, body = postJSON(t, baseURL+"/register", registerReq)
	if code != http.StatusConflict {
		t.Fatalf("duplicate register expected 409: status=%d body=%s", code, body)
	}

	loginReq := map[string]string{
		"tenant":   tenant,
		"email":    email,
		"password": password,
	}

	code, body = postJSON(t, baseURL+"/login", loginReq)
	if code != http.StatusOK {
		t.Fatalf("login failed: status=%d body=%s", code, body)
	}

	var loginResp struct {
		TokenType string `json:"token_type"`
		Token     string `json:"token"`
	}
	if err := json.Unmarshal([]byte(body), &loginResp); err != nil {
		t.Fatalf("decode login response: %v body=%s", err, body)
	}
	if strings.TrimSpace(loginResp.Token) == "" {
		t.Fatalf("empty token body=%s", body)
	}
	if loginResp.TokenType != "Bearer" {
		t.Fatalf("unexpected token type: %q body=%s", loginResp.TokenType, body)
	}

	badLogin := map[string]string{
		"tenant":   tenant,
		"email":    email,
		"password": "wrong-password-123",
	}
	code, body = postJSON(t, baseURL+"/login", badLogin)
	if code != http.StatusUnauthorized {
		t.Fatalf("wrong password expected 401: status=%d body=%s", code, body)
	}
}

func waitForReady(t *testing.T, baseURL string) {
	t.Helper()

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(60 * time.Second)

	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, baseURL+"/readyz", nil)
		if err == nil {
			resp, err := client.Do(req)
			if err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	t.Fatalf("auth server not ready at %s", baseURL)
}

func postJSON(t *testing.T, url string, payload any) (int, string) {
	t.Helper()

	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	return resp.StatusCode, string(body)
}
