// Package main demonstrates explicit build metadata in a packaged Go binary.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
)

// Linker -X can replace string variables initialized with constant strings.
var version = "dev"
var revision = "unknown"

var (
	ErrInvalidVersion  = errors.New("version must contain 1 to 64 ASCII letters, digits, dots, underscores, or hyphens")
	ErrInvalidRevision = errors.New("revision must be unknown or 40 lowercase hexadecimal characters")
	ErrArguments       = errors.New("usage: build-info")
)

type BuildInfo struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	GoVersion string `json:"go_version"`
}

func newBuildInfo(buildVersion, buildRevision string) (BuildInfo, error) {
	if len(buildVersion) < 1 || len(buildVersion) > 64 {
		return BuildInfo{}, ErrInvalidVersion
	}
	for _, char := range buildVersion {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-') {
			return BuildInfo{}, ErrInvalidVersion
		}
	}
	if buildRevision != "unknown" {
		if len(buildRevision) != 40 {
			return BuildInfo{}, ErrInvalidRevision
		}
		for _, char := range buildRevision {
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
				return BuildInfo{}, ErrInvalidRevision
			}
		}
	}
	return BuildInfo{Version: buildVersion, Revision: buildRevision, GoVersion: runtime.Version()}, nil
}

func execute(args []string, output io.Writer) error {
	if len(args) != 0 {
		return ErrArguments
	}
	info, err := newBuildInfo(version, revision)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(info)
}

func main() {
	if err := execute(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
