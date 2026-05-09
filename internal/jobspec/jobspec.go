// Package jobspec defines the canonical job-template shape and validation
// rules used by both the REST API (POST /v1/jobs and POST /v1/scheduled-jobs)
// and any client that wants to validate a spec before submitting.
package jobspec

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// JobSpec is the canonical representation of a job template, used by both
// user-submitted jobs and scheduled-job templates. Matches createJobRequest.
type JobSpec struct {
	Name     string            `json:"name"`
	Priority string            `json:"priority"`
	Labels   map[string]string `json:"labels"`
	Tasks    []TaskSpec        `json:"tasks"`
}

// TaskSpec mirrors the existing taskSpec type, exported for reuse.
//
// A task carries one or more commands that the agent runs sequentially in the
// same workspace and environment. Specs may set EITHER the legacy single
// Command (a one-element argv) OR the multi-command Commands. Setting both is
// rejected by Validate; a single Command is normalized into a one-element
// Commands so downstream code only deals with Commands.
type TaskSpec struct {
	Name           string            `json:"name"`
	Command        []string          `json:"command,omitempty"`
	Commands       [][]string        `json:"commands,omitempty"`
	Env            map[string]string `json:"env"`
	Requires       map[string]string `json:"requires"`
	TimeoutSeconds *int32            `json:"timeout_seconds"`
	Retries        int32             `json:"retries"`
	DependsOn      []string          `json:"depends_on"`
	Source         *SourceSpec       `json:"source,omitempty"`
}

// SourceSpec describes how to prepare a workspace before the task runs.
type SourceSpec struct {
	Type               string      `json:"type"`
	Stream             string      `json:"stream,omitempty"`
	Sync               []SyncEntry `json:"sync,omitempty"`
	Unshelves          []int64     `json:"unshelves,omitempty"`
	WorkspaceExclusive bool        `json:"workspace_exclusive,omitempty"`
	ClientTemplate     *string     `json:"client_template,omitempty"`
}

// SyncEntry is a single depot path + revision to sync.
type SyncEntry struct {
	Path string `json:"path"`
	Rev  string `json:"rev"`
}

var (
	revHeadRe    = regexp.MustCompile(`^#head$`)
	revCLRe      = regexp.MustCompile(`^@\d+$`)
	revLabelRe   = regexp.MustCompile(`^@[A-Za-z0-9._-]+$`)
	revNumRe     = regexp.MustCompile(`^#\d+$`)
	clientTmplRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

// Validate applies the same checks as POST /v1/jobs and normalizes each
// task's command form: a legacy single Command is rewritten into a one-element
// Commands and Command is cleared. Setting both Command and Commands is
// rejected. Validate mutates spec to apply normalization. Returns the first
// problem found, or nil.
func Validate(spec *JobSpec) error {
	if spec.Name == "" {
		return errors.New("name is required")
	}
	if len(spec.Tasks) == 0 {
		return errors.New("at least one task is required")
	}
	nameSet := make(map[string]struct{}, len(spec.Tasks))
	for i := range spec.Tasks {
		ts := &spec.Tasks[i]
		if ts.Name == "" {
			return errors.New("task name is required")
		}
		if err := normalizeTaskCommands(ts); err != nil {
			return fmt.Errorf("task %s: %w", ts.Name, err)
		}
		if _, dup := nameSet[ts.Name]; dup {
			return fmt.Errorf("duplicate task name: %s", ts.Name)
		}
		nameSet[ts.Name] = struct{}{}
	}
	for _, ts := range spec.Tasks {
		for _, dep := range ts.DependsOn {
			if _, ok := nameSet[dep]; !ok {
				return fmt.Errorf("unknown depends_on: %s", dep)
			}
		}
	}
	for _, ts := range spec.Tasks {
		if err := validateSourceSpec(ts.Source); err != nil {
			return fmt.Errorf("task %s: %w", ts.Name, err)
		}
	}
	return nil
}

// normalizeTaskCommands enforces command-form rules and collapses to Commands.
func normalizeTaskCommands(ts *TaskSpec) error {
	hasCommand := len(ts.Command) > 0
	hasCommands := len(ts.Commands) > 0
	switch {
	case hasCommand && hasCommands:
		return errors.New("set either command or commands, not both")
	case hasCommand:
		ts.Commands = [][]string{ts.Command}
		ts.Command = nil
	case !hasCommands:
		return errors.New("commands is required")
	}
	for i, argv := range ts.Commands {
		if len(argv) == 0 {
			return fmt.Errorf("commands[%d]: argv must not be empty", i)
		}
	}
	return nil
}

func validateSourceSpec(s *SourceSpec) error {
	if s == nil {
		return nil
	}
	if s.Type != "perforce" {
		return fmt.Errorf("unsupported source type: %s", s.Type)
	}
	if s.Stream == "" {
		return errors.New("stream is required")
	}
	if !strings.HasPrefix(s.Stream, "//") {
		return errors.New("stream must start with //")
	}
	if len(s.Sync) == 0 {
		return errors.New("source.sync must have at least one sync entry")
	}
	for i, e := range s.Sync {
		if !strings.HasPrefix(e.Path, "//") {
			return fmt.Errorf("sync[%d].path must start with //", i)
		}
		if e.Path != s.Stream &&
			e.Path != s.Stream+"/..." &&
			!strings.HasPrefix(e.Path, s.Stream+"/") {
			return fmt.Errorf("sync[%d].path must be under stream %s", i, s.Stream)
		}
		if !(revHeadRe.MatchString(e.Rev) || revCLRe.MatchString(e.Rev) ||
			revLabelRe.MatchString(e.Rev) || revNumRe.MatchString(e.Rev)) {
			return fmt.Errorf("sync[%d].rev: invalid rev %q", i, e.Rev)
		}
	}
	for i, cl := range s.Unshelves {
		if cl <= 0 {
			return fmt.Errorf("unshelves[%d]: unshelve must be positive", i)
		}
	}
	if s.ClientTemplate != nil && !clientTmplRe.MatchString(*s.ClientTemplate) {
		return fmt.Errorf("invalid client_template %q", *s.ClientTemplate)
	}
	return nil
}
