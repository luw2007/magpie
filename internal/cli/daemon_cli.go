package cli

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/yetone/magpie/internal/daemon"
	"github.com/yetone/magpie/internal/gateway"
)

const daemonUsage = "usage: magpie daemon [install|uninstall|start|stop|restart|status]"

// daemonWait is how long install, start and restart wait for the gateway
// to answer before saying where its log is; a var for the tests.
var daemonWait = 5 * time.Second

// magpie daemon …: `magpie serve` as a service of the user's, started at
// boot or login and again when it stops (systemd --user, launchd).
func daemonCmd(args []string) error {
	if len(args) > 1 {
		return errors.New(daemonUsage)
	}
	cmd := "status"
	if len(args) == 1 {
		cmd = args[0]
	}
	var err error
	switch cmd {
	case "status":
		return daemonStatus()
	case "install":
		err = daemon.Install()
	case "uninstall", "remove":
		if err := daemon.Uninstall(); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), "the magpie service is removed")
		return nil
	case "start":
		err = daemon.Start()
	case "stop":
		if err := daemon.Stop(); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), "the magpie service is stopped")
		return nil
	case "restart":
		err = daemon.Restart()
	default:
		return errors.New(daemonUsage)
	}
	if err != nil {
		return err
	}
	st, _ := daemon.Get()
	switch cmd {
	case "install":
		fmt.Println(green.Render("✓"), "magpie serves the gateway as a service, from boot or login, started again when it stops")
		fmt.Println(muted.Render("  file   "), st.File)
		if a := daemon.Linger(); a != "" {
			fmt.Println(amber.Render("!"), a)
		}
		if daemon.Node() == "" {
			fmt.Println(amber.Render("!"), "no node on the service's PATH: plugins, and the MCP servers they start, need Node.js; install it, then run `magpie daemon install` again")
		}
	default:
		fmt.Println(green.Render("✓"), "the magpie service is "+cmd+"ed")
	}
	deadline := time.Now().Add(daemonWait)
	for {
		if o := gateway.ServedBy(); o.Running {
			fmt.Println(green.Render("●"), "the gateway answers on", bold.Render(gateway.URL()))
			return nil
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	fmt.Println(amber.Render("!"), "the gateway doesn't answer on", gateway.URL(), "yet ·", st.Logs)
	return nil
}

func daemonStatus() error {
	st, err := daemon.Get()
	if err != nil {
		return err
	}
	served := gateway.ServedBy()
	if !st.Installed {
		fmt.Println("the magpie service isn't installed ·", muted.Render("magpie daemon install"))
		if served.Running {
			fmt.Println(muted.Render("  another magpie (the app, magpie serve) answers on"), gateway.URL())
		}
		return nil
	}
	mark, word := amber.Render("○"), "not running"
	if st.Running {
		mark, word = green.Render("●"), "running"
	}
	line := "the magpie service is " + word
	if st.PID > 0 {
		line += " · pid " + strconv.Itoa(st.PID)
	}
	fmt.Println(mark, line)
	if st.State != "" {
		fmt.Println(muted.Render("  state  "), st.State)
	}
	if st.LastExit != "" {
		fmt.Println(muted.Render("  last exit"), st.LastExit)
	}
	start := "at boot or login"
	if !st.Enabled {
		start = "only when started"
	}
	fmt.Println(muted.Render("  start  "), start)
	fmt.Println(muted.Render("  file   "), st.File)
	fmt.Println(muted.Render("  logs   "), st.Logs)
	if served.Running {
		fmt.Println(muted.Render("  gateway"), "answers on", gateway.URL(), muted.Render(served.Version))
	} else {
		fmt.Println(muted.Render("  gateway"), "doesn't answer on", gateway.URL())
	}
	for _, a := range st.Advice {
		fmt.Println(amber.Render("!"), a)
	}
	return nil
}
