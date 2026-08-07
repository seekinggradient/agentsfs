package core

import "strings"

// Rendering and summary helpers shared by the backlog's two readers: `afs
// tasks` (the full derived view) and `afs prime` (its top slice, inside the
// orientation pack). One task has to look the same in both — an agent reads
// them in the same session — so the line format lives here rather than being
// written twice and drifting apart. How the lines are GROUPED is each view's
// own business; what a task looks like is not.

// TaskLine renders one task: the checkbox marker exactly as the page writes it
// (so [/] and [-] survive the round trip through a derived view instead of
// being flattened into open/done), the author's text untouched, and the slug
// bracketed at the end. The caret stays inside the brackets because ^slug is
// what a reference is written from — [[#^slug]] — so a reader can build one
// from what the view showed them.
func TaskLine(t *Task) string {
	line := "[" + taskMarker(t.Status) + "] " + t.Text
	if t.Slug != "" {
		line += " [^" + t.Slug + "]"
	}
	return line
}

func taskMarker(s TaskStatus) string {
	switch s {
	case TaskInProgress:
		return "/"
	case TaskDone:
		return "x"
	case TaskDropped:
		return "-"
	default:
		return " "
	}
}

// TaskCounts summarizes the parts of the backlog a working view does not list:
// what is held, what is parked, what is finished. The default `afs tasks` view
// shows in-progress and ready work and reports the rest as these three numbers,
// so the page's size never leaks into its orientation value.
type TaskCounts struct {
	Blocked int // unfinished tasks carrying an active blocker of their own
	Parked  int // unfinished tasks in the Someday band (the parking lot)
	Done    int
}

// Counts computes the summary. A parked task that also carries a blocker counts
// in both columns: the two numbers answer different questions ("how much work is
// held up" and "how much did we deliberately shelve"), and forcing them to
// partition would make each one lie about its own question. Descendants of a
// blocked ancestor are not counted as blocked — the count is of blockers
// written on the page, which is what a reader can act on.
func (b *Backlog) Counts() TaskCounts {
	var c TaskCounts
	if b == nil {
		return c
	}
	for _, t := range b.Flat() {
		if t.Status == TaskDone {
			c.Done++
			continue
		}
		if t.Status == TaskDropped {
			continue
		}
		if t.BlockedActive {
			c.Blocked++
		}
		if strings.EqualFold(strings.TrimSpace(t.Band), BandSomeday) {
			c.Parked++
		}
	}
	return c
}

// TasksInBand returns every task in a band, document order, matched
// case-insensitively — the page writes "## Now", a caller types `--band now`.
func (b *Backlog) TasksInBand(band string) []*Task {
	want := strings.ToLower(strings.TrimSpace(band))
	var out []*Task
	for _, t := range b.Flat() {
		if strings.ToLower(strings.TrimSpace(t.Band)) == want {
			out = append(out, t)
		}
	}
	return out
}
