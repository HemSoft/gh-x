package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	monitorConfigDirName  = "gh-x"
	monitorConfigFileName = "config.yml"
	monitorStateFileName  = "state.json"

	defaultMonitorLimit    = 30
	maximumMonitorFetch    = 100
	minimumMonitorInterval = 10 * time.Second
	maximumMonitorInterval = 6 * time.Hour
	defaultMonitorInterval = 10 * time.Minute
	monitorConfigVersion   = 1

	monitorRepoAll = "All repos"
)

// monitorSection is a named GitHub search filter shown as a sub-tab.
type monitorSection struct {
	Title   string `yaml:"title"`
	Filters string `yaml:"filters"`
	Limit   int    `yaml:"limit,omitempty"`
}

type monitorDefaults struct {
	Limit    int    `yaml:"limit"`
	Interval string `yaml:"interval"`
}

// monitorConfig is the on-disk configuration for gh x monitor.
type monitorConfig struct {
	Version       int              `yaml:"version,omitempty"`
	Repos         []string         `yaml:"repos"`
	Defaults      monitorDefaults  `yaml:"defaults"`
	PRSections    []monitorSection `yaml:"prSections"`
	IssueSections []monitorSection `yaml:"issueSections"`
}

// monitorRepository is the normalized identity of one configured repository.
// Plain OWNER/REPO entries belong to github.com; Enterprise entries retain
// their explicit host so requests and UI row keys cannot cross hosts.
type monitorRepository struct {
	Host  string
	Owner string
	Name  string
}

func parseMonitorRepository(value string) (monitorRepository, error) {
	trimmed := strings.Trim(strings.TrimSpace(value), "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 || len(parts) > 3 {
		return monitorRepository{}, fmt.Errorf("repo %q must be OWNER/REPO (or HOST/OWNER/REPO)", value)
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if parts[i] == "" {
			return monitorRepository{}, fmt.Errorf("repo %q has an empty segment", value)
		}
	}

	host := defaultGitHubHost
	if len(parts) == 3 {
		host = normalizeRemoteHost(parts[0])
		if host == "" {
			return monitorRepository{}, fmt.Errorf("repo %q has an empty host", value)
		}
	}
	return monitorRepository{
		Host:  host,
		Owner: parts[len(parts)-2],
		Name:  parts[len(parts)-1],
	}, nil
}

func parseMonitorRepositories(values []string) ([]monitorRepository, error) {
	repositories := make([]monitorRepository, 0, len(values))
	for _, value := range values {
		repository, err := parseMonitorRepository(value)
		if err != nil {
			return nil, err
		}
		repositories = append(repositories, repository)
	}
	return repositories, nil
}

func (r monitorRepository) nameWithOwner() string {
	return r.Owner + "/" + r.Name
}

func (r monitorRepository) configValue() string {
	if r.Host == defaultGitHubHost {
		return r.nameWithOwner()
	}
	return r.Host + "/" + r.nameWithOwner()
}

func normalizeMonitorRepoValues(values []string) []string {
	normalized := append([]string(nil), values...)
	for i, value := range normalized {
		if repository, err := parseMonitorRepository(value); err == nil {
			normalized[i] = repository.configValue()
		}
	}
	return normalized
}

// monitorConfigPath returns the platform-appropriate config file location:
// %AppData%\gh-x\config.yml on Windows, ~/.config/gh-x/config.yml elsewhere.
func monitorConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve config directory: %w", err)
	}
	return filepath.Join(dir, monitorConfigDirName, monitorConfigFileName), nil
}

func monitorStatePath() (string, error) {
	configPath, err := monitorConfigPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(configPath), monitorStateFileName), nil
}

// defaultMonitorSections returns the starter sections used when no config
// exists. "All open" is always the last section of each tab.
func defaultMonitorSections() (prs, issues []monitorSection) {
	prs = []monitorSection{
		{Title: "Mine", Filters: "is:open author:@me"},
		{Title: "Review requests", Filters: "is:open review-requested:@me"},
		{Title: "All open", Filters: "is:open"},
	}
	issues = []monitorSection{
		{Title: "Assigned to me", Filters: "is:open assignee:@me"},
		{Title: "All open", Filters: "is:open"},
	}
	return prs, issues
}

func defaultMonitorConfig(seedRepo string) *monitorConfig {
	prs, issues := defaultMonitorSections()
	repos := make([]string, 0, 1)
	if seedRepo != "" {
		repos = append(repos, seedRepo)
	}
	return &monitorConfig{
		Version:       monitorConfigVersion,
		Repos:         repos,
		Defaults:      monitorDefaults{Limit: defaultMonitorLimit, Interval: formatMonitorInterval(defaultMonitorInterval)},
		PRSections:    prs,
		IssueSections: issues,
	}
}

