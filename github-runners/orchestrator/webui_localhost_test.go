// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cloudeng.io/cmdutil/keys"
	"cloudeng.io/webapp/webauth/jwtutil"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

func TestStartWebUILocalhostAndJWT(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ims := keys.NewInMemoryKeyStore()
	ctx = keys.ContextWithKeyStore(ctx, ims)

	keyInfo, err := jwtutil.NewED25519KeyInfo("admin", "test-webui-key")
	if err != nil {
		t.Fatalf("NewED25519KeyInfo: %v", err)
	}
	ims.Add(keyInfo)

	cfg := Config{
		WebUI: WebUIConfig{
			Enabled:       true,
			ListenAddress: "127.0.0.1:8088",
			JWTVerification: &JWTValidatorConfig{
				VerificationKeys: []keys.KeySpec{keyInfo.KeySpec()},
			},
		},
	}

	backend := newWebUIBackend(cfg, "/tmp/cfg.yml")

	t.Run("jwt disabled allows loopback requests", func(t *testing.T) {
		srv, err := startWebUI(ctx, cfg, backend, false, "jwt")
		if err != nil {
			t.Fatalf("startWebUI failed: %v", err)
		}
		if srv == nil {
			t.Fatal("expected non-nil server")
		}
		defer shutdownWebUI(srv)

		// Loopback request succeeds
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8088/api/v1/config", nil)
		req.RemoteAddr = "127.0.0.1:54321"
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
		}

		// Non-loopback request is blocked by websec.NewLocalHost
		reqNonLoopback := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8088/api/v1/config", nil)
		reqNonLoopback.RemoteAddr = "192.168.1.100:54321"
		recNonLoopback := httptest.NewRecorder()
		srv.Handler.ServeHTTP(recNonLoopback, reqNonLoopback)
		if recNonLoopback.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden for non-loopback, got %d", recNonLoopback.Code)
		}

		// Invalid host is blocked by websec.NewLocalHost
		reqBadHost := httptest.NewRequest(http.MethodGet, "http://evil.com/api/v1/config", nil)
		reqBadHost.RemoteAddr = "127.0.0.1:54321"
		reqBadHost.Host = "evil.com"
		recBadHost := httptest.NewRecorder()
		srv.Handler.ServeHTTP(recBadHost, reqBadHost)
		if recBadHost.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden for bad host, got %d", recBadHost.Code)
		}
	})

	t.Run("jwt enabled rejects unauthenticated requests", func(t *testing.T) {
		srv, err := startWebUI(ctx, cfg, backend, true, "jwt")
		if err != nil {
			t.Fatalf("startWebUI failed: %v", err)
		}
		if srv == nil {
			t.Fatal("expected non-nil server")
		}
		defer shutdownWebUI(srv)

		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8088/api/v1/config", nil)
		req.RemoteAddr = "127.0.0.1:54321"
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("jwt enabled accepts valid token in cookie", func(t *testing.T) {
		srv, err := startWebUI(ctx, cfg, backend, true, "jwt")
		if err != nil {
			t.Fatalf("startWebUI failed: %v", err)
		}
		if srv == nil {
			t.Fatal("expected non-nil server")
		}
		defer shutdownWebUI(srv)

		signer, err := jwtutil.SignerForKey(ctx, keyInfo.KeySpec())
		if err != nil {
			t.Fatalf("SignerForKey: %v", err)
		}
		tok, err := jwt.NewBuilder().
			Subject("admin").
			Issuer("github-runner-orchestrator").
			IssuedAt(time.Now()).
			Expiration(time.Now().Add(time.Hour)).
			Build()
		if err != nil {
			t.Fatalf("jwt.NewBuilder: %v", err)
		}
		tokenBytes, err := signer.Sign(ctx, tok)
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8088/api/v1/config", nil)
		req.RemoteAddr = "127.0.0.1:54321"
		req.AddCookie(&http.Cookie{
			Name:  "jwt",
			Value: string(tokenBytes),
		})
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK with valid cookie, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("jwt enabled rejects invalid token in cookie", func(t *testing.T) {
		srv, err := startWebUI(ctx, cfg, backend, true, "jwt")
		if err != nil {
			t.Fatalf("startWebUI failed: %v", err)
		}
		if srv == nil {
			t.Fatal("expected non-nil server")
		}
		defer shutdownWebUI(srv)

		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8088/api/v1/config", nil)
		req.RemoteAddr = "127.0.0.1:54321"
		req.AddCookie(&http.Cookie{
			Name:  "jwt",
			Value: "invalid.jwt.token",
		})
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized with invalid cookie, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("custom cookie name", func(t *testing.T) {
		srv, err := startWebUI(ctx, cfg, backend, true, "custom_auth")
		if err != nil {
			t.Fatalf("startWebUI failed: %v", err)
		}
		if srv == nil {
			t.Fatal("expected non-nil server")
		}
		defer shutdownWebUI(srv)

		signer, err := jwtutil.SignerForKey(ctx, keyInfo.KeySpec())
		if err != nil {
			t.Fatalf("SignerForKey: %v", err)
		}
		tok, err := jwt.NewBuilder().
			Subject("admin").
			Issuer("github-runner-orchestrator").
			IssuedAt(time.Now()).
			Expiration(time.Now().Add(time.Hour)).
			Build()
		if err != nil {
			t.Fatalf("jwt.NewBuilder: %v", err)
		}
		tokenBytes, err := signer.Sign(ctx, tok)
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}

		// Wrong cookie name fails
		reqWrong := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8088/api/v1/config", nil)
		reqWrong.RemoteAddr = "127.0.0.1:54321"
		reqWrong.AddCookie(&http.Cookie{
			Name:  "jwt",
			Value: string(tokenBytes),
		})
		recWrong := httptest.NewRecorder()
		srv.Handler.ServeHTTP(recWrong, reqWrong)
		if recWrong.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 with wrong cookie name, got %d", recWrong.Code)
		}

		// Correct custom cookie name succeeds
		reqRight := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8088/api/v1/config", nil)
		reqRight.RemoteAddr = "127.0.0.1:54321"
		reqRight.AddCookie(&http.Cookie{
			Name:  "custom_auth",
			Value: string(tokenBytes),
		})
		recRight := httptest.NewRecorder()
		srv.Handler.ServeHTTP(recRight, reqRight)
		if recRight.Code != http.StatusOK {
			t.Errorf("expected 200 OK with custom cookie, got %d: %s", recRight.Code, recRight.Body.String())
		}
	})
}
