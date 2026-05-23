// Command inspector-task is the inspector role's handoff CLI in the agentbus multi-agent
// system. The inspector calls it to report a verdict to the orchestrator or to consult
// the oracle.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/jessevdk/go-flags"

	"github.com/umputun/ralphex/pkg/agentbus"
)

const toolName = agentbus.ToolInspector

type toOrchestratorCmd struct {
	TaskStatus string `long:"task-status" choice:"incomplete" choice:"done" description:"inspector's verdict on task completion"`
	Handoff    string `long:"handoff" required:"true" description:"report for the orchestrator"`
}

func (c *toOrchestratorCmd) Execute([]string) error {
	return submit(agentbus.RoleOrchestrator, agentbus.TaskStatus(c.TaskStatus), c.Handoff, "orchestrator")
}

type toOracleCmd struct {
	Handoff string `long:"handoff" required:"true" description:"question for the oracle"`
}

func (c *toOracleCmd) Execute([]string) error {
	return submit(agentbus.RoleOracle, agentbus.StatusUnset, c.Handoff, "oracle")
}

func submit(to agentbus.Role, status agentbus.TaskStatus, message, label string) error {
	h, err := agentbus.SubmitHandoff(toolName, to, status, "", message)
	if err != nil {
		return fmt.Errorf("submit handoff: %w", err)
	}
	fmt.Printf("handed off to %s (seq %d)\n", label, h.Seq)
	return nil
}

type options struct {
	ToOrchestrator toOrchestratorCmd `command:"to-orchestrator" description:"report a verdict to the orchestrator"`
	ToOracle       toOracleCmd       `command:"to-oracle" description:"consult the oracle"`
}

func main() {
	var opts options
	if _, err := flags.Parse(&opts); err != nil {
		var fe *flags.Error
		if errors.As(err, &fe) && fe.Type == flags.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