// starterMonitorConfigYAML renders a commented starter config. Comments are
// hand-maintained here because yaml.Marshal would drop them.
func starterMonitorConfigYAML(cfg *monitorConfig) string {
	var sb strings.Builder
	sb.WriteString("# gh x monitor configuration.\n")
	sb.WriteString("# Sections use GitHub search syntax; configured repos are added\n")
	sb.WriteString("# automatically, so do not include repo: qualifiers in filters.\n\n")
	fmt.Fprintf(&sb, "version: %d\n\n", monitorConfigVersion)
	sb.WriteString("repos:\n")
	for _, repo := range cfg.Repos {
		fmt.Fprintf(&sb, "  - %s\n", repo)
	}
	if len(cfg.Repos) == 0 {
		sb.WriteString("  [] # add [host/]owner/repo entries here\n")
	}
	fmt.Fprintf(&sb, "\ndefaults:\n")
	fmt.Fprintf(&sb, "  limit: %d      # rows fetched per section (max 100)\n", cfg.Defaults.Limit)
	fmt.Fprintf(&sb, "  interval: %s   # auto-refresh cadence\n\n", cfg.Defaults.Interval)
	sb.WriteString("prSections:\n")
	for _, section := range cfg.PRSections {
		writeStarterSection(&sb, section)
	}
	sb.WriteString("\nissueSections:\n")
	for _, section := range cfg.IssueSections {
		writeStarterSection(&sb, section)
	}
	return sb.String()
}

func writeStarterSection(sb *strings.Builder, section monitorSection) {
	if section.Limit > 0 {
		fmt.Fprintf(sb, "  - title: %q\n    filters: %q\n    limit: %d\n", section.Title, section.Filters, section.Limit)
		return
	}
	fmt.Fprintf(sb, "  - title: %q\n    filters: %q\n", section.Title, section.Filters)
}

// loadOrCreateMonitorConfig reads the config at path, creating a commented
// starter file seeded with seedRepo when it does not exist yet. The second
// return reports whether a new file was created.
func loadOrCreateMonitorConfig(path, seedRepo, legacyHost string) (*monitorConfig, bool, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		cfg := defaultMonitorConfig(seedRepo)
		if writeErr := writeStarterMonitorConfig(path, cfg); writeErr != nil {
			return nil, false, writeErr
		}
		return cfg, true, nil
	default:
		return nil, false, fmt.Errorf("read config: %w", err)
	}

	var cfg monitorConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, false, fmt.Errorf("parse %s: %w", path, err)
	}
	legacyConfig := cfg.Version == 0
	if legacyConfig {
		cfg.Repos = qualifyLegacyMonitorRepos(cfg.Repos, legacyHost)
	}
	normalizeMonitorConfig(&cfg)
	if err := validateMonitorConfig(&cfg); err != nil {
		return nil, false, err
	}
	if legacyConfig {
		migrated, err := migrateLegacyMonitorConfigYAML(data, cfg.Repos)
		if err != nil {
			return nil, false, fmt.Errorf("migrate legacy config: %w", err)
		}
		if err := writeMonitorConfigData(path, migrated); err != nil {
			return nil, false, fmt.Errorf("persist migrated config: %w", err)
		}
	}
	return &cfg, false, nil
}

// qualifyLegacyMonitorRepos preserves pre-host-routing configs. Before host
// prefixes were supported, every OWNER/REPO entry inherited one endpoint. If
// the current endpoint is Enterprise and the config contains no explicit host,
// retain that behavior by qualifying every repository during load.
func qualifyLegacyMonitorRepos(values []string, legacyHost string) []string {
	host := normalizeRemoteHost(legacyHost)
	if host == "" || host == defaultGitHubHost {
		return values
	}
	for _, value := range values {
		if len(strings.Split(strings.Trim(strings.TrimSpace(value), "/"), "/")) == 3 {
			return values
		}
	}
	qualified := append([]string(nil), values...)
	for i, value := range qualified {
		if len(strings.Split(strings.Trim(strings.TrimSpace(value), "/"), "/")) == 2 {
			qualified[i] = host + "/" + strings.Trim(strings.TrimSpace(value), "/")
		}
	}
	return qualified
}

