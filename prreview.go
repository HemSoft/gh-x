package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"unicode"
)

const (
	defaultReviewAgent  = "codex"
	defaultReviewModel  = "gpt-5.5"
	defaultReviewEffort = "high"
	defaultReviewMode   = "strict"
	defaultReviewer     = "gh-x PR Reviewer"
	reviewPRFields      = "number,title,body,baseRefName,headRefName,url,author"
	reviewBodyLimit     = 6000
)

type prReviewOptions struct {
	target           string
	repo             string
	agent            string
	command          string
	model            string
	effort           string
	mode             string
	base             string
	instructions     string
	instructionsFile string
	reviewer         string
	dryRun           bool
	post             bool
	allowApprove     bool
}

type reviewPullRequest struct {
	Number      int     `json:"number"`
	Title       string  `json:"title"`
	Body        string  `json:"body"`
	BaseRefName string  `json:"baseRefName"`
	HeadRefName string  `json:"headRefName"`
	URL         string  `json:"url"`
	Author      *author `json:"author"`
}

type reviewAgentInvocation struct {
	Name          string
	Args          []string
	Prompt        string
	PromptOnStdin bool
}

func runReview(args []string, stdout io.Writer, stderr io.Writer) error {
	options, err := parseReviewOptions(args, stderr)
	if err != nil {
		if errors.Is(err, errHelpDisplayed) {
			return nil
		}
		return err
	}

	return executeReviewFunc(options, stdout, stderr)
}

func defaultReviewOptions() prReviewOptions {
	agent := strings.TrimSpace(os.Getenv("GH_X_PR_REVIEW_AGENT"))
	if agent == "" {
		agent = defaultReviewAgent
	}

	model := strings.TrimSpace(os.Getenv("GH_X_PR_REVIEW_MODEL"))
	if model == "" {
		model = defaultReviewModel
	}
	effort := strings.TrimSpace(os.Getenv("GH_X_PR_REVIEW_EFFORT"))
	if effort == "" {
		effort = defaultReviewEffort
	}
	mode := strings.TrimSpace(os.Getenv("GH_X_PR_REVIEW_MODE"))
	if mode == "" {
		mode = defaultReviewMode
	}
	reviewer := strings.TrimSpace(os.Getenv("GH_X_PR_REVIEW_IDENTITY"))
	if reviewer == "" {
		reviewer = defaultReviewer
	}

	return prReviewOptions{
		agent:        agent,
		command:      strings.TrimSpace(os.Getenv("GH_X_PR_REVIEW_COMMAND")),
		model:        model,
		effort:       effort,
		mode:         mode,
		reviewer:     reviewer,
		post:         reviewBoolEnv("GH_X_PR_REVIEW_POST"),
		allowApprove: reviewBoolEnv("GH_X_PR_REVIEW_ALLOW_APPROVE"),
	}
}

