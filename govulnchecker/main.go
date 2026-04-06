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

// GovulncheckMessage represents a single JSON object emitted by govulncheck -json.
type GovulncheckMessage struct {
	// Older versions of govulncheck emit OSV
	OSV *struct {
		ID string `json:"id"`
	} `json:"osv,omitempty"`
	// Newer versions emit Finding which references the OSV ID
	Finding *struct {
		OSV string `json:"osv"`
	} `json:"finding,omitempty"`
}

func main() {
	var configFile string
	flag.StringVar(&configFile, "config", ".govulnchecker.yaml", "Path to the YAML configuration file containing ignored vulnerabilities")
	flag.Parse()

	// 1. Read and parse the YAML configuration
	b, err := os.ReadFile(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading config file: %v\n", err)
		os.Exit(1)
	}

	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing config file: %v\n", err)
		os.Exit(1)
	}

	ignoredVars := make(map[string]bool)
	for _, item := range cfg.Ignore {
		ignoredVars[strings.TrimSpace(item.ID)] = true
	}

	// 2. Prepare the govulncheck command
	// flag.Args() automatically contains everything passed after `--`
	// (or any non-flag arguments if `--` wasn't explicitly used).
	args := []string{"--format=json"}
	args = append(args, flag.Args()...)

	cmd := exec.Command("govulncheck", args...)

	// govulncheck writes its JSON output to stdout.
	// We'll pass stderr straight through in case of fatal runtime errors.
	cmd.Stderr = os.Stderr
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error setting up stdout pipe: %v\n", err)
		os.Exit(1)
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting govulncheck: %v\n", err)
		os.Exit(1)
	}

	// 3. Process the JSON stream
	decoder := json.NewDecoder(stdoutPipe)
	unmatchedFound := false

	for {
		// We decode into both our target struct and a raw map just so we can
		// print the complete raw text of the finding if it's unmatched.
		var raw map[string]interface{}
		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			fmt.Fprintf(os.Stderr, "Error decoding govulncheck JSON output: %v\n", err)
			continue
		}

		// Re-marshal to our struct to easily check the fields
		rawBytes, _ := json.Marshal(raw)
		var msg GovulncheckMessage
		json.Unmarshal(rawBytes, &msg)

		var vulnID string
		if msg.Finding != nil {
			vulnID = msg.Finding.OSV
		} //else if msg.OSV != nil {
		//vulnID = msg.OSV.ID
		//}

		// If this is a vulnerability finding, check if we should filter it
		if vulnID != "" {
			if ignoredVars[vulnID] {
				continue // Filtered out
			}
			unmatchedFound = true

			// Print the unmatched vulnerability
			indentedJSON, _ := json.MarshalIndent(raw, "", "  ")
			fmt.Printf("Unmatched Vulnerability Found (%s):\n%s\n\n", vulnID, string(indentedJSON))
		}
	}

	cmdErr := cmd.Wait()

	// 4. Determine exit status
	if unmatchedFound {
		fmt.Fprintln(os.Stderr, "Error: Unmatched vulnerabilities were found.")
		os.Exit(1)
	}

	// govulncheck exits with codes depending on what it found. If it errored
	// for a reason other than vulnerabilities (e.g. bad package), we should fail too.
	if cmdErr != nil && !unmatchedFound {
		// govulncheck returns exit code 3 if vulns are found in recent versions,
		// but since we caught no unignored ones, exit with 0 unless there was an execution failure.
		if exitErr, ok := cmdErr.(*exec.ExitError); ok && exitErr.ExitCode() == 3 {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "govulncheck failed: %v\n", cmdErr)
		os.Exit(1)
	}
}
