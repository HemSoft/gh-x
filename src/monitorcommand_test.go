package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestRunMonitorCmdHelpAndUnknown(t *testing.T) {
	if err := runMonitorCmd([]string{"--help"}, io.Discard, io.Discard); err != nil {
		t.Fatalf("help should succeed: %v", err)
	}
	err := runMonitorCmd([]string{"--nope"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "unknown argument") {
		t.Fatalf("unknown arg should error: %v", err)
	}
}

func TestRunMonitorCmdRequiresTTY(t *testing.T) {
	saved := monitorTTYFunc
	defer func() { monitorTTYFunc = saved }()
	monitorTTYFunc = func() bool { return false }
	if err := runMonitorCmd(nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("expected TTY error: %v", err)
	}
}

func TestRunMonitorCmdBootstrapErrorSurfaces(t *testing.T) {
	savedTTY := monitorTTYFunc
	savedProgram := newMonitorProgramFunc
	defer func() {
		monitorTTYFunc = savedTTY
		newMonitorProgramFunc = savedProgram
	}()
	monitorTTYFunc = func() bool { return true }

	var ran bool
	newMonitorProgramFunc = func(model monitorModel) monitorProgram {
		return fakeMonitorProgram{onRun: func() (tea.Model, error) { ran = true; return model, nil }}
	}
	// bootstrap reads the real user config dir; point it at a temp HOME.
	isolateMonitorHome(t)

	if err := runMonitorCmd(nil, io.Discard, io.Discard); err != nil {
		t.Fatalf("happy path should run: %v", err)
	}
	if !ran {
		t.Fatal("program did not run")
	}
}

func TestMonitorSeedRepoPreservesEnterpriseHost(t *testing.T) {
	savedResolve := monitorResolveRepoFunc
	savedHost := monitorRepoHostFunc
	defer func() {
		monitorResolveRepoFunc = savedResolve
		monitorRepoHostFunc = savedHost
	}()
	monitorResolveRepoFunc = func(string) (string, string, error) {
		return "Acme", "Widgets", nil
	}
	monitorRepoHostFunc = func() string { return "GHE.Example.COM." }

	if got := monitorSeedRepo(); got != "ghe.example.com/Acme/Widgets" {
		t.Fatalf("monitorSeedRepo() = %q, want enterprise-qualified repo", got)
	}
}

func TestMonitorSeedRepoHonorsGHRepoHostOverCheckoutRemote(t *testing.T) {
	savedResolve := monitorResolveRepoFunc
	savedHost := monitorRepoHostFunc
	savedRemote := gitRemoteURLFunc
	defer func() {
		monitorResolveRepoFunc = savedResolve
		monitorRepoHostFunc = savedHost
		gitRemoteURLFunc = savedRemote
		resetRemoteCache()
	}()
	t.Setenv("GH_REPO", "ghe.example.com/Acme/Widgets")
	gitRemoteURLFunc = func() string { return "https://github.com/HemSoft/gh-x.git" }
	resetRemoteCache()
	monitorResolveRepoFunc = func(string) (string, string, error) {
		return "Acme", "Widgets", nil
	}
	monitorRepoHostFunc = func() string { return targetHost(nil) }

	if got := monitorSeedRepo(); got != "ghe.example.com/Acme/Widgets" {
		t.Fatalf("monitorSeedRepo() = %q, want GH_REPO host to win", got)
	}
}

func TestLegacyMonitorHostHonorsGHHostOverCheckoutRemote(t *testing.T) {
	savedHost := monitorRepoHostFunc
	defer func() { monitorRepoHostFunc = savedHost }()
	t.Setenv("GH_HOST", "GHE.Example.COM.")
	monitorRepoHostFunc = func() string { return defaultGitHubHost }

	if got := legacyMonitorHost(); got != "ghe.example.com" {
		t.Fatalf("legacyMonitorHost() = %q, want GH_HOST", got)
	}
}

func TestLegacyMonitorHostDefaultsToPublicDespiteRepositoryContext(t *testing.T) {
	savedHost := monitorRepoHostFunc
	defer func() { monitorRepoHostFunc = savedHost }()
	t.Setenv("GH_HOST", "")
	monitorRepoHostFunc = func() string { return "ghe.example.com" }

	if got := legacyMonitorHost(); got != defaultGitHubHost {
		t.Fatalf("legacyMonitorHost() = %q, want %s", got, defaultGitHubHost)
	}
}

func TestPrintMonitorQuerySeparatesHostsAndKeepsQualifiersHostless(t *testing.T) {
	isolateMonitorHome(t)
	configPath, err := monitorConfigPath()
	if err != nil {
		t.Fatalf("monitorConfigPath: %v", err)
	}
	cfg := defaultMonitorConfig("")
	cfg.Repos = []string{"HemSoft/gh-x", "ghe.example.com/Acme/Widgets"}
	if err := saveMonitorConfig(configPath, cfg); err != nil {
		t.Fatalf("saveMonitorConfig: %v", err)
	}

	savedResolve := monitorResolveRepoFunc
	savedHost := monitorRepoHostFunc
	defer func() {
		monitorResolveRepoFunc = savedResolve
		monitorRepoHostFunc = savedHost
	}()
	monitorResolveRepoFunc = func(string) (string, string, error) {
		return "ignored", "seed", nil
	}
	monitorRepoHostFunc = func() string { return defaultGitHubHost }

	var stdout bytes.Buffer
	if err := printMonitorQuery(&stdout); err != nil {
		t.Fatalf("printMonitorQuery: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"# github.com\n",
		"# ghe.example.com\n",
		"query Monitor1 {",
		"query Monitor2 {",
		"repo:HemSoft/gh-x",
		"repo:Acme/Widgets",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("printed queries missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "repo:ghe.example.com/Acme/Widgets") {
		t.Fatalf("enterprise hostname leaked into repo qualifier:\n%s", output)
	}
}

func isolateMonitorHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AppData", dir)         // Windows user config dir
	t.Setenv("XDG_CONFIG_HOME", dir) // Unix user config dir
}

type fakeMonitorProgram struct {
	onRun func() (tea.Model, error)
}

func (f fakeMonitorProgram) Run() (tea.Model, error) { return f.onRun() }

func TestExecuteMonitorFetchSuccessAndFailure(t *testing.T) {
	saved := monitorGHExecFunc
	defer func() { monitorGHExecFunc = saved }()

	cfg := defaultMonitorConfig("owner/one")
	now := time.Date(2026, 8, 22, 8, 0, 0, 0, time.UTC)

	payload := `{"data":{"rateLimit":{"remaining":99,"resetAt":"2026-08-22T09:00:00Z"},
		"pr0":{"issueCount":1,"nodes":[{"number":3,"title":"t","state":"OPEN",
		"updatedAt":"2026-08-22T07:00:00Z","repository":{"nameWithOwner":"owner/one"}}]},
		"is0":{"issueCount":0,"nodes":[]}}}`
	monitorGHExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		var out bytes.Buffer
		out.WriteString(payload)
		return out, bytes.Buffer{}, nil
	}
	result, err := executeMonitorFetch(cfg, now)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if result.RateRemaining != 99 || len(result.PRSections[0].Rows) != 1 {
		t.Fatalf("unexpected payload: %+v", result)
	}

	monitorGHExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		return bytes.Buffer{}, bytes.Buffer{}, errBoom()
	}
	if _, err := executeMonitorFetch(cfg, now); err == nil {
		t.Fatal("gh failure must surface")
	}
}

