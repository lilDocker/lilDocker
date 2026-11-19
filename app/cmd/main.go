package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"text/tabwriter"

	"lildocker/runtime"
)

const version = "0.2.0"

type command struct {
	usage string
	desc  string
	run   func(args []string) int
}

var (
	commands = map[string]command{}
	order    []string
)

func reg(name, usage, desc string, run func([]string) int) {
	commands[name] = command{usage: usage, desc: desc, run: run}
	order = append(order, name)
}

var groups = map[string]string{
	"container": "Manage containers",
	"image":     "Manage images",
	"volume":    "Manage volumes",
	"system":    "Manage lilDocker itself",
}

var unsupported = map[string]string{
	"login":      "there is no registry account support, `pull` only reads public images",
	"logout":     "there is no registry account support",
	"push":       "pushing to a registry is not supported, use `save` and `load` to move images",
	"search":     "searching a registry is not supported",
	"events":     "an event stream is not supported",
	"stats":      "live resource stats are not supported, try `top`",
	"network":    "containers share the host network, use -p to publish ports",
	"compose":    "compose is not supported",
	"swarm":      "swarm is not supported",
	"service":    "swarm is not supported",
	"stack":      "swarm is not supported",
	"node":       "swarm is not supported",
	"secret":     "swarm is not supported",
	"config":     "swarm is not supported",
	"context":    "there is only one local engine",
	"plugin":     "plugins are not supported",
	"buildx":     "use `build`",
	"builder":    "use `build`",
	"manifest":   "manifests are not supported",
	"trust":      "content trust is not supported",
	"checkpoint": "checkpoints are not supported",
}

func init() {
	reg("run", "run [options] <image> [command...]", "Create and start a container", cmdRun)
	reg("create", "create [options] <image> [command...]", "Create a container without starting it", cmdCreate)
	reg("start", "start [-a] <container>...", "Start stopped containers", cmdStart)
	reg("stop", "stop [-t seconds] <container>...", "Stop running containers", cmdStop)
	reg("restart", "restart [-t seconds] <container>...", "Restart containers", cmdRestart)
	reg("kill", "kill [-s signal] <container>...", "Send a signal to containers", cmdKill)
	reg("pause", "pause <container>...", "Freeze the processes of containers", cmdPause)
	reg("unpause", "unpause <container>...", "Resume paused containers", cmdUnpause)
	reg("rm", "rm [-f] <container>...", "Remove containers", cmdRm)
	reg("ps", "ps [options]", "List containers", cmdPs)
	reg("logs", "logs [options] <container>", "Print a container's output", cmdLogs)
	reg("attach", "attach <container>", "Follow a container's output", cmdAttach)
	reg("exec", "exec [options] <container> <command>...", "Run a command inside a container's filesystem", cmdExec)
	reg("inspect", "inspect [-f format] <name>...", "Show details of containers, images or volumes", cmdInspect)
	reg("top", "top <container>", "Show a container's process", cmdTop)
	reg("port", "port <container>", "List port mappings", cmdPort)
	reg("rename", "rename <container> <new-name>", "Rename a container", cmdRename)
	reg("wait", "wait <container>...", "Wait for containers to stop and print their exit codes", cmdWait)
	reg("cp", "cp <container>:<path> <dest>  |  cp <src> <container>:<path>", "Copy files between a container and your machine", cmdCp)
	reg("diff", "diff <container>", "Show files changed since the container was created", cmdDiff)
	reg("commit", "commit <container> <image[:tag]>", "Save a container's filesystem as a new image", cmdCommit)
	reg("export", "export [-o file] <container>", "Write a container's filesystem to a tar file", cmdExport)
	reg("update", "update --restart <policy> <container>...", "Change a container's restart policy", cmdUpdate)
	reg("build", "build [options] <path>", "Build an image from a lilDockfile", cmdBuild)
	reg("images", "images [options] [name]", "List images", cmdImages)
	reg("rmi", "rmi [-f] <image>...", "Remove images", cmdRmi)
	reg("tag", "tag <source> <target[:tag]>", "Give an image another name", cmdTag)
	reg("history", "history <image>", "Show the instructions an image was built from", cmdHistory)
	reg("save", "save [-o file] <image>", "Write an image to a tar file", cmdSave)
	reg("load", "load [-i file]", "Load an image from a tar file", cmdLoad)
	reg("import", "import <file|-> <image[:tag]>", "Create an image from a filesystem tarball", cmdImport)
	reg("pull", "pull <image[:tag]>", "Download an image from a registry", cmdPull)
	reg("prune", "prune [-f]", "Remove all stopped containers", func(a []string) int { return cmdContainerPrune(a) })
	reg("info", "info", "Show system information", cmdInfo)
	reg("version", "version", "Show the version", cmdVersion)
}

