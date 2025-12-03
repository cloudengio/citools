// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"cloudeng.io/file/diskusage"
	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/net/http/httpfs"
)

type VersionFlags struct {
	Channel     string `subcmd:"channel,stable,chrome for testing channel channel to install"`
	Platform    string `subcmd:"platform,,'platform to install chrome for testing for, e.g. linux64, mac-arm64, win64'"`
	Application string `subcmd:"application,chrome,'name of the application to install, e.g. chrome, chromedriver'"`
}

func (vf *VersionFlags) ParseRequestedDownload() (RequestedDownload, error) {
	var rd RequestedDownload
	if vf.Platform == "" {
		vf.Platform = currentPlatform()
	}
	platform, err := ParsePlatform(vf.Platform)

	if err != nil {
		return rd, fmt.Errorf("parsing platform: %w", err)
	}
	channel, err := ParseChannel(vf.Channel)
	if err != nil {
		return rd, fmt.Errorf("parsing channel: %w", err)
	}
	application, err := ParseApplication(vf.Application)
	if err != nil {
		return rd, fmt.Errorf("parsing application: %w", err)
	}
	return RequestedDownload{
		Platform:    platform,
		Channel:     channel,
		Application: application,
	}, nil
}

type installFlags struct {
	VersionFlags
	CacheFlags
	Debug      bool `subcmd:"debug,false,eenable debug output"`
	Initialize bool `subcmd:"initialize,false,initialize browser profile after installation"`
}

type downloadInstallCmd struct{}

func (ic *downloadInstallCmd) installCmd(ctx context.Context, f any, args []string) error {
	fv := f.(*installFlags)
	sd, err := ic.getSelectedDownload(ctx, fv.VersionFlags)
	if err != nil {
		return fmt.Errorf("getting download: %w", err)
	}
	level := slog.LevelInfo
	if fv.Debug {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	}))
	ctx = ctxlog.WithLogger(ctx, logger)

	cache, err := newToolCache(&fv.CacheFlags)
	if err != nil {
		return fmt.Errorf("creating tool cache: %w", err)
	}

	prefix, binaryPath, installDir, err := cache.applicationPaths(sd)
	if err != nil {
		return fmt.Errorf("getting application paths: %w", err)
	}

	if !cache.binaryExists(binaryPath) {
		downloadPath, err := ic.download(ctx, cache, sd)
		if err != nil {
			return fmt.Errorf("downloading file: %w", err)
		}

		fmt.Printf("Extracting download %q to %q (prefix: %q)\n", downloadPath, installDir, prefix)
		if err := unzip(ctx, prefix, downloadPath, installDir); err != nil {
			return fmt.Errorf("unzipping download: %w", err)
		}

		if err := prepareInstallDir(ctx, installDir); err != nil {
			return fmt.Errorf("preparing install dir: %w", err)
		}
	}

	version, err := getVersion(ctx, fv.Debug, binaryPath)
	if err != nil {
		return fmt.Errorf("failed to get version for %q: %w", binaryPath, err)
	}

	logger.Info("installation complete", "binary", binaryPath, "version", version)
	if err := updateGithubActionOutput("chrome-path", binaryPath); err != nil {
		return fmt.Errorf("updating github action output: %w", err)
	}

	if !fv.Initialize {
		return nil
	}

	userDataDir, err := getUserDataDir(runtime.GOOS)
	if err != nil {
		return fmt.Errorf("determining user data dir: %w", err)
	}
	if err := updateGithubActionOutput("chrome-user-data-dir", userDataDir); err != nil {
		return fmt.Errorf("updating github action output: %w", err)
	}

	browser := browser{
		goos:        runtime.GOOS,
		binaryPath:  binaryPath,
		userDataDir: userDataDir,
		debug:       fv.Debug,
	}
	logger.Info("initializing browser profile", "user_data_dir", userDataDir)
	if err := browser.init(ctx, 30*time.Second); err != nil {
		return fmt.Errorf("initializing browser profile: %w", err)
	}
	return nil
}

func (ic *downloadInstallCmd) download(ctx context.Context, cache *toolCache, sd SelectedDownload) (string, error) {
	downloadPath, err := cache.downloadPath(sd.Download.URL)
	if err != nil {
		return "", fmt.Errorf("failed to determine path for download: %w", err)
	}
	if cache.fileExists(downloadPath) {
		return downloadPath, nil
	}
	logger := ctxlog.Logger(ctx)
	logger.Info("downloading file", "url", sd.Download.URL, "path", downloadPath)
	start := time.Now()
	downloader := httpfs.NewDownloader().
		WithReaderOptions(httpfs.WithLargeFileBlockSize(64 * 1024 * 1024))
	n, err := downloader.DownloadFile(ctx, sd.Download.URL, downloadPath)
	if err != nil {
		return "", fmt.Errorf("downloading %q: %w", sd.Download.URL, err)
	}
	took := time.Since(start)
	logger.Info("downloaded file", "url", sd.Download.URL, "path", downloadPath, "size", diskusage.Decimal(n), "duration", took.Round(time.Millisecond*10), "speed_MBps", diskusage.MB.Value(n)/took.Seconds())
	return downloadPath, nil
}

func (downloadInstallCmd) getSelectedDownload(ctx context.Context, vf VersionFlags) (SelectedDownload, error) {
	var sd SelectedDownload
	rd, err := vf.ParseRequestedDownload()
	if err != nil {
		return sd, fmt.Errorf("invalid requested download: %w", err)
	}
	ep := endpoints{}
	versions, err := ep.getLastKnownGoodVersions(ctx)
	if err != nil {
		return sd, fmt.Errorf("failed getting last good versions: %w", err)
	}
	sd, err = versions.GetRequestedDownload(rd)
	if err != nil {
		return sd, fmt.Errorf("getting selected download: %w", err)
	}
	return sd, nil
}

func getVersion(ctx context.Context, debug bool, binaryPath string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := []string{"--version", "--headless=new", "--disable-gpu"}
	ctxlog.Debug(ctx, "running", "binary", binaryPath, "args", args)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	if debug {
		cmd.Stderr = io.MultiWriter(stderr, os.Stderr)
		cmd.Stdout = io.MultiWriter(stdout, os.Stdout)
	} else {
		cmd.Stderr = stderr
		cmd.Stdout = stdout
	}
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("running %v: %w", strings.Join(cmd.Args, " "), err)
	}
	return string(bytes.TrimSpace(stdout.Bytes())), nil
}
