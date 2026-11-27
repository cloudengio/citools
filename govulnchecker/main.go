// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

// Command govulnchecker is a wrapper around the standard 'govulncheck' tool that
// provides the ability to ignore specific known vulnerabilities.
//
// It runs 'govulncheck -json' with any provided arguments and parses the output
// stream, filtering out any vulnerabilities that match the IDs specified in a
// YAML configuration file. If any non-ignored vulnerabilities are found, the
// command exits with status 1.
//
// The default configuration file is '.govulnchecker.yaml', which can be
// overridden using the '-config' flag.
//
// Configuration File Format:
//
// The configuration file is a YAML document with an 'ignore' key containing a
// list of vulnerabilities to ignore. Each item must have an 'id' (the OSV ID)
// and typically a 'why' field providing the justification.
//
// Example:
//
//	ignore:
//	  - id: GO-2023-1234
//	    why: "We do not use the vulnerable function in this module."
//	  - id: GO-2022-5678
//	    why: "This is a false positive for our specific deployment environment."
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents the YAML configuration defining vulnerabilities to ignore.
type Config struct {
	Ignore []Item `yaml:"ignore"`
}

type Item struct {
	ID  string `json:"id"`
	Why string `json:"why"`
}

// Position describes a source position within a file.
type Position struct {
	Filename string `json:"filename,omitempty"`
	Offset   int    `json:"offset"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
}

// Frame represents a single entry in a finding's call trace.
type Frame struct {
	// Module is the module path; "stdlib" for standard library packages.
	Module   string    `json:"module"`
	Version  string    `json:"version,omitempty"`
	Package  string    `json:"package,omitempty"`
	Function string    `json:"function,omitempty"`
	Receiver string    `json:"receiver,omitempty"`
	Position *Position `json:"position,omitempty"`
}

// Finding represents a vulnerability finding emitted by govulncheck -json.
type Finding struct {
	OSV          string   `json:"osv,omitempty"`
	FixedVersion string   `json:"fixed_version,omitempty"`
	Trace        []*Frame `json:"trace,omitempty"`
}

// ScanConfig is emitted as the first message by govulncheck -json.
type ScanConfig struct {
	ProtocolVersion string `json:"protocol_version"`
	ScannerName     string `json:"scanner_name"`
	ScannerVersion  string `json:"scanner_version"`
	DB              string `json:"db"`
	DBLastModified  string `json:"db_last_modified"`
	GoVersion       string `json:"go_version"`
	ScanLevel       string `json:"scan_level"`
	ScanMode        string `json:"scan_mode"`
}

// GovulncheckMessage represents a single JSON object emitted by govulncheck -json.
type GovulncheckMessage struct {
	Config *ScanConfig `json:"config,omitempty"`
	// Older versions of govulncheck emit OSV
	OSV *struct {
		ID string `json:"id"`
	} `json:"osv,omitempty"`
	// Newer versions emit Finding which references the OSV ID
	Finding *Finding `json:"finding,omitempty"`
}

// importedButNotCalled reports whether f was reached only through an import,
// not through an actual call chain. At symbol scan level this is indicated by
// a single-frame trace whose frame carries no Function name.
func importedButNotCalled(f *Finding) bool {
	if len(f.Trace) != 1 {
		return false
	}
	return f.Trace[0].Function == ""
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, exec.Command))
}

func loadIgnored(configFile string) (map[string]bool, error) {
	b, err := os.ReadFile(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("Error reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("Error parsing config file: %w", err)
	}

	ignoredVars := make(map[string]bool, len(cfg.Ignore))
	for _, item := range cfg.Ignore {
		ignoredVars[strings.TrimSpace(item.ID)] = true
	}
	return ignoredVars, nil
}

func filterStream(r io.Reader, out, errOut io.Writer, ignoredVars map[string]bool, includeNotCalled bool) bool {
	decoder := json.NewDecoder(r)
	unmatchedFound := false
	scanLevel := "symbol" // default; overridden by the config message

	for {
		// We decode into both our target struct and a raw map just so we can
		// print the complete raw text of the finding if it's unmatched.
		var raw map[string]any
		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			fmt.Fprintf(errOut, "Error decoding govulncheck JSON output: %v\n", err)
			break
		}

		// Re-marshal to our struct to easily check the fields
		rawBytes, _ := json.Marshal(raw)
		var msg GovulncheckMessage
		_ = json.Unmarshal(rawBytes, &msg)

		if msg.Config != nil {
			scanLevel = msg.Config.ScanLevel
		}

		if msg.Finding == nil {
			continue
		}

		vulnID := msg.Finding.OSV
		if vulnID == "" {
			continue
		}

		if ignoredVars[vulnID] {
			continue
		}

		// At symbol scan level, skip findings where the vulnerable symbol is
		// imported but never actually called.
		if scanLevel == "symbol" && !includeNotCalled && importedButNotCalled(msg.Finding) {
			continue
		}

		unmatchedFound = true
		indentedJSON, _ := json.MarshalIndent(raw, "", "  ")
		fmt.Fprintf(out, "Unmatched Vulnerability Found (%s):\n%s\n\n", vulnID, string(indentedJSON))
	}

	return unmatchedFound
}

func run(cliArgs []string, stdout, stderr io.Writer, newCmd func(string, ...string) *exec.Cmd) int {
	fs := flag.NewFlagSet("govulnchecker", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var configFile string
	var includeNotCalled bool
	fs.StringVar(&configFile, "config", ".govulnchecker.yaml", "Path to the YAML configuration file containing ignored vulnerabilities")
	fs.BoolVar(&includeNotCalled, "include-not-called", false, "Include vulnerabilities that are imported but never called (symbol scan level only)")
	if err := fs.Parse(cliArgs); err != nil {
		return 2
	}

	// 1. Read and parse the YAML configuration
	ignoredVars, err := loadIgnored(configFile)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	// 2. Prepare the govulncheck command
	// fs.Args() automatically contains everything passed after `--`
	// (or any non-flag arguments if `--` wasn't explicitly used).
	args := []string{"--format=json"}
	args = append(args, fs.Args()...)

	cmd := newCmd("govulncheck", args...)

	// govulncheck writes its JSON output to stdout.
	// We'll pass stderr straight through in case of fatal runtime errors.
	cmd.Stderr = stderr
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(stderr, "Error setting up stdout pipe: %v\n", err)
		return 1
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "Error starting govulncheck: %v\n", err)
		return 1
	}

	// 3. Process the JSON stream
	unmatchedFound := filterStream(stdoutPipe, stdout, stderr, ignoredVars, includeNotCalled)

	cmdErr := cmd.Wait()

	// 4. Determine exit status
	if unmatchedFound {
		fmt.Fprintln(stderr, "Error: Unmatched vulnerabilities were found.")
		return 1
	}

	// govulncheck exits with codes depending on what it found. If it errored
	// for a reason other than vulnerabilities (e.g. bad package), we should fail too.
	if cmdErr != nil && !unmatchedFound {
		// govulncheck returns exit code 3 if vulns are found in recent versions,
		// but since we caught no unignored ones, exit with 0 unless there was an execution failure.
		if exitErr, ok := cmdErr.(*exec.ExitError); ok && exitErr.ExitCode() == 3 {
			return 0
		}
		fmt.Fprintf(stderr, "govulncheck failed: %v\n", cmdErr)
		return 1
	}

	return 0
}
