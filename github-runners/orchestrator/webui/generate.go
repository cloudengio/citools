// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

// Package webui serves the orchestrator's management web UI and its
// OpenAPI-specified JSON API. The API types and server interface in api.gen.go
// are generated from openapi.yaml.
package webui

//go:generate go tool oapi-codegen -config oapi-codegen.yaml openapi.yaml

// The frontend's typed client and embedded bundle are regenerated separately
// (they require Node/npm); see README.md:
//   npm --prefix frontend run gen && npm --prefix frontend run build
