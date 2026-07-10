// Command build_agent_bundle rebuilds the agentsfs-chat source archive embedded
// by the Hub. Run from the agentsfs repository root:
//
//	go run ./scripts/build_agent_bundle.go -source ../agentsfs-chat
package main

import (
	"flag"
	"fmt"
	"os"

	"agentsfs.ai/afs/internal/hub/agentbundle"
)

func main() {
	source := flag.String("source", "../agentsfs-chat", "agentsfs-chat source directory")
	output := flag.String("output", "internal/hub/agent-bundle.tgz", "bundle output path")
	flag.Parse()
	if err := agentbundle.Build(*source, *output); err != nil {
		fmt.Fprintln(os.Stderr, "build agent bundle:", err)
		os.Exit(1)
	}
	fmt.Println("wrote", *output)
}
