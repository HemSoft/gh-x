package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	gh "github.com/cli/go-gh/v2"
	"github.com/cli/go-gh/v2/pkg/term"
	"github.com/muesli/termenv"
	"gopkg.in/yaml.v3"
)

type workflowListOptions struct {
	repo string
	all  bool
}

type workflowEntry struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	State    string `json:"state"`
	Path     string `json:"path"`
	Triggers string `json:"-"`
}

func runWorkflowList(args []string, stdout io.Writer, stderr io.Writer) error {
	options, err := parseWorkflowListOptions(args, stderr)
	if err != nil {
		if errors.Is(err, errHelpDisplayed) {
			return nil
		}
		return err
	}
	return executeWorkflowList(options, stdout)
}

func parseWorkflowListOptions(args []string, stderr io.Writer) (workflowListOptions, error) {
	var options workflowListOptions

	flags := flag.NewFlagSet("workflow list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		writeWorkflowListUsage(stderr)
	}

	flags.StringVar(&options.repo, "repo", "", "Select another repository using the [HOST/]OWNER/REPO format")
	flags.StringVar(&options.repo, "R", "", "Select another repository using the [HOST/]OWNER/REPO format")
	flags.BoolVar(&options.all, "all", false, "Include disabled workflows")
	flags.BoolVar(&options.all, "a", false, "Include disabled workflows")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return options, errHelpDisplayed
		}
		return options, err
	}

	if flags.NArg() > 0 {
		return options, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), ", "))
	}

	return options, nil
}

func buildWorkflowListArgs(options workflowListOptions) []string {
	args := []string{"workflow", "list", "--json", "id,name,state,path"}
	if options.all {
		args = append(args, "--all")
	}
	if options.repo != "" {
		args = append(args, "--repo", options.repo)
	}
	return args
}

func resolveRepoURL(repoOverride string) (string, error) {
	args := []string{"repo", "view", "--json", "url", "--jq", ".url"}
	if repoOverride != "" {
		args = append(args, "--repo", repoOverride)
	}
	stdoutBuf, stderrBuf, err := gh.Exec(args...)
	if err != nil {
		return "", wrapExecError(fmt.Errorf("resolve repo URL: %w", err), stderrBuf.String())
	}
	return strings.TrimSpace(stdoutBuf.String()), nil
}

// fetchWorkflowsFunc is swapped in tests to avoid real API calls.
var fetchWorkflowsFunc = fetchWorkflows

// fetchWorkflowTriggersFunc is swapped in tests to avoid real API calls.
var fetchWorkflowTriggersFunc = fetchWorkflowTriggers

// resolveRepoURLFunc is swapped in tests to avoid real API calls.
var resolveRepoURLFunc = resolveRepoURL

// isColorEnabledFunc is swapped in tests to control color behavior.
var isColorEnabledFunc = func() bool { return term.FromEnv().IsColorEnabled() }

func fetchWorkflows(options workflowListOptions) ([]workflowEntry, error) {
	ghArgs := buildWorkflowListArgs(options)
	stdoutBuf, stderrBuf, err := gh.Exec(ghArgs...)
	if err != nil {
		return nil, wrapExecError(fmt.Errorf("gh workflow list: %w", err), stderrBuf.String())
	}

	var workflows []workflowEntry
	if err := json.Unmarshal(stdoutBuf.Bytes(), &workflows); err != nil {
		return nil, fmt.Errorf("parsing workflow list: %w", err)
	}

	return workflows, nil
}

func executeWorkflowList(options workflowListOptions, stdout io.Writer) error {
	workflows, err := fetchWorkflowsFunc(options)
	if err != nil {
		return err
	}

	if len(workflows) == 0 {
		fmt.Fprintln(stdout, "No workflows found.")
		return nil
	}

	workflows = fetchWorkflowTriggersFunc(options, workflows)
	colorEnabled := isColorEnabledFunc()

	var repoURL string
	if colorEnabled {
		repoURL, err = resolveRepoURLFunc(options.repo)
		if err != nil {
			return err
		}
	}

	return renderWorkflowTable(stdout, workflows, repoURL, colorEnabled)
}

