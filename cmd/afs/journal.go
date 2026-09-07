package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"agentsfs.ai/afs/internal/core"
)

const journalUsage = `usage: afs journal <begin|list|checkpoint|finish|prepare|consolidate|recover> [options] [path]
  begin --session KEY --description TEXT [path]
  list [path]
  checkpoint --id ID --expect HASH --body FILE [--status running|interrupted] [path]
  finish --id ID --expect HASH --body FILE [path]
  prepare [path] > plan.json
  consolidate --plan FILE --bootstrap FILE [path]
  recover [path]
Options precede the optional instance path. JSON output includes IDs, paths, and hashes.
These commands write local files only; review, commit, and sync the completed work.`

func runJournal(args []string) {
	if len(args) == 0 {
		fmt.Println(journalUsage)
		return
	}
	verb := args[0]
	f := flag.NewFlagSet("journal "+verb, flag.ContinueOnError)
	session := f.String("session", "", "stable harness trajectory key; reuse on resume")
	description := f.String("description", "", "what this episode is for")
	id := f.String("id", "", "episode ID")
	expected := f.String("expect", "", "hash from the last read")
	bodyFile := f.String("body", "", "Markdown checkpoint body file")
	status := f.String("status", "running", "running or interrupted")
	planFile := f.String("plan", "", "prepared JSON plan file")
	bootstrapFile := f.String("bootstrap", "", "proposed complete bootstrap Markdown file")
	if err := f.Parse(args[1:]); err != nil {
		fail(err)
	}
	if len(f.Args()) > 1 {
		fail(fmt.Errorf("%s", journalUsage))
	}
	root := instanceRoot(f.Args(), 0)
	var out any
	var err error
	switch verb {
	case "begin":
		out, err = core.BeginEpisode(root, *session, *description)
	case "list":
		out, err = core.JournalEpisodes(root)
	case "checkpoint", "finish":
		var body []byte
		body, err = os.ReadFile(*bodyFile)
		if err == nil {
			if verb == "finish" {
				*status = "complete"
			}
			out, err = core.CheckpointEpisode(root, *id, *expected, string(body), *status)
		}
	case "prepare":
		out, err = core.PrepareJournal(root)
	case "consolidate":
		var b []byte
		b, err = os.ReadFile(*planFile)
		var plan core.JournalPlan
		if err == nil {
			err = json.Unmarshal(b, &plan)
		}
		if err == nil {
			b, err = os.ReadFile(*bootstrapFile)
		}
		if err == nil {
			err = core.ConsolidateJournal(root, plan, b)
		}
		out = map[string]any{"consolidated": len(plan.Episodes), "bootstrap": plan.Journal + "/bootstrap.md"}
	case "recover":
		err = core.RecoverJournal(root)
		out = map[string]bool{"recovered": err == nil}
	default:
		err = fmt.Errorf("%s", journalUsage)
	}
	if err != nil {
		fail(err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err = enc.Encode(out); err != nil {
		fail(err)
	}
}
