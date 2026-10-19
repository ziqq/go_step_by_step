package main

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBuildInfoValidatesLabelsAndRevision(t *testing.T) {
	for _, label := range []string{"dev", "lesson26", "v1.2.3-alpha.1", "build_123", strings.Repeat("a", 64)} {
		for _, revision := range []string{"unknown", strings.Repeat("a", 40)} {
			info, err := newBuildInfo(label, revision)
			if err != nil || info.Version != label || info.Revision != revision || info.GoVersion != runtime.Version() {
				t.Fatalf("newBuildInfo(%q, %q) = %#v, %v", label, revision, info, err)
			}
		}
	}
	for _, label := range []string{"", strings.Repeat("a", 65), "with spaces", "v1\n", "версия", "v1/secret"} {
		if _, err := newBuildInfo(label, "unknown"); !errors.Is(err, ErrInvalidVersion) {
			t.Fatalf("version %q error = %v, want ErrInvalidVersion", label, err)
		}
	}
	for _, revision := range []string{"", "main", "abc123", strings.Repeat("a", 41), strings.Repeat("A", 40), strings.Repeat("z", 40)} {
		if _, err := newBuildInfo("dev", revision); !errors.Is(err, ErrInvalidRevision) {
			t.Fatalf("revision %q error = %v, want ErrInvalidRevision", revision, err)
		}
	}
}

func TestExecuteReturnsOneJSONDocument(t *testing.T) {
	var output bytes.Buffer
	if err := execute(nil, &output); err != nil {
		t.Fatal(err)
	}
	info := decodeInfo(t, &output)
	if info.Version != version || info.Revision != revision || info.GoVersion != runtime.Version() {
		t.Fatalf("default build info = %#v", info)
	}
}

func TestExecuteRejectsArgumentsBeforeWriting(t *testing.T) {
	var output bytes.Buffer
	if err := execute([]string{"serve"}, &output); !errors.Is(err, ErrArguments) || output.Len() != 0 {
		t.Fatalf("execute(serve) error=%v output=%s", err, &output)
	}
}

func TestExecutePropagatesOutputFailure(t *testing.T) {
	problem := errors.New("synthetic writer failure")
	if err := execute(nil, failingWriter{err: problem}); !errors.Is(err, problem) {
		t.Fatalf("execute() error = %v, want writer error", err)
	}
}

func TestBuiltArtifactContainsLinkerMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "build-info")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	const buildVersion = "lesson26-test"
	buildRevision := strings.Repeat("a", 40)
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-buildvcs=false",
		"-ldflags=-X main.version="+buildVersion+" -X main.revision="+buildRevision,
		"-o", binary, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build artifact: %v\n%s", err, output)
	}

	run := exec.CommandContext(ctx, binary)
	// Runtime configuration cannot rewrite metadata already linked into a binary.
	run.Env = append(os.Environ(), "APP_VERSION=forged-runtime-label", "SOURCE_REVISION=forged-runtime-revision")
	output, err := run.Output()
	if err != nil {
		t.Fatalf("run linked artifact: %v", err)
	}
	info := decodeInfo(t, bytes.NewReader(output))
	if info.Version != buildVersion || info.Revision != buildRevision || info.GoVersion == "" {
		t.Fatalf("linked artifact reported %#v", info)
	}

	compiled, err := buildinfo.ReadFile(binary)
	if err != nil {
		t.Fatalf("read Go build settings: %v", err)
	}
	settings := make(map[string]string)
	for _, setting := range compiled.Settings {
		settings[setting.Key] = setting.Value
		if strings.HasPrefix(setting.Key, "vcs.") {
			t.Fatalf("unexpected automatic VCS metadata: %s", setting.Key)
		}
	}
	if settings["-trimpath"] != "true" || settings["CGO_ENABLED"] != "0" {
		t.Fatalf("artifact build controls = %#v", settings)
	}

	badRun := exec.CommandContext(ctx, binary, "serve")
	var badOutput bytes.Buffer
	badRun.Stdout = &badOutput
	if err := badRun.Run(); err == nil || badOutput.Len() != 0 {
		t.Fatalf("unsupported artifact arguments: error=%v stdout=%s", err, &badOutput)
	}
}

func decodeInfo(t *testing.T, reader io.Reader) BuildInfo {
	t.Helper()
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var info BuildInfo
	if err := decoder.Decode(&info); err != nil {
		t.Fatalf("decode build info: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("trailing output after build info: %v", err)
	}
	return info
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }
