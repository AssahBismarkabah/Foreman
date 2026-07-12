// Package policy defines configurable rules for tool-use approval.
//
// A policy matches an agent tool call by tool name (glob pattern) and
// optional input field regexes. When a tool_use event matches a policy
// whose action is "require_approval", the coordinator enters a pending
// approval state and waits for a human decision.
package policy

import (
	"fmt"
	"path/filepath"
	"regexp"
	"time"
)

// Config is a single policy rule, typically deserialised from YAML.
type Config struct {
	Name    string        `yaml:"name"`
	Match   MatchDef      `yaml:"match"`
	Action  string        `yaml:"action"`  // "require_approval"
	Timeout time.Duration `yaml:"timeout"` // zero = use DefaultTimeout
}

// MatchDef describes which tool calls this policy applies to.
type MatchDef struct {
	Tool   string            `yaml:"tool"`   // glob pattern, e.g. "git.*"
	Inputs map[string]string `yaml:"inputs"` // optional field -> regex
}

// Policy is a compiled, ready-to-use policy rule.
type Policy struct {
	Config
	toolGlob   func(tool string) bool
	inputRegex map[string]*regexp.Regexp
}

// CompileConfigs compiles YAML configs into runnable Policy values.
func CompileConfigs(cfgs []Config) ([]Policy, error) {
	if len(cfgs) == 0 {
		return nil, nil
	}
	policies := make([]Policy, 0, len(cfgs))
	for _, cfg := range cfgs {
		p := Policy{Config: cfg}

		// Compile tool glob.
		if cfg.Match.Tool == "" || cfg.Match.Tool == "*" {
			p.toolGlob = func(string) bool { return true }
		} else {
			pat := cfg.Match.Tool
			p.toolGlob = func(tool string) bool {
				ok, _ := filepath.Match(pat, tool)
				return ok
			}
		}

		// Compile input-field regexes.
		p.inputRegex = make(map[string]*regexp.Regexp, len(cfg.Match.Inputs))
		for field, pat := range cfg.Match.Inputs {
			re, err := regexp.Compile(pat)
			if err != nil {
				return nil, fmt.Errorf("policy %q: regex for field %q: %w", cfg.Name, field, err)
			}
			p.inputRegex[field] = re
		}

		policies = append(policies, p)
	}
	return policies, nil
}

// DefaultTimeout returns the configured timeout or 5 minutes.
func (p *Policy) DefaultTimeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return 5 * time.Minute
}

// Matches checks whether a tool_use event matches this policy.
func (p *Policy) Matches(tool string, inputs map[string]any) bool {
	if !p.toolGlob(tool) {
		return false
	}
	for field, re := range p.inputRegex {
		val, ok := inputs[field]
		if !ok {
			return false
		}
		if !re.MatchString(fmt.Sprint(val)) {
			return false
		}
	}
	return true
}

// ToolUse is a simplified tool_use event used for policy matching.
type ToolUse struct {
	Tool   string         `json:"tool"`
	Inputs map[string]any `json:"inputs,omitempty"`
}

// MatchResult describes a single policy match found in agent output.
type MatchResult struct {
	Policy  Policy
	ToolUse ToolUse
}
