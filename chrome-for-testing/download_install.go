// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"cloudeng.io/file/diskusage"
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
		vf.Platform = defaultPlatform()
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
}

type downloadInstallCmd struct{}

func (ic *downloadInstallCmd) installCmd(ctx context.Context, f any, args []string) error {
	fv := f.(*installFlags)
	sd, err := ic.getSelectedDownload(ctx, fv.VersionFlags)
	if err != nil {
		return fmt.Errorf("getting download: %w", err)
	}

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
		if err := unzip(prefix, downloadPath, installDir); err != nil {
			return fmt.Errorf("unzipping download: %w", err)
		}
	}
	version, err := getVersion(ctx, binaryPath)
	if err != nil {
		return fmt.Errorf("failed to get version for %q: %w", binaryPath, err)
	}
	fmt.Printf("Installed version: %v\n", version)
	if err := updateGithubActionOutput("chrome-path", binaryPath); err != nil {
		return fmt.Errorf("updating github action output: %w", err)
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
	fmt.Printf("Downloading %v to %v\n", sd.Download.URL, downloadPath)
	start := time.Now()
	downloader := httpfs.NewDownloader().
		WithReaderOptions(httpfs.WithLargeFileBlockSize(64 * 1024 * 1024))
	n, err := downloader.DownloadFile(ctx, sd.Download.URL, downloadPath)
	if err != nil {
		return "", fmt.Errorf("downloading %q: %w", sd.Download.URL, err)
	}
	took := time.Since(start)
	fmt.Printf("Downloaded %q to %q: %.2f in %v (%.2f MB/s)\n", sd.Download.URL, downloadPath, diskusage.Decimal(n), took.Round(time.Millisecond*10), diskusage.MB.Value(n)/took.Seconds())
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

func getVersion(ctx context.Context, binaryPath string) (string, error) {
	cmd := exec.CommandContext(ctx, binaryPath, "--version")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("running %q --version: %w", binaryPath, err)
	}
	return string(bytes.TrimSpace(out)), nil
}
