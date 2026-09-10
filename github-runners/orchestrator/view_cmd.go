// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"cloudeng.io/algo/ratecontrol"
	"cloudeng.io/net/pssh"
)

// ViewCommand forwards a local port to a remote host running the orchestrator
// via persistent SSH, and opens a local browser to view the web UI.
type ViewCommand struct{}

type ViewFlags struct {
	RemotePort        int           `subcmd:"remote-port,8088,port of the web UI on the remote host"`
	LocalPort         int           `subcmd:"local-port,0,'local port to forward to; defaults to remote-port if free, or an ephemeral free port'"`
	User              string        `subcmd:"user,,ssh user name (defaults to current user)"`
	SSHPort           int           `subcmd:"ssh-port,22,default ssh port if not specified in host"`
	AcceptNewHostKeys bool          `subcmd:"accept-new-host-keys,true,accept and record new SSH host keys"`
	DialTimeout       time.Duration `subcmd:"dial-timeout,30s,timeout for establishing the initial SSH connection"`
	NoBrowser         bool          `subcmd:"no-browser,false,do not automatically open the web browser"`
}

var openBrowserFn = openBrowser

func openBrowser(targetURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", targetURL)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", targetURL)
	default:
		cmd = exec.Command("xdg-open", targetURL)
	}
	return cmd.Start()
}

type parsedTarget struct {
	user       string
	host       string
	sshPort    int
	localPort  int
	remotePort int
}

func parsePort(s string, name string) (int, error) {
	p, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid %s port %q: %w", name, s, err)
	}
	if p < 1 || p > 65535 {
		return 0, fmt.Errorf("invalid %s port %d: must be between 1 and 65535", name, p)
	}
	return p, nil
}

func parseSSHTarget(target string, defaultUser string, defaultSSHPort, defaultLocalPort, defaultRemotePort int) (parsedTarget, error) {
	res := parsedTarget{
		user:       defaultUser,
		sshPort:    defaultSSHPort,
		localPort:  defaultLocalPort,
		remotePort: defaultRemotePort,
	}

	target = strings.TrimSpace(target)
	if target == "" {
		return res, fmt.Errorf("empty host specified")
	}

	// Extract user if present, handling user@host, user@local:host:remote, or local:user@host:remote.
	if atIdx := strings.Index(target, "@"); atIdx != -1 {
		prefix := target[:atIdx]
		suffix := target[atIdx+1:]
		if suffix == "" {
			return res, fmt.Errorf("missing host after '@'")
		}
		if lastColon := strings.LastIndex(prefix, ":"); lastColon != -1 {
			res.user = prefix[lastColon+1:]
			target = prefix[:lastColon+1] + suffix
		} else {
			res.user = prefix
			target = suffix
		}
		if res.user == "" {
			res.user = defaultUser
		}
	}

	// Check for bracketed IPv6 host: e.g. [::1], 8080:[::1]:8088, etc.
	openIdx := strings.Index(target, "[")
	closeIdx := strings.Index(target, "]")
	if openIdx != -1 && closeIdx != -1 && openIdx < closeIdx {
		res.host = target[openIdx+1 : closeIdx]
		if res.host == "" {
			return res, fmt.Errorf("invalid host: empty IPv6 address in brackets")
		}
		prefix := target[:openIdx]
		if prefix != "" {
			prefix = strings.TrimSuffix(prefix, ":")
			if prefix != "" {
				p, err := parsePort(prefix, "local")
				if err != nil {
					return res, err
				}
				res.localPort = p
			}
		}
		suffix := target[closeIdx+1:]
		if suffix != "" {
			suffix = strings.TrimPrefix(suffix, ":")
			if suffix != "" {
				parts := strings.Split(suffix, ":")
				if len(parts) == 1 {
					p, err := parsePort(parts[0], "remote")
					if err != nil {
						return res, err
					}
					res.remotePort = p
				} else if len(parts) == 2 {
					sp, err := parsePort(parts[0], "ssh")
					if err != nil {
						return res, err
					}
					res.sshPort = sp
					rp, err := parsePort(parts[1], "remote")
					if err != nil {
						return res, err
					}
					res.remotePort = rp
				} else {
					return res, fmt.Errorf("too many colons in target suffix %q", suffix)
				}
			}
		}
		return res, nil
	}

	tokens := strings.Split(target, ":")
	switch len(tokens) {
	case 1:
		res.host = tokens[0]
		if res.host == "" {
			return res, fmt.Errorf("empty host specified")
		}
	case 2:
		// Could be local-port:host or host:ssh-port
		if p0, err := strconv.Atoi(tokens[0]); err == nil && p0 > 0 && p0 <= 65535 {
			res.localPort = p0
			res.host = tokens[1]
			if res.host == "" {
				return res, fmt.Errorf("empty host specified after local port")
			}
		} else {
			res.host = tokens[0]
			if res.host == "" {
				return res, fmt.Errorf("empty host specified")
			}
			p1, err := parsePort(tokens[1], "ssh")
			if err != nil {
				return res, err
			}
			res.sshPort = p1
		}
	case 3:
		// local-port:host:remote-port
		if tokens[0] != "" {
			p0, err := parsePort(tokens[0], "local")
			if err != nil {
				return res, err
			}
			res.localPort = p0
		}
		res.host = tokens[1]
		if res.host == "" {
			return res, fmt.Errorf("empty host specified in local-port:host:remote-port")
		}
		if tokens[2] != "" {
			p2, err := parsePort(tokens[2], "remote")
			if err != nil {
				return res, err
			}
			res.remotePort = p2
		}
	case 4:
		// local-port:host:ssh-port:remote-port
		if tokens[0] != "" {
			p0, err := parsePort(tokens[0], "local")
			if err != nil {
				return res, err
			}
			res.localPort = p0
		}
		res.host = tokens[1]
		if res.host == "" {
			return res, fmt.Errorf("empty host specified in local-port:host:ssh-port:remote-port")
		}
		if tokens[2] != "" {
			sp, err := parsePort(tokens[2], "ssh")
			if err != nil {
				return res, err
			}
			res.sshPort = sp
		}
		if tokens[3] != "" {
			p3, err := parsePort(tokens[3], "remote")
			if err != nil {
				return res, err
			}
			res.remotePort = p3
		}
	default:
		return res, fmt.Errorf("too many colons in target %q; format is [local-port:]host[:remote-port]", target)
	}

	return res, nil
}