func printHelp() {
	fmt.Printf("lilDocker %s, a small container tool\n\nUsage:  lildocker <command> [options]\n\nCommands:\n", version)
	tw := tabwriter.NewWriter(os.Stdout, 0, 8, 3, ' ', 0)
	for _, n := range order {
		fmt.Fprintf(tw, "  %s\t%s\n", n, commands[n].desc)
	}
	tw.Flush()
	fmt.Println("\nManagement commands:")
	var gs []string
	for g := range groups {
		gs = append(gs, g)
	}
	sort.Strings(gs)
	for _, g := range gs {
		fmt.Fprintf(tw, "  %s\t%s\n", g, groups[g])
	}
	tw.Flush()
	fmt.Println("\nRun 'lildocker <command> --help' for details on a command.")
	fmt.Println("Data lives in ~/.lildocker, or in $LILDOCKER_HOME if set.")
}

func commandHelp(name string, fs *flag.FlagSet) {
	c := commands[name]
	fmt.Printf("Usage:  lildocker %s\n\n%s\n", c.usage, c.desc)
	n := 0
	fs.VisitAll(func(*flag.Flag) { n++ })
	if n > 0 {
		fmt.Println("\nOptions:")
		fs.SetOutput(os.Stdout)
		fs.PrintDefaults()
	}
}

func main() {
	signal.Ignore(syscall.SIGPIPE)
	args := os.Args[1:]
	if len(args) == 0 {
		printHelp()
		return
	}
	name, rest := args[0], args[1:]

	switch name {
	case "_supervise":
		if len(rest) == 1 {
			runtime.Supervise(rest[0])
		}
		return
	case "help", "-h", "--help":
		if len(rest) > 0 {
			if _, ok := commands[rest[0]]; ok {
				os.Exit(commands[rest[0]].run([]string{"--help"}))
			}
		}
		printHelp()
		return
	case "--version", "-v":
		name = "version"
	}

	if _, ok := groups[name]; ok {
		os.Exit(runGroup(name, rest))
	}
	if c, ok := commands[name]; ok {
		os.Exit(c.run(rest))
	}
	if reason, ok := unsupported[name]; ok {
		fmt.Fprintf(os.Stderr, "lildocker: %q is not supported: %s\n", name, reason)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "lildocker: unknown command %q\nRun 'lildocker --help' for the list of commands.\n", name)
	os.Exit(1)
}

var groupMap = map[string]map[string]string{
	"container": {
		"ls": "ps", "list": "ps", "ps": "ps", "run": "run", "create": "create", "start": "start", "stop": "stop",
		"restart": "restart", "kill": "kill", "pause": "pause", "unpause": "unpause", "rm": "rm", "remove": "rm",
		"logs": "logs", "attach": "attach", "exec": "exec", "inspect": "inspect", "top": "top", "port": "port",
		"rename": "rename", "wait": "wait", "cp": "cp", "diff": "diff", "commit": "commit", "export": "export",
		"update": "update", "prune": "prune",
	},
	"image": {
		"ls": "images", "list": "images", "build": "build", "pull": "pull", "rm": "rmi", "remove": "rmi",
		"tag": "tag", "history": "history", "save": "save", "load": "load", "import": "import",
		"inspect": "inspect", "prune": "image-prune",
	},
}

func runGroup(group string, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch group {
	case "volume":
		return cmdVolume(sub, args)
	case "system":
		return cmdSystem(sub, args)
	}
	target, ok := groupMap[group][sub]
	if !ok {
		fmt.Printf("Usage:  lildocker %s <command>\n\n%s\n\nCommands:\n", group, groups[group])
		var subs []string
		for s := range groupMap[group] {
			subs = append(subs, s)
		}
		sort.Strings(subs)
		for _, s := range subs {
			fmt.Printf("  %s\n", s)
		}
		return 1
	}
	if target == "image-prune" {
		return cmdImagePrune(args)
	}
	return commands[target].run(args)
}
