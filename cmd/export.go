package cmd

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"jasper/go/internal/client"
	"jasper/go/internal/models"
	"jasper/go/internal/ui"
)

// RunExport exports all report units from a server folder to a YAML manifest file.
func RunExport(args []string) {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	var (
		server   string
		user     string
		password string
		folder   string
		output   string
		debug    bool
	)
	fs.StringVar(&server, "server", "", "JasperReports Server base URL")
	fs.StringVar(&user, "user", "", "Username")
	fs.StringVar(&password, "password", "", "Password (prompted without echo if omitted)")
	fs.StringVar(&folder, "folder", "", "Server folder to export recursively (default /)")
	fs.StringVar(&output, "output", "", "Output YAML file (default export.yml)")
	fs.BoolVar(&debug, "debug", false, "Log every HTTP request URL and response to a log file")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: jaspergo export [flags]

Exports all report units from a server folder to a YAML manifest file.
Resources under /themes are skipped.

Flags:`)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, `
Examples:
  jaspergo export -server http://localhost:8080/jasperserver -user admin -folder /reports -output export.yml
  jaspergo export -server http://localhost:8080/jasperserver -user admin -debug -output export.yml`)
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
	if folder == "" {
		folder = ui.PromptWithDefault(reader, "Folder to export", "/")
	}
	if output == "" {
		output = ui.PromptWithDefault(reader, "Output YAML file", "export.yml")
	}

	c := client.NewJasperClient(server, user, password)
	c.Debug = debug
	if debug {
		logPath := filepath.Join("logs", strings.TrimSuffix(filepath.Base(output), ".yml")+".log")
		if err := os.MkdirAll("logs", 0o755); err != nil {
			ui.Fatalf("Creating logs directory: %v\n", err)
		}
		lf, err := os.Create(logPath)
		if err != nil {
			ui.Fatalf("Creating log file %s: %v\n", logPath, err)
		}
		defer lf.Close()
		c.DebugWriter = lf
		fmt.Printf("Debug log → %s\n", logPath)
	}

	fmt.Printf("Connecting to %s …\n", server)
	if err := c.Ping(); err != nil {
		ui.Fatalf("Cannot reach server: %v\n", err)
	}
	fmt.Println("Connected.")

	exportDir := strings.TrimSuffix(output, filepath.Ext(output))
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		ui.Fatalf("Creating export directory %s: %v\n", exportDir, err)
	}

	manifest := models.ExportManifest{Server: server}

	fmt.Printf("Listing datasources in %s (recursive)…\n", folder)
	allDS, err := c.ListResources(folder, true, "jdbcDataSource")
	if err != nil {
		ui.Fatalf("Listing datasources: %v\n", err)
	}
	var datasources []models.ServerResource
	for _, r := range allDS {
		if !isThemePath(r.URI) {
			datasources = append(datasources, r)
		}
	}
	fmt.Printf("Found %d datasource(s).\n", len(datasources))
	for i, r := range datasources {
		fmt.Printf("  [%d/%d] Exporting datasource %s …\n", i+1, len(datasources), r.URI)
		ds, err := c.GetDatasource(r.URI)
		if err != nil {
			fmt.Fprintf(os.Stderr, "    WARNING: skipping %s: %v\n", r.URI, err)
			continue
		}
		manifest.Datasources = append(manifest.Datasources, models.ExportDatasource{
			URI:           ds.URI,
			Label:         ds.Label,
			Description:   ds.Description,
			DriverClass:   ds.DriverClass,
			ConnectionURL: ds.ConnectionURL,
			Username:      ds.Username,
			Password:      ds.Password,
			Timezone:      ds.Timezone,
		})
	}

	fmt.Printf("Listing reportUnits in %s (recursive)…\n", folder)
	all, err := c.ListResources(folder, true, "reportUnit")
	if err != nil {
		ui.Fatalf("Listing resources: %v\n", err)
	}

	var reports []models.ServerResource
	for _, r := range all {
		if !isThemePath(r.URI) {
			reports = append(reports, r)
		}
	}
	fmt.Printf("Found %d report(s) (skipped %d under /themes).\n", len(reports), len(all)-len(reports))

	for i, r := range reports {
		fmt.Printf("  [%d/%d] Exporting %s …\n", i+1, len(reports), r.URI)
		detail, err := c.GetReportUnit(r.URI)
		if err != nil {
			fmt.Fprintf(os.Stderr, "    WARNING: skipping %s: %v\n", r.URI, err)
			continue
		}

		er := models.ExportReport{
			URI:         detail.URI,
			Label:       detail.Label,
			Description: detail.Description,
		}
		if detail.DataSource != nil {
			er.Datasource = detail.DataSource.DataSourceReference.URI
		}

		if detail.JRXML != nil && detail.JRXML.JRXMLFile != nil && detail.JRXML.JRXMLFile.Content != "" {
			raw, err := base64.StdEncoding.DecodeString(detail.JRXML.JRXMLFile.Content)
			if err != nil {
				fmt.Fprintf(os.Stderr, "    WARNING: skipping jrxml for %s (decode: %v)\n", r.URI, err)
			} else {
				relPath := uriToRelPath(detail.URI, ".jrxml", exportDir)
				if err := writeExportFile(relPath, raw); err != nil {
					fmt.Fprintf(os.Stderr, "    WARNING: could not write jrxml %s: %v\n", relPath, err)
				} else {
					er.JRXML, _ = filepath.Rel(filepath.Dir(output), relPath)
				}
			}
		}

		if detail.Resources != nil {
			reportFolder := detail.URI[:strings.LastIndex(detail.URI, "/")]
			for _, res := range detail.Resources.Resource {
				er2 := models.ExportResource{Name: res.Name}
				if res.File.FileResource != nil && res.File.FileResource.Content != "" {
					er2.Type = res.File.FileResource.Type
					er2.URI = res.File.FileResource.URI
					er2.Shared = !strings.HasPrefix(er2.URI, reportFolder+"/")
					raw, err := base64.StdEncoding.DecodeString(res.File.FileResource.Content)
					if err != nil {
						fmt.Fprintf(os.Stderr, "    WARNING: skipping resource %s (decode: %v)\n", res.Name, err)
						er.Resources = append(er.Resources, er2)
						continue
					}
					ext := typeToExt(er2.Type)
					relPath := uriToRelPath(res.File.FileResource.URI, ext, exportDir)
					if err := writeExportFile(relPath, raw); err != nil {
						fmt.Fprintf(os.Stderr, "    WARNING: could not write resource %s: %v\n", relPath, err)
					} else {
						er2.File, _ = filepath.Rel(filepath.Dir(output), relPath)
					}
				} else if res.File.FileReference != nil {
					er2.URI = res.File.FileReference.URI
					er2.Shared = !strings.HasPrefix(er2.URI, reportFolder+"/")
				}
				er.Resources = append(er.Resources, er2)
			}
		}

		manifest.Reports = append(manifest.Reports, er)
	}

	data, err := yaml.Marshal(&manifest)
	if err != nil {
		ui.Fatalf("Marshaling manifest: %v\n", err)
	}
	if err := os.WriteFile(output, data, 0o644); err != nil {
		ui.Fatalf("Writing %s: %v\n", output, err)
	}
	fmt.Printf("\nExported %d report(s) → %s/\n", len(manifest.Reports), exportDir)
}

