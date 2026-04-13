package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type Tart struct {
	BaseName string
	Verbose  bool
}

func (t *Tart) log(format string, args ...interface{}) {
	if t.Verbose {
		fmt.Printf("[TART DEBUG] "+format+"\n", args...)
	}
}

func (t *Tart) Clone(ctx context.Context, newName string) error {
	t.log("Cloning VM %s to %s", t.BaseName, newName)
	cmd := exec.CommandContext(ctx, "tart", "clone", t.BaseName, newName)
	if t.Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

func (t *Tart) Run(ctx context.Context, vmName string) (*exec.Cmd, error) {
	t.log("Starting VM %s (--no-graphics)", vmName)
	cmd := exec.CommandContext(ctx, "tart", "run", "--no-graphics", vmName)
	if t.Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Start(); err != nil {
		t.log("Failed to start VM %s: %v", vmName, err)
		return nil, err
	}
	return cmd, nil
}

func (t *Tart) GetIP(ctx context.Context, vmName string) (string, error) {
	t.log("Waiting for IP for VM %s...", vmName)
	for i := 0; i < 12; i++ { // Try for 60 seconds
		cmd := exec.CommandContext(ctx, "tart", "ip", vmName)
		out, err := cmd.Output()
		if err == nil {
			ip := strings.TrimSpace(string(out))
			if ip != "" {
				t.log("Found IP for VM %s: %s", vmName, ip)
				return ip, nil
			}
		}
		t.log("Retrying tart ip %s (%d/12)...", vmName, i+1)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return "", fmt.Errorf("timeout waiting for IP for VM %s", vmName)
}

func (t *Tart) Delete(ctx context.Context, vmName string) error {
	t.log("Deleting VM %s", vmName)
	cmd := exec.CommandContext(ctx, "tart", "delete", vmName)
	if t.Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

func (t *Tart) InjectConfig(ctx context.Context, ip, url, token, name, labels string) error {
	t.log("Configuring and starting runner on %s via SSH...", ip)
	config := &ssh.ClientConfig{
		User: "admin",
		Auth: []ssh.AuthMethod{
			ssh.Password("admin"),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}

	var client *ssh.Client
	var err error
	for i := 0; i < 15; i++ { // More retries for SSH boot
		t.log("Attempting SSH connection to %s (%d/15)...", ip, i+1)
		client, err = ssh.Dial("tcp", net.JoinHostPort(ip, "22"), config)
		if err == nil {
			t.log("SSH connection established to %s", ip)
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	if err != nil {
		t.log("Failed to dial SSH: %v", err)
		return fmt.Errorf("failed to dial SSH: %v", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		t.log("Failed to create SSH session: %v", err)
		return fmt.Errorf("failed to create SSH session: %v", err)
	}
	defer session.Close()

	// Direct configuration and startup.
	// We use nohup or & to ensure the session doesn't block the orchestrator indefinitely if we want,
	// but here we actually want it to run. However, the runner .run.sh blocks.
	// So we'll run the config command and then run.sh in the background OR as part of a script that handles it.
	
	setupCmd := fmt.Sprintf(`cd /Users/admin/actions-runner && \
./config.sh --url %s --token %s --name %s --labels %s --unattended --ephemeral --replace && \
./run.sh && \
sudo shutdown -h now`, url, token, name, labels)

	t.log("Running configuration and startup command on %s...", ip)
	// We run this in the background on the remote host so we don't hang the SSH session if it blocks.
	// But ./run.sh IS blocking. If we want the orchestrator to continue, we might want to background it.
	// Actually, the session.Run will wait for the command to finish.
	// Since ./run.sh finishes when the job is done, this is fine, but we might want a timeout or to run it in background.
	// Better to run it in background remote and exit session.
	
	bgCmd := fmt.Sprintf("nohup bash -c %s > /Users/admin/actions-runner/orch.log 2>&1 &", q(setupCmd))
	
	if err := session.Run(bgCmd); err != nil {
		t.log("Failed to start runner via SSH: %v", err)
		return fmt.Errorf("failed to start runner via SSH: %v", err)
	}

	return nil
}

func q(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// Just in case we need to copy a file directly
func copyFile(session *ssh.Session, remotePath string, content string) error {
	w, err := session.StdinPipe()
	if err != nil {
		return err
	}
	go func() {
		defer w.Close()
		fmt.Fprint(w, content)
	}()
	return session.Run(fmt.Sprintf("cat > %s", remotePath))
}
