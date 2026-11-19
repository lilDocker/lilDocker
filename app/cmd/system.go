package main

import (
	"fmt"
	"os"
	goruntime "runtime"
	"strings"
	"text/tabwriter"

	"lildocker/config"
	"lildocker/image"
	"lildocker/runtime"
)

func cmdVersion(args []string) int {
	fmt.Printf("lilDocker version %s\n OS/Arch: %s/%s\n Built with: %s\n", version, goruntime.GOOS, goruntime.GOARCH, goruntime.Version())
	return 0
}

func cmdInfo(args []string) int {
	n := runtime.Count()
	imgs, _ := image.List()
	vols, _ := runtime.ListVolumes()
	fmt.Println("lilDocker", version)
	fmt.Printf(" Containers: %d\n  Running: %d\n  Paused: %d\n  Stopped: %d\n", n.Total, n.Running, n.Paused, n.Stopped)
	fmt.Printf(" Images: %d\n Volumes: %d\n", len(imgs), len(vols))
	fmt.Printf(" Storage: %s\n OS/Arch: %s/%s\n Isolation: %s\n", config.Home(), goruntime.GOOS, goruntime.GOARCH, isolationNote())
	return 0
}

func isolationNote() string {
	if goruntime.GOOS != "linux" {
		return "not available on " + goruntime.GOOS
	}
	if os.Geteuid() != 0 {
		return "available with root (--isolate)"
	}
	return "available (--isolate)"
}

func cmdSystem(sub string, args []string) int {
	switch sub {
	case "info":
		return cmdInfo(args)
	case "df":
		return cmdDf()
	case "prune":
		return cmdSystemPrune(args)
	}
	fmt.Println("Usage:  lildocker system <command>\n\nCommands:\n  df      show disk usage\n  info    show system information\n  prune   remove stopped containers, unused volumes (and images with -a)")
	return 1
}

func cmdDf() int {
	imgs, _ := image.List()
	var imgSize int64
	inUse := 0
	for _, m := range imgs {
		imgSize += m.Size
		if len(runtime.UsedBy(m.ID)) > 0 {
			inUse++
		}
	}
	cs, _ := runtime.List(true)
	var cSize int64
	active := 0
	for _, c := range cs {
		cSize += c.Size()
		if c.Active() {
			active++
		}
	}
	vs, _ := runtime.ListVolumes()
	var vSize int64
	vUse := 0
	for _, v := range vs {
		vSize += runtime.DirSize(v.Path)
		if len(v.UsedBy) > 0 {
			vUse++
		}
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "TYPE\tTOTAL\tACTIVE\tSIZE")
	fmt.Fprintf(tw, "Images\t%d\t%d\t%s\n", len(imgs), inUse, size(imgSize))
	fmt.Fprintf(tw, "Containers\t%d\t%d\t%s\n", len(cs), active, size(cSize))
	fmt.Fprintf(tw, "Volumes\t%d\t%d\t%s\n", len(vs), vUse, size(vSize))
	tw.Flush()
	return 0
}

func cmdSystemPrune(args []string) int {
	fs := newFlags("prune")
	var all, force, vols bool
	boolFlag(fs, &all, "also remove images no container uses", "a", "all")
	boolFlag(fs, &vols, "also remove unused volumes", "volumes")
	boolFlag(fs, &force, "do not ask for confirmation", "f", "force")
	if fs.Parse(args) != nil {
		return 1
	}
	msg := "This will remove all stopped containers"
	if vols {
		msg += ", unused volumes"
	}
	if all {
		msg += " and unused images"
	}
	if !confirm(msg+". Continue?", force) {
		return 0
	}
	gone, err := runtime.Prune()
	if err != nil {
		return fail(err)
	}
	fmt.Printf("Deleted containers: %d\n", len(gone))
	if vols {
		vg, err := runtime.PruneVolumes()
		if err != nil {
			return fail(err)
		}
		fmt.Printf("Deleted volumes: %d\n", len(vg))
	}
	if all {
		return cmdImagePrune([]string{"-a", "-f"})
	}
	return 0
}

func cmdVolume(sub string, args []string) int {
	switch sub {
	case "create":
		name := ""
		if len(args) > 1 {
			return volumeUsage()
		}
		if len(args) == 1 {
			name = args[0]
		}
		if _, err := runtime.CreateVolume(name); err != nil {
			return fail(err)
		}
		if name == "" {
			vs, _ := runtime.ListVolumes()
			fmt.Println(vs[len(vs)-1].Name)
		} else {
			fmt.Println(name)
		}
		return 0

	case "ls", "list":
		fs := newFlags("volume ls")
		var quiet bool
		boolFlag(fs, &quiet, "only show names", "q", "quiet")
		if fs.Parse(args) != nil {
			return 1
		}
		vs, err := runtime.ListVolumes()
		if err != nil {
			return fail(err)
		}
		if quiet {
			for _, v := range vs {
				fmt.Println(v.Name)
			}
			return 0
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
		fmt.Fprintln(tw, "VOLUME NAME\tIN USE BY")
		for _, v := range vs {
			fmt.Fprintf(tw, "%s\t%s\n", v.Name, strings.Join(v.UsedBy, ", "))
		}
		tw.Flush()
		return 0

	case "rm", "remove":
		fs := newFlags("volume rm")
		var force bool
		boolFlag(fs, &force, "remove even if in use", "f", "force")
		if fs.Parse(args) != nil || fs.NArg() < 1 {
			return volumeUsage()
		}
		return eachContainer("volume rm", fs.Args(), func(n string) error { return runtime.RemoveVolume(n, force) })

	case "inspect":
		if len(args) < 1 {
			return volumeUsage()
		}
		return cmdInspect(args)

	case "prune":
		fs := newFlags("volume prune")
		var force bool
		boolFlag(fs, &force, "do not ask for confirmation", "f", "force")
		if fs.Parse(args) != nil {
			return 1
		}
		if !confirm("This will remove all volumes no container uses. Continue?", force) {
			return 0
		}
		gone, err := runtime.PruneVolumes()
		if err != nil {
			return fail(err)
		}
		for _, g := range gone {
			fmt.Println(g)
		}
		return 0
	}
	return volumeUsage()
}

func volumeUsage() int {
	fmt.Println("Usage:  lildocker volume <command>\n\nCommands:\n  create [name]   create a volume\n  ls [-q]         list volumes\n  rm [-f] <name>  remove volumes\n  inspect <name>  show a volume\n  prune [-f]      remove unused volumes")
	return 1
}