func fetchWorkflowTriggers(options workflowListOptions, workflows []workflowEntry) []workflowEntry {
	enriched := make([]workflowEntry, len(workflows))
	copy(enriched, workflows)

	for i := range enriched {
		content, err := readWorkflowContent(options, enriched[i])
		if err != nil {
			enriched[i].Triggers = "unknown"
			continue
		}
		enriched[i].Triggers = formatWorkflowTriggers(content)
	}

	return enriched
}

func readWorkflowContent(options workflowListOptions, wf workflowEntry) ([]byte, error) {
	if path.Ext(wf.Path) != ".yml" && path.Ext(wf.Path) != ".yaml" {
		return nil, fmt.Errorf("unsupported workflow path: %s", wf.Path)
	}

	if options.repo == "" {
		return readLocalWorkflowContent(wf.Path)
	}

	owner, repo, err := resolveRepo(options.repo)
	if err != nil {
		return nil, err
	}

	apiPath := fmt.Sprintf("repos/%s/%s/contents/%s", owner, repo, escapeWorkflowPath(wf.Path))
	stdoutBuf, stderrBuf, err := gh.Exec("api", apiPath, "--jq", ".content")
	if err != nil {
		return nil, wrapExecError(fmt.Errorf("gh api workflow content: %w", err), stderrBuf.String())
	}

	encoded := strings.Join(strings.Fields(stdoutBuf.String()), "")
	return base64.StdEncoding.DecodeString(encoded)
}

func readLocalWorkflowContent(workflowPath string) ([]byte, error) {
	content, err := os.ReadFile(workflowPath)
	if err == nil {
		return content, nil
	}

	root, rootErr := gitRoot()
	if rootErr != nil {
		return nil, err
	}

	content, rootReadErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(workflowPath)))
	if rootReadErr != nil {
		return nil, err
	}
	return content, nil
}

func gitRoot() (string, error) {
	stdout, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(stdout)), nil
}

