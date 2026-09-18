// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cloudeng.io/cmdutil/keys/keyscmd"
	"cloudeng.io/file/localfs"
	"cloudeng.io/webapp/webauth/jwtutil"
)

type JWTCommand struct{}

type JWTCreateFlags struct {
	KeyID   string `subcmd:"key-id,github-orchestrator-jwt-key,key ID for the signing key pair"`
	KeyUser string `subcmd:"key-user,,user associated with the key pair"`
	Update  bool   `subcmd:"update,false,overwrite the destination file if it already exists"`
}

// Create generates a new Ed25519 JWT signing key pair and writes it, as
// YAML, to the file named by the command's sole argument. IsDstSafe and
// CopyContents (via SafeWriteToLocal) are used so that "-" or "" safely
// write to stdout only when it is piped, exactly as for the other keyscmd
// commands.
func (c JWTCommand) Create(ctx context.Context, flags any, args []string) error {
	fv := flags.(*JWTCreateFlags)
	if fv.KeyID == "" {
		return fmt.Errorf("key ID is required")
	}
	if fv.KeyUser == "" {
		return fmt.Errorf("key user is required")
	}
	if len(args) != 1 {
		return fmt.Errorf("expected a single argument, the file to write the new key to, got %d", len(args))
	}
	filename := args[0]

	if err := keyscmd.IsDstSafe(filename); err != nil {
		return err
	}
	if ext := filepath.Ext(filename); ext != ".json" {
		return fmt.Errorf("expected a .json file, got %q with extension %q", filename, ext)
	}
	if !fv.Update && !keyscmd.IsStdoutStdin(filename) {
		if _, err := localfs.New().Stat(ctx, filename); err == nil {
			return fmt.Errorf("%q already exists, use --update to overwrite it", filename)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to check for an existing %q: %w", filename, err)
		}
	}

	info, err := jwtutil.NewED25519KeyInfo(fv.KeyUser, fv.KeyID)
	if err != nil {
		return err
	}

	out, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to marshal the new key: %w", err)
	}

	dst := localfs.New()
	if err := dst.WriteFile(filename, out, 0600); err != nil {
		return fmt.Errorf("failed to write the new key to %q: %w", filename, err)
	}

	pkFilename := strings.TrimSuffix(filename, ".json") + ".pub.json"

	fmt.Printf("Created Ed25519 JWT signing key pair:\n")
	fmt.Printf("             file: %s\n", filename)
	fmt.Printf("  public key file: %s\n", pkFilename)

	fmt.Printf("           key ID: %s\n", fv.KeyID)
	fmt.Printf("             user: %s\n", fv.KeyUser)

	pk := jwtutil.CloneKeyInfoForPublicKey(info)
	out, err = json.Marshal(pk)
	if err != nil {
		return fmt.Errorf("failed to marshal the public key: %w", err)
	}
	if err := dst.WriteFile(pkFilename, out, 0600); err != nil {
		return fmt.Errorf("failed to write the public key to %q: %w", pkFilename, err)
	}

	return nil
}

type JWTIssuerFlags struct {
	Address       string `subcmd:"address,127.0.0.1:0,'listen address for the authentication server (defaults to 127.0.0.1 on an ephemeral port)'"`
	OneShot       bool   `subcmd:"one-shot,false,shut down the server after serving the first authentication request"`
	OpenBrowser   bool   `subcmd:"open-browser,false,automatically open the authentication URL in the default browser"`
	FixedURLPath  string `subcmd:"fixed-url-path,,'fixed URL path for the authentication server (overrides the random path)'"`
	IssueRedirect bool   `subcmd:"issue-redirect,true,automatically issue a redirect after setting the authentication cookie"`
}

var jwtOpenBrowserFn = func(url string) error {
	return exec.Command("open", url).Start()
}

// Issuer serves a single JWT authentication cookie at a randomly generated
// URL, using the jwt_issuer configuration (Config.JWTIssuer) for everything
// but the listen address, subject and one-shot/open-browser behaviour, which
// are specific to running this as an interactive, one-off command.
func (c JWTCommand) Issuer(ctx context.Context, flags any, _ []string) error {
	fv := flags.(*JWTIssuerFlags)
	if err := validateListenAddress(fv.Address); err != nil {
		return err
	}

	cfg, ok := ConfigFromContext(ctx)
	if !ok {
		return fmt.Errorf("no configuration loaded")
	}
	if cfg.JWTIssuer == nil {
		return fmt.Errorf("jwt_issuer is not configured")
	}
	jwtCfg := cfg.JWTIssuer
	if err := jwtCfg.Validate(); err != nil {
		return fmt.Errorf("invalid jwt_issuer configuration: %w", err)
	}

	spec := jwtCfg.SigningKey
	signer, err := jwtutil.SignerForKey(ctx, spec)
	if err != nil {
		return fmt.Errorf("failed to load JWT signing key %v: %w", spec, err)
	}

	subject := jwtCfg.Subject
	if subject == "" {
		if spec.User != "" {
			subject = spec.User
		} else {
			subject = "orchestrator-admin"
		}
	}

	redirectURL := jwtCfg.Redirect
	if redirectURL == "" && cfg.WebUI.Enabled && cfg.WebUI.ListenAddress != "" {
		redirectURL = formatWebURL(cfg.WebUI.ListenAddress)
	}

	issuerOpts := append(jwtCfg.IssuerOptions(false), jwtutil.WithSubject(subject))
	if fv.IssueRedirect && redirectURL != "" {
		issuerOpts = append(issuerOpts, jwtutil.WithRedirect(redirectURL))
	}

	issuer, err := jwtutil.NewJWTIssuer(signer, issuerOpts...)
	if err != nil {
		return fmt.Errorf("failed to create JWT issuer: %w", err)
	}

	randBytes := make([]byte, 16)
	if _, err := rand.Read(randBytes); err != nil {
		return fmt.Errorf("failed to generate random token: %w", err)
	}
	randomHex := hex.EncodeToString(randBytes)
	authPath := "/auth/" + randomHex
	if fv.FixedURLPath != "" {
		authPath = fv.FixedURLPath
	}

	mux := http.NewServeMux()
	doneCh := make(chan struct{})
	var shutdownOnce sync.Once

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != authPath && r.URL.Path != authPath+"/" {
			http.NotFound(w, r)
			return
		}
		issuer.ServeHTTP(w, r)
		if fv.OneShot {
			shutdownOnce.Do(func() {
				close(doneCh)
			})
		}
	})

	ln, err := net.Listen("tcp", fv.Address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", fv.Address, err)
	}
	defer func() { _ = ln.Close() }()

	actualAddr := ln.Addr().String()
	authURL := fmt.Sprintf("http://%s%s", actualAddr, authPath)
	fmt.Printf("\nAuthentication URL (visit in browser to set JWT cookie):\n  %s\n\n", authURL)
	if redirectURL != "" && fv.IssueRedirect {
		fmt.Printf("After setting the cookie, you will be redirected to:\n  %s\n\n", redirectURL)
	}

	if fv.OpenBrowser {
		_ = jwtOpenBrowserFn(authURL)
	}

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-errCh:
		return err
	case <-doneCh:
		time.Sleep(100 * time.Millisecond)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		fmt.Println("Authentication cookie issued. Server stopped.")
		return nil
	}
}
