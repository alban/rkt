package main

import (
	"flag"
	"fmt"
	//"io/ioutil"
	"os"
	//"strings"
)

var (
	globalFlagset = flag.NewFlagSet("inspect", flag.ExitOnError)
	globalFlags   = struct {
		ExitCode int
		CheckCwd string
	}{}
)

func init() {
	globalFlagset.IntVar(&globalFlags.ExitCode, "exit-code", 0, "Return this exit code")
	globalFlagset.StringVar(&globalFlags.CheckCwd, "check-cwd", "", "Check the current working directory")
}

func main() {
	globalFlagset.Parse(os.Args[1:])
	args := globalFlagset.Args()
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "Wrong parameters\n")
		os.Exit(1)
	}

	if globalFlags.CheckCwd != "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Cannot get working directory: %v\n", err)
			os.Exit(1)
		}
		if wd != globalFlags.CheckCwd {
			fmt.Fprintf(os.Stderr, "Working directory: %q. Expected: %q.\n", wd, globalFlags.CheckCwd)
			os.Exit(1)
		}
	}

	os.Exit(globalFlags.ExitCode)
}
