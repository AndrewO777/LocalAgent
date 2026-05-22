// Package skills implements discovery and loading of agent skills using the
// Anthropic Claude Code SKILL.md format. A skill is a directory containing a
// SKILL.md file with YAML frontmatter (name + description) and a markdown
// body of instructions. Skills are discovered from two locations:
//
//	~/.localagent/skills/<name>/SKILL.md   (user-global)
//	<workdir>/.localagent/skills/<name>/SKILL.md   (project-local)
//
// Project-local skills override user-global ones on name collision. The
// format is intentionally identical to Claude Code, OpenCode, and Codex CLI
// so skills written for those agents (caveman, WNP-Loop, etc.) drop in
// unmodified.
package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Source describes where a skill was discovered from. Project beats User on
// name collision; this field exists so we can surface that in the UI.
type Source string

const (
	SourceUser    Source = "user"
	SourceProject Source = "project"
)

// Skill is one parsed SKILL.md. Name and Description come from the YAML
// frontmatter; Body is everything after the closing `---`.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"-"` // omitted from list responses; loaded lazily for the UI
	Path        string `json:"path"`
	Source      Source `json:"source"`
}

// Summary is the lightweight projection sent to the UI. Body is omitted —
// the body can be 50KB+ and we only need names + descriptions for the
// catalog checkboxes.
type Summary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      Source `json:"source"`
	Path        string `json:"path"`
}

// Catalog is the discovered set of skills, deduped by name.
type Catalog struct {
	byName map[string]*Skill
	order  []string
}

// Names returns skill names in stable order (project first, then user, then
// alphabetical within each).
func (c *Catalog) Names() []string { return append([]string(nil), c.order...) }

// Get returns the skill with the given name, or nil if absent.
func (c *Catalog) Get(name string) *Skill {
	if c == nil {
		return nil
	}
	return c.byName[name]
}

// Summaries returns the lightweight projection for the UI.
func (c *Catalog) Summaries() []Summary {
	if c == nil {
		return nil
	}
	out := make([]Summary, 0, len(c.order))
	for _, n := range c.order {
		s := c.byName[n]
		out = append(out, Summary{Name: s.Name, Description: s.Description, Source: s.Source, Path: s.Path})
	}
	return out
}

// All returns the full Skill slice in stable order. Bodies are loaded.
func (c *Catalog) All() []*Skill {
	if c == nil {
		return nil
	}
	out := make([]*Skill, 0, len(c.order))
	for _, n := range c.order {
		out = append(out, c.byName[n])
	}
	return out
}

// Discover walks the user-global and project-local skill directories and
// returns a deduped Catalog. workdir may be empty, in which case only
// user-global skills are returned.
//
// Errors reading individual SKILL.md files are logged via the returned
// `warnings` slice rather than aborting discovery — one malformed skill
// shouldn't block all the others.
func Discover(workdir string) (*Catalog, []string, error) {
	cat := &Catalog{byName: make(map[string]*Skill)}
	var warnings []string

	// Project-local takes precedence; load it first so user-global entries
	// can't shadow it.
	if workdir != "" {
		dir := filepath.Join(workdir, ".localagent", "skills")
		ws, err := loadDir(dir, SourceProject, cat)
		warnings = append(warnings, ws...)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return cat, warnings, fmt.Errorf("read project skills: %w", err)
		}
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		dir := filepath.Join(home, ".localagent", "skills")
		ws, err := loadDir(dir, SourceUser, cat)
		warnings = append(warnings, ws...)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return cat, warnings, fmt.Errorf("read user skills: %w", err)
		}
	}

	// Stable order: project before user (insertion order is preserved in
	// cat.order), alphabetical within each source.
	sort.SliceStable(cat.order, func(i, j int) bool {
		si, sj := cat.byName[cat.order[i]], cat.byName[cat.order[j]]
		if si.Source != sj.Source {
			return si.Source == SourceProject
		}
		return si.Name < sj.Name
	})
	return cat, warnings, nil
}