func parseReviewOptions(args []string, stderr io.Writer) (prReviewOptions, error) {
	options := defaultReviewOptions()
	flagArgs, target, err := splitReviewFlagArgs(args)
	if err != nil {
		return options, err
	}

	flags := flag.NewFlagSet("review", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		writeReviewUsage(stderr)
	}

	flags.StringVar(&options.repo, "repo", "", "Select another repository using the [HOST/]OWNER/REPO format")
	flags.StringVar(&options.repo, "R", "", "Select another repository using the [HOST/]OWNER/REPO format")
	flags.StringVar(&options.agent, "agent", options.agent, "Agent preset: codex, claude, copilot, gemini, opencode, or custom")
	flags.StringVar(&options.agent, "a", options.agent, "Agent preset: codex, claude, copilot, gemini, opencode, or custom")
	flags.StringVar(&options.command, "command", options.command, "Custom command template for --agent custom")
	flags.StringVar(&options.model, "model", options.model, "Model to pass through to supported agents")
	flags.StringVar(&options.model, "m", options.model, "Model to pass through to supported agents")
	flags.StringVar(&options.effort, "effort", options.effort, "Reasoning effort for supported agents: low, medium, or high")
	flags.StringVar(&options.mode, "mode", options.mode, "Review mode: strict, medium, or fast-lane")
	flags.StringVar(&options.mode, "preset", options.mode, "Alias for --mode")
	flags.StringVar(&options.base, "base", "", "Override the PR base branch in the review prompt")
	flags.StringVar(&options.base, "B", "", "Override the PR base branch in the review prompt")
	flags.StringVar(&options.instructions, "instructions", "", "Additional review instructions")
	flags.StringVar(&options.instructions, "i", "", "Additional review instructions")
	flags.StringVar(&options.instructionsFile, "instructions-file", "", "Read additional review instructions from a file")
	flags.StringVar(&options.reviewer, "reviewer", options.reviewer, "Reviewer identity used in posted review reports")
	flags.BoolVar(&options.dryRun, "dry-run", false, "Print the resolved command and prompt without running the agent")
	flags.BoolVar(&options.post, "post", options.post, "Post a GitHub pull request review with inline comments")
	flags.BoolVar(&options.allowApprove, "allow-approve", options.allowApprove, "Allow strict-mode approval when the review has no findings")

	if err := flags.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return options, errHelpDisplayed
		}
		return options, err
	}

	if flags.NArg() > 0 {
		return options, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), ", "))
	}
	options.target = target

	options.agent = strings.TrimSpace(options.agent)
	if options.agent == "" {
		options.agent = defaultReviewAgent
	}
	options.command = strings.TrimSpace(options.command)
	options.model = strings.TrimSpace(options.model)
	options.effort, err = normalizeReviewEffort(options.effort)
	if err != nil {
		return options, err
	}
	options.mode, err = normalizeReviewMode(options.mode)
	if err != nil {
		return options, err
	}
	options.base = strings.TrimSpace(options.base)
	options.reviewer = strings.TrimSpace(options.reviewer)
	if options.reviewer == "" {
		options.reviewer = defaultReviewer
	}

	return options, nil
}

func splitReviewFlagArgs(args []string) ([]string, string, error) {
	valueFlags := map[string]bool{
		"--repo": true, "-R": true,
		"--agent": true, "-a": true,
		"--command": true,
		"--model":   true, "-m": true,
		"--effort": true,
		"--mode":   true, "--preset": true,
		"--base": true, "-B": true,
		"--instructions": true, "-i": true,
		"--instructions-file": true,
		"--reviewer":          true,
	}

	var flagArgs []string
	target := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			for _, positional := range args[i+1:] {
				if target != "" {
					return nil, "", fmt.Errorf("unexpected arguments: %s", positional)
				}
				target = positional
			}
			break
		}

		if strings.HasPrefix(arg, "-") && arg != "-" {
			flagArgs = append(flagArgs, arg)
			if strings.Contains(arg, "=") {
				continue
			}
			if valueFlags[arg] && i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
			continue
		}

		if target != "" {
			return nil, "", fmt.Errorf("unexpected arguments: %s", arg)
		}
		target = arg
	}
	return flagArgs, target, nil
}

func normalizeReviewEffort(value string) (string, error) {
	effort := strings.ToLower(strings.TrimSpace(value))
	if effort == "" {
		return defaultReviewEffort, nil
	}
	switch effort {
	case "low", "medium", "high":
		return effort, nil
	default:
		return "", fmt.Errorf("unsupported review effort %q", value)
	}
}

func normalizeReviewMode(value string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	if mode == "" {
		return defaultReviewMode, nil
	}
	switch mode {
	case "strict", "medium", "fast-lane":
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported review mode %q", value)
	}
}

var executeReviewFunc = executeReview

