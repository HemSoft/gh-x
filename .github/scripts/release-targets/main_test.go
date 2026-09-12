package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

var repositoryTargets = []string{
	"darwin/amd64=darwin-amd64",
	"darwin/arm64=darwin-arm64",
	"freebsd/386=freebsd-386",
	"freebsd/amd64=freebsd-amd64",
	"freebsd/arm64=freebsd-arm64",
	"linux/386=linux-386",
	"linux/amd64=linux-amd64",
	"linux/arm=linux-arm",
	"linux/arm64=linux-arm64",
	"windows/386=windows-386.exe",
	"windows/amd64=windows-amd64.exe",
	"windows/arm64=windows-arm64.exe",
}

func TestRepositoryManifestDefinesExpectedTargets(t *testing.T) {
	supported, err := loadSupportedTargets()
	if err != nil {
		t.Fatal(err)
	}

	targets, err := loadTargets("../../release-targets.json", supported)
	if err != nil {
		t.Fatal(err)
	}
	actual := make([]string, 0, len(targets))
	for _, target := range targets {
		actual = append(actual, target.id()+"="+target.asset())
	}
	if !reflect.DeepEqual(actual, repositoryTargets) {
		t.Fatalf("release targets = %v, want %v", actual, repositoryTargets)
	}
}

func TestDecodeTargetsRejectsInvalidManifest(t *testing.T) {
	supported := map[string]struct{}{"linux/amd64": {}}
	tests := []struct {
		name     string
		manifest string
		want     string
	}{
		{name: "empty", manifest: `[]`, want: "must not be empty"},
		{name: "missing operating system", manifest: `[{"goarch":"amd64"}]`, want: "must define non-empty"},
		{name: "missing architecture", manifest: `[{"goos":"linux"}]`, want: "must define non-empty"},
		{name: "unknown field", manifest: `[{"goos":"linux","goarch":"amd64","asset":"bad"}]`, want: "unknown field"},
		{name: "unsupported", manifest: `[{"goos":"plan9","goarch":"mips"}]`, want: "unsupported by the Go toolchain"},
		{name: "duplicate", manifest: `[{"goos":"linux","goarch":"amd64"},{"goos":"linux","goarch":"amd64"}]`, want: "duplicated"},
		{name: "trailing value", manifest: `[{"goos":"linux","goarch":"amd64"}] {}`, want: "unexpected trailing JSON value"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeTargets([]byte(test.manifest), supported)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("decodeTargets() error = %v, want text %q", err, test.want)
			}
		})
	}
}

func TestBuildArgumentsBindDeterministicMetadata(t *testing.T) {
	config := buildConfig{version: "v1.2.3", buildDate: "2026-09-12", outputDir: "unused"}
	want := []string{
		"build", "-buildvcs=false", "-trimpath", "-ldflags",
		"-s -w -X main.version=v1.2.3 -X main.buildDate=2026-09-12",
		"-o", "dist/linux-amd64", "./src",
	}
	if got := buildArguments("dist/linux-amd64", config); !reflect.DeepEqual(got, want) {
		t.Fatalf("buildArguments() = %q, want %q", got, want)
	}
}

func TestBuildTargetsEmbedsConfiguredMetadata(t *testing.T) {
	originalDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("../../.."); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	config := buildConfig{
		version:   "v99.88.77-release-target-test",
		buildDate: "2099-12-31",
		outputDir: t.TempDir(),
	}
	target := releaseTarget{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	if err := buildTargets([]releaseTarget{target}, config); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(filepath.Join(config.outputDir, target.asset()))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{config.version, config.buildDate} {
		if !bytes.Contains(binary, []byte(value)) {
			t.Errorf("built binary does not contain configured value %q", value)
		}
	}
}

func TestLoadBuildConfig(t *testing.T) {
	t.Setenv("RELEASE_VERSION", "v1.2.3")
	t.Setenv("RELEASE_BUILD_DATE", "2026-09-12")
	t.Setenv("RELEASE_OUTPUT_DIR", t.TempDir())

	got, err := loadBuildConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.version != "v1.2.3" || got.buildDate != "2026-09-12" || got.outputDir == "" {
		t.Fatalf("loadBuildConfig() = %+v", got)
	}
}

func TestLoadBuildConfigRejectsInvalidValues(t *testing.T) {
	for _, name := range []string{"RELEASE_VERSION", "RELEASE_BUILD_DATE", "RELEASE_OUTPUT_DIR"} {
		t.Run(name+" empty", func(t *testing.T) {
			setValidBuildEnvironment(t)
			t.Setenv(name, "")
			_, err := loadBuildConfig()
			if err == nil || !strings.Contains(err.Error(), name+" must not be empty") {
				t.Fatalf("loadBuildConfig() error = %v", err)
			}
		})
		t.Run(name+" multiline", func(t *testing.T) {
			setValidBuildEnvironment(t)
			t.Setenv(name, "bad\nvalue")
			_, err := loadBuildConfig()
			if err == nil || !strings.Contains(err.Error(), name+" must be a single line") {
				t.Fatalf("loadBuildConfig() error = %v", err)
			}
		})
	}
}

func setValidBuildEnvironment(t *testing.T) {
	t.Helper()
	for name, value := range map[string]string{
		"RELEASE_VERSION":    "test",
		"RELEASE_BUILD_DATE": "2026-09-12",
		"RELEASE_OUTPUT_DIR": os.TempDir(),
	} {
		t.Setenv(name, value)
	}
}
