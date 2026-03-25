package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
)

const versionUsage = `Usage:
  routex version [flags]

Flags:
  --json   Output as JSON`

func versionCommand(args []string) error {
	return versionCommandTo(os.Stdout, args)
}

func versionCommandTo(out io.Writer, args []string) error {
	var jsonOut string
	flags := map[string]*string{"json": &jsonOut}

	_, err := parseFlags(args, flags)
	if err != nil {
		return err
	}

	type versionInfo struct {
		Version   string `json:"version"`
		GoVersion string `json:"go_version"`
		OS        string `json:"os"`
		Arch      string `json:"arch"`
	}

	info := versionInfo{
		Version:   Version,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}

	if jsonOut == "true" {
		data, _ := marshalJSON(info)
		fmt.Fprintln(out, string(data))
		return nil
	}

	fmt.Fprintf(out, "routex %s\n", info.Version)
	fmt.Fprintf(out, "go     %s\n", info.GoVersion)
	fmt.Fprintf(out, "os     %s/%s\n", info.OS, info.Arch)
	return nil
}