func executeReview(options prReviewOptions, stdout io.Writer, stderr io.Writer) error {
	pr, err := fetchReviewPullRequestFunc(options)
	if err != nil {
		return err
	}
	if options.base != "" {
		pr.BaseRefName = options.base
	}

	prompt, err := buildReviewPrompt(options, pr)
	if err != nil {
		return err
	}

	invocation, err := buildReviewInvocation(options, pr, prompt)
	if err != nil {
		return err
	}

	if options.dryRun {
		return renderReviewDryRun(stdout, options, invocation)
	}

	fmt.Fprintf(stderr, "Running %s review for PR #%d...\n", reviewAgentLabel(options), pr.Number)
	if options.postsReview() {
		return executePostedReview(options, pr, invocation, stdout, stderr)
	}
	return runReviewAgentFunc(invocation, stdout, stderr)
}

var fetchReviewPullRequestFunc = fetchReviewPullRequest

func fetchReviewPullRequest(options prReviewOptions) (reviewPullRequest, error) {
	ghArgs := []string{"pr", "view"}
	if options.target != "" {
		ghArgs = append(ghArgs, options.target)
	}
	ghArgs = append(ghArgs, "--json", reviewPRFields)
	if options.repo != "" {
		ghArgs = append(ghArgs, "--repo", options.repo)
	}

	stdoutBuf, stderrBuf, err := ghExecFunc(ghArgs...)
	if err != nil {
		return reviewPullRequest{}, wrapExecError(fmt.Errorf("gh pr view: %w", err), stderrBuf.String())
	}

	var pr reviewPullRequest
	if err := json.Unmarshal(stdoutBuf.Bytes(), &pr); err != nil {
		return reviewPullRequest{}, fmt.Errorf("decode gh pr view output: %w", err)
	}
	if pr.Number == 0 {
		return reviewPullRequest{}, errors.New("gh pr view returned no pull request number")
	}
	return pr, nil
}