func escapeWorkflowPath(workflowPath string) string {
	parts := strings.Split(workflowPath, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func formatWorkflowTriggers(data []byte) string {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return "unknown"
	}

	root := documentRoot(&document)
	onNode := mappingValue(root, "on")
	if onNode == nil {
		return "unknown"
	}

	triggers := formatOnNode(onNode)
	if len(triggers) == 0 {
		return "unknown"
	}
	return strings.Join(triggers, ", ")
}

func documentRoot(document *yaml.Node) *yaml.Node {
	if document == nil {
		return nil
	}
	if document.Kind == yaml.DocumentNode && len(document.Content) > 0 {
		return document.Content[0]
	}
	return document
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func formatOnNode(node *yaml.Node) []string {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Value != "" {
			return []string{formatWorkflowTrigger(node.Value, nil)}
		}
	case yaml.SequenceNode:
		events := scalarValues(node)
		triggers := make([]string, 0, len(events))
		for _, event := range events {
			triggers = append(triggers, formatWorkflowTrigger(event, nil))
		}
		return triggers
	case yaml.MappingNode:
		triggers := make([]string, 0, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			triggers = append(triggers, formatWorkflowTrigger(node.Content[i].Value, node.Content[i+1]))
		}
		return triggers
	}
	return nil
}

func formatWorkflowTrigger(event string, config *yaml.Node) string {
	if event == "schedule" {
		return formatScheduleTrigger(config)
	}

	label := workflowTriggerLabel(event)
	filters := formatTriggerFilters(config)
	if len(filters) == 0 {
		return label
	}
	return fmt.Sprintf("%s (%s)", label, strings.Join(filters, "; "))
}

func workflowTriggerLabel(event string) string {
	switch event {
	case "workflow_dispatch":
		return "manual"
	case "workflow_run":
		return "after workflow run"
	default:
		return event
	}
}

func formatScheduleTrigger(config *yaml.Node) string {
	schedules := make([]string, 0)
	if config != nil && config.Kind == yaml.SequenceNode {
		for _, entry := range config.Content {
			cron := mappingValue(entry, "cron")
			if cron != nil && cron.Value != "" {
				schedules = append(schedules, formatCronSchedule(cron.Value))
			}
		}
	}
	if len(schedules) == 0 {
		return "schedule"
	}
	return "schedule: " + strings.Join(schedules, "; ")
}

func formatTriggerFilters(config *yaml.Node) []string {
	if config == nil || config.Kind != yaml.MappingNode {
		return nil
	}

	filterKeys := []string{"branches", "branches-ignore", "tags", "tags-ignore", "workflows", "types"}
	filters := make([]string, 0, len(filterKeys))
	for _, key := range filterKeys {
		values := scalarValues(mappingValue(config, key))
		if len(values) > 0 {
			filters = append(filters, fmt.Sprintf("%s: %s", key, strings.Join(values, ", ")))
		}
	}
	return filters
}

func formatCronSchedule(cron string) string {
	fields := strings.Fields(cron)
	if len(fields) != 5 {
		return customCronSchedule(cron)
	}

	minute, minuteOk := parseCronNumber(fields[0], 0, 59)
	hour, hourOk := parseCronNumber(fields[1], 0, 23)
	if !minuteOk {
		return customCronSchedule(cron)
	}

	dayOfMonth, month, dayOfWeek := fields[2], fields[3], fields[4]
	if fields[1] == "*" && dayOfMonth == "*" && month == "*" && dayOfWeek == "*" {
		return fmt.Sprintf("hourly at minute %02d UTC", minute)
	}
	if !hourOk {
		return customCronSchedule(cron)
	}

	timeText := fmt.Sprintf("%02d:%02d UTC", hour, minute)
	if dayOfMonth == "*" && month == "*" && dayOfWeek == "*" {
		return "daily at " + timeText
	}
	if dayOfMonth == "*" && month == "*" && dayOfWeek != "*" {
		return formatWeeklyCronSchedule(dayOfWeek, timeText, cron)
	}
	if dayOfMonth != "*" && month == "*" && dayOfWeek == "*" {
		return fmt.Sprintf("monthly on day %s at %s", dayOfMonth, timeText)
	}
	if dayOfMonth != "*" && month != "*" && dayOfWeek == "*" {
		return fmt.Sprintf("yearly on %s %s at %s", formatCronMonth(month), dayOfMonth, timeText)
	}

	return customCronSchedule(cron)
}

func parseCronNumber(field string, minValue, maxValue int) (int, bool) {
	value, err := strconv.Atoi(field)
	if err != nil || value < minValue || value > maxValue {
		return 0, false
	}
	return value, true
}

func formatWeeklyCronSchedule(dayOfWeek, timeText, cron string) string {
	switch strings.ToUpper(dayOfWeek) {
	case "1-5", "MON-FRI":
		return "weekdays at " + timeText
	case "0,6", "6,0", "SAT,SUN", "SUN,SAT":
		return "weekends at " + timeText
	}

	days := formatCronDays(dayOfWeek)
	if days == "" {
		return customCronSchedule(cron)
	}
	return "weekly on " + days + " at " + timeText
}

func formatCronDays(dayOfWeek string) string {
	if strings.Contains(dayOfWeek, "-") {
		parts := strings.Split(dayOfWeek, "-")
		if len(parts) != 2 {
			return ""
		}
		start := cronDayName(parts[0])
		end := cronDayName(parts[1])
		if start == "" || end == "" {
			return ""
		}
		return start + "-" + end
	}

	parts := strings.Split(dayOfWeek, ",")
	days := make([]string, 0, len(parts))
	for _, part := range parts {
		day := cronDayName(part)
		if day == "" {
			return ""
		}
		days = append(days, day)
	}
	return strings.Join(days, ", ")
}

func cronDayName(day string) string {
	switch strings.ToUpper(day) {
	case "0", "7", "SUN":
		return "Sunday"
	case "1", "MON":
		return "Monday"
	case "2", "TUE":
		return "Tuesday"
	case "3", "WED":
		return "Wednesday"
	case "4", "THU":
		return "Thursday"
	case "5", "FRI":
		return "Friday"
	case "6", "SAT":
		return "Saturday"
	default:
		return ""
	}
}

func formatCronMonth(month string) string {
	switch strings.ToUpper(month) {
	case "1", "JAN":
		return "January"
	case "2", "FEB":
		return "February"
	case "3", "MAR":
		return "March"
	case "4", "APR":
		return "April"
	case "5", "MAY":
		return "May"
	case "6", "JUN":
		return "June"
	case "7", "JUL":
		return "July"
	case "8", "AUG":
		return "August"
	case "9", "SEP":
		return "September"
	case "10", "OCT":
		return "October"
	case "11", "NOV":
		return "November"
	case "12", "DEC":
		return "December"
	default:
		return month
	}
}

func customCronSchedule(cron string) string {
	return "custom schedule (" + cron + ")"
}

func scalarValues(node *yaml.Node) []string {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.ScalarNode {
		if node.Value == "" {
			return nil
		}
		return []string{node.Value}
	}
	if node.Kind != yaml.SequenceNode {
		return nil
	}

	values := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		if item.Value != "" {
			values = append(values, item.Value)
		}
	}
	return values
}