// writeStarterMonitorConfig writes the hand-commented starter template.
func writeStarterMonitorConfig(path string, cfg *monitorConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(starterMonitorConfigYAML(cfg)), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// saveMonitorConfig writes the config atomically-ish: temp file then rename.
func saveMonitorConfig(path string, cfg *monitorConfig) error {
	if cfg.Version == 0 {
		cfg.Version = monitorConfigVersion
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return writeMonitorConfigData(path, data)
}

func writeMonitorConfigData(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

type monitorYAMLEdit struct {
	start       int
	end         int
	replacement string
}

func migrateLegacyMonitorConfigYAML(data []byte, repos []string) ([]byte, error) {
	root, err := parseMonitorYAMLRoot(data)
	if err != nil {
		return nil, err
	}
	lineStarts := yamlLineStarts(data)
	repoEdits, err := monitorRepoYAMLEdits(data, lineStarts, findYAMLMappingValue(root, "repos"), repos)
	if err != nil {
		return nil, err
	}
	versionEdit, err := monitorVersionYAMLEdit(data, lineStarts, root)
	if err != nil {
		return nil, err
	}
	return applyMonitorYAMLEdits(data, append(repoEdits, versionEdit)), nil
}

func parseMonitorYAMLRoot(data []byte) (*yaml.Node, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("config root must be a mapping")
	}
	return document.Content[0], nil
}

func findYAMLMappingValue(root *yaml.Node, name string) *yaml.Node {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == name {
			return root.Content[i+1]
		}
	}
	return nil
}

func monitorRepoYAMLEdits(data []byte, lineStarts []int, repoSequence *yaml.Node, repos []string) ([]monitorYAMLEdit, error) {
	if repoSequence == nil || repoSequence.Kind != yaml.SequenceNode {
		return nil, errors.New("config repos must be a sequence")
	}
	if len(repoSequence.Content) != len(repos) {
		return nil, fmt.Errorf("config repos changed during migration: parsed %d, expected %d", len(repoSequence.Content), len(repos))
	}

	edits := make([]monitorYAMLEdit, 0, len(repos))
	for i, repoNode := range repoSequence.Content {
		if repoNode.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("config repo %d must be a scalar", i+1)
		}
		start, err := yamlNodeOffset(lineStarts, repoNode)
		if err != nil {
			return nil, err
		}
		end, err := yamlScalarEnd(data, start, repoNode.Style)
		if err != nil {
			return nil, err
		}
		edits = append(edits, monitorYAMLEdit{
			start:       start,
			end:         end,
			replacement: renderYAMLScalar(repos[i], repoNode.Style),
		})
	}
	return edits, nil
}

func monitorVersionYAMLEdit(data []byte, lineStarts []int, root *yaml.Node) (monitorYAMLEdit, error) {
	if versionNode := findYAMLMappingValue(root, "version"); versionNode != nil {
		start, err := yamlNodeOffset(lineStarts, versionNode)
		if err != nil {
			return monitorYAMLEdit{}, err
		}
		end, err := yamlScalarEnd(data, start, versionNode.Style)
		return monitorYAMLEdit{start: start, end: end, replacement: strconv.Itoa(monitorConfigVersion)}, err
	}
	if len(root.Content) == 0 {
		return monitorYAMLEdit{}, errors.New("config mapping is empty")
	}
	start, err := yamlNodeOffset(lineStarts, root.Content[0])
	if err != nil {
		return monitorYAMLEdit{}, err
	}
	return monitorYAMLEdit{
		start:       start,
		end:         start,
		replacement: fmt.Sprintf("version: %d%s", monitorConfigVersion, yamlNewline(data)),
	}, nil
}

func applyMonitorYAMLEdits(data []byte, edits []monitorYAMLEdit) []byte {
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	result := append([]byte(nil), data...)
	for _, edit := range edits {
		updated := make([]byte, 0, len(result)-(edit.end-edit.start)+len(edit.replacement))
		updated = append(updated, result[:edit.start]...)
		updated = append(updated, edit.replacement...)
		updated = append(updated, result[edit.end:]...)
		result = updated
	}
	return result
}

