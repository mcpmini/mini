package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
)

type connectChecked struct {
	id       int
	removals initcmd.Removals
}

// connectChecks runs Connect's connection checks in the background, one run at a time.
type connectChecks struct {
	runs     int
	stop     context.CancelFunc
	ended    chan struct{}
	stopping []chan struct{}
	done     bool
	removals initcmd.Removals
}

func (c *connectChecks) start(plan connectPlan) tea.Cmd {
	c.cancel()
	ctx, stop := context.WithCancel(context.Background())
	c.runs++
	id, ended, result := c.runs, make(chan struct{}), make(chan initcmd.Removals, 1)
	c.stop, c.ended, c.done, c.removals = stop, ended, false, initcmd.Removals{}
	go func() {
		defer close(ended)
		result <- plan.Check(ctx)
	}()
	return func() tea.Msg {
		return connectChecked{id: id, removals: <-result}
	}
}

func (c *connectChecks) finished(msg connectChecked) {
	if msg.id == c.runs && c.stop != nil {
		c.done, c.removals = true, msg.removals
		c.stop, c.ended = nil, nil
	}
}

// Closing a probed server can take a moment, so leaving Connect doesn't wait for it; Run does.
func (c *connectChecks) cancel() {
	if c.stop == nil {
		return
	}
	c.stop()
	c.stopping = append(c.stopping, c.ended)
	c.stop, c.ended = nil, nil
}

func (c *connectChecks) cancelAndWait() {
	c.cancel()
	for _, ended := range c.stopping {
		<-ended // a probe can start a server's process, which init kills before it exits
	}
	c.stopping = nil
}