func buildReviewPrompt(options prReviewOptions, pr reviewPullRequest) (string, error) {
	extra, err := loadReviewInstructions(options)
	if err != nil {
		return "", err
	}
	if options.postsReview() {
		return buildStructuredReviewPrompt(options, pr, extra)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Review GitHub pull request #%d", pr.Number)
	if pr.Title != "" {
		fmt.Fprintf(&b, ": %s", pr.Title)
	}
	b.WriteString(".\n\n")
	b.WriteString("Operate in read-only mode. Do not edit files, commit, push, merge, approve, request changes, or post PR comments.\n")
	b.WriteString("Report only actionable review findings, ordered by severity. Include file and line references when you can verify them. If there are no findings, say that directly and note any residual risk.\n")
	if err := appendReviewModeInstructions(&b, options.mode); err != nil {
		return "", err
	}
	b.WriteString("\n")

	b.WriteString("Pull request context:\n")
	fmt.Fprintf(&b, "- Number: %d\n", pr.Number)
	appendPromptValue(&b, "URL", pr.URL)
	appendPromptValue(&b, "Repository", reviewRepoLabel(options))
	appendPromptValue(&b, "Author", reviewAuthorLabel(pr.Author))
	appendPromptValue(&b, "Base branch", pr.BaseRefName)
	appendPromptValue(&b, "Head branch", pr.HeadRefName)
	if body := strings.TrimSpace(pr.Body); body != "" {
		fmt.Fprintf(&b, "- Body:\n%s\n", indentPromptBlock(trimPromptText(body, reviewBodyLimit)))
	}

	b.WriteString("\nUseful read-only commands:\n")
	fmt.Fprintf(&b, "- gh pr view %s%s --json %s\n", reviewTarget(pr), reviewRepoFlag(options), reviewPRFields)
	fmt.Fprintf(&b, "- gh pr diff %s%s\n", reviewTarget(pr), reviewRepoFlag(options))
	if pr.BaseRefName != "" && pr.HeadRefName != "" {
		fmt.Fprintf(&b, "- git diff %s...%s\n", pr.BaseRefName, pr.HeadRefName)
	}

	if extra != "" {
		b.WriteString("\nAdditional instructions:\n")
		b.WriteString(extra)
		if !strings.HasSuffix(extra, "\n") {
			b.WriteByte('\n')
		}
	}

	return b.String(), nil
}

func appendReviewModeInstructions(b *strings.Builder, mode string) error {
	normalized, err := normalizeReviewMode(mode)
	if err != nil {
		return err
	}

	fmt.Fprintf(b, "\nReview mode: %s\n", normalized)
	switch normalized {
	case "strict":
		b.WriteString("- Treat this as a thorough pre-merge review.\n")
		b.WriteString("- Prioritize correctness, security, data loss, regressions, API or workflow contract drift, and meaningful missing tests.\n")
		b.WriteString("- Verify claims against the diff, nearby code, and available tests before reporting them.\n")
		b.WriteString("- Do not include style-only findings unless they hide a real defect.\n")
	case "medium":
		b.WriteString("- Treat this as a balanced review between strict and fast-lane.\n")
		b.WriteString("- Prioritize correctness, security, user-visible regressions, and test gaps that materially affect confidence.\n")
		b.WriteString("- Avoid broad architecture critique unless it directly affects this change.\n")
		b.WriteString("- Skip minor polish issues unless they create ambiguity, maintenance risk, or user-facing quality problems.\n")
	case "fast-lane":
		b.WriteString("- Treat this as a high-signal review for low-risk changes.\n")
		b.WriteString("- Report only blockers, likely regressions, security issues, data loss risks, and clearly missing validation.\n")
		b.WriteString("- Skip nits, speculative improvements, and low-impact style feedback.\n")
	}
	return nil
}

func loadReviewInstructions(options prReviewOptions) (string, error) {
	parts := make([]string, 0, 2)
	if text := strings.TrimSpace(options.instructions); text != "" {
		parts = append(parts, text)
	}
	if options.instructionsFile != "" {
		data, err := os.ReadFile(options.instructionsFile)
		if err != nil {
			return "", fmt.Errorf("read instructions file: %w", err)
		}
		if text := strings.TrimSpace(string(data)); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

func appendPromptValue(b *strings.Builder, label, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	fmt.Fprintf(b, "- %s: %s\n", label, strings.TrimSpace(value))
}

func reviewRepoLabel(options prReviewOptions) string {
	if options.repo != "" {
		return options.repo
	}
	return "current repository"
}

func reviewAuthorLabel(a *author) string {
	if a == nil {
		return ""
	}
	if a.Name != "" && a.Login != "" {
		return fmt.Sprintf("%s (%s)", a.Name, a.Login)
	}
	if a.Name != "" {
		return a.Name
	}
	return a.Login
}

func reviewTarget(pr reviewPullRequest) string {
	return strconv.Itoa(pr.Number)
}

func reviewRepoFlag(options prReviewOptions) string {
	if options.repo == "" {
		return ""
	}
	return " --repo " + options.repo
}

func trimPromptText(value string, limit int) string {
	if limit < 1 || len(value) <= limit {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "\n[truncated]"
}

func indentPromptBlock(value string) string {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}

func buildReviewInvocation(options prReviewOptions, pr reviewPullRequest, prompt string) (reviewAgentInvocation, error) {
	if options.command != "" {
		return buildCustomReviewInvocation(options, pr, prompt)
	}

	agent := strings.ToLower(strings.TrimSpace(options.agent))
	switch agent {
	case "", "codex":
		args := []string{"exec", "--sandbox", "read-only"}
		if options.model != "" {
			args = append(args, "--model", options.model)
		}
		if options.effort != "" {
			args = append(args, "-c", "model_reasoning_effort="+strconv.Quote(options.effort))
		}
		args = append(args, "-")
		return reviewAgentInvocation{Name: "codex", Args: args, Prompt: prompt, PromptOnStdin: true}, nil
	case "claude", "claude-code":
		args := []string{"-p", "--permission-mode", "plan"}
		if options.model != "" {
			args = append(args, "--model", options.model)
		}
		args = append(args, prompt)
		return reviewAgentInvocation{Name: "claude", Args: args, Prompt: prompt}, nil
	case "copilot", "github-copilot":
		args := []string{
			"-p", prompt,
			"--allow-tool=shell(git:*)",
			"--allow-tool=shell(gh:*)",
			"--deny-tool=shell(git push)",
			"--deny-tool=shell(gh pr merge)",
			"--deny-tool=shell(gh pr review)",
			"--deny-tool=shell(gh pr comment)",
			"--deny-tool=write",
		}
		if options.model != "" {
			args = append([]string{"--model", options.model}, args...)
		}
		return reviewAgentInvocation{Name: "copilot", Args: args, Prompt: prompt}, nil
	case "gemini":
		args := []string{"-p", prompt, "--approval-mode", "plan"}
		if options.model != "" {
			args = append([]string{"--model", options.model}, args...)
		}
		return reviewAgentInvocation{Name: "gemini", Args: args, Prompt: prompt}, nil
	case "opencode":
		args := []string{"run"}
		if options.model != "" {
			args = append(args, "--model", options.model)
		}
		args = append(args, prompt)
		return reviewAgentInvocation{Name: "opencode", Args: args, Prompt: prompt}, nil
	case "custom":
		return reviewAgentInvocation{}, errors.New("--agent custom requires --command or GH_X_PR_REVIEW_COMMAND")
	default:
		return reviewAgentInvocation{}, fmt.Errorf("unsupported review agent %q", options.agent)
	}
}

func buildCustomReviewInvocation(options prReviewOptions, pr reviewPullRequest, prompt string) (reviewAgentInvocation, error) {
	parts, err := splitCommandLine(options.command)
	if err != nil {
		return reviewAgentInvocation{}, err
	}
	if len(parts) == 0 {
		return reviewAgentInvocation{}, errors.New("custom review command cannot be empty")
	}

	usesPromptArg := false
	for i, part := range parts {
		if strings.Contains(part, "{prompt}") {
			usesPromptArg = true
		}
		parts[i] = replaceReviewPlaceholders(part, options, pr, prompt)
	}

	return reviewAgentInvocation{
		Name:          parts[0],
		Args:          parts[1:],
		Prompt:        prompt,
		PromptOnStdin: !usesPromptArg,
	}, nil
}

func replaceReviewPlaceholders(value string, options prReviewOptions, pr reviewPullRequest, prompt string) string {
	replacer := strings.NewReplacer(
		"{prompt}", prompt,
		"{number}", strconv.Itoa(pr.Number),
		"{repo}", options.repo,
		"{base}", pr.BaseRefName,
		"{head}", pr.HeadRefName,
		"{url}", pr.URL,
		"{title}", pr.Title,
		"{model}", options.model,
		"{effort}", options.effort,
		"{mode}", options.mode,
	)
	return replacer.Replace(value)
}

func splitCommandLine(command string) ([]string, error) {
	var args []string
	var current strings.Builder
	var quote rune
	escaped := false
	inToken := false

	flush := func() {
		if inToken {
			args = append(args, current.String())
			current.Reset()
			inToken = false
		}
	}

	for _, r := range command {
		switch {
		case escaped:
			current.WriteRune(r)
			inToken = true
			escaped = false
		case r == '\\':
			escaped = true
			inToken = true
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
			inToken = true
		case r == '\'' || r == '"':
			quote = r
			inToken = true
		case unicode.IsSpace(r):
			flush()
		default:
			current.WriteRune(r)
			inToken = true
		}
	}

	if escaped {
		current.WriteRune('\\')
	}
	if quote != 0 {
		return nil, errors.New("custom review command has unterminated quote")
	}
	flush()
	return args, nil
}

var runReviewAgentFunc = runReviewAgent

func runReviewAgent(invocation reviewAgentInvocation, stdout io.Writer, stderr io.Writer) error {
	cmd := exec.Command(invocation.Name, invocation.Args...)
	if invocation.PromptOnStdin {
		cmd.Stdin = strings.NewReader(invocation.Prompt)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s: %w", invocation.Name, err)
	}
	return nil
}

func renderReviewDryRun(stdout io.Writer, options prReviewOptions, invocation reviewAgentInvocation) error {
	fmt.Fprintf(stdout, "Agent: %s\n", reviewAgentLabel(options))
	fmt.Fprintf(stdout, "Mode: %s\n", options.mode)
	fmt.Fprintf(stdout, "Effort: %s\n", options.effort)
	fmt.Fprintf(stdout, "Post review: %t\n", options.postsReview())
	fmt.Fprintf(stdout, "Allow approve: %t\n", options.allowApprove)
	fmt.Fprintf(stdout, "Reviewer: %s\n", options.reviewer)
	fmt.Fprintf(stdout, "Command: %s\n", formatReviewCommand(invocation))
	if invocation.PromptOnStdin {
		fmt.Fprintln(stdout, "Prompt: stdin")
	}
	fmt.Fprintln(stdout)
	fmt.Fprint(stdout, invocation.Prompt)
	return nil
}

func reviewAgentLabel(options prReviewOptions) string {
	if options.command != "" {
		return "custom"
	}
	if options.agent == "" {
		return defaultReviewAgent
	}
	return options.agent
}

func formatReviewCommand(invocation reviewAgentInvocation) string {
	parts := append([]string{invocation.Name}, invocation.Args...)
	for i, part := range parts {
		parts[i] = quoteCommandPart(part)
	}
	return strings.Join(parts, " ")
}

func quoteCommandPart(part string) string {
	if part == "" || strings.ContainsFunc(part, unicode.IsSpace) || strings.ContainsAny(part, "\"'") {
		return strconv.Quote(part)
	}
	return part
}

func writeReviewUsage(w io.Writer) {
	fmt.Fprint(w, reviewUsage)
}

const reviewUsage = `Usage:
  gh x pr review [number|url|branch] [flags]

Run a pull request review with an agentic CLI.

Flags:
  -R, --repo string                Select another repository using the [HOST/]OWNER/REPO format
  -a, --agent string               Agent preset: codex, claude, copilot, gemini, opencode, or custom
      --command string             Custom command template for --agent custom
  -m, --model string               Model to pass through to supported agents
      --effort string              Reasoning effort for supported agents: low, medium, or high
      --mode string                Review mode: strict, medium, or fast-lane
      --preset string              Alias for --mode
  -B, --base string                Override the PR base branch in the review prompt
  -i, --instructions string        Additional review instructions
      --instructions-file string   Read additional review instructions from a file
      --reviewer string            Reviewer identity used in posted review reports
      --dry-run                    Print the resolved command and prompt without running the agent
      --post                       Post a GitHub pull request review with inline comments
      --allow-approve              Allow strict-mode approval when the review has no findings

Configuration:
  GH_X_PR_REVIEW_AGENT      Default agent preset
  GH_X_PR_REVIEW_MODEL      Default model
  GH_X_PR_REVIEW_EFFORT     Default reasoning effort
  GH_X_PR_REVIEW_MODE       Default review mode
  GH_X_PR_REVIEW_COMMAND    Default custom command template
  GH_X_PR_REVIEW_IDENTITY   Default reviewer identity for posted reports
  GH_X_PR_REVIEW_POST       Set true to post reviews by default
  GH_X_PR_REVIEW_ALLOW_APPROVE
                            Set true to allow strict-mode approvals by default

Custom command templates may use {prompt}, {number}, {repo}, {base}, {head}, {url}, {title}, {model}, {effort}, and {mode}.
If {prompt} is omitted, the prompt is sent on stdin.
`