func yamlLineStarts(data []byte) []int {
	starts := []int{0}
	for i, value := range data {
		if value == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func yamlNodeOffset(lineStarts []int, node *yaml.Node) (int, error) {
	if node.Line <= 0 || node.Line > len(lineStarts) || node.Column <= 0 {
		return 0, fmt.Errorf("invalid YAML position %d:%d", node.Line, node.Column)
	}
	return lineStarts[node.Line-1] + node.Column - 1, nil
}

func yamlScalarEnd(data []byte, start int, style yaml.Style) (int, error) {
	if start < 0 || start >= len(data) {
		return 0, errors.New("YAML scalar starts outside document")
	}
	switch style {
	case yaml.SingleQuotedStyle:
		return singleQuotedYAMLScalarEnd(data, start)
	case yaml.DoubleQuotedStyle:
		return doubleQuotedYAMLScalarEnd(data, start)
	default:
		return plainYAMLScalarEnd(data, start), nil
	}
}

func singleQuotedYAMLScalarEnd(data []byte, start int) (int, error) {
	for i := start + 1; i < len(data); i++ {
		if data[i] != '\'' {
			continue
		}
		if i+1 < len(data) && data[i+1] == '\'' {
			i++
			continue
		}
		return i + 1, nil
	}
	return 0, errors.New("unterminated YAML scalar")
}

func doubleQuotedYAMLScalarEnd(data []byte, start int) (int, error) {
	escaped := false
	for i := start + 1; i < len(data); i++ {
		if data[i] == '"' && !escaped {
			return i + 1, nil
		}
		escaped = data[i] == '\\' && !escaped
	}
	return 0, errors.New("unterminated YAML scalar")
}

func plainYAMLScalarEnd(data []byte, start int) int {
	for i := start; i < len(data); i++ {
		switch data[i] {
		case ' ', '\t', '\r', '\n', ',', ']', '}', '#':
			return i
		}
	}
	return len(data)
}

func renderYAMLScalar(value string, style yaml.Style) string {
	switch style {
	case yaml.SingleQuotedStyle:
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	case yaml.DoubleQuotedStyle:
		return strconv.Quote(value)
	default:
		return value
	}
}

func yamlNewline(data []byte) string {
	if strings.Contains(string(data), "\r\n") {
		return "\r\n"
	}
	return "\n"
}

// normalizeMonitorConfig fills unset defaults so callers never see zeros.
func normalizeMonitorConfig(cfg *monitorConfig) {
	if cfg.Version == 0 {
		cfg.Version = monitorConfigVersion
	}
	cfg.Repos = normalizeMonitorRepoValues(cfg.Repos)
	if len(cfg.PRSections) == 0 {
		prs, _ := defaultMonitorSections()
		cfg.PRSections = prs
	}
	if len(cfg.IssueSections) == 0 {
		_, issues := defaultMonitorSections()
		cfg.IssueSections = issues
	}
	if cfg.Defaults.Limit <= 0 {
		cfg.Defaults.Limit = defaultMonitorLimit
	}
	if !validMonitorIntervalText(cfg.Defaults.Interval) {
		cfg.Defaults.Interval = formatMonitorInterval(defaultMonitorInterval)
	}
}

// validateMonitorConfig enforces structural rules that keep the TUI sane.
func validateMonitorConfig(cfg *monitorConfig) error {
	if err := validateMonitorRepos(cfg.Repos); err != nil {
		return err
	}
	if _, err := parseMonitorInterval(cfg.Defaults.Interval); err != nil {
		return fmt.Errorf("defaults.interval: %w", err)
	}
	if err := validateMonitorSections(cfg.PRSections); err != nil {
		return err
	}
	return validateMonitorSections(cfg.IssueSections)
}

func validateMonitorRepos(repos []string) error {
	if len(repos) == 0 {
		return errors.New("config must list at least one repo under `repos:`")
	}
	for _, repo := range repos {
		if err := validateMonitorRepo(repo); err != nil {
			return err
		}
	}
	return nil
}

// validateMonitorRepo accepts OWNER/REPO or HOST/OWNER/REPO entries.
func validateMonitorRepo(repo string) error {
	_, err := parseMonitorRepository(repo)
	return err
}

func validateMonitorSections(sections []monitorSection) error {
	for i, section := range sections {
		if strings.TrimSpace(section.Title) == "" {
			return fmt.Errorf("section %d is missing a title", i)
		}
		if strings.TrimSpace(section.Filters) == "" {
			return fmt.Errorf("section %q is missing filters", section.Title)
		}
		if section.Limit < 0 || section.Limit > maximumMonitorFetch {
			return fmt.Errorf("section %q limit must be between 0 and %d", section.Title, maximumMonitorFetch)
		}
	}
	return nil
}

// parseMonitorInterval parses a duration string with monitor bounds applied.
func parseMonitorInterval(value string) (time.Duration, error) {
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", value, err)
	}
	if duration < minimumMonitorInterval {
		return 0, fmt.Errorf("interval %s is below the minimum %s", duration, formatMonitorInterval(minimumMonitorInterval))
	}
	if duration > maximumMonitorInterval {
		return 0, fmt.Errorf("interval %s is above the maximum %s", duration, formatMonitorInterval(maximumMonitorInterval))
	}
	return duration, nil
}

func validMonitorIntervalText(value string) bool {
	_, err := parseMonitorInterval(value)
	return err == nil
}

// parseMonitorIntervalOrDefault falls back when the stored text is invalid.
func parseMonitorIntervalOrDefault(value string, fallback time.Duration) time.Duration {
	duration, err := parseMonitorInterval(value)
	if err != nil {
		return fallback
	}
	return duration
}

// formatMonitorInterval renders durations compactly ("10m", "1h30m").
func formatMonitorInterval(duration time.Duration) string {
	return duration.String()
}

// monitorSectionLimit resolves the fetch limit for one section.
func monitorSectionLimit(section monitorSection, globalLimit int) int {
	limit := section.Limit
	if limit <= 0 {
		limit = globalLimit
	}
	if limit > maximumMonitorFetch {
		return maximumMonitorFetch
	}
	return limit
}