func errBoom() error { return &monitorTestError{"api down"} }

type monitorTestError struct{ msg string }

func (e *monitorTestError) Error() string { return e.msg }

func TestUpdateDispatchSmoke(t *testing.T) {
	m := modelWithData()
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if updated := resized.(monitorModel); !updated.ready || updated.layout.Width != 100 {
		t.Fatal("resize not applied")
	}
	m2 := sizedModel()
	model, cmd := m2.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if cmd == nil {
		t.Fatal("'r' via Update should start refresh")
	}
	if !(model.(monitorModel)).refreshing {
		t.Fatal("refreshing flag missing")
	}
	m3 := sizedModel()
	type unknownMonitorMsg struct{}
	out, _ := m3.Update(unknownMonitorMsg{})
	if out == nil {
		t.Fatal("unknown messages still return a model")
	}
}

func TestHandleResizeClampsCursor(t *testing.T) {
	m := modelWithData()
	m.subTab = 0 // first PR section holds the seeded rows
	m.cursor = 99
	model, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if updated := model.(monitorModel); updated.cursor >= len(updated.visibleRows()) {
		t.Fatalf("cursor not clamped on resize: %d", updated.cursor)
	}
}

func TestSelectSubTabByClickBounds(t *testing.T) {
	m := sizedModel()
	m.subTab = 0
	model, _ := m.selectSubTabByClick(9)
	if updated := model.(monitorModel); updated.subTab != 0 {
		t.Fatalf("out-of-range subtab click ignored: %d", updated.subTab)
	}
	model, _ = m.selectSubTabByClick(1)
	if updated := model.(monitorModel); updated.subTab != 1 {
		t.Fatalf("valid subtab click failed: %d", updated.subTab)
	}
}

