package cmd

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"jasper/go/internal/client"
	"jasper/go/internal/jrxml"
	"jasper/go/internal/models"
	"jasper/go/internal/ui"
)

// refFlag is a repeatable flag for resource references.
// Each value must be of the form "name=local-path" or "name=/server/uri".
type refFlag []models.ResourceFile

func (f *refFlag) String() string {
	parts := make([]string, len(*f))
	for i, r := range *f {
		if r.URI != "" {
			parts[i] = r.Name + "=" + r.URI
		} else {
			parts[i] = r.Name + "=" + r.FilePath
		}
	}
	return strings.Join(parts, ", ")
}

func (f *refFlag) Set(v string) error {
	rf, err := parseResourceRef(v)
	if err != nil {
		return err
	}
	*f = append(*f, rf)
	return nil
}

func parseResourceRef(v string) (models.ResourceFile, error) {
	idx := strings.IndexByte(v, '=')
	if idx < 1 {
		return models.ResourceFile{}, fmt.Errorf("format must be name=local-path or name=/server/uri (got %q)", v)
	}
	name, ref := v[:idx], v[idx+1:]
	if strings.HasPrefix(ref, "/") {
		if _, err := os.Stat(ref); err != nil {
			return models.ResourceFile{Name: name, URI: ref, Type: jrxml.ToResourceType(name)}, nil
		}
	}
	return models.ResourceFile{Name: name, FilePath: ref, Type: jrxml.ToResourceType(name)}, nil
}

// reportJob holds all resolved parameters for a single report publish operation.
type reportJob struct {
	file          string
	name          string
	description   string
	folder        string
	datasourceURI string
	resourceDir   string
	resourceRefs  []models.ResourceFile
}

