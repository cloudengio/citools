// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cloudeng.io/cmdutil/keys"
	"cloudeng.io/cmdutil/keys/keyscmd"
	"cloudeng.io/webapp/cookies"
	"cloudeng.io/webapp/webauth/jwtutil"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

func TestJWTCreate(t *testing.T) {
	ctx := context.Background()
	ims := keys.NewInMemoryKeyStore()
	ctx = keys.ContextWithKeyStore(ctx, ims)

	cmd := JWTCommand{}
	filename := filepath.Join(t.TempDir(), "orch-key-1.json")

	// Test 1: Create a new key pair, written to filename.
	flags := &JWTCreateFlags{
		KeyID:   "orch-key-1",
		KeyUser: "testuser",
		Update:  false,
	}
	if err := cmd.Create(ctx, flags, []string{filename}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Test 1a: a single argument is required.
	if err := cmd.Create(ctx, flags, nil); err == nil {
		t.Error("expected an error when no filename argument is given")
	}
	if err := cmd.Create(ctx, flags, []string{filename, "extra"}); err == nil {
		t.Error("expected an error when more than one filename argument is given")
	}

	// Verify key was written to filename.
	info, err := keyscmd.ReadKeyInfoFromLocalJSON(ctx, filename)
	if err != nil {
		t.Fatalf("ReadKeyInfoFromLocalJSON failed: %v", err)
	}
	if info.ID != "orch-key-1" || info.User != "testuser" {
		t.Fatalf("unexpected key info: %+v", info)
	}

	// Verify that the stored key can be used to sign a token and that the
	// public key stored alongside it verifies that token.
	kctx := keys.ContextWithKey(ctx, info)
	signed, err := signTestToken(t, kctx, info)
	if err != nil {
		t.Fatalf("signing with the stored key: %v", err)
	}
	validator, err := jwtutil.ValidatorForKeys(kctx, info)
	if err != nil {
		t.Fatalf("ValidatorForKeys failed: %v", err)
	}
	if _, err := validator.ParseAndValidate(kctx, signed); err != nil {
		t.Fatalf("the stored public key does not verify the stored private key: %v", err)
	}

	// Verify key was added to in-memory keystore in context.
	if _, found := ims.Get("testuser", "orch-key-1"); !found {
		t.Errorf("key was not added to context keystore")
	}

	// Test 2: Creating the same file again without update should fail, and
	// must not touch the existing file.
	if err := cmd.Create(ctx, flags, []string{filename}); err == nil {
		t.Fatalf("expected error on duplicate key creation without update flag")
	}
	unchanged, err := keyscmd.ReadKeyInfoFromLocalJSON(ctx, filename)
	if err != nil {
		t.Fatalf("ReadKeyInfoFromLocalJSON failed: %v", err)
	}
	if string(unchanged.Token().Value()) != string(info.Token().Value()) {
		t.Errorf("the existing key was overwritten despite update=false")
	}

	// Test 3: Creating the same file with update should succeed and produce
	// a new key pair.
	flags.Update = true
	if err := cmd.Create(ctx, flags, []string{filename}); err != nil {
		t.Fatalf("Create with update failed: %v", err)
	}
	updated, err := keyscmd.ReadKeyInfoFromLocalJSON(ctx, filename)
	if err != nil {
		t.Fatalf("ReadKeyInfoFromLocalJSON failed: %v", err)
	}
	if string(updated.Token().Value()) == string(info.Token().Value()) {
		t.Errorf("Create with update=true did not generate a new key")
	}
}

func TestJWTIssuer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create the signing key and add it directly to the context's keystore
	// (JWTCommand.Issuer reads it via jwtutil.SignerForKey, which does not
	// itself know about keychains or local files).
	keyInfo, err := jwtutil.NewED25519KeyInfo("alice", "serve-key-1")
	if err != nil {
		t.Fatalf("NewED25519KeyInfo failed: %v", err)
	}
	ctx = keys.ContextWithKey(ctx, keyInfo)

	cfg := Config{
		JWTIssuer: &JWTIssuerConfig{
			JWTCookieSignerConfig: jwtutil.JWTCookieSignerConfig{
				JWTCookieConfig: jwtutil.JWTCookieConfig{
					Name:     "orch_jwt",
					Insecure: true, // plain HTTP for testing
					ScopeAndDuration: cookies.ScopeAndDuration{
						Domain:   "127.0.0.1",
						Path:     "/",
						Duration: time.Hour,
					},
				},
				JWTSignerConfig: jwtutil.JWTSignerConfig{
					Issuer:   "github-runner-orchestrator",
					Audience: []string{"orchestrator"},
				},
			},
			SigningKey: keyInfo.KeySpec(),
		},
	}
	ctx = ContextWithConfig(ctx, cfg)

	// Set up capture of the generated auth URL via jwtOpenBrowserFn.
	urlCh := make(chan string, 1)
	origOpenBrowser := jwtOpenBrowserFn
	defer func() { jwtOpenBrowserFn = origOpenBrowser }()
	jwtOpenBrowserFn = func(u string) error {
		urlCh <- u
		return nil
	}

	cmd := JWTCommand{}
	issuerFlags := &JWTIssuerFlags{
		Address:     "127.0.0.1:0",
		OneShot:     true,
		OpenBrowser: true,
	}

	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- cmd.Issuer(ctx, issuerFlags, nil)
	}()

	// Wait for the auth URL to be printed/opened.
	var authURL string
	select {
	case authURL = <-urlCh:
	case err := <-serveErrCh:
		t.Fatalf("Issuer failed early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for auth URL")
	}

	parsedURL, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("invalid auth URL %q: %v", authURL, err)
	}
	if !strings.HasPrefix(parsedURL.Path, "/auth/") {
		t.Fatalf("expected /auth/ prefix in %s", parsedURL.Path)
	}

	// Verify that requesting an unauthenticated path returns 404.
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}

	baseURL := "http://" + parsedURL.Host
	resp, err := client.Get(baseURL + "/unauthorized")
	if err != nil {
		t.Fatalf("GET /unauthorized: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for /unauthorized, got %d", resp.StatusCode)
	}

	// Now request the random auth URL to obtain the JWT cookie.
	resp, err = client.Get(authURL)
	if err != nil {
		t.Fatalf("GET authURL: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for authURL, got %d", resp.StatusCode)
	}

	// Verify that the cookie was stored in the client jar.
	respCookies := client.Jar.Cookies(parsedURL)
	var foundCookie *http.Cookie
	for _, ck := range respCookies {
		if ck.Name == "orch_jwt" {
			foundCookie = ck
			break
		}
	}
	if foundCookie == nil {
		t.Fatalf("cookie orch_jwt was not set in client jar")
	}

	// Verify that Issuer exits cleanly after one-shot request.
	select {
	case err := <-serveErrCh:
		if err != nil {
			t.Fatalf("Issuer returned error on one-shot exit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for one-shot server shutdown")
	}

	// Verify the JWT in the cookie using the public key stored with the
	// signing key.
	validator, err := jwtutil.ValidatorForKeys(ctx, keyInfo)
	if err != nil {
		t.Fatalf("ValidatorForKeys: %v", err)
	}
	tok, err := validator.ParseAndValidate(ctx, []byte(foundCookie.Value))
	if err != nil {
		t.Fatalf("token validation failed: %v", err)
	}
	sub, _ := tok.Subject()
	if sub != "alice" {
		t.Errorf("expected subject alice, got %q", sub)
	}
	iss, _ := tok.Issuer()
	if iss != "github-runner-orchestrator" {
		t.Errorf("expected issuer github-runner-orchestrator, got %q", iss)
	}
}