func TestScrollDetailKeys(t *testing.T) {
	m := modelWithData()
	m.subTab = 0 // first PR section holds the seeded rows
	row := m.visibleRows()[0]
	row.Body = strings.Repeat("line\n", 60)
	m.data.PRSections[0].Rows[0] = row
	m.focus = monitorFocusDetail

	model, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if updated := model.(monitorModel); updated.detailScroll == 0 {
		t.Fatal("pgdn should scroll detail")
	}
	model, _ = model.(monitorModel).handleKey(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if updated := model.(monitorModel); updated.detailScroll != 0 {
		t.Fatalf("pgup should clamp at top: %d", updated.detailScroll)
	}
}

func TestKeyInScopeScoping(t *testing.T) {
	m := sizedModel()
	m.repoIdx = 1 // owner/one selected
	if !m.keyInScope("owner/one#pr#1") || m.keyInScope("owner/two#pr#2") {
		t.Fatal("repo scoping broken")
	}
	m.repoIdx = 0
	if !m.keyInScope("owner/two#pr#2") {
		t.Fatal("all-repos scope should include everything")
	}
	m.repoIdx = 99
	if m.keyInScope("owner/two#pr#2") {
		t.Fatal("invalid index scope should exclude")
	}
}

func TestRenderSettingsScreenContent(t *testing.T) {
	m := sizedModel()
	m.settings.open(m.cfg)
	screen := m.renderSettingsScreen()
	for _, want := range []string{"Settings", "Repos", "interval"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("settings screen missing %q: %s", want, screen)
		}
	}
	m.settings.errText = "bad input"
	if !strings.Contains(m.renderSettingsScreen(), "bad input") {
		t.Fatal("error text missing from settings screen")
	}
}

func TestCenteringHelpers(t *testing.T) {
	block := blockCentered("line1\nline2", 40)
	if !strings.HasPrefix(block, " ") {
		t.Fatal("block not indented")
	}
	out := centeredDim("nothing here", 50, 7)
	lines := strings.Split(out, "\n")
	if len(lines) != 7 || !strings.Contains(lines[3], "nothing here") {
		t.Fatalf("centered empty-state wrong: %q", out)
	}
}

func TestIssueColumnShape(t *testing.T) {
	cols := monitorIssueColumns()
	labels := make([]string, len(cols))
	for i, col := range cols {
		labels[i] = col.Title
	}
	want := "#,Title,Repo,Author,State,Labels,Assignees,Upd"
	if strings.Join(labels, ",") != want {
		t.Fatalf("issue columns changed: %s", labels)
	}
}

func TestViewSetsAltScreenAndMouse(t *testing.T) {
	m := modelWithData()
	view := m.View()
	if !view.AltScreen {
		t.Fatal("alt screen not enabled")
	}
	if view.MouseMode != tea.MouseModeCellMotion {
		t.Fatal("mouse mode not enabled")
	}
}

func TestInitSchedulesInitialFetch(t *testing.T) {
	m := newTestMonitorModel()
	if m.Init() == nil {
		t.Fatal("Init must fetch")
	}
}