// RunImport imports report units from a YAML manifest produced by RunExport.
func RunImport(args []string) {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	var (
		server   string
		user     string
		password string
		input    string
		yes      bool
		no       bool
		auto     bool
		debug    bool
	)
	fs.StringVar(&server, "server", "", "JasperReports Server base URL (overrides manifest server)")
	fs.StringVar(&user, "user", "", "Username")
	fs.StringVar(&password, "password", "", "Password (prompted without echo if omitted)")
	fs.StringVar(&input, "input", "", "Input YAML manifest file (may also be given as a positional argument)")
	fs.BoolVar(&yes, "yes", false, "Answer yes to all overwrite confirmation prompts")
	fs.BoolVar(&no, "no", false, "Skip existing resources without prompting (import new only)")
	fs.BoolVar(&auto, "auto", false, "Publish only reports that differ from the server; skip unchanged ones without prompting")
	fs.BoolVar(&debug, "debug", false, "Log every HTTP request URL and response to a log file")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: jaspergo import [flags] [manifest.yml]

Imports report units from a YAML manifest file produced by "jaspergo export".
Resources under /themes are skipped.

Flags:`)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, `
Examples:
  jaspergo import -server http://prod/jasperserver -user admin export.yml
  jaspergo import -server http://prod/jasperserver -user admin -yes export.yml
  jaspergo import -server http://prod/jasperserver -user admin -auto export.yml
  jaspergo import -server http://prod/jasperserver -user admin -debug export.yml`)
	}
	fs.Parse(args) //nolint:errcheck

	if input == "" && fs.NArg() > 0 {
		input = fs.Arg(0)
	}

	reader := bufio.NewReader(os.Stdin)

	if input == "" {
		input = ui.MustPrompt(reader, "Input YAML file: ")
	}

	data, err := os.ReadFile(input)
	if err != nil {
		ui.Fatalf("Reading %s: %v\n", input, err)
	}
	var manifest models.ExportManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		ui.Fatalf("Parsing %s: %v\n", input, err)
	}

	if server == "" {
		server = manifest.Server
	}
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
	c.Debug = debug
	if debug {
		logPath := filepath.Join("logs", strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))+".import.log")
		if err := os.MkdirAll("logs", 0o755); err != nil {
			ui.Fatalf("Creating logs directory: %v\n", err)
		}
		lf, err := os.Create(logPath)
		if err != nil {
			ui.Fatalf("Creating log file %s: %v\n", logPath, err)
		}
		defer lf.Close()
		c.DebugWriter = lf
		fmt.Printf("Debug log → %s\n", logPath)
	}

	fmt.Printf("Connecting to %s …\n", server)
	if err := c.Ping(); err != nil {
		ui.Fatalf("Cannot reach server: %v\n", err)
	}
	fmt.Println("Connected.")

	fmt.Println("Listing all existing resources on server…")
	allServerResources, serverExists := listAllServerResources(c, manifest)

	if len(allServerResources) > 0 {
		fmt.Printf("\nFound %d existing resource(s) on server:\n", len(allServerResources))
		byType := make(map[string][]string)
		for _, res := range allServerResources {
			byType[res.ResourceType] = append(byType[res.ResourceType], res.URI)
		}
		// Sort types alphabetically
		var types []string
		for t := range byType {
			types = append(types, t)
		}
		sort.Strings(types)
		for _, resType := range types {
			uris := byType[resType]
			// Sort URIs within each type
			sort.Strings(uris)
			fmt.Printf("  [%s] (%d):\n", resType, len(uris))
			for _, uri := range uris {
				fmt.Printf("    • %s\n", uri)
			}
		}
	}

	ok, failed := 0, 0

	if len(manifest.Datasources) > 0 {
		fmt.Printf("\nImporting %d datasource(s)…\n", len(manifest.Datasources))
	}
	for i, eds := range manifest.Datasources {
		fmt.Printf("\n[%d/%d] Datasource %s → %s\n", i+1, len(manifest.Datasources), eds.Label, eds.URI)

		exists := serverExists[eds.URI]
		if exists {
			if no {
				fmt.Println("  Skipped (already exists).")
				continue
			}
			changes, err := datasourceDiffers(c, eds)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  WARNING: could not compare with server: %v\n", err)
			} else if len(changes) > 0 {
				fmt.Println("  Changes detected:")
				for _, change := range changes {
					fmt.Printf("    %s\n", change)
				}
			} else {
				fmt.Println("  No field changes detected (password may have changed).")
			}
			if !yes && !ui.ConfirmOverwrite(reader, "Overwrite this datasource?") {
				fmt.Println("  Skipped.")
				continue
			}
		}

		ds := &models.DatasourceDetail{
			URI:           eds.URI,
			Label:         eds.Label,
			Description:   eds.Description,
			ResourceType:  "jdbcDataSource",
			DriverClass:   eds.DriverClass,
			ConnectionURL: eds.ConnectionURL,
			Username:      eds.Username,
			Password:      eds.Password,
			Timezone:      eds.Timezone,
		}
		if err := c.CreateOrUpdateDatasource(ds, exists); err != nil {
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n", err)
			failed++
		} else {
			action := "created"
			if exists {
				action = "updated"
			}
			fmt.Printf("  Imported OK (%s).\n", action)
			ok++
		}
	}

	uploadedShared := make(map[string]bool)
	for i, er := range manifest.Reports {
		if isThemePath(er.URI) {
			fmt.Printf("  [%d/%d] Skipping theme path: %s\n", i+1, len(manifest.Reports), er.URI)
			continue
		}

		fmt.Printf("\n[%d/%d] %s → %s\n", i+1, len(manifest.Reports), er.Label, er.URI)

		jrxmlPath := resolveImportPath(input, er.JRXML)
		if _, err := os.Stat(jrxmlPath); err != nil {
			fmt.Fprintf(os.Stderr, "  ERROR: jrxml file not found: %s\n", jrxmlPath)
			failed++
			continue
		}

		var resources []models.ResourceFile
		for _, res := range er.Resources {
			if res.File == "" && res.URI != "" {
				resources = append(resources, models.ResourceFile{
					Name: res.Name,
					URI:  res.URI,
					Type: res.Type,
				})
				continue
			}
			if res.File == "" {
				fmt.Fprintf(os.Stderr, "  WARNING: skipping resource %s (no file or uri)\n", res.Name)
				continue
			}
			resPath := resolveImportPath(input, res.File)
			if _, err := os.Stat(resPath); err != nil {
				fmt.Fprintf(os.Stderr, "  WARNING: skipping resource %s (file not found: %s)\n", res.Name, resPath)
				continue
			}
			resources = append(resources, models.ResourceFile{
				Name:     res.Name,
				FilePath: resPath,
				URI:      res.URI,
				Type:     res.Type,
				Shared:   res.Shared,
			})
		}

		exists := serverExists[er.URI]
		if exists {
			if no {
				fmt.Println("  Skipped (already exists).")
				continue
			}
			changes, err := reportDiffers(c, er, jrxmlPath, resources)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  WARNING: could not compare with server: %v\n", err)
			} else if len(changes) > 0 {
				fmt.Println("  Changes detected:")
				for _, change := range changes {
					fmt.Printf("    %s\n", change)
				}
			} else if !auto {
				fmt.Println("  Already exists on server.")
			} else {
				fmt.Println("  No changes detected.")
			}
			if auto && len(changes) == 0 {
				fmt.Println("  Skipped (unchanged).")
				continue
			}
			if !yes && !ui.ConfirmOverwrite(reader, "Overwrite this report?") {
				fmt.Println("  Skipped.")
				continue
			}
		}

		lastSlash := strings.LastIndex(er.URI, "/")
		reportName := er.Label
		if reportName == "" {
			reportName = er.URI[lastSlash+1:]
		}

		if len(resources) > 0 {
			rnames := make([]string, len(resources))
			for j, r := range resources {
				if r.Shared {
					rnames[j] = r.Name + " (shared → " + r.URI + ")"
				} else if r.URI != "" && r.FilePath == "" {
					rnames[j] = r.Name + " (server ref)"
				} else {
					rnames[j] = r.Name
				}
			}
			fmt.Printf("  Resources:\n    %s\n", strings.Join(rnames, "\n    "))
		}

		for _, r := range resources {
			if !r.Shared || uploadedShared[r.URI] {
				continue
			}
			fmt.Printf("  Uploading shared resource %s → %s\n", r.Name, r.URI)
			if err := c.UploadSharedFile(r.URI, r.Type, r.FilePath); err != nil {
				fmt.Fprintf(os.Stderr, "  WARNING: could not upload shared resource %s: %v\n", r.Name, err)
			} else {
				uploadedShared[r.URI] = true
			}
		}

		if err := c.PublishReport(er.URI, reportName, er.Description, er.Datasource, jrxmlPath, resources); err != nil {
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n", err)
			failed++
		} else {
			action := "created"
			if exists {
				action = "updated"
			}
			fmt.Printf("  Imported OK (%s).\n", action)
			ok++
		}
	}

	fmt.Printf("\nResult: %d imported, %d failed.\n", ok, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

// typeFolderFlag is a repeatable flag mapping resource type → default folder.
type typeFolderFlag map[string]string

func (f typeFolderFlag) String() string {
	parts := make([]string, 0, len(f))
	for t, folder := range f {
		parts = append(parts, t+"="+folder)
	}
	return strings.Join(parts, ", ")
}

func (f typeFolderFlag) Set(v string) error {
	idx := strings.IndexByte(v, '=')
	if idx < 1 {
		return fmt.Errorf("format must be type=/folder (got %q)", v)
	}
	f[v[:idx]] = v[idx+1:]
	return nil
}

// RunMergeShared detects duplicate resources across reports and marks them as shared.
func RunMergeShared(args []string) {
	fs := flag.NewFlagSet("mergeshared", flag.ExitOnError)
	var input string
	defaultFolders := make(typeFolderFlag)
	fs.StringVar(&input, "input", "", "YAML manifest file to process (may also be a positional argument)")
	fs.Var(defaultFolders, "default", "Default server folder for a resource type: type=/folder (repeatable; e.g. img=/shared/images)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: jaspergo mergeshared [flags] [manifest.yml]

Scans all resource files in a YAML manifest, detects duplicates across reports
(by file content), and marks them as shared resources with a common server URI.
jrxml resources are skipped. The manifest is updated in-place.

Flags:`)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, `
Examples:
  jaspergo mergeshared export.yml
  jaspergo mergeshared -input export.yml
  jaspergo mergeshared -default img=/shared/images -default jrtx=/shared/styles export.yml`)
	}
	fs.Parse(args) //nolint:errcheck

	if input == "" && fs.NArg() > 0 {
		input = fs.Arg(0)
	}

	reader := bufio.NewReader(os.Stdin)
	if input == "" {
		input = ui.MustPrompt(reader, "Input YAML file: ")
	}

	data, err := os.ReadFile(input)
	if err != nil {
		ui.Fatalf("Reading %s: %v\n", input, err)
	}
	var manifest models.ExportManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		ui.Fatalf("Parsing %s: %v\n", input, err)
	}

	manifestDir := filepath.Dir(input)
	exportDir := filepath.Join(manifestDir, strings.TrimSuffix(filepath.Base(input), filepath.Ext(input)))
	dirsToClean := make(map[string]bool)

	type location struct{ report, resource int }
	type fileInfo struct {
		hash      string
		locations []location
	}
	hashMap := make(map[string]*fileInfo)
	locHash := make(map[location]string)

	for ri, er := range manifest.Reports {
		for rsi, res := range er.Resources {
			if res.File == "" || res.Type == "jrxml" {
				continue
			}
			absPath := res.File
			if !filepath.IsAbs(absPath) {
				absPath = filepath.Join(manifestDir, res.File)
			}
			content, err := os.ReadFile(absPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  WARNING: cannot read %s: %v\n", res.File, err)
				continue
			}
			h := fmt.Sprintf("%x", sha256.Sum256(content))
			loc := location{ri, rsi}
			locHash[loc] = h
			if hashMap[h] == nil {
				hashMap[h] = &fileInfo{hash: h}
			}
			hashMap[h].locations = append(hashMap[h].locations, loc)
		}
	}

	type sharedGroup struct {
		hash      string
		locations []location
		sampleRes models.ExportResource
	}
	var groups []sharedGroup
	for _, fi := range hashMap {
		if len(fi.locations) < 2 {
			continue
		}
		firstURI := manifest.Reports[fi.locations[0].report].Resources[fi.locations[0].resource].URI
		allSameShared := firstURI != ""
		for _, loc := range fi.locations {
			res := manifest.Reports[loc.report].Resources[loc.resource]
			if !res.Shared || res.URI != firstURI {
				allSameShared = false
				break
			}
		}
		if allSameShared {
			continue
		}
		sample := manifest.Reports[fi.locations[0].report].Resources[fi.locations[0].resource]
		groups = append(groups, sharedGroup{
			hash:      fi.hash,
			locations: fi.locations,
			sampleRes: sample,
		})
	}

	updated := 0
	hashToURI := make(map[string]string)

	if len(groups) > 0 {
		fmt.Printf("Found %d shared resource group(s).\n\n", len(groups))
		for i, g := range groups {
			fmt.Printf("[%d/%d] Resource: %s  (type: %s)\n", i+1, len(groups), g.sampleRes.File, g.sampleRes.Type)
			reportSeen := make(map[int]bool)
			for _, loc := range g.locations {
				if reportSeen[loc.report] {
					continue
				}
				reportSeen[loc.report] = true
				res := manifest.Reports[loc.report].Resources[loc.resource]
				shared := ""
				if res.Shared {
					shared = " (shared)"
				}
				fmt.Printf("  %s%s\n", manifest.Reports[loc.report].URI, shared)
			}
			var defaultURI string
			if folder := defaultFolders[g.sampleRes.Type]; folder != "" {
				defaultURI = strings.TrimRight(folder, "/") + "/" + filepath.Base(g.sampleRes.File)
			}
			var uri string
			if defaultURI != "" {
				uri = ui.PromptWithDefault(reader, "  Server URI for this shared resource", defaultURI)
			} else {
				uri = ui.MustPrompt(reader, "  Server URI for this shared resource (e.g. /resources/shared/logo.png): ")
			}
			hashToURI[g.hash] = uri
			fmt.Println()
		}
	} else {
		fmt.Println("No duplicate resources found across reports.")
	}

	hashMoved := make(map[string]string)
	for ri := range manifest.Reports {
		for rsi := range manifest.Reports[ri].Resources {
			res := &manifest.Reports[ri].Resources[rsi]
			if res.File == "" || res.Type == "jrxml" {
				continue
			}
			h, known := locHash[location{ri, rsi}]
			if !known {
				continue
			}
			uri, ok := hashToURI[h]
			if !ok {
				continue
			}
			absPath := res.File
			if !filepath.IsAbs(absPath) {
				absPath = filepath.Join(manifestDir, res.File)
			}

			targetAbs := filepath.Join(exportDir, filepath.FromSlash(strings.TrimPrefix(uri, "/")))

			if movedTo, alreadyMoved := hashMoved[h]; alreadyMoved {
				if absPath != movedTo {
					if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
						fmt.Fprintf(os.Stderr, "  WARNING: could not delete duplicate %s: %v\n", res.File, err)
					} else {
						dirsToClean[filepath.Dir(absPath)] = true
					}
				}
			} else {
				if absPath != targetAbs {
					if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
						fmt.Fprintf(os.Stderr, "  WARNING: could not create dir for %s: %v\n", targetAbs, err)
					} else if err := os.Rename(absPath, targetAbs); err != nil {
						fmt.Fprintf(os.Stderr, "  WARNING: could not move %s → %s: %v\n", res.File, targetAbs, err)
					} else {
						dirsToClean[filepath.Dir(absPath)] = true
					}
				}
				hashMoved[h] = targetAbs
			}

			targetRel, _ := filepath.Rel(manifestDir, targetAbs)
			res.File = targetRel
			res.URI = uri
			res.Shared = true
			updated++
		}
	}

	// Step 4: Same filename as a shared resource → ask to use shared.
	sharedByFilename := make(map[string]string)
	for _, er := range manifest.Reports {
		for _, res := range er.Resources {
			if !res.Shared || res.URI == "" {
				continue
			}
			base := filepath.Base(res.URI)
			if res.File != "" {
				base = filepath.Base(res.File)
			}
			if base != "" && base != "." {
				sharedByFilename[base] = res.URI
			}
		}
	}

	for ri := range manifest.Reports {
		er := &manifest.Reports[ri]
		for rsi := range er.Resources {
			res := &er.Resources[rsi]
			if res.File == "" || res.Shared || res.Type == "jrxml" {
				continue
			}
			baseName := filepath.Base(res.File)
			sharedURI, ok := sharedByFilename[baseName]
			if !ok {
				continue
			}
			fmt.Printf("\n[%s] %q has the same filename as shared resource %s.\n", er.URI, res.Name, sharedURI)
			v := ui.Prompt(reader, "  Use shared resource instead? [Y/n]: ")
			if v == "" || strings.EqualFold(v, "y") || strings.EqualFold(v, "yes") {
				absPath := res.File
				if !filepath.IsAbs(absPath) {
					absPath = filepath.Join(manifestDir, res.File)
				}
				if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
					fmt.Fprintf(os.Stderr, "  WARNING: could not delete %s: %v\n", res.File, err)
				} else {
					dirsToClean[filepath.Dir(absPath)] = true
				}
				res.File = ""
				res.URI = sharedURI
				res.Shared = true
				updated++
			}
		}
	}

	// Step 5: Remove resources not referenced in the report's JRXML.
	for ri := range manifest.Reports {
		er := &manifest.Reports[ri]
		var jrxmlContent []byte
		if er.JRXML != "" {
			jrxmlContent, _ = os.ReadFile(resolveImportPath(input, er.JRXML))
		}
		var toRemove []int
		for rsi := range er.Resources {
			res := &er.Resources[rsi]
			if res.File == "" || res.Shared || res.Type == "jrxml" {
				continue
			}
			if len(jrxmlContent) == 0 {
				continue
			}
			if bytes.Contains(jrxmlContent, []byte(filepath.Base(res.File))) {
				continue
			}
			fmt.Printf("\n[%s] %q (%s) is not referenced in the report JRXML.\n", er.URI, res.Name, filepath.Base(res.File))
			v := ui.Prompt(reader, "  Remove this resource from the manifest? [Y/n]: ")
			if v == "" || strings.EqualFold(v, "y") || strings.EqualFold(v, "yes") {
				toRemove = append(toRemove, rsi)
				updated++
			}
		}
		for i := len(toRemove) - 1; i >= 0; i-- {
			rsi := toRemove[i]
			er.Resources = append(er.Resources[:rsi], er.Resources[rsi+1:]...)
		}
	}

	// Step 6: Shared resources with same filename but different URIs → consolidate.
	type sharedURI struct{ uri, reportURI, resName string }
	filenameURIs := make(map[string][]sharedURI)
	for _, er := range manifest.Reports {
		for _, res := range er.Resources {
			if !res.Shared || res.URI == "" || res.Type == "jrxml" {
				continue
			}
			base := filepath.Base(res.URI)
			filenameURIs[base] = append(filenameURIs[base], sharedURI{res.URI, er.URI, res.Name})
		}
	}
	for base, occurrences := range filenameURIs {
		seen := make(map[string]bool)
		var distinctURIs []string
		for _, o := range occurrences {
			if !seen[o.uri] {
				seen[o.uri] = true
				distinctURIs = append(distinctURIs, o.uri)
			}
		}
		if len(distinctURIs) < 2 {
			continue
		}
		fmt.Printf("\nShared resource %q is used with %d different URIs:\n", base, len(distinctURIs))
		for idx, u := range distinctURIs {
			fmt.Printf("  [%d] %s\n", idx+1, u)
			for _, o := range occurrences {
				if o.uri == u {
					fmt.Printf("      used by %s (%s)\n", o.reportURI, o.resName)
				}
			}
		}
		v := ui.Prompt(reader, "  Choose URI number to consolidate to (or Enter to skip): ")
		if v == "" {
			continue
		}
		choice := 0
		fmt.Sscanf(v, "%d", &choice)
		if choice < 1 || choice > len(distinctURIs) {
			fmt.Println("  Invalid choice, skipping.")
			continue
		}
		chosenURI := distinctURIs[choice-1]
		for _, u := range distinctURIs {
			if u == chosenURI {
				continue
			}
			localPath := filepath.Join(exportDir, filepath.FromSlash(strings.TrimPrefix(u, "/")))
			if err := os.Remove(localPath); err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "  WARNING: could not delete %s: %v\n", localPath, err)
			} else {
				dirsToClean[filepath.Dir(localPath)] = true
			}
		}
		for ri := range manifest.Reports {
			for rsi := range manifest.Reports[ri].Resources {
				res := &manifest.Reports[ri].Resources[rsi]
				if !res.Shared || res.URI == "" || res.Type == "jrxml" {
					continue
				}
				if filepath.Base(res.URI) == base && res.URI != chosenURI {
					res.URI = chosenURI
					updated++
				}
			}
		}
	}

	// Step 7: Shared resource URIs not in their default folder → ask to move.
	type uriEntry struct{ uri, resType string }
	seenURIs := make(map[string]bool)
	var urisToCheck []uriEntry
	for _, er := range manifest.Reports {
		for _, res := range er.Resources {
			if !res.Shared || res.URI == "" || res.Type == "jrxml" || seenURIs[res.URI] {
				continue
			}
			seenURIs[res.URI] = true
			if _, ok := defaultFolders[res.Type]; ok {
				urisToCheck = append(urisToCheck, uriEntry{res.URI, res.Type})
			}
		}
	}
	for _, entry := range urisToCheck {
		defaultFolder := defaultFolders[entry.resType]
		if path.Dir(entry.uri) == strings.TrimRight(defaultFolder, "/") {
			continue
		}
		newURI := strings.TrimRight(defaultFolder, "/") + "/" + path.Base(entry.uri)
		fmt.Printf("\nShared resource %s (type: %s) is not in default folder %s.\n", entry.uri, entry.resType, defaultFolder)
		fmt.Printf("  Used by:\n")
		for _, er := range manifest.Reports {
			for _, res := range er.Resources {
				if res.Shared && res.URI == entry.uri {
					fmt.Printf("    %s (%s)\n", er.URI, res.Name)
				}
			}
		}
		v := ui.Prompt(reader, fmt.Sprintf("  Move to %s? [Y/n]: ", newURI))
		if v != "" && !strings.EqualFold(v, "y") && !strings.EqualFold(v, "yes") {
			continue
		}
		oldLocalPath := filepath.Join(exportDir, filepath.FromSlash(strings.TrimPrefix(entry.uri, "/")))
		newLocalPath := filepath.Join(exportDir, filepath.FromSlash(strings.TrimPrefix(newURI, "/")))
		if oldLocalPath != newLocalPath {
			if err := os.MkdirAll(filepath.Dir(newLocalPath), 0o755); err != nil {
				fmt.Fprintf(os.Stderr, "  WARNING: could not create dir for %s: %v\n", newLocalPath, err)
				continue
			}
			if err := os.Rename(oldLocalPath, newLocalPath); err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "  WARNING: could not move %s → %s: %v\n", oldLocalPath, newLocalPath, err)
				continue
			}
			dirsToClean[filepath.Dir(oldLocalPath)] = true
		}
		newFile := filepath.Join(strings.TrimSuffix(filepath.Base(input), filepath.Ext(input)),
			filepath.FromSlash(strings.TrimPrefix(newURI, "/")))
		for ri := range manifest.Reports {
			for rsi := range manifest.Reports[ri].Resources {
				res := &manifest.Reports[ri].Resources[rsi]
				if !res.Shared || res.URI != entry.uri {
					continue
				}
				res.URI = newURI
				if res.File != "" {
					res.File = newFile
				}
				updated++
			}
		}
	}

	for dir := range dirsToClean {
		for {
			if dir == exportDir || dir == manifestDir || dir == "." || dir == "/" {
				break
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) > 0 {
				break
			}
			if err := os.Remove(dir); err != nil {
				break
			}
			dir = filepath.Dir(dir)
		}
	}

	out, err := yaml.Marshal(&manifest)
	if err != nil {
		ui.Fatalf("Marshaling manifest: %v\n", err)
	}
	if err := os.WriteFile(input, out, 0o644); err != nil {
		ui.Fatalf("Writing %s: %v\n", input, err)
	}
	fmt.Printf("Updated %d resource entry(s) in %s.\n\n", updated, input)

	type resourceSummary struct{ reportURI, target string }
	var sharedList, notSharedList []resourceSummary
	for _, er := range manifest.Reports {
		for _, res := range er.Resources {
			if res.Type == "jrxml" {
				continue
			}
			if res.Shared {
				sharedList = append(sharedList, resourceSummary{er.URI, res.URI})
			} else if res.File != "" {
				notSharedList = append(notSharedList, resourceSummary{er.URI, res.File})
			}
		}
	}
	fmt.Printf("Shared resources (%d):\n", len(sharedList))
	for _, r := range sharedList {
		fmt.Printf("  %s → %s\n", r.reportURI, r.target)
	}
	fmt.Printf("\nNot shared resources (%d):\n", len(notSharedList))
	for _, r := range notSharedList {
		fmt.Printf("  %s → %s\n", r.reportURI, r.target)
	}
}

// --- helpers ---

func isThemePath(uri string) bool {
	return uri == "/themes" || strings.HasPrefix(uri, "/themes/")
}

func typeToExt(t string) string {
	switch t {
	case "img":
		return ".png"
	case "jrxml":
		return ".jrxml"
	case "jrtx":
		return ".jrtx"
	case "font":
		return ".ttf"
	case "jar":
		return ".jar"
	case "resourceBundle":
		return ".properties"
	default:
		return ".bin"
	}
}

var knownExportExts = map[string]bool{
	".jrxml": true, ".jasper": true,
	".jrtx": true,
	".png":  true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true, ".bmp": true,
	".ttf": true, ".otf": true,
	".jar":        true,
	".properties": true,
}

func uriToRelPath(uri, extOverride, exportDir string) string {
	rel := strings.TrimPrefix(uri, "/")
	if extOverride != "" && !knownExportExts[strings.ToLower(filepath.Ext(rel))] {
		rel += extOverride
	}
	return filepath.Join(exportDir, filepath.FromSlash(rel))
}

func writeExportFile(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func resolveImportPath(manifestPath, relPath string) string {
	if filepath.IsAbs(relPath) {
		return relPath
	}
	return filepath.Join(filepath.Dir(manifestPath), relPath)
}

func listAllServerResources(c *client.JasperClient, manifest models.ExportManifest) ([]models.ServerResource, map[string]bool) {
	// List all resources from root recursively to capture everything on the server
	var allResources []models.ServerResource
	existing := make(map[string]bool)

	resources, err := c.ListResources("/", true, "")
	if err != nil {
		// Fallback: list only from manifest folders if root listing fails
		folderSet := make(map[string]bool)
		add := func(uri string) {
			if idx := strings.LastIndex(uri, "/"); idx > 0 {
				folderSet[uri[:idx]] = true
			} else {
				folderSet["/"] = true
			}
		}
		for _, ds := range manifest.Datasources {
			add(ds.URI)
		}
		for _, r := range manifest.Reports {
			add(r.URI)
		}

		for folder := range folderSet {
			res, err := c.ListResources(folder, true, "")
			if err != nil {
				continue
			}
			for _, r := range res {
				allResources = append(allResources, r)
				existing[r.URI] = true
			}
		}
	} else {
		for _, r := range resources {
			allResources = append(allResources, r)
			existing[r.URI] = true
		}
	}

	return allResources, existing
}

func buildExistenceMap(c *client.JasperClient, manifest models.ExportManifest) map[string]bool {
	_, existing := listAllServerResources(c, manifest)
	return existing
}

func reportDiffers(c *client.JasperClient, er models.ExportReport, jrxmlPath string, resources []models.ResourceFile) ([]string, error) {
	var changes []string
	detail, err := c.GetReportUnit(er.URI)
	if err != nil {
		return nil, err
	}

	if detail.Label != er.Label {
		changes = append(changes, fmt.Sprintf("[LABEL] %q → %q", detail.Label, er.Label))
	}
	if detail.Description != er.Description {
		if detail.Description == "" && er.Description != "" {
			changes = append(changes, fmt.Sprintf("[DESCRIPTION] (empty) → %q", er.Description))
		} else if detail.Description != "" && er.Description == "" {
			changes = append(changes, fmt.Sprintf("[DESCRIPTION] %q → (empty)", detail.Description))
		} else {
			changes = append(changes, fmt.Sprintf("[DESCRIPTION] %q → %q", detail.Description, er.Description))
		}
	}

	localDS := er.Datasource
	var serverDS string
	if detail.DataSource != nil {
		serverDS = detail.DataSource.DataSourceReference.URI
	}
	if serverDS != localDS {
		if serverDS == "" && localDS != "" {
			changes = append(changes, fmt.Sprintf("[DATASOURCE] (none) → %q", localDS))
		} else if serverDS != "" && localDS == "" {
			changes = append(changes, fmt.Sprintf("[DATASOURCE] %q → (none)", serverDS))
		} else {
			changes = append(changes, fmt.Sprintf("[DATASOURCE] %q → %q", serverDS, localDS))
		}
	}

	localJRXML, err := os.ReadFile(jrxmlPath)
	if err != nil {
		return nil, fmt.Errorf("reading jrxml: %w", err)
	}
	if detail.JRXML == nil || detail.JRXML.JRXMLFile == nil || detail.JRXML.JRXMLFile.Content == "" {
		changes = append(changes, fmt.Sprintf("[JRXML] Server has no content, local: %d bytes", len(localJRXML)))
	} else {
		serverJRXML, err := base64.StdEncoding.DecodeString(detail.JRXML.JRXMLFile.Content)
		if err != nil {
			return nil, fmt.Errorf("decoding server jrxml: %w", err)
		}
		if !bytes.Equal(localJRXML, serverJRXML) {
			changes = append(changes, fmt.Sprintf("[JRXML] Size: %d → %d bytes", len(serverJRXML), len(localJRXML)))
		}
	}

	serverResMap := make(map[string]string)
	serverResDetails := make(map[string]int64)
	if detail.Resources != nil {
		for _, res := range detail.Resources.Resource {
			if res.File.FileResource != nil {
				serverResMap[res.Name] = res.File.FileResource.Content
				decoded, err := base64.StdEncoding.DecodeString(res.File.FileResource.Content)
				if err == nil {
					serverResDetails[res.Name] = int64(len(decoded))
				}
			}
		}
	}

	var added, modified, removed []string
	for _, r := range resources {
		serverContent, ok := serverResMap[r.Name]
		if !ok {
			if r.FilePath != "" {
				data, err := os.ReadFile(r.FilePath)
				if err == nil {
					added = append(added, fmt.Sprintf("%s (%d bytes)", r.Name, len(data)))
				} else {
					added = append(added, r.Name)
				}
			} else {
				added = append(added, fmt.Sprintf("%s (reference)", r.Name))
			}
			continue
		}
		if r.URI != "" {
			continue
		}
		localData, err := os.ReadFile(r.FilePath)
		if err != nil {
			return nil, fmt.Errorf("reading resource %s: %w", r.Name, err)
		}
		if serverContent != base64.StdEncoding.EncodeToString(localData) {
			serverSize := serverResDetails[r.Name]
			modified = append(modified, fmt.Sprintf("%s: %d → %d bytes", r.Name, serverSize, len(localData)))
		}
	}

	for name := range serverResMap {
		found := false
		for _, r := range resources {
			if r.Name == name {
				found = true
				break
			}
		}
		if !found {
			removed = append(removed, fmt.Sprintf("%s (%d bytes)", name, serverResDetails[name]))
		}
	}

	if len(added) > 0 {
		changes = append(changes, fmt.Sprintf("[RESOURCES] Added (%d): %s", len(added), strings.Join(added, ", ")))
	}
	if len(modified) > 0 {
		changes = append(changes, fmt.Sprintf("[RESOURCES] Modified (%d): %s", len(modified), strings.Join(modified, "; ")))
	}
	if len(removed) > 0 {
		changes = append(changes, fmt.Sprintf("[RESOURCES] Removed (%d): %s", len(removed), strings.Join(removed, ", ")))
	}

	return changes, nil
}

func datasourceDiffers(c *client.JasperClient, eds models.ExportDatasource) ([]string, error) {
	var changes []string
	detail, err := c.GetDatasource(eds.URI)
	if err != nil {
		return nil, err
	}

	if detail.Label != eds.Label {
		changes = append(changes, fmt.Sprintf("[LABEL] %q → %q", detail.Label, eds.Label))
	}

	if detail.Description != eds.Description {
		if detail.Description == "" && eds.Description != "" {
			changes = append(changes, fmt.Sprintf("[DESCRIPTION] (empty) → %q", eds.Description))
		} else if detail.Description != "" && eds.Description == "" {
			changes = append(changes, fmt.Sprintf("[DESCRIPTION] %q → (empty)", detail.Description))
		} else {
			changes = append(changes, fmt.Sprintf("[DESCRIPTION] %q → %q", detail.Description, eds.Description))
		}
	}

	if detail.DriverClass != eds.DriverClass {
		changes = append(changes, fmt.Sprintf("[DRIVER] %q → %q", detail.DriverClass, eds.DriverClass))
	}

	if detail.ConnectionURL != eds.ConnectionURL {
		changes = append(changes, fmt.Sprintf("[CONNECTION_URL] %q → %q", detail.ConnectionURL, eds.ConnectionURL))
	}

	if detail.Username != eds.Username {
		if detail.Username == "" && eds.Username != "" {
			changes = append(changes, fmt.Sprintf("[USERNAME] (empty) → %q", eds.Username))
		} else if detail.Username != "" && eds.Username == "" {
			changes = append(changes, fmt.Sprintf("[USERNAME] %q → (empty)", detail.Username))
		} else {
			changes = append(changes, fmt.Sprintf("[USERNAME] %q → %q", detail.Username, eds.Username))
		}
	}

	if detail.Timezone != eds.Timezone {
		if detail.Timezone == "" && eds.Timezone != "" {
			changes = append(changes, fmt.Sprintf("[TIMEZONE] (empty) → %q", eds.Timezone))
		} else if detail.Timezone != "" && eds.Timezone == "" {
			changes = append(changes, fmt.Sprintf("[TIMEZONE] %q → (empty)", detail.Timezone))
		} else {
			changes = append(changes, fmt.Sprintf("[TIMEZONE] %q → %q", detail.Timezone, eds.Timezone))
		}
	}

	return changes, nil
}
