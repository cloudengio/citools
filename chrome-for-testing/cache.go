// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"cloudeng.io/logging/ctxlog"
	"github.com/google/uuid"
)

type CacheFlags struct {
	RunnerTemp   string `subcmd:"runner-temp,,path to the runner temp directory if RUNNER_TEMP is not set"`
	RunnerCache  string `subcmd:"runner-tool-cache,,path to the runner tool cache directory if RUNNER_TOOL_CACHE is not set"`
	UUIDDownload bool   `subcmd:"uuid-download,true,'use a uuid for download cache files, if false, the download filename is fixed based on the url which is useful for testing'"`
}

type toolCache struct {
	tempDir  string
	cacheDir string
	uuid     bool
}

var cwd string

func init() {
	var err error
	cwd, err = os.Getwd()
	if err != nil {
		panic(fmt.Sprintf("getting current working directory: %v", err))
	}
}

func toAbs(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cwd, path)
}

func newToolCache(cf *CacheFlags) (*toolCache, error) {
	tc := &toolCache{}
	tc.tempDir = os.Getenv("RUNNER_TEMP")
	tc.cacheDir = os.Getenv("RUNNER_TOOL_CACHE")

	if tc.tempDir == "" {
		if cf.RunnerTemp == "" {
			return nil, fmt.Errorf("runner temp dir must be specified via environment variable or command line flag")
		}
		tc.tempDir = toAbs(cf.RunnerTemp)
	}

	if tc.cacheDir == "" {
		if cf.RunnerCache == "" {
			return nil, fmt.Errorf("runner tool cache dir must be specified via environment variable or command line flag")
		}
		tc.cacheDir = toAbs(cf.RunnerCache)
	}
	tc.uuid = cf.UUIDDownload
	return tc, nil
}

func (tc toolCache) downloadPath(downloadURL string) (string, error) {
	if tc.uuid {
		uuid, err := uuid.NewRandom()
		if err != nil {
			return "", fmt.Errorf("generating uuid for download cache file: %v", err)
		}
		return filepath.Join(tc.tempDir, uuid.String()), nil
	}
	// sha256.Sum256 takes a byte slice and returns a [32]byte array
	hashInBytes := sha256.Sum256([]byte(downloadURL))
	hashString := base64.RawURLEncoding.EncodeToString(hashInBytes[:])
	return filepath.Join(tc.tempDir, hashString), nil
}

func (tc toolCache) applicationPaths(sd SelectedDownload) (prefix, binary, install string, err error) {
	var specs map[Platform]installSpec
	switch sd.Application {
	case ApplicationChrome:
		specs = chromeInstallSpecs
	case ApplicationChromeDriver:
		specs = chromeDriverInstallSpecs
	default:
		return "", "", "", fmt.Errorf("unknown application %q", sd.Application)
	}
	spec, ok := specs[sd.Platform]
	if !ok {
		return "", "", "", fmt.Errorf("no install spec for platform %q", sd.Platform)
	}

	binary = filepath.Join(
		tc.cacheDir,
		"setup-chrome",
		sd.Application.String(),
		sd.Channel.String(),
		spec.to,
		spec.binary,
	)

	install = filepath.Join(
		tc.cacheDir,
		"setup-chrome",
		sd.Application.String(),
		sd.Channel.String(),
		spec.to,
	)

	prefix = spec.from

	return
}

type installSpec struct {
	from, to, binary string
}

var chromeInstallSpecs = map[Platform]installSpec{
	PlatformLinux64: {"chrome-linux64", "x64", "chrome"},
	PlatformWin64:   {"chrome-win64", "x64", "chrome.exe"},
	PlatformMacArm64: {
		"chrome-mac-arm64",
		filepath.Join("x64"),
		filepath.Join("Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing"),
	},
}

var chromeDriverInstallSpecs = map[Platform]installSpec{
	PlatformLinux64:  {"chromedriver-linux64", "x64", "chromedriver"},
	PlatformWin64:    {"chromedriver-win64", "x64", "chromedriver.exe"},
	PlatformMacArm64: {"chromedriver-mac-arm64", "x64", "chromedriver"},
}

func (t toolCache) binaryExists(path string) bool {
	lpath, err := exec.LookPath(path)
	return err == nil && lpath == path
}

func (t toolCache) fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func unzip(ctx context.Context, prefix, src, dst string) error {
	logger := ctxlog.Logger(ctx)
	// file names in a zip file always use forward slashes
	// and we want to strip the first level of the path for
	// all extracted files.
	prefix = filepath.Clean(prefix) + "/"
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	logger.Info("unzipping file", "source", src, "destination", dst, "prefix", prefix)
	defer r.Close()
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("opening zip contents file %q: %w", f.Name, err)
		}
		stripped := strings.TrimPrefix(path.Clean(f.Name), prefix)
		localized, err := filepath.Localize(stripped)
		if err != nil {
			return fmt.Errorf("localizing path %q (prefix: %q) %q: %w", f.Name, prefix, stripped, err)
		}
		name := filepath.Join(dst, localized)
		if f.FileInfo().IsDir() {
			logger.Debug("creating directory", "zip_entry", f.Name, "stripped", stripped, "localized", localized, "destination", name)
			if err := os.MkdirAll(name, f.Mode()); err != nil {
				return fmt.Errorf("creating directory %q: %w", name, err)
			}
			continue
		}
		logger.Debug("extracting file", "zip_entry", f.Name, "stripped", stripped, "localized", localized, "destination", name)

		out, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("creating file %q: %w", name, err)
			}
			if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
				return fmt.Errorf("creating directory for %q: %w", name, err)
			}
			out, err = os.Create(name)
			if err != nil {
				return fmt.Errorf("creating file %q after creating directory: %w", name, err)
			}
		}
		n, err := io.Copy(out, rc)
		if err != nil {
			return fmt.Errorf("extracting file %q: %w", name, err)
		}
		if err := out.Close(); err != nil {
			return fmt.Errorf("closing file %q: %w", name, err)
		}
		if n != int64(f.UncompressedSize64) {
			return fmt.Errorf("extracted size mismatch for file %q: expected %d, got %d", name, f.UncompressedSize64, n)
		}
		if err := rc.Close(); err != nil {
			return fmt.Errorf("closing zip contents file %q: %w", f.Name, err)
		}
	}
	return nil
}

func updateGithubActionOutput(name, value string) error {
	githubOutput := os.Getenv("GITHUB_OUTPUT")
	if githubOutput == "" {
		return nil
	}
	f, err := os.OpenFile(githubOutput, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("opening GITHUB_OUTPUT file %q: %w", githubOutput, err)
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s=%s\n", name, value)
	if err != nil {
		return fmt.Errorf("writing to GITHUB_OUTPUT file %q: %w", githubOutput, err)
	}
	return nil
}