func TestGetJWTValidator(t *testing.T) {
	ctx := context.Background()
	ims := keys.NewInMemoryKeyStore()
	ctx = keys.ContextWithKeyStore(ctx, ims)

	info1, err := jwtutil.NewED25519KeyInfo("user1", "key-1")
	if err != nil {
		t.Fatalf("NewED25519KeyInfo: %v", err)
	}
	ims.Add(info1)
	tokBytes, err := signTestToken(t, ctx, info1)
	if err != nil {
		t.Fatalf("signTestToken: %v", err)
	}

	// Case 1: a configured verification key validates a token signed by it.
	cfg := Config{
		WebUI: WebUIConfig{
			JWTVerification: &JWTValidatorConfig{
				VerificationKeys: []keys.KeySpec{info1.KeySpec()},
			},
		},
	}
	v1, err := getJWTValidator(ctx, cfg)
	if err != nil {
		t.Fatalf("getJWTValidator: %v", err)
	}
	if _, err := v1.ParseAndValidate(ctx, tokBytes); err != nil {
		t.Errorf("ParseAndValidate: %v", err)
	}

	// Case 2: no web_ui.jwt_verifier configured fails.
	if _, err := getJWTValidator(ctx, Config{}); err == nil {
		t.Error("getJWTValidator: got nil error, want an error when JWTVerification is unconfigured")
	}

	// Case 3: a configured but nonexistent verification key fails.
	cfgMissing := Config{
		WebUI: WebUIConfig{
			JWTVerification: &JWTValidatorConfig{
				VerificationKeys: []keys.KeySpec{{ID: "nonexistent", User: "user1"}},
			},
		},
	}
	if _, err := getJWTValidator(ctx, cfgMissing); err == nil {
		t.Error("getJWTValidator: got nil error, want an error for a nonexistent key")
	}
}

// signTestToken signs a token with the key described by info, which must be
// present in the key store in ctx.
func signTestToken(t *testing.T, ctx context.Context, info keys.Info) ([]byte, error) {
	t.Helper()
	signer, err := jwtutil.SignerForKey(ctx, info.KeySpec())
	if err != nil {
		return nil, err
	}
	tok, err := jwt.NewBuilder().Subject("test").IssuedAt(time.Now()).
		Expiration(time.Now().Add(time.Hour)).Build()
	if err != nil {
		return nil, err
	}
	return signer.Sign(ctx, tok)
}
