// Command worker-task is the worker role's handoff CLI in the agentbus multi-agent system.
// The worker calls it to finish a task (handing off to the inspector), to consult the
// oracle, or to restate the current task. It authenticates with the worker's own token.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/jessevdk/go-flags"

	"github.com/umputun/ralphex/pkg/agentbus"
)

const toolName = agentbus.ToolWorker

type noFurtherActionsCmd struct {
	ConfirmCurrent string `long:"confirm-current" required:"true" description:"restate the current task verbatim to confirm what was done"`
	Handoff        string `long:"handoff" required:"true" description:"message to the inspector"`
}

func (c *noFurtherActionsCmd) Execute([]string) error {
	h, err := agentbus.SubmitHandoff(toolName, agentbus.RoleInspector, agentbus.StatusUnset, c.ConfirmCurrent, c.Handoff)
	if err != nil {
		return err
	}
	fmt.Printf("handed off to inspector (seq %d)\n", h.Seq)
	return nil
}

type askOracleCmd struct {
	Handoff string `long:"handoff" required:"true" description:"question for the oracle"`
}

func (c *askOracleCmd) Execute([]string) error {
	h, err := agentbus.SubmitHandoff(toolName, agentbus.RoleOracle, agentbus.StatusUnset, "", c.Handoff)
	if err != nil {
		return err
	}
	fmt.Printf("asked oracle (seq %d)\n", h.Seq)
	return nil
}

type restateCurrentCmd struct{}

func (c *restateCurrentCmd) Execute([]string) error {
	if err := agentbus.Authenticate(toolName); err != nil {
		return err
	}
	task, err := agentbus.CurrentTask()
	if err != nil {
		return err
	}
	fmt.Printf("Task %s: %s\n\nInstructions:\n%s\n\nMinimum tests:\n", task.ID, task.Summary, task.Instructions)
	for _, mt := range task.MinTests {
		fmt.Printf("  - %s\n", mt)
	}
	return nil
}

type options struct {
	NoFurtherActions noFurtherActionsCmd `command:"no_further_actions" description:"declare the task complete and hand off to the inspector"`
	AskOracle        askOracleCmd        `command:"ask_oracle" description:"ask the oracle for guidance"`
	RestateCurrent   restateCurrentCmd   `command:"restate_current" description:"print the current task"`
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
