package prompt

import (
	"cmp"
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
	"time"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/filepathext"
	"github.com/NaturalSelect/angela/internal/home"
	"github.com/NaturalSelect/angela/internal/shell"
	"github.com/NaturalSelect/angela/internal/skills"
)

// Prompt represents a template-based prompt generator.
type Prompt struct {
	name         string
	tmpl         *template.Template
	now          func() time.Time
	platform     string
	workingDir   string
	contextPaths []string
	preamble     string
	extra        map[string]any
}

type PromptDat struct {
	Provider           string
	Model              string
	Config             config.Config
	WorkingDir         string
	IsGitRepo          bool
	Platform           string
	Date               string
	GitStatus          string
	ContextFiles       []ContextFile
	GlobalContextFiles []ContextFile
	AvailSkillXML      string
	// Extra carries per-call values a specific agent's template needs
	// and no other does. It stays a bag rather than typed fields so
	// adding one does not widen the struct every agent renders.
	Extra map[string]any
}

type ContextFile struct {
	Path    string
	Content string
}

type Option func(*Prompt)

func WithTimeFunc(fn func() time.Time) Option {
	return func(p *Prompt) {
		p.now = fn
	}
}

func WithPlatform(platform string) Option {
	return func(p *Prompt) {
		p.platform = platform
	}
}

func WithWorkingDir(workingDir string) Option {
	return func(p *Prompt) {
		p.workingDir = workingDir
	}
}

// WithContextPaths overrides the context files an agent's prompt
// loads. When unset, promptData falls back to
// Config.Options.ContextPaths so built-in agents keep the global
// behavior.
func WithContextPaths(paths []string) Option {
	return func(p *Prompt) {
		p.contextPaths = paths
	}
}

// WithPreamble prepends fixed text to the rendered prompt.
//
// It carries what the session imposes on the agent rather than what the
// agent is, so it sits outside the template on purpose: a user who replaces
// the whole prompt still gets it. It is written verbatim, not rendered, so
// the text cannot reach the template data.
func WithPreamble(text string) Option {
	return func(p *Prompt) {
		p.preamble = text
	}
}

// WithExtra supplies values reachable from a template as {{.Extra.Key}}.
func WithExtra(extra map[string]any) Option {
	return func(p *Prompt) {
		p.extra = extra
	}
}

// sharedTemplates holds template definitions every agent prompt can
// invoke. Keeping the context-file rendering here rather than copied
// into each agent template is what makes it impossible for a subagent
// to silently miss the project's AGENTS.md.
//
//go:embed templates/context_files.md.tpl
var sharedTemplates string

func NewPrompt(name, promptTemplate string, opts ...Option) (*Prompt, error) {
	p := &Prompt{
		name: name,
		now:  time.Now,
	}
	for _, opt := range opts {
		opt(p)
	}
	t, err := template.New(name).Parse(sharedTemplates)
	if err != nil {
		return nil, fmt.Errorf("parsing shared templates: %w", err)
	}
	t, err = t.Parse(promptTemplate)
	if err != nil {
		return nil, fmt.Errorf("parsing template: %w", err)
	}
	p.tmpl = t
	return p, nil
}

func (p *Prompt) Build(ctx context.Context, provider, model string, store *config.ConfigStore) (string, error) {
	var sb strings.Builder
	d, err := p.promptData(ctx, provider, model, store)
	if err != nil {
		return "", err
	}
	if p.preamble != "" {
		sb.WriteString(strings.TrimRight(p.preamble, "\n"))
		sb.WriteString("\n\n")
	}
	if err := p.tmpl.Execute(&sb, d); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}

	return sb.String(), nil
}

func processFile(filePath string, seen *[]os.FileInfo) *ContextFile {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil
	}
	// NOTE: os.SameFile compares device+inode on Unix and the file
	// index on Windows, so a symlink alias (e.g. CLAUDE.md -> AGENTS.md)
	// resolves to the same identity as its target and is skipped here.
	for _, s := range *seen {
		if os.SameFile(info, s) {
			return nil
		}
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	*seen = append(*seen, info)
	return &ContextFile{
		Path:    filePath,
		Content: string(content),
	}
}

func processContextPath(p string, store *config.ConfigStore, seen *[]os.FileInfo) []ContextFile {
	var contexts []ContextFile
	fullPath := filepathext.SmartJoin(store.WorkingDir(), p)
	info, err := os.Stat(fullPath)
	if err != nil {
		return contexts
	}
	if info.IsDir() {
		filepath.WalkDir(fullPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				if result := processFile(path, seen); result != nil {
					contexts = append(contexts, *result)
				}
			}
			return nil
		})
	} else {
		result := processFile(fullPath, seen)
		if result != nil {
			contexts = append(contexts, *result)
		}
	}
	return contexts
}

// expandPath expands ~ and environment variables in file paths
func expandPath(path string, store *config.ConfigStore) string {
	path = home.Long(path)
	// Handle environment variable expansion using the same pattern as config
	if strings.HasPrefix(path, "$") {
		if expanded, err := store.Resolver().ResolveValue(path); err == nil {
			path = expanded
		}
	}

	return path
}

