// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"cloudeng.io/webapi/operations"
)

type endpointsFlags struct {
	Output string `subcmd:"output,,output file for endpoints data (defaults to stdout)"`
}

type endpoints struct{}

func (endpoints) getLastKnownGoodVersions(ctx context.Context) (*Versions, error) {
	const lastKnownGoodVersionsEndpoint = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"
	ep := operations.NewEndpoint[*Versions]()
	versions, _, _, err := ep.Get(ctx, lastKnownGoodVersionsEndpoint)
	if err != nil {
		return nil, err
	}
	return versions, nil
}

type endpointsCmd struct {
}

func (e endpointsCmd) Get(ctx context.Context, f any, args []string) error {
	fv := f.(*endpointsFlags)
	ep := endpoints{}
	versions, err := ep.getLastKnownGoodVersions(ctx)
	if err != nil {
		return err
	}
	var outputData []byte
	outputData, err = json.MarshalIndent(versions, "", "  ")
	if err != nil {
		return err
	}
	if fv.Output == "" {
		fmt.Println(string(outputData))
	} else {
		err = os.WriteFile(fv.Output, outputData, 0600)
		if err != nil {
			return err
		}
	}
	return nil
}
