package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type GitHub struct {
	Token   string
	Owner   string
	Repo    string
	Verbose bool
}

type Runner struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	OS     string `json:"os"`
	Status string `json:"status"`
	Busy   bool   `json:"busy"`
}

type RunnerList struct {
	TotalCount int      `json:"total_count"`
	Runners    []Runner `json:"runners"`
}

type RegistrationToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Job struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	RunnerName string `json:"runner_name"`
}

type JobList struct {
	TotalCount int   `json:"total_count"`
	Jobs       []Job `json:"jobs"`
}

func (g *GitHub) log(format string, args ...interface{}) {
	if g.Verbose {
		fmt.Printf("[GITHUB DEBUG] "+format+"\n", args...)
	}
}

func (g *GitHub) GetRunners(ctx context.Context) ([]Runner, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/runners", g.Owner, g.Repo)
	g.log("GET %s", url)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		g.log("Request failed: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		g.log("API error status: %s", resp.Status)
		return nil, fmt.Errorf("github api error: %s", resp.Status)
	}

	var list RunnerList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		g.log("Failed to decode response: %v", err)
		return nil, err
	}
	g.log("Found %d runners", len(list.Runners))
	return list.Runners, nil
}

func (g *GitHub) CreateRegistrationToken(ctx context.Context) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/runners/registration-token", g.Owner, g.Repo)
	g.log("POST %s", url)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		g.log("Request failed: %v", err)
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		g.log("API error status: %s", resp.Status)
		return "", fmt.Errorf("github api error: %s", resp.Status)
	}

	var token RegistrationToken
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		g.log("Failed to decode response: %v", err)
		return "", err
	}
	return token.Token, nil
}

type RunList struct {
	TotalCount int `json:"total_count"`
}

func (g *GitHub) GetQueuedRunCount(ctx context.Context) (int, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/runs?status=queued", g.Owner, g.Repo)
	g.log("GET %s", url)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		g.log("Request failed: %v", err)
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		g.log("API error status: %s", resp.Status)
		return 0, fmt.Errorf("github api error: %s", resp.Status)
	}

	var list RunList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		g.log("Failed to decode response: %v", err)
		return 0, err
	}
	g.log("Queued runs: %d", list.TotalCount)
	return list.TotalCount, nil
}

func (g *GitHub) GetRunnerJobConclusion(ctx context.Context, runnerName string) (string, error) {
	// This is slightly tricky as we need to find the job that ran on this specific runner.
	// We'll search for recent jobs in the repo.
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/jobs?per_page=50", g.Owner, g.Repo)
	g.log("GET %s (searching for runner %s)", url, runnerName)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		g.log("Request failed: %v", err)
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		g.log("API error status: %s", resp.Status)
		return "", fmt.Errorf("github api error: %s", resp.Status)
	}

	var list JobList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		g.log("Failed to decode response: %v", err)
		return "", err
	}

	for _, job := range list.Jobs {
		if job.RunnerName == runnerName {
			g.log("Found job for runner %s: status=%s, conclusion=%s", runnerName, job.Status, job.Conclusion)
			return job.Conclusion, nil
		}
	}

	g.log("Could not find job for runner %s in recent 50 jobs", runnerName)
	return "", fmt.Errorf("could not find job for runner %s", runnerName)
}