// RunPublish publishes one or more JRXML reports to the server.
func RunPublish(args []string) {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	var (
		server        string
		user          string
		password      string
		folder        string
		name          string
		description   string
		resourceDir   string
		datasourceURI string
		yes           bool
		no            bool
		debug         bool
		resourceRefs  refFlag
	)
	fs.StringVar(&server, "server", "", "JasperReports Server base URL (e.g. http://localhost:8080/jasperserver)")
	fs.StringVar(&user, "user", "", "Username")
	fs.StringVar(&password, "password", "", "Password (prompted without echo if omitted)")
	fs.StringVar(&folder, "folder", "/reports", "Target folder path on the server")
	fs.StringVar(&name, "name", "", "Report display name (single-report mode; defaults to file base name)")
	fs.StringVar(&description, "desc", "", "Report description")
	fs.StringVar(&resourceDir, "resources", "", "Directory containing report resources (images, sub-reports, fonts, etc.)")
	fs.StringVar(&datasourceURI, "datasource", "", "Server URI of the JDBC datasource (e.g. /datasources/MyDS)")
	fs.BoolVar(&yes, "yes", false, "Overwrite existing resources without prompting")
	fs.BoolVar(&no, "no", false, "Skip existing resources without prompting (publish new only)")
	fs.BoolVar(&debug, "debug", false, "Save request JSON payload to *.publish.debug.json for inspection")
	fs.Var(&resourceRefs, "resource-ref", "Resource reference: name=local-path or name=/server/uri (repeatable; local-path uploads inline, /server/uri references an existing server resource)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: jaspergo publish [flags] [report.jrxml ...]

Publishes one or more JRXML reports to a JasperReports Server via REST API v2.
Any required value not supplied is prompted.

Flags:`)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, `
Examples:
  jaspergo publish -server http://localhost:8080/jasperserver -user jasperadmin report.jrxml
  jaspergo publish -server http://js.example.com/jasperserver -user admin -folder /reports/hr *.jrxml
  jaspergo publish -yes -folder /reports/hr report.jrxml
  jaspergo publish -no  -folder /reports/hr report.jrxml
  jaspergo publish -resource-ref central.jrtx=./resources/central.jrtx report.jrxml
  jaspergo publish -resource-ref central.jrtx=/repo/shared/central.jrtx report.jrxml`)
	}
	fs.Parse(args) //nolint:errcheck

	var jobs []reportJob
	for _, f := range fs.Args() {
		jobs = append(jobs, reportJob{
			file:          f,
			name:          name,
			folder:        folder,
			datasourceURI: datasourceURI,
			resourceDir:   resourceDir,
			resourceRefs:  resourceRefs,
		})
	}

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

	if len(jobs) == 0 {
		file := ui.MustPrompt(reader, "Path to report file (.jrxml): ")
		dir := filepath.Dir(file)
		if resourceDir != "" {
			dir = resourceDir
		}
		rName := ui.PromptWithDefault(reader, "Report name", ui.BaseName(file))
		desc := ui.OptionalPrompt(reader, "Description (press Enter to skip): ")
		jobs = append(jobs, reportJob{
			file:          file,
			name:          rName,
			description:   desc,
			folder:        folder,
			datasourceURI: datasourceURI,
			resourceDir:   dir,
			resourceRefs:  resourceRefs,
		})
	}

	if len(jobs) == 1 && jobs[0].name == "" {
		jobs[0].name = ui.PromptWithDefault(reader, "Report name", ui.BaseName(jobs[0].file))
		if jobs[0].description == "" {
			jobs[0].description = ui.OptionalPrompt(reader, "Description (press Enter to skip): ")
		}
	} else if name != "" && len(jobs) > 1 {
		fmt.Fprintln(os.Stderr, "WARNING: -name is ignored when publishing multiple reports; each report uses its file base name.")
	}

	for i := range jobs {
		if jobs[i].resourceDir == "" {
			jobs[i].resourceDir = filepath.Dir(jobs[i].file)
		}
	}

	c := client.NewJasperClient(server, user, password)
	c.Debug = debug

	fmt.Printf("Connecting to %s …\n", server)
	if err := c.Ping(); err != nil {
		ui.Fatalf("Cannot reach server: %v\n", err)
	}
	fmt.Println("Connected.")

	ok, failed := 0, 0

	for _, job := range jobs {
		reportName := job.name
		if reportName == "" {
			reportName = ui.BaseName(job.file)
		}

		serverPath := job.folder + "/" + reportName
		fmt.Printf("\n[%s] → %s\n", job.file, serverPath)

		if _, err := os.Stat(job.file); err != nil {
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n", err)
			failed++
			continue
		}
		if !strings.EqualFold(filepath.Ext(job.file), ".jrxml") {
			fmt.Fprintln(os.Stderr, "  ERROR: file does not have a .jrxml extension")
			failed++
			continue
		}

		exists, err := c.ResourceExists(serverPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ERROR checking existence: %v\n", err)
			failed++
			continue
		}
		if exists {
			if no {
				fmt.Println("  Skipped (already exists).")
				continue
			}
			if !yes && !ui.ConfirmOverwrite(reader, serverPath) {
				fmt.Println("  Skipped.")
				continue
			}
		}

		parsed, missingRefs, err := jrxml.ParseResources(job.file, job.resourceDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  WARNING: could not parse resources: %v\n", err)
		}

		var localFiles []models.ResourceFile
		var repoRefs []models.ResourceFile
		for _, r := range parsed {
			if r.URI != "" {
				repoRefs = append(repoRefs, r)
			} else {
				localFiles = append(localFiles, r)
			}
		}

		for _, m := range missingRefs {
			fmt.Fprintf(os.Stderr, "  WARNING: resource not found locally: %s\n", m.Ref)
			path := ui.Prompt(reader, fmt.Sprintf("  Provide local file path for %q (or Enter to skip): ", m.Ref))
			if path != "" {
				localFiles = append(localFiles, models.ResourceFile{
					Name:     m.Name,
					FilePath: path,
					Type:     m.Type,
				})
			}
		}

		var verifiedRefs []models.ResourceFile
		if len(repoRefs) > 0 {
			fmt.Printf("  Checking %d server resource(s) referenced in JRXML…\n", len(repoRefs))
			folderSet := make(map[string]bool)
			for _, r := range repoRefs {
				if idx := strings.LastIndex(r.URI, "/"); idx > 0 {
					folderSet[r.URI[:idx]] = true
				} else {
					folderSet["/"] = true
				}
			}
			serverExists := make(map[string]bool)
			for f := range folderSet {
				listed, listErr := c.ListResources(f, false, "")
				if listErr != nil {
					fmt.Fprintf(os.Stderr, "  WARNING: could not list folder %s: %v\n", f, listErr)
					continue
				}
				for _, sr := range listed {
					serverExists[sr.URI] = true
				}
			}
			for _, r := range repoRefs {
				if serverExists[r.URI] {
					verifiedRefs = append(verifiedRefs, r)
				} else {
					fmt.Fprintf(os.Stderr, "  WARNING: repo resource not found on server: %s\n", r.URI)
					path := ui.Prompt(reader, fmt.Sprintf("  Provide local file path for %q (or Enter to skip): ", r.URI))
					if path != "" {
						localFiles = append(localFiles, models.ResourceFile{
							Name:     r.Name,
							FilePath: path,
							Type:     r.Type,
						})
					}
				}
			}
		}

		resources := append(localFiles, verifiedRefs...)
		resources = append(resources, job.resourceRefs...)

		if len(resources) > 0 {
			names := make([]string, len(resources))
			for i, r := range resources {
				if r.URI != "" {
					names[i] = r.Name + " (server ref)"
				} else {
					names[i] = filepath.Base(r.FilePath)
				}
			}
			fmt.Printf("  Resources:\n    %s\n", strings.Join(names, "\n    "))
		}

		if err := c.PublishReport(serverPath, reportName, job.description, job.datasourceURI, job.file, resources); err != nil {
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n", err)
			failed++
		} else {
			action := "created"
			if exists {
				action = "updated"
			}
			fmt.Printf("  Published OK (%s).\n", action)
			ok++
		}
	}

	fmt.Printf("\nResult: %d published, %d failed.\n", ok, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
