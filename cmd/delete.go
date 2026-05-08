package cmd

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"jasper/go/internal/client"
	"jasper/go/internal/ui"
)

// RunDelete deletes all resources in a folder, skipping /themes.
func RunDelete(args []string) {
	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	var (
		server   string
		user     string
		password string
		folder   string
		yes      bool
		no       bool
	)
	fs.StringVar(&server, "server", "", "JasperReports Server base URL")
	fs.StringVar(&user, "user", "", "Username")
	fs.StringVar(&password, "password", "", "Password (prompted without echo if omitted)")
	fs.StringVar(&folder, "folder", "/", "Root folder to delete from (default /)")
	fs.BoolVar(&yes, "yes", false, "Delete without prompting")
	fs.BoolVar(&no, "no", false, "Abort if any resources would be deleted (dry-run guard)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: jaspergo delete [flags]

Deletes all resources in a server folder recursively.
Resources under /themes are always skipped.

Flags:`)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, `
Examples:
  jaspergo delete -server http://localhost:8080/jasperserver -user admin -folder /reports
  jaspergo delete -server http://localhost:8080/jasperserver -user admin -yes
  jaspergo delete -server http://localhost:8080/jasperserver -user admin -no`)
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

	c := client.NewJasperClient(server, user, password)

	fmt.Printf("Connecting to %s …\n", server)
	if err := c.Ping(); err != nil {
		ui.Fatalf("Cannot reach server: %v\n", err)
	}
	fmt.Println("Connected.")

	fmt.Printf("Listing resources in %s (recursive)…\n", folder)
	all, err := c.ListResources(folder, true, "")
	if err != nil {
		ui.Fatalf("Listing resources: %v\n", err)
	}
	// Filter out /themes paths.
	var serverResources = all[:0]
	for _, r := range all {
		if r.URI != "/themes" && !strings.HasPrefix(r.URI, "/themes/") {
			serverResources = append(serverResources, r)
		}
	}

	if len(serverResources) == 0 {
		fmt.Println("No resources to delete.")
		return
	}

	fmt.Printf("\n%d resource(s) will be deleted (under %s, excluding /themes):\n", len(serverResources), folder)
	for _, r := range serverResources {
		fmt.Printf("  %s  [%s]\n", r.URI, r.ResourceType)
	}

	if no {
		fmt.Printf("Aborted (-no): %d resource(s) would be deleted.\n", len(serverResources))
		return
	}
	if !yes {
		v := ui.Prompt(reader, fmt.Sprintf("\nDelete all %d resource(s)? [y/N]: ", len(serverResources)))
		if !strings.EqualFold(v, "y") && !strings.EqualFold(v, "yes") {
			fmt.Println("Aborted.")
			return
		}
	}

	fmt.Println()
	deleted, errs := c.DeleteAllResources(serverResources, func(uri string) {
		fmt.Printf("  Deleting %s …\n", uri)
	})

	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "  ERROR: %v\n", e)
	}
	fmt.Printf("\nDeleted %d resource(s), %d error(s).\n", deleted, len(errs))
	if len(errs) > 0 {
		os.Exit(1)
	}
}
