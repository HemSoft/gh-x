package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const defaultManifest = ".github/release-targets.json"

type releaseTarget struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
}

func (target releaseTarget) id() string {
	return target.GOOS + "/" + target.GOARCH
}

func (target releaseTarget) asset() string {
	extension := ""
	if target.GOOS == "windows" {
		extension = ".exe"
	}
	return target.GOOS + "-" + target.GOARCH + extension
}

type buildConfig struct {
	version   string
	buildDate string
	outputDir string
}

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "list" && os.Args[1] != "build") {
		fatal("usage: release-targets <list|build>")
	}

	supported, err := loadSupportedTargets()
	if err != nil {
		fatal(err.Error())
	}
	targets, err := loadTargets(defaultManifest, supported)
	if err != nil {
		fatal(err.Error())
	}

	if os.Args[1] == "list" {
		for _, target := range targets {
			fmt.Fprintf(os.Stdout, "%s -> %s\n", target.id(), target.asset())
		}
		return
	}

	config, err := loadBuildConfig()
	if err != nil {
		fatal(err.Error())
	}
	if err := buildTargets(targets, config); err != nil {
		fatal(err.Error())
	}
}

func loadSupportedTargets() (map[string]struct{}, error) {
	output, err := exec.Command("go", "tool", "dist", "list").Output()
	if err != nil {
		return nil, fmt.Errorf("list Go toolchain targets: %w", err)
	}
	supported := make(map[string]struct{})
	for _, target := range strings.Fields(string(output)) {
		supported[target] = struct{}{}
	}
	if len(supported) == 0 {
		return nil, errors.New("go toolchain returned no supported targets")
	}
	return supported, nil
}

func loadTargets(path string, supported map[string]struct{}) ([]releaseTarget, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read release target manifest %s: %w", path, err)
	}
	return decodeTargets(contents, supported)
}

func decodeTargets(contents []byte, supported map[string]struct{}) ([]releaseTarget, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var targets []releaseTarget
	if err := decoder.Decode(&targets); err != nil {
		return nil, fmt.Errorf("parse release target manifest: %w", err)
	}
	if err := requireJSONEnd(decoder); err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, errors.New("release target manifest must not be empty")
	}

	seen := make(map[string]struct{}, len(targets))
	for index, target := range targets {
		if target.GOOS == "" || target.GOARCH == "" {
			return nil, fmt.Errorf("release target %d must define non-empty goos and goarch", index+1)
		}
		id := target.id()
		if _, ok := supported[id]; !ok {
			return nil, fmt.Errorf("release target %q is unsupported by the Go toolchain", id)
		}
		if _, ok := seen[id]; ok {
			return nil, fmt.Errorf("release target %q is duplicated", id)
		}
		seen[id] = struct{}{}
	}
	return targets, nil
}

func requireJSONEnd(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("parse release target manifest: %w", err)
	}
	return errors.New("parse release target manifest: unexpected trailing JSON value")
}

func loadBuildConfig() (buildConfig, error) {
	config := buildConfig{
		version:   os.Getenv("RELEASE_VERSION"),
		buildDate: os.Getenv("RELEASE_BUILD_DATE"),
		outputDir: os.Getenv("RELEASE_OUTPUT_DIR"),
	}
	for _, setting := range []struct {
		name  string
		value string
	}{
		{name: "RELEASE_VERSION", value: config.version},
		{name: "RELEASE_BUILD_DATE", value: config.buildDate},
		{name: "RELEASE_OUTPUT_DIR", value: config.outputDir},
	} {
		name, value := setting.name, setting.value
		if strings.TrimSpace(value) == "" {
			return buildConfig{}, fmt.Errorf("%s must not be empty", name)
		}
		if strings.ContainsAny(value, "\r\n") {
			return buildConfig{}, fmt.Errorf("%s must be a single line", name)
		}
	}
	return config, nil
}

func buildTargets(targets []releaseTarget, config buildConfig) error {
	if err := os.MkdirAll(config.outputDir, 0o755); err != nil {
		return fmt.Errorf("create release output directory: %w", err)
	}
	for _, target := range targets {
		output := filepath.Join(config.outputDir, target.asset())
		fmt.Fprintf(os.Stdout, "Building %s -> %s\n", target.id(), output)
		command := exec.Command("go", buildArguments(output, config)...)
		command.Env = append(os.Environ(), "GOOS="+target.GOOS, "GOARCH="+target.GOARCH, "CGO_ENABLED=0")
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			return fmt.Errorf("build release target %s as %s: %w", target.id(), target.asset(), err)
		}
	}
	return nil
}

func buildArguments(output string, config buildConfig) []string {
	ldflags := fmt.Sprintf("-s -w -X main.version=%s -X main.buildDate=%s", config.version, config.buildDate)
	return []string{"build", "-buildvcs=false", "-trimpath", "-ldflags", ldflags, "-o", output, "./src"}
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, "release-targets:", message)
	os.Exit(1)
}
