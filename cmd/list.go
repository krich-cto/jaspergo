package cmd

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"jasper/go/internal/client"
	"jasper/go/internal/ui"
)

// RunList lists resources in a server folder.
func RunList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	var (
		server       string
		user         string
		password     string
		resourceType string
		recursive    bool
		sortBy       string
	)
	fs.StringVar(&server, "server", "", "JasperReports Server base URL (e.g. http://localhost:8080/jasperserver)")
	fs.StringVar(&user, "user", "", "Username")
	fs.StringVar(&password, "password", "", "Password (prompted without echo if omitted)")
	fs.StringVar(&resourceType, "type", "", `Filter by resource type (e.g. "reportUnit", "folder")`)
	fs.BoolVar(&recursive, "recursive", false, "Include resources in subfolders")
	fs.StringVar(&sortBy, "sort", "uri", `Sort results by field: "uri", "label", or "type"`)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: jaspergo list [flags] [folder-path]

Lists resources in a JasperReports Server folder.
folder-path defaults to / (root) when not specified.

Flags:`)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, `
Examples:
  jaspergo list -server http://localhost:8080/jasperserver -user jasperadmin
  jaspergo list -server http://js.example.com/jasperserver -user admin /reports/hr
  jaspergo list -server http://js.example.com/jasperserver -user admin -type reportUnit -recursive /
  jaspergo list -server http://js.example.com/jasperserver -user admin -sort label /reports`)
	}
	fs.Parse(args) //nolint:errcheck

	reader := bufio.NewReader(os.Stdin)
	if server == "" {
		server = ui.MustPrompt(reader, "Server URL: ")
	}
	if user == "" {
		user = ui.MustPrompt(reader, "Username: ")
	}
	if password == "" {
		password = ui.PromptPassword(reader, "Password: ")
	}

	folderPath := "/"
	if fs.NArg() > 0 {
		folderPath = fs.Arg(0)
	}

	c := client.NewJasperClient(server, user, password)

	fmt.Printf("Connecting to %s …\n", server)
	if err := c.Ping(); err != nil {
		ui.Fatalf("Cannot reach server: %v\n", err)
	}

	resources, err := c.ListResources(folderPath, recursive, resourceType)
	if err != nil {
		ui.Fatalf("Listing resources: %v\n", err)
	}

	if len(resources) == 0 {
		fmt.Printf("No resources found in %s\n", folderPath)
		return
	}

	switch sortBy {
	case "uri":
		sort.Slice(resources, func(i, j int) bool { return resources[i].URI < resources[j].URI })
	case "label":
		sort.Slice(resources, func(i, j int) bool { return resources[i].Label < resources[j].Label })
	case "type":
		sort.Slice(resources, func(i, j int) bool { return resources[i].ResourceType < resources[j].ResourceType })
	case "":
		// server order preserved
	default:
		ui.Fatalf("unknown -sort value %q; valid values: uri, label, type\n", sortBy)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "URI\tTYPE\tLABEL")
	fmt.Fprintln(w, "---\t----\t-----")
	for _, r := range resources {
		fmt.Fprintf(w, "%s\t%s\t%s\n", r.URI, r.ResourceType, r.Label)
	}
	w.Flush()
	fmt.Printf("\n%d resource(s) in %s\n", len(resources), folderPath)
}