func selectLocalPort(requested, remotePort int) (int, error) {
	if requested > 0 {
		return requested, nil
	}
	if remotePort > 0 {
		if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", remotePort)); err == nil {
			_ = l.Close()
			return remotePort, nil
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("finding free local port: %w", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func checkForwardReady(ctx context.Context, port int) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
	if err != nil {
		return false
	}
	client := &http.Client{
		Timeout: 400 * time.Millisecond,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		return true
	}
	return false
}

func (v ViewCommand) Run(ctx context.Context, flags any, args []string) error {
	fv := flags.(*ViewFlags)
	if len(args) == 0 {
		return fmt.Errorf("missing remote host argument; usage: orchestrator view <[local-port:][user@]host[:remote-port]>")
	}
	if len(args) > 1 {
		return fmt.Errorf("unexpected arguments: %v; expected a single host argument", args[1:])
	}

	target, err := parseSSHTarget(args[0], fv.User, fv.SSHPort, fv.LocalPort, fv.RemotePort)
	if err != nil {
		return err
	}

	remotePort := target.remotePort
	if remotePort <= 0 {
		remotePort = 8088
	}

	localPort, err := selectLocalPort(target.localPort, remotePort)
	if err != nil {
		return err
	}

	sshPort := target.sshPort
	if sshPort <= 0 {
		sshPort = 22
	}
	hostPort := net.JoinHostPort(target.host, strconv.Itoa(sshPort))

	var psshOpts []pssh.Option
	psshOpts = append(psshOpts,
		pssh.WithLocalPortForward(localPort, remotePort),
		pssh.WithDialTimeout(fv.DialTimeout),
	)
	if target.user != "" {
		psshOpts = append(psshOpts, pssh.WithUser(target.user))
	}
	if fv.AcceptNewHostKeys {
		psshOpts = append(psshOpts, pssh.WithAcceptNewHostKeys(true))
	}

	client := pssh.NewClient(ctx, "tcp", hostPort, psshOpts...)
	defer client.Close()

	backoffFn := func() ratecontrol.Backoff {
		return ratecontrol.NewExponentialBackoff(500*time.Millisecond, 8)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.ConnectAndWait(ctx, backoffFn)
	}()

	webURL := fmt.Sprintf("http://127.0.0.1:%d", localPort)
	fmt.Printf("Connecting to %s via SSH and forwarding %s -> remote port %d...\n", hostPort, webURL, remotePort)

	// Wait for the forward to become active or connection to fail.
	timeout := fv.DialTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	ready := false
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

waitLoop:
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, pssh.ErrClientClosed) {
				if errors.Is(err, pssh.ErrNoAgent) {
					return fmt.Errorf("ssh agent required: %w (ensure ssh-agent is running and keys are added with ssh-add)", err)
				}
				return fmt.Errorf("ssh connection to %s failed: %w", hostPort, err)
			}
			return nil
		case <-ctx.Done():
			client.Close()
			return ctx.Err()
		case <-ticker.C:
			if checkForwardReady(ctx, localPort) {
				ready = true
				break waitLoop
			}
		}
	}

	if ready {
		fmt.Printf("Web UI is ready at %s\n", webURL)
	} else {
		fmt.Printf("Web UI port forward established at %s (waiting for remote web service response)\n", webURL)
	}

	if !fv.NoBrowser {
		fmt.Printf("Opening browser at %s...\n", webURL)
		if err := openBrowserFn(webURL); err != nil {
			fmt.Printf("Failed to open browser automatically: %v\n", err)
		}
	}

	fmt.Println("Press Ctrl+C to close the connection.")

	select {
	case <-ctx.Done():
		client.Close()
		<-errCh
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, pssh.ErrClientClosed) {
			if errors.Is(err, pssh.ErrNoAgent) {
				return fmt.Errorf("ssh agent required: %w (ensure ssh-agent is running and keys are added with ssh-add)", err)
			}
			return fmt.Errorf("ssh connection to %s lost: %w", hostPort, err)
		}
		return nil
	}
}
