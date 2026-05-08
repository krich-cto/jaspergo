package main

import (
	"fmt"
	"os"

	"jasper/go/cmd"
)

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "list":
			cmd.RunList(os.Args[2:])
			return
		case "publish":
			cmd.RunPublish(os.Args[2:])
			return
		case "export":
			cmd.RunExport(os.Args[2:])
			return
		case "import":
			cmd.RunImport(os.Args[2:])
			return
		case "delete":
			cmd.RunDelete(os.Args[2:])
			return
		case "mergeshared":
			cmd.RunMergeShared(os.Args[2:])
			return
		case "help", "-help", "--help", "-h":
			printUsage()
			return
		}
	}
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	// Default: publish mode (backward compatible — no subcommand required).
	cmd.RunPublish(os.Args[1:])
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage: jaspergo <command> [flags] [args]

Commands:
  publish      Publish one or more JRXML reports to the server (default)
  list         List resources in a server folder
  export       Export all report units from a folder to a YAML manifest file
  import       Import report units from an exported YAML manifest file
  delete       Delete all resources in a folder (skips /themes)
  mergeshared  Detect duplicate resources across reports and mark them as shared

Run "jaspergo <command> -h" for command-specific help.

Examples:
  jaspergo publish -server http://localhost:8080/jasperserver -user jasperadmin report.jrxml
  jaspergo list    -server http://localhost:8080/jasperserver -user admin /reports
  jaspergo export  -server http://localhost:8080/jasperserver -user admin -folder /reports -output export.yml
  jaspergo import       -server http://prod/jasperserver -user admin export.yml
  jaspergo mergeshared  export.yml
  jaspergo report.jrxml   (publish shorthand — no subcommand required)`)
}
