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
	t.log("Injecting runner config into %s via SSH...", ip)
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
	for i := 0; i < 10; i++ {
		t.log("Attempting SSH connection to %s (%d/10)...", ip, i+1)
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

	content := fmt.Sprintf("RUNNER_URL=%s\nRUNNER_TOKEN=%s\nRUNNER_NAME=%s\nRUNNER_LABELS=%s\n", url, token, name, labels)
	remotePath := "/Users/admin/.github-runner-config"

	// Using a simple heredoc to write the file
	cmd := fmt.Sprintf("cat << 'EOF' > %s\n%sEOF\n", remotePath, content)
	t.log("Writing runner config to %s via SSH...", remotePath)
	if err := session.Run(cmd); err != nil {
		t.log("Failed to write config file via SSH: %v", err)
		return fmt.Errorf("failed to write config file via SSH: %v", err)
	}

	// Kick the service to make sure it picks up the new config if it already started
	t.log("Kicking Github runner service via SSH...")
	session2, err := client.NewSession()
	if err == nil {
		if err := session2.Run("launchctl kickstart -k gui/$(id -u admin)/com.github.actions.runner"); err != nil {
			t.log("Failed to kickstart runner service: %v (ignoring)", err)
		}
		session2.Close()
	}

	return nil
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
