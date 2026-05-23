// Command oracle-task is the oracle role's handoff CLI in the agentbus multi-agent
// system. The oracle calls it to send guidance back to the worker, to forward to the
// inspector with a task-status assessment, or to escalate to the orchestrator.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/jessevdk/go-flags"

	"github.com/umputun/ralphex/pkg/agentbus"
)

const toolName = agentbus.ToolOracle

type toWorkerCmd struct {
	Handoff string `long:"handoff" required:"true" description:"guidance for the worker"`
}

func (c *toWorkerCmd) Execute([]string) error {
	return submit(agentbus.RoleWorker, agentbus.StatusUnset, c.Handoff, "worker")
}

type toInspectorCmd struct {
	TaskStatus string `long:"task-status" choice:"incomplete" choice:"done" required:"true" description:"oracle's assessment of task completion"`
	Handoff    string `long:"handoff" required:"true" description:"message for the inspector"`
}

func (c *toInspectorCmd) Execute([]string) error {
	return submit(agentbus.RoleInspector, agentbus.TaskStatus(c.TaskStatus), c.Handoff, "inspector")
}

type toOrchestratorCmd struct {
	Handoff string `long:"handoff" required:"true" description:"message for the orchestrator"`
}

func (c *toOrchestratorCmd) Execute([]string) error {
	return submit(agentbus.RoleOrchestrator, agentbus.StatusUnset, c.Handoff, "orchestrator")
}

func submit(to agentbus.Role, status agentbus.TaskStatus, message, label string) error {
	h, err := agentbus.SubmitHandoff(toolName, to, status, "", message)
	if err != nil {
		return err
	}
	fmt.Printf("handed off to %s (seq %d)\n", label, h.Seq)
	return nil
}

type options struct {
	ToWorker       toWorkerCmd       `command:"to-worker" description:"send guidance back to the worker"`
	ToInspector    toInspectorCmd    `command:"to-inspector" description:"forward to the inspector with a status assessment"`
	ToOrchestrator toOrchestratorCmd `command:"to-orchestrator" description:"escalate to the orchestrator"`
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
