package core

import (
	"path/filepath"
)

// Cross-instance triage: `afs tasks <search-root>` answers "which project should
// I pick up", not "what is the single most important thing I own". Discovery is
// `afs status`'s machinery verbatim — the same bounded, read-only, symlink-free
// walk with the same scope-completeness honesty — and the result is GROUPED BY
// INSTANCE. No global order is invented across instances: the owner is the
// cross-project ranking function, and inventing one here would quietly claim
// that Now in one repo outranks Now in another.

// tasksFleetReadyCap bounds one instance's ready list. The view is for choosing
// a project; `afs tasks` inside that project is the full list, one command away.
const tasksFleetReadyCap = 10

// TasksFleetReport is the machine-readable cross-instance triage view.
// SearchRoots, Scopes, and Issues are the discovery scan's own report: a JSON
// caller must check every scopes[].complete before reading absence as absence,
// exactly as with StatusReport.
type TasksFleetReport struct {
	SchemaVersion int             `json:"schema_version"`
	SearchRoots   []string        `json:"search_roots"`
	Scopes        []StatusScope   `json:"scopes"`
	Instances     []InstanceTasks `json:"instances"`
	Issues        []StatusIssue   `json:"issues"`
}

// InstanceTasks is one instance's slice of the triage view: what is being
// worked, what is waiting on the owner, and the top of what is ready.
type InstanceTasks struct {
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	// Backlog is the spine's path relative to the instance, "" when the
	// instance declares no backlog at all.
	Backlog      string     `json:"backlog,omitempty"`
	InProgress   []*Task    `json:"in_progress"`
	OwnerBlocked []*Task    `json:"owner_blocked"`
	Ready        []*Task    `json:"ready"`
	ReadyTotal   int        `json:"ready_total"`
	Counts       TaskCounts `json:"counts"`
	// Error is set when this instance's backlog could not be read. One
	// unreadable backlog must not take down the triage view for the fleet, so
	// the instance is still listed, saying why it is empty.
	Error string `json:"error,omitempty"`
}

// TasksAcrossInstances discovers every instance at or below searchRoots and
// returns each one's ready view. Instances with no backlog are listed with an
// empty view rather than dropped: "this project has nothing to pull" and "this
// project is not in the scan" are different answers, and only the first is
// something the reader can act on.
func TasksAcrossInstances(searchRoots []string, opts StatusOptions) (TasksFleetReport, error) {
	status := StatusInstances(searchRoots, opts)
	report := TasksFleetReport{
		SchemaVersion: 1,
		SearchRoots:   status.SearchRoots,
		Scopes:        status.Scopes,
		Issues:        status.Issues,
		Instances:     []InstanceTasks{},
	}
	for _, inst := range status.Instances {
		report.Instances = append(report.Instances, instanceTasks(inst))
	}
	return report, nil
}

func instanceTasks(inst InstanceStatus) InstanceTasks {
	out := InstanceTasks{
		Path:         inst.Path,
		Description:  inst.Description,
		InProgress:   []*Task{},
		OwnerBlocked: []*Task{},
		Ready:        []*Task{},
	}
	b, found, err := LoadBacklog(inst.Path)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if !found {
		return out
	}
	out.Backlog = filepath.ToSlash(b.Spine)
	out.InProgress = append(out.InProgress, b.InProgress()...)
	out.OwnerBlocked = append(out.OwnerBlocked, b.OwnerBlocked()...)
	ready := b.ReadyTasks()
	out.ReadyTotal = len(ready)
	if len(ready) > tasksFleetReadyCap {
		ready = ready[:tasksFleetReadyCap]
	}
	out.Ready = append(out.Ready, ready...)
	out.Counts = b.Counts()
	return out
}
