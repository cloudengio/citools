// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"strings"
)

type RequestedDownload struct {
	Channel     Channel
	Platform    Platform
	Application Application
}

type SelectedDownload struct {
	Platform    Platform
	Channel     Channel
	Application Application
	Download    Download
	Version     string
	Revision    string
	Prefix      string
}

type Versions struct {
	Timestamp string   `json:"timestamp"`
	Channels  Channels `json:"channels"`
}

type Channels map[string]ChannelInfo

func (v Versions) GetByChannel(channel Channel) (ChannelInfo, error) {
	channelStr := strings.ToLower(channel.String())
	for k, v := range v.Channels {
		if strings.EqualFold(k, channelStr) {
			return v, nil
		}
	}
	return ChannelInfo{}, fmt.Errorf("channel %q not found, available channels: %v", channel, v.ChannelsFound())
}

func (v Versions) ChannelsFound() []string {
	channels := make([]string, 0, len(v.Channels))
	for ch := range v.Channels {
		channels = append(channels, ch)
	}
	return channels
}

func (v Versions) GetRequestedDownload(rd RequestedDownload) (SelectedDownload, error) {
	var sd SelectedDownload
	channels, err := v.GetByChannel(rd.Channel)
	if err != nil {
		return sd, err
	}
	downloads, err := channels.GetByApplication(rd.Application)
	if err != nil {
		return sd, err
	}
	dl, err := downloads.GetByPlatform(rd.Platform)
	return SelectedDownload{
		Platform:    rd.Platform,
		Channel:     rd.Channel,
		Application: rd.Application,
		Download:    dl,
		Version:     channels.Version,
		Revision:    channels.Revision,
		Prefix:      downloads.LongestCommonPrefix(),
	}, err
}

type Download struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
}

type Downloads []Download

func (d Downloads) GetByPlatform(platform Platform) (Download, error) {
	platformStr := strings.ToLower(platform.String())
	for _, dl := range d {
		if strings.EqualFold(dl.Platform, platformStr) {
			return dl, nil
		}
	}
	return Download{}, fmt.Errorf("no download for platform %q: available platforms: %v", platform, d.PlatformsFound())
}

func (d Downloads) LongestCommonPrefix() string {
	if len(d) == 0 {
		return ""
	}
	prefix := d[0].URL
	for _, dl := range d[1:] {
		for !strings.HasPrefix(dl.URL, prefix) {
			prefix = prefix[:len(prefix)-1]
			if prefix == "" {
				return ""
			}
		}
	}
	return prefix
}

func (d Downloads) PlatformsFound() []string {
	platforms := make([]string, 0, len(d))
	for _, dl := range d {
		platforms = append(platforms, dl.Platform)
	}
	return platforms
}

type ChannelInfo struct {
	Channel   string               `json:"channel"`
	Version   string               `json:"version"`
	Revision  string               `json:"revision"`
	Downloads map[string]Downloads `json:"downloads"`
}

func (ci ChannelInfo) GetByApplication(application Application) (Downloads, error) {
	appStr := strings.ToLower(application.String())
	for k, v := range ci.Downloads {
		if strings.EqualFold(k, appStr) {
			return v, nil
		}
	}
	return nil, fmt.Errorf("no downloads for application %q: available applications: %v", application.String(), ci.ApplicationsFound())
}

func (ci ChannelInfo) ApplicationsFound() []string {
	apps := make([]string, 0, len(ci.Downloads))
	for app := range ci.Downloads {
		apps = append(apps, app)
	}
	return apps
}

type Platform int

const (
	PlatformLinux64 Platform = iota
	PlatformMacArm64
	PlatformMacX64
	PlatformWin64
)

func ParsePlatform(s string) (Platform, error) {
	switch s {
	case "linux64":
		return PlatformLinux64, nil
	case "mac-arm64":
		return PlatformMacArm64, nil
	case "mac-x64":
		return PlatformMacX64, nil
	case "win64":
		return PlatformWin64, nil
	default:
		return 0, fmt.Errorf("unknown platform: %q: use of linux64, mac-arm64, mac-x64, win64", s)
	}
}

func (p Platform) String() string {
	switch p {
	case PlatformLinux64:
		return "linux64"
	case PlatformMacArm64:
		return "mac-arm64"
	case PlatformMacX64:
		return "mac-x64"
	case PlatformWin64:
		return "win64"
	default:
		return "unknown"
	}
}

type Channel int

const (
	ChannelStable Channel = iota
	ChannelBeta
	ChannelDev
	ChannelCanary
)

func ParseChannel(s string) (Channel, error) {
	switch s {
	case "stable":
		return ChannelStable, nil
	case "beta":
		return ChannelBeta, nil
	case "dev":
		return ChannelDev, nil
	case "canary":
		return ChannelCanary, nil
	default:
		return 0, fmt.Errorf("unknown channel: %q: use one of stable, beta, dev, canary", s)
	}
}

func (c Channel) String() string {
	switch c {
	case ChannelStable:
		return "stable"
	case ChannelBeta:
		return "beta"
	case ChannelDev:
		return "dev"
	case ChannelCanary:
		return "canary"
	default:
		return "unknown"
	}
}

type Application int

const (
	ApplicationChrome Application = iota
	ApplicationChromeDriver
	ApplicationChromeHeadlessShell
)

func ParseApplication(s string) (Application, error) {
	switch s {
	case "chrome":
		return ApplicationChrome, nil
	case "chromedriver":
		return ApplicationChromeDriver, nil
	case "chrome-headless-shell":
		return ApplicationChromeHeadlessShell, nil
	default:
		return 0, fmt.Errorf("unknown application: %q: use one of chrome, chromedriver, chrome-headless-shell", s)
	}
}

func (a Application) String() string {
	switch a {
	case ApplicationChrome:
		return "chrome"
	case ApplicationChromeDriver:
		return "chromedriver"
	case ApplicationChromeHeadlessShell:
		return "chrome-headless-shell"
	default:
		return "unknown"
	}
}
