// Command orchestrator-task is the orchestrator role's CLI in the agentbus multi-agent
// system. It manages the PRD task store, assigns and launches work, rolls back to an
// earlier checkpoint, and runs the supervisor that routes handoffs between roles.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jessevdk/go-flags"

	"github.com/umputun/ralphex/pkg/agentbus"
)

const toolName = agentbus.ToolOrchestrator

type addTaskCmd struct {
	ID           string   `long:"id" required:"true" description:"task id"`
	Summary      string   `long:"summary" required:"true" description:"one-line summary"`
	Instructions string   `long:"instructions" required:"true" description:"descriptive instructions"`
	MinTests     []string `long:"min-test" required:"true" description:"a test that must pass (repeatable)"`
}

func (c *addTaskCmd) Execute([]string) error {
	if err := agentbus.Authenticate(toolName); err != nil {
		return err
	}
	if err := agentbus.AddTask(task(c.ID, c.Summary, c.Instructions, c.MinTests)); err != nil {
		return err
	}
	fmt.Printf("added task %s\n", c.ID)
	return nil
}

type updateTaskCmd struct {
	ID           string   `long:"id" required:"true" description:"task id to update"`
	Summary      string   `long:"summary" required:"true" description:"one-line summary"`
	Instructions string   `long:"instructions" required:"true" description:"descriptive instructions"`
	MinTests     []string `long:"min-test" required:"true" description:"a test that must pass (repeatable)"`
}

func (c *updateTaskCmd) Execute([]string) error {
	if err := agentbus.Authenticate(toolName); err != nil {
		return err
	}
	if err := agentbus.UpdateTask(task(c.ID, c.Summary, c.Instructions, c.MinTests)); err != nil {
		return err
	}
	fmt.Printf("updated task %s\n", c.ID)
	return nil
}

type removeTaskCmd struct {
	ID string `long:"id" required:"true" description:"task id to remove"`
}

func (c *removeTaskCmd) Execute([]string) error {
	if err := agentbus.Authenticate(toolName); err != nil {
		return err
	}
	if err := agentbus.RemoveTask(c.ID); err != nil {
		return err
	}
	fmt.Printf("removed task %s\n", c.ID)
	return nil
}

type assignCmd struct {
	ID string `long:"id" required:"true" description:"task id to assign and run"`
}

func (c *assignCmd) Execute([]string) error {
	if err := agentbus.Authenticate(toolName); err != nil {
		return err
	}
	if err := agentbus.AssignAndRun(context.Background(), agentbus.NewTmuxLauncher(), c.ID); err != nil {
		return err
	}
	fmt.Printf("assigned and launched task %s on the worker\n", c.ID)
	return nil
}

type rollbackCmd struct {
	To string `long:"to" required:"true" description:"git ref to hard-reset the working tree to"`
}

func (c *rollbackCmd) Execute([]string) error {
	if err := agentbus.Authenticate(toolName); err != nil {
		return err
	}
	if err := agentbus.NewGit(agentbus.RepoRoot()).RollbackTo(context.Background(), c.To); err != nil {
		return err
	}
	fmt.Printf("rolled back to %s\n", c.To)
	return nil
}

type serveCmd struct {
	HealthInterval time.Duration `long:"health-interval" default:"30m" description:"how often to send a health-check to the active role"`
}

func (c *serveCmd) Execute([]string) error {
	if !agentbus.RegistryExists() {
		if _, gerr := agentbus.GenerateRegistry(); gerr != nil {
			return gerr
		}
		fmt.Println("generated token registry")
	}

	launcher := agentbus.NewTmuxLauncher()
	roleEnv, err := agentbus.RoleEnvFromRegistry()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := launcher.EnsureSessions(ctx, roleEnv); err != nil {
		return err
	}

	sup := agentbus.NewSupervisor(launcher, agentbus.NewGit(agentbus.RepoRoot()), c.HealthInterval,
		func(format string, args ...any) { fmt.Fprintf(os.Stderr, "[supervisor] "+format+"\n", args...) })
	fmt.Printf("supervisor watching %s (health every %s)\n", agentbus.Dir(), c.HealthInterval)
	if err := sup.Watch(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func task(id, summary, instructions string, minTests []string) agentbus.Task {
	return agentbus.Task{ID: id, Summary: summary, Instructions: instructions, MinTests: minTests}
}

type options struct {
	AddTask    addTaskCmd    `command:"add-task" description:"add a task to the PRD"`
	UpdateTask updateTaskCmd `command:"update-task-definition" description:"update an existing task"`
	RemoveTask removeTaskCmd `command:"remove-task" description:"remove a task"`
	Assign     assignCmd     `command:"assign-and-run-task" description:"assign a task and launch the worker"`
	Rollback   rollbackCmd   `command:"rollback" description:"hard-reset the working tree to a checkpoint"`
	Serve      serveCmd      `command:"serve" description:"generate credentials and run the handoff supervisor"`
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
