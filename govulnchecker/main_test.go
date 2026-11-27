// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestImportedButNotCalled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		finding *Finding
		want    bool
	}{
		{
			name:    "nil trace",
			finding: &Finding{},
			want:    false,
		},
		{
			name:    "empty trace",
			finding: &Finding{Trace: []*Frame{}},
			want:    false,
		},
		{
			name: "single frame without function",
			finding: &Finding{
				OSV:   "GO-2026-0001",
				Trace: []*Frame{{Module: "example.com/mod", Package: "example.com/mod/pkg"}},
			},
			want: true,
		},
		{
			name: "single frame with function",
			finding: &Finding{
				OSV:   "GO-2026-0001",
				Trace: []*Frame{{Module: "example.com/mod", Package: "example.com/mod/pkg", Function: "VulnFunc"}},
			},
			want: false,
		},
		{
			name: "multiple frames with empty function",
			finding: &Finding{
				OSV: "GO-2026-0001",
				Trace: []*Frame{
					{Module: "example.com/mod", Package: "example.com/mod/pkg"},
					{Module: "main", Package: "main"},
				},
			},
			want: false,
		},
		{
			name: "multiple frames with functions",
			finding: &Finding{
				OSV: "GO-2026-0001",
				Trace: []*Frame{
					{Module: "example.com/mod", Package: "example.com/mod/pkg", Function: "VulnFunc"},
					{Module: "main", Package: "main", Function: "main"},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := importedButNotCalled(tt.finding); got != tt.want {
				t.Errorf("importedButNotCalled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadIgnored(t *testing.T) {
	t.Parallel()

	t.Run("missing file treated as empty", func(t *testing.T) {
		t.Parallel()
		missing, err := loadIgnored(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		emptyPath := filepath.Join(t.TempDir(), "empty.yaml")
		if err := os.WriteFile(emptyPath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		empty, err := loadIgnored(emptyPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(missing, empty) || len(missing) != 0 {
			t.Errorf("missing = %v, empty = %v; want both empty", missing, empty)
		}
	})

	t.Run("valid yaml trims whitespace", func(t *testing.T) {
		t.Parallel()
		cfgPath := filepath.Join(t.TempDir(), "config.yaml")
		content := `ignore:
  - id: " GO-2023-1234 "
    why: "not reachable"
  - id: GO-2024-5678
    why: "false positive"
`
		if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}

		got, err := loadIgnored(cfgPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := map[string]bool{
			"GO-2023-1234": true,
			"GO-2024-5678": true,
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("loadIgnored() = %v, want %v", got, want)
		}
	})

	t.Run("empty file returns empty map", func(t *testing.T) {
		t.Parallel()
		cfgPath := filepath.Join(t.TempDir(), "empty.yaml")
		if err := os.WriteFile(cfgPath, nil, 0o600); err != nil {
			t.Fatal(err)
		}

		got, err := loadIgnored(cfgPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want empty map", got)
		}
	})

	t.Run("invalid yaml returns error", func(t *testing.T) {
		t.Parallel()
		cfgPath := filepath.Join(t.TempDir(), "bad.yaml")
		if err := os.WriteFile(cfgPath, []byte("ignore: [unclosed"), 0o600); err != nil {
			t.Fatal(err)
		}

		if _, err := loadIgnored(cfgPath); err == nil {
			t.Fatal("expected error for invalid YAML, got nil")
		}
	})

	t.Run("directory path returns read error", func(t *testing.T) {
		t.Parallel()
		if _, err := loadIgnored(t.TempDir()); err == nil {
			t.Fatal("expected error when reading a directory, got nil")
		}
	})
}

func TestFilterStream(t *testing.T) {
	t.Parallel()

	configSymbol := `{"config":{"scan_level":"symbol"}}`
	configPackage := `{"config":{"scan_level":"package"}}`
	osvOnly := `{"osv":{"id":"GO-2026-0001"}}`
	emptyOSVFinding := `{"finding":{"trace":[{"module":"example.com/m","function":"F"}]}}`
	calledFinding := `{"finding":{"osv":"GO-2026-0001","trace":[{"module":"example.com/m","package":"example.com/m","function":"F"}]}}`
	ignoredFinding := `{"finding":{"osv":"GO-2026-9999","trace":[{"module":"example.com/m","package":"example.com/m","function":"F"}]}}`
	notCalledFinding := `{"finding":{"osv":"GO-2026-0002","trace":[{"module":"example.com/m","package":"example.com/m"}]}}`

	tests := []struct {
		name             string
		stream           string
		ignored          map[string]bool
		includeNotCalled bool
		wantUnmatched    bool
		wantOutSubstr    []string
		wantOutEmpty     bool
		wantErrSubstr    string
	}{
		{
			name:         "empty stream",
			stream:       "",
			wantOutEmpty: true,
		},
		{
			name:         "config and osv messages only",
			stream:       configSymbol + "\n" + osvOnly + "\n",
			wantOutEmpty: true,
		},
		{
			name:         "finding with empty osv is skipped",
			stream:       configSymbol + "\n" + emptyOSVFinding + "\n",
			wantOutEmpty: true,
		},
		{
			name:         "ignored vulnerability is skipped",
			stream:       configSymbol + "\n" + ignoredFinding + "\n",
			ignored:      map[string]bool{"GO-2026-9999": true},
			wantOutEmpty: true,
		},
		{
			name:          "unmatched called vulnerability is reported",
			stream:        configSymbol + "\n" + calledFinding + "\n",
			wantUnmatched: true,
			wantOutSubstr: []string{"Unmatched Vulnerability Found (GO-2026-0001):", `"osv": "GO-2026-0001"`},
		},
		{
			name:         "imported but not called skipped at symbol level by default",
			stream:       configSymbol + "\n" + notCalledFinding + "\n",
			wantOutEmpty: true,
		},
		{
			name:         "imported but not called skipped when scan level omitted (defaults to symbol)",
			stream:       notCalledFinding + "\n",
			wantOutEmpty: true,
		},
		{
			name:             "imported but not called reported when includeNotCalled is true",
			stream:           configSymbol + "\n" + notCalledFinding + "\n",
			includeNotCalled: true,
			wantUnmatched:    true,
			wantOutSubstr:    []string{"Unmatched Vulnerability Found (GO-2026-0002):"},
		},
		{
			name:          "imported but not called reported at package scan level",
			stream:        configPackage + "\n" + notCalledFinding + "\n",
			wantUnmatched: true,
			wantOutSubstr: []string{"Unmatched Vulnerability Found (GO-2026-0002):"},
		},
		{
			name:          "malformed json logs error and stops",
			stream:        calledFinding + "\n{not-json}\n",
			wantUnmatched: true,
			wantOutSubstr: []string{"Unmatched Vulnerability Found (GO-2026-0001):"},
			wantErrSubstr: "Error decoding govulncheck JSON output:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out, errOut bytes.Buffer
			got := filterStream(strings.NewReader(tt.stream), &out, &errOut, tt.ignored, tt.includeNotCalled)
			if got != tt.wantUnmatched {
				t.Errorf("filterStream() = %v, want %v", got, tt.wantUnmatched)
			}
			if tt.wantOutEmpty && out.Len() != 0 {
				t.Errorf("expected empty stdout, got %q", out.String())
			}
			for _, sub := range tt.wantOutSubstr {
				if !strings.Contains(out.String(), sub) {
					t.Errorf("stdout %q missing %q", out.String(), sub)
				}
			}
			if tt.wantErrSubstr == "" && errOut.Len() != 0 {
				t.Errorf("expected empty stderr, got %q", errOut.String())
			}
			if tt.wantErrSubstr != "" && !strings.Contains(errOut.String(), tt.wantErrSubstr) {
				t.Errorf("stderr %q missing %q", errOut.String(), tt.wantErrSubstr)
			}
		})
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	calledFinding := `{"finding":{"osv":"GO-2026-0001","trace":[{"module":"example.com/m","package":"example.com/m","function":"F"}]}}`
	notCalledFinding := `{"finding":{"osv":"GO-2026-0002","trace":[{"module":"example.com/m","package":"example.com/m"}]}}`

	makeHelperCmd := func(t *testing.T, stdout string, exitCode int, wantArgs []string) func(string, ...string) *exec.Cmd {
		t.Helper()
		return func(_ string, args ...string) *exec.Cmd {
			if wantArgs != nil && !reflect.DeepEqual(args, wantArgs) {
				t.Errorf("govulncheck args = %v, want %v", args, wantArgs)
			}
			cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
			cmd.Env = append(os.Environ(),
				"GO_WANT_HELPER_PROCESS=1",
				"GO_HELPER_STDOUT="+stdout,
				"GO_HELPER_EXIT_CODE="+strconv.Itoa(exitCode),
			)
			return cmd
		}
	}

	t.Run("no vulnerabilities exits 0", func(t *testing.T) {
		t.Parallel()
		var out, errOut bytes.Buffer
		code := run([]string{"-config", filepath.Join(t.TempDir(), "missing.yaml"), "--", "./..."},
			&out, &errOut, makeHelperCmd(t, `{"config":{"scan_level":"symbol"}}`+"\n", 0, []string{"--format=json", "./..."}))
		if code != 0 {
			t.Fatalf("run() = %d, want 0 (stderr: %s)", code, errOut.String())
		}
	})

	t.Run("all vulnerabilities ignored with exit code 3 exits 0", func(t *testing.T) {
		t.Parallel()
		cfgPath := filepath.Join(t.TempDir(), "cfg.yaml")
		if err := os.WriteFile(cfgPath, []byte("ignore:\n  - id: GO-2026-0001\n    why: ok\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, errOut bytes.Buffer
		code := run([]string{"-config", cfgPath}, &out, &errOut, makeHelperCmd(t, calledFinding+"\n", 3, nil))
		if code != 0 {
			t.Fatalf("run() = %d, want 0 (stderr: %s)", code, errOut.String())
		}
		if out.Len() != 0 {
			t.Errorf("expected no stdout, got %q", out.String())
		}
	})

	t.Run("unmatched vulnerability exits 1", func(t *testing.T) {
		t.Parallel()
		var out, errOut bytes.Buffer
		code := run([]string{"-config", filepath.Join(t.TempDir(), "missing.yaml")},
			&out, &errOut, makeHelperCmd(t, calledFinding+"\n", 3, nil))
		if code != 1 {
			t.Fatalf("run() = %d, want 1", code)
		}
		if !strings.Contains(out.String(), "GO-2026-0001") {
			t.Errorf("stdout %q missing GO-2026-0001", out.String())
		}
		if !strings.Contains(errOut.String(), "Error: Unmatched vulnerabilities were found.") {
			t.Errorf("stderr %q missing error summary", errOut.String())
		}
	})

	t.Run("include-not-called flag reports uncalled vulnerability", func(t *testing.T) {
		t.Parallel()
		var out, errOut bytes.Buffer
		code := run([]string{"-config", filepath.Join(t.TempDir(), "missing.yaml"), "-include-not-called"},
			&out, &errOut, makeHelperCmd(t, notCalledFinding+"\n", 3, nil))
		if code != 1 {
			t.Fatalf("run() = %d, want 1", code)
		}
		if !strings.Contains(out.String(), "GO-2026-0002") {
			t.Errorf("stdout %q missing GO-2026-0002", out.String())
		}
	})

	t.Run("govulncheck non-3 failure exits 1", func(t *testing.T) {
		t.Parallel()
		var out, errOut bytes.Buffer
		code := run([]string{"-config", filepath.Join(t.TempDir(), "missing.yaml")},
			&out, &errOut, makeHelperCmd(t, "", 2, nil))
		if code != 1 {
			t.Fatalf("run() = %d, want 1", code)
		}
		if !strings.Contains(errOut.String(), "govulncheck failed:") {
			t.Errorf("stderr %q missing failure message", errOut.String())
		}
	})

	t.Run("invalid config file exits 1", func(t *testing.T) {
		t.Parallel()
		cfgPath := filepath.Join(t.TempDir(), "bad.yaml")
		if err := os.WriteFile(cfgPath, []byte("ignore: [bad"), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, errOut bytes.Buffer
		code := run([]string{"-config", cfgPath}, &out, &errOut, makeHelperCmd(t, "", 0, nil))
		if code != 1 {
			t.Fatalf("run() = %d, want 1", code)
		}
		if !strings.Contains(errOut.String(), "error parsing config file:") {
			t.Errorf("stderr %q missing parse error", errOut.String())
		}
	})
}

func TestRun_MissingDefaultConfig(t *testing.T) {
	t.Chdir(t.TempDir())

	makeCmd := func(stdout string, exitCode int) func(string, ...string) *exec.Cmd {
		return func(_ string, _ ...string) *exec.Cmd {
			cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
			cmd.Env = append(os.Environ(),
				"GO_WANT_HELPER_PROCESS=1",
				"GO_HELPER_STDOUT="+stdout,
				"GO_HELPER_EXIT_CODE="+strconv.Itoa(exitCode),
			)
			return cmd
		}
	}

	var out, errOut bytes.Buffer
	code := run(nil, &out, &errOut, makeCmd(`{"config":{"scan_level":"symbol"}}`+"\n", 0))
	if code != 0 {
		t.Fatalf("run() with missing default config and no vulns = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("expected empty stderr, got %q", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	calledFinding := `{"finding":{"osv":"GO-2026-0001","trace":[{"module":"example.com/m","package":"example.com/m","function":"F"}]}}`
	code = run(nil, &out, &errOut, makeCmd(calledFinding+"\n", 3))
	if code != 1 {
		t.Fatalf("run() with missing default config and finding = %d, want 1", code)
	}
	if !strings.Contains(out.String(), "GO-2026-0001") {
		t.Errorf("stdout %q missing GO-2026-0001", out.String())
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	_, _ = fmt.Fprint(os.Stdout, os.Getenv("GO_HELPER_STDOUT"))
	code, _ := strconv.Atoi(os.Getenv("GO_HELPER_EXIT_CODE"))
	os.Exit(code)
}