func workflowURL(repoURL string, wf workflowEntry) string {
	ext := path.Ext(wf.Path)
	if ext == ".yml" || ext == ".yaml" {
		return repoURL + "/actions/workflows/" + url.PathEscape(path.Base(wf.Path))
	}
	return repoURL + "/actions/workflows/" + strconv.Itoa(wf.ID)
}

func (s tableStyler) workflowStateCell(state string) tableCell {
	switch state {
	case "active":
		return s.colored(state, termenv.ANSIGreen)
	case "disabled_manually", "disabled_inactivity", "disabled_fork":
		return s.colored(state, termenv.ANSIYellow)
	default:
		return s.dim(state)
	}
}

func renderWorkflowTable(stdout io.Writer, workflows []workflowEntry, repoURL string, colorEnabled bool) error {
	styler := newTableStyler(stdout, colorEnabled)
	actionsURL := repoURL + "/actions"

	headers := []tableCell{
		styler.dimLinkCell("NAME", actionsURL),
		styler.dimLinkCell("STATE", actionsURL),
		styler.dimLinkCell("TRIGGERS", actionsURL),
		styler.dimLinkCell("ID", actionsURL),
	}

	rows := make([][]tableCell, len(workflows))
	for i, wf := range workflows {
		wfURL := workflowURL(repoURL, wf)
		rows[i] = []tableCell{
			styler.plain(wf.Name),
			styler.workflowStateCell(wf.State),
			styler.plain(wf.Triggers),
			styler.linkCell(strconv.Itoa(wf.ID), wfURL, termenv.ANSICyan),
		}
	}

	colWidths := computeColumnWidths(headers, rows)

	flexibleCols := []int{0, 2}
	colWidths = fitColumnsToTerminal(colWidths, flexibleCols, getTerminalWidth())
	rows = truncateCells(rows, colWidths, flexibleCols)

	writeRow(stdout, headers, colWidths)
	for _, row := range rows {
		writeRow(stdout, row, colWidths)
	}

	return nil
}

func writeWorkflowListUsage(w io.Writer) {
	fmt.Fprint(w, workflowListUsage)
}

const workflowListUsage = `Usage:
  gh x workflow list [flags]

List workflows for the current repository.

Flags:
  -R, --repo string   Select another repository using the [HOST/]OWNER/REPO format
  -a, --all           Include disabled workflows
`
