package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/andrew/localagent/internal/skills"
	"github.com/andrew/localagent/internal/tools"
)

// newActivateSkillTool returns a tools.Tool that loads a skill's body on
// demand. The handler closes over `active`, a session-scoped map of which
// skills are currently activated. Re-activating an already-active skill is a
// no-op — we return a short confirmation rather than reinjecting the body
// (which would just bloat the conversation).
//
// The tool returns the full SKILL.md body as its tool_result content. The
// model sees the body in its next iteration's input and follows the new
// instructions; subsequent iterations keep the body in conversation history
// so the activation persists for the run (and survives /continue because we
// persist messages).
//
// `inactive` is the list of skill names not yet active when the catalog was
// presented to the model. It's used for both the tool's description (so the
// model knows what's available) and for input validation. New activations
// mutate `active` under `mu`.
func newActivateSkillTool(cat *skills.Catalog, active map[string]bool, mu *sync.Mutex, emit func(Event)) tools.Tool {
	// Build the menu shown in the tool description so the model knows what
	// names are valid. We list every catalog entry, marking active ones.
	var menu strings.Builder
	names := cat.Names()
	sort.Strings(names)
	for _, n := range names {
		sk := cat.Get(n)
		state := "available"
		if active[n] {
			state = "already active"
		}
		fmt.Fprintf(&menu, "\n- %s (%s): %s", n, state, flattenDescription(sk.Description))
	}

	enum := append([]string(nil), names...)

	return tools.Tool{
		Name: "activate_skill",
		Description: "Load the full instructions for one of the available skills into your context. " +
			"Use this when a skill's catalog description suggests it would help with the current task. " +
			"After activation, follow the skill's instructions for the rest of the session.\n" +
			"Available skills:" + menu.String(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"skill_name": map[string]any{
					"type":        "string",
					"description": "Name of the skill to activate. Must be one of the listed skills.",
					"enum":        enum,
				},
			},
			"required": []string{"skill_name"},
		},
		Handler: func(_ context.Context, args string) (string, error) {
			var in struct {
				SkillName string `json:"skill_name"`
			}
			dec := json.NewDecoder(strings.NewReader(args))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&in); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if in.SkillName == "" {
				return "", errors.New("skill_name is required")
			}
			sk := cat.Get(in.SkillName)
			if sk == nil {
				return "", fmt.Errorf("unknown skill %q — see the activate_skill description for the list", in.SkillName)
			}

			mu.Lock()
			alreadyActive := active[in.SkillName]
			if !alreadyActive {
				active[in.SkillName] = true
			}
			mu.Unlock()

			if alreadyActive {
				return fmt.Sprintf("Skill %q is already active for this session — its instructions are above in the conversation. No need to re-activate.", in.SkillName), nil
			}

			ev := newEvent(EventSkill)
			ev.Skill = sk.Name
			ev.Text = sk.Description
			emit(ev)

			return fmt.Sprintf("Skill %q activated. Follow these instructions for the rest of the session:\n\n%s", sk.Name, sk.Body), nil
		},
	}
}

// renderActiveSkillsBlock returns the markdown block injected into the system
// prompt for skills pre-activated at session start. Order is stable.
func renderActiveSkillsBlock(cat *skills.Catalog, activeNames []string) string {
	if len(activeNames) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Active skills\n\nThe following skills are loaded for this session — follow their instructions throughout.\n")
	for _, n := range activeNames {
		sk := cat.Get(n)
		if sk == nil {
			continue
		}
		fmt.Fprintf(&b, "\n### Skill: %s\n\n%s\n", sk.Name, strings.TrimSpace(sk.Body))
	}
	return b.String()
}

// renderInactiveSkillsCatalog returns a brief catalog of not-yet-active
// skills shown in the system prompt. Bodies are NOT included — the model
// must call activate_skill to load them. Returns "" if every skill is
// already active.
func renderInactiveSkillsCatalog(cat *skills.Catalog, active map[string]bool) string {
	if cat == nil {
		return ""
	}
	var items []string
	for _, n := range cat.Names() {
		if active[n] {
			continue
		}
		sk := cat.Get(n)
		items = append(items, fmt.Sprintf("- `%s`: %s", sk.Name, flattenDescription(sk.Description)))
	}
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Available skills (call activate_skill to load)\n\n")
	b.WriteString(strings.Join(items, "\n"))
	return b.String()
}

// flattenDescription collapses internal whitespace so a multi-line skill
// description fits on one line inside a menu or prompt. Different from
// compactor.go's `oneline`, which preserves line breaks as a marker glyph.
func flattenDescription(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// dedupKnownSkills filters `names` to only those present in `cat`, preserving
// order and removing duplicates. Returns nil when cat is nil.
func dedupKnownSkills(cat *skills.Catalog, names []string) []string {
	if cat == nil {
		return nil
	}
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if seen[n] {
			continue
		}
		if cat.Get(n) == nil {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// hasInactiveSkills reports whether the catalog contains any skill not in
// the active set.
func hasInactiveSkills(cat *skills.Catalog, active map[string]bool) bool {
	for _, n := range cat.Names() {
		if !active[n] {
			return true
		}
	}
	return false
}