func loadDir(dir string, src Source, cat *Catalog) ([]string, error) {
	var warnings []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return warnings, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillPath := filepath.Join(dir, e.Name(), "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // directory without a SKILL.md — skip silently
			}
			warnings = append(warnings, fmt.Sprintf("%s: %v", skillPath, err))
			continue
		}
		sk, err := parse(data)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", skillPath, err))
			continue
		}
		sk.Path = skillPath
		sk.Source = src
		// Project-local skills already in the catalog win — skip user-global
		// shadows.
		if existing, ok := cat.byName[sk.Name]; ok {
			if existing.Source == SourceProject {
				continue
			}
		}
		if _, ok := cat.byName[sk.Name]; !ok {
			cat.order = append(cat.order, sk.Name)
		}
		cat.byName[sk.Name] = sk
	}
	return warnings, nil
}

// --- frontmatter parser -----------------------------------------------------

// frontmatterRe matches a YAML-style frontmatter block at the start of a
// file: a `---` line, content, a `---` line. We anchor on \A so it must be
// the very first content. (?s) lets `.` cross newlines.
var frontmatterRe = regexp.MustCompile(`(?s)\A---\s*\n(.*?)\n---\s*(?:\n|\z)`)

// fieldRe matches a single top-level YAML scalar like `name: foo` or
// `description: lots of words`. Multi-line YAML values via `|` or `>` are
// folded back into one line — sufficient for our two-field schema.
var fieldRe = regexp.MustCompile(`(?m)^([a-zA-Z_][a-zA-Z0-9_-]*)\s*:\s*(.*?)\s*$`)

// parse extracts name + description from frontmatter and returns the body.
// Returns an error if frontmatter is missing or `name` is empty.
func parse(data []byte) (*Skill, error) {
	m := frontmatterRe.FindSubmatchIndex(data)
	if m == nil {
		return nil, errors.New("missing YAML frontmatter (---...--- block at top)")
	}
	front := string(data[m[2]:m[3]])
	body := strings.TrimSpace(string(data[m[1]:]))

	sk := &Skill{Body: body}
	currentKey := ""
	var currentVal strings.Builder
	flush := func() {
		v := strings.TrimSpace(currentVal.String())
		switch currentKey {
		case "name":
			sk.Name = v
		case "description":
			sk.Description = v
		}
		currentKey = ""
		currentVal.Reset()
	}
	for _, line := range strings.Split(front, "\n") {
		// Lines that look like a new `key: value` start a new field.
		if mm := fieldRe.FindStringSubmatch(line); mm != nil && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			flush()
			currentKey = mm[1]
			currentVal.WriteString(mm[2])
			continue
		}
		// Continuation line of the current field — common for description
		// blocks that wrap across many lines.
		if currentKey != "" {
			if currentVal.Len() > 0 {
				currentVal.WriteByte(' ')
			}
			currentVal.WriteString(strings.TrimSpace(line))
		}
	}
	flush()

	if sk.Name == "" {
		return nil, errors.New("frontmatter missing required field `name`")
	}
	if !validNameRe.MatchString(sk.Name) {
		return nil, fmt.Errorf("invalid skill name %q (must match %s)", sk.Name, validNameRe)
	}
	return sk, nil
}

// validNameRe matches the lowercase-kebab-case naming convention used by
// Claude Code skills (and by both example skills we looked at). Strict
// because skill names appear in slash commands and tool arguments.
var validNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// --- slash-command parsing --------------------------------------------------

// slashRe matches a leading `/skill-name` token, optionally followed by
// whitespace and the rest of the goal.
var slashRe = regexp.MustCompile(`^/([a-z][a-z0-9-]{0,63})(?:\s+|$)`)

// ParseSlashCommands strips leading `/skill-name` tokens from goal and
// returns (stripped_goal, found_names). Unknown skill names are NOT
// stripped — they stay in the goal and `found` only contains names that
// matched the catalog. Up to 8 leading commands are honored to avoid silly
// inputs spinning forever.
func ParseSlashCommands(goal string, cat *Catalog) (string, []string) {
	if cat == nil {
		return goal, nil
	}
	var found []string
	g := strings.TrimSpace(goal)
	for i := 0; i < 8; i++ {
		m := slashRe.FindStringSubmatch(g)
		if m == nil {
			break
		}
		name := m[1]
		if cat.Get(name) == nil {
			break // leave unknown commands alone — model may want to see them
		}
		// Avoid duplicates while preserving order.
		seen := false
		for _, n := range found {
			if n == name {
				seen = true
				break
			}
		}
		if !seen {
			found = append(found, name)
		}
		g = strings.TrimSpace(g[len(m[0]):])
	}
	return g, found
}
