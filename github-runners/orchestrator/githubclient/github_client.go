// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubclient

import (
	"context"
	"fmt"
	"sync"
	"time"

	"cloudeng.io/webapi/clients/github"
	"cloudeng.io/webapi/operations"
	gogithub "github.com/google/go-github/v89/github"
)

// Repo provides a client for GitHub repository operations. There will be a
// separate client for each repository that is being managed.
type Repo struct {
	owner, repo string
	opts        []operations.Option
	mu          sync.Mutex
	token       *gogithub.RegistrationToken
}

func New(owner, repo string, opts ...operations.Option) *Repo {
	return &Repo{
		owner: owner,
		repo:  repo,
		opts:  opts,
	}
}

func (c *Repo) existingToken() *gogithub.RegistrationToken {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == nil || c.token.GetToken() == "" {
		return nil
	}
	if c.token.GetExpiresAt().Before(time.Now()) {
		c.token = nil
		return nil
	}
	return c.token
}

func (c *Repo) GetRegistrationToken(ctx context.Context) (*gogithub.RegistrationToken, error) {
	if token := c.existingToken(); token != nil {
		return token, nil
	}
	token, err := github.CreateRegistrationToken(ctx, c.owner, c.repo, c.opts...)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = &token
	return c.token, nil
}

type RepoClients struct {
	repos map[string]*Repo
	mu    sync.Mutex
}

func NewRepoClients() *RepoClients {
	return &RepoClients{
		repos: make(map[string]*Repo),
	}
}

func (rc *RepoClients) AddClient(owner, repo string, opts ...operations.Option) *Repo {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	key := owner + "/" + repo
	if existing, ok := rc.repos[key]; ok {
		return existing
	}
	newRepo := New(owner, repo, opts...)
	rc.repos[key] = newRepo
	return newRepo
}

func (rc *RepoClients) GetClient(owner, repo string) (*Repo, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	key := owner + "/" + repo
	existing, ok := rc.repos[key]
	return existing, ok
}

func (rc *RepoClients) GetClientFullName(fullName string) (*Repo, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	existing, ok := rc.repos[fullName]
	return existing, ok
}

func (rc *RepoClients) GetToken(ctx context.Context, owner, repo string) (*gogithub.RegistrationToken, error) {
	r, ok := rc.GetClient(owner, repo)
	if !ok {
		return nil, fmt.Errorf("no client for repository %s/%s", owner, repo)
	}
	return r.GetRegistrationToken(ctx)
}

func (rc *RepoClients) GetTokenFullName(ctx context.Context, fullName string) (*gogithub.RegistrationToken, error) {
	r, ok := rc.GetClientFullName(fullName)
	if !ok {
		return nil, fmt.Errorf("no client for repository %s", fullName)
	}
	return r.GetRegistrationToken(ctx)
}