// loadContextFiles loads and deduplicates context files from a list of paths.
// loadContextFiles loads and deduplicates context files from a list of
// paths. Deduplication happens twice: first by the configured path
// string, so re-listing the same entry is a no-op, then by file
// identity, so a symlink alias of an already-loaded file (e.g.
// CLAUDE.md pointing at AGENTS.md) is not embedded twice under two
// names.
func loadContextFiles(paths []string, store *config.ConfigStore) map[string][]ContextFile {
	files := map[string][]ContextFile{}
	var seen []os.FileInfo
	for _, pth := range paths {
		expanded := expandPath(pth, store)
		// NOTE: an empty path joins to the working directory itself,
		// which would walk and embed the whole project tree as context.
		if strings.TrimSpace(expanded) == "" {
			continue
		}
		pathKey := strings.ToLower(expanded)
		if _, ok := files[pathKey]; ok {
			continue
		}
		files[pathKey] = processContextPath(expanded, store, &seen)
	}
	return files
}

func (p *Prompt) promptData(ctx context.Context, provider, model string, store *config.ConfigStore) (PromptDat, error) {
	workingDir := cmp.Or(p.workingDir, store.WorkingDir())
	platform := cmp.Or(p.platform, runtime.GOOS)

	cfg := store.Config()
	contextPaths := cfg.Options.ContextPaths
	if p.contextPaths != nil {
		contextPaths = p.contextPaths
	}
	contextFiles := loadContextFiles(contextPaths, store)
	globalContextFiles := loadContextFiles(cfg.Options.GlobalContextPaths, store)

	// Discover and load skills metadata.
	var availSkillXML string

	// Start with builtin skills.
	allSkills := skills.DiscoverBuiltin()
	builtinNames := make(map[string]bool, len(allSkills))
	for _, s := range allSkills {
		builtinNames[s.Name] = true
	}

	// Discover user skills from configured paths.
	if len(cfg.Options.SkillsPaths) > 0 {
		expandedPaths := make([]string, 0, len(cfg.Options.SkillsPaths))
		for _, pth := range cfg.Options.SkillsPaths {
			expandedPaths = append(expandedPaths, expandPath(pth, store))
		}
		for _, userSkill := range skills.Discover(expandedPaths) {
			if builtinNames[userSkill.Name] {
				slog.Warn("User skill overrides builtin skill", "name", userSkill.Name)
			}
			allSkills = append(allSkills, userSkill)
		}
	}

	// Deduplicate: user skills override builtins with the same name.
	allSkills = skills.Deduplicate(allSkills)

	// Filter out disabled skills.
	allSkills = skills.Filter(allSkills, cfg.Options.DisabledSkills)

	if len(allSkills) > 0 {
		availSkillXML = skills.ToPromptXML(allSkills)
	}

	isGit := isGitRepo(store.WorkingDir())
	data := PromptDat{
		Provider:      provider,
		Model:         model,
		Config:        *cfg,
		WorkingDir:    filepath.ToSlash(workingDir),
		IsGitRepo:     isGit,
		Platform:      platform,
		Date:          p.now().Format("1/2/2006"),
		AvailSkillXML: availSkillXML,
		Extra:         p.extra,
	}
	if isGit {
		var err error
		data.GitStatus, err = getGitStatus(ctx, store.WorkingDir())
		if err != nil {
			return PromptDat{}, err
		}
	}

	for _, files := range contextFiles {
		data.ContextFiles = append(data.ContextFiles, files...)
	}
	for _, files := range globalContextFiles {
		data.GlobalContextFiles = append(data.GlobalContextFiles, files...)
	}
	return data, nil
}

func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func getGitStatus(ctx context.Context, dir string) (string, error) {
	sh := shell.NewShell(&shell.Options{
		WorkingDir: dir,
	})
	branch, err := getGitBranch(ctx, sh)
	if err != nil {
		return "", err
	}
	status, err := getGitStatusSummary(ctx, sh)
	if err != nil {
		return "", err
	}
	commits, err := getGitRecentCommits(ctx, sh)
	if err != nil {
		return "", err
	}
	return branch + status + commits, nil
}

func getGitBranch(ctx context.Context, sh *shell.Shell) (string, error) {
	out, _, err := sh.Exec(ctx, "git branch --show-current 2>/dev/null")
	if err != nil {
		return "", nil
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", nil
	}
	return fmt.Sprintf("Current branch: %s\n", out), nil
}

func getGitStatusSummary(ctx context.Context, sh *shell.Shell) (string, error) {
	out, _, err := sh.Exec(ctx, "git status --short 2>/dev/null | head -20")
	if err != nil {
		return "", nil
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "Status: clean\n", nil
	}
	return fmt.Sprintf("Status:\n%s\n", out), nil
}

func getGitRecentCommits(ctx context.Context, sh *shell.Shell) (string, error) {
	out, _, err := sh.Exec(ctx, "git log --oneline -n 3 2>/dev/null")
	if err != nil || out == "" {
		return "", nil
	}
	out = strings.TrimSpace(out)
	return fmt.Sprintf("Recent commits:\n%s\n", out), nil
}

func (p *Prompt) Name() string {
	return p.name
}
