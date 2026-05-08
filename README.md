# jaspergo

A command-line tool for managing reports on a JasperReports Server via REST API v2. Publish JRXML files, export entire folder trees to portable YAML manifests, import them to any server, and clean up resources — all from one binary.

## Requirements

- Go 1.21+ (module: `jasper/go`)
- A running JasperReports Server (Community or Commercial) reachable over HTTP/HTTPS

## Installation

```bash
git clone <repo>
cd jaspergo
go build -o jaspergo .
```

## Commands

```
jaspergo <command> [flags] [args]

Commands:
  publish      Publish one or more JRXML reports to the server (default)
  list         List resources in a server folder
  export       Export all report units from a folder to a YAML manifest
  import       Import report units from a YAML manifest
  delete       Delete all resources in a folder (skips /themes)
  mergeshared  Detect duplicate resources across reports and mark them as shared
```

Run `jaspergo <command> -h` for command-specific help.

Any required flag not supplied on the command line is prompted interactively. Passwords are read without echo when stdin is a terminal.

---

## publish

Publish one or more JRXML files to a JasperReports Server folder. Auto-discovers images, sub-reports, style templates (`.jrtx`), and other embedded resources from the JRXML source and uploads them inline. Resources already present on the server can be referenced by URI instead of re-uploaded.

```
jaspergo publish [flags] [report.jrxml ...]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-server` | (prompted) | Server base URL, e.g. `http://localhost:8080/jasperserver` |
| `-user` | (prompted) | Username |
| `-password` | (prompted) | Password (no-echo when stdin is a TTY) |
| `-folder` | `/reports` | Target folder path on the server |
| `-name` | file base name | Report display name (single-report mode only) |
| `-desc` | | Report description |
| `-resources` | JRXML directory | Directory to search for resource files |
| `-datasource` | | Server URI of a JDBC datasource, e.g. `/datasources/MyDS` |
| `-resource-ref` | | Extra resource reference: `name=local-path` or `name=/server/uri` (repeatable) |
| `-yes` | false | Overwrite existing reports without prompting |
| `-no` | false | Skip existing reports without prompting |
| `-debug` | false | Save publish payload JSON to `logs/<name>.publish.debug.json` |

The shorthand form `jaspergo report.jrxml` (no subcommand) also invokes publish.

**Resource resolution order**

1. JRXML is parsed for `imageExpression`, `subreportExpression`, and `repo:` references.
2. Referenced files are searched in `-resources` dir, then the JRXML's own directory.
3. `repo:/absolute/path` references are verified against the server; missing ones prompt for a local fallback.
4. Explicit `-resource-ref` values are appended last and take precedence by name.

**Publish method**: DELETE then PUT to the full resource URI. This ensures Jasper uses the URL path as the authoritative resource ID and avoids silent duplication or optimistic-lock conflicts that occur with POST + `?overwrite=true`.

**Examples**

```bash
# Single report, prompted credentials
jaspergo publish report.jrxml

# Multiple reports, explicit credentials
jaspergo publish -server http://localhost:8080/jasperserver -user jasperadmin \
  -folder /reports/hr *.jrxml

# Overwrite all without prompting
jaspergo publish -yes -folder /reports/hr report.jrxml

# Skip already-published reports
jaspergo publish -no -folder /reports/hr *.jrxml

# Attach a server-side style template by URI
jaspergo publish -resource-ref central.jrtx=/repo/shared/central.jrtx report.jrxml

# Attach a local style template by file path
jaspergo publish -resource-ref central.jrtx=./resources/central.jrtx report.jrxml
```

---

## list

List resources in a server folder with optional filtering and sorting.

```
jaspergo list [flags] [folder-path]
```

`folder-path` defaults to `/` when omitted.

| Flag | Default | Description |
|------|---------|-------------|
| `-server` | (prompted) | Server base URL |
| `-user` | (prompted) | Username |
| `-password` | (prompted) | Password |
| `-type` | (all types) | Filter by resource type, e.g. `reportUnit`, `folder` |
| `-recursive` | false | Include resources in subfolders |
| `-sort` | `uri` | Sort by field: `uri`, `label`, or `type` |

**Examples**

```bash
# List everything at the root
jaspergo list -server http://localhost:8080/jasperserver -user jasperadmin

# List only reports recursively under /reports/hr
jaspergo list -server http://js.example.com/jasperserver -user admin \
  -type reportUnit -recursive /reports/hr

# Sort by label
jaspergo list -server http://js.example.com/jasperserver -user admin \
  -sort label /reports
```

---

## export

Export all report units (and their JDBC datasources) from a server folder to a YAML manifest file and a local directory tree that mirrors the server folder structure.

```
jaspergo export [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-server` | (prompted) | Server base URL |
| `-user` | (prompted) | Username |
| `-password` | (prompted) | Password |
| `-folder` | `/` | Server folder to export recursively |
| `-output` | `export.yml` | Output YAML manifest file |
| `-debug` | false | Log every HTTP request/response to `logs/<output-basename>.log` |

The export creates:

- `<output>.yml` — YAML manifest listing all datasources and reports with relative file paths
- `<output-basename>/` — directory tree containing all JRXML files and embedded resources, mirroring the server URI structure

Resources under `/themes` are always skipped.

**Examples**

```bash
jaspergo export -server http://localhost:8080/jasperserver -user admin \
  -folder /reports -output export.yml

jaspergo export -server http://localhost:8080/jasperserver -user admin \
  -debug -output export.yml
```

---

## import

Import report units and datasources from a YAML manifest produced by `export`. Datasources are imported first so reports can reference them. Shared resources are uploaded to their server URI before the report unit that references them.

```
jaspergo import [flags] [manifest.yml]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-server` | manifest value | Target server base URL (overrides manifest `server` field) |
| `-user` | (prompted) | Username |
| `-password` | (prompted) | Password |
| `-input` | (prompted) | YAML manifest file (may also be a positional argument) |
| `-yes` | false | Overwrite existing resources without prompting |
| `-no` | false | Skip existing resources without prompting |
| `-auto` | false | Skip reports whose label, description, datasource, JRXML, and resources are identical to the server copy |
| `-debug` | false | Log every HTTP request/response to `logs/<manifest-basename>.import.log` |

**Existence checks**: before the import loop, all parent folders are listed once to build a `map[string]bool`. Per-resource individual GETs are avoided because they can return HTTP 406 for typed resources, causing false negatives.

**Examples**

```bash
# Import to a different server than the export source
jaspergo import -server http://prod/jasperserver -user admin export.yml

# Overwrite all without prompting
jaspergo import -server http://prod/jasperserver -user admin -yes export.yml

# Only publish reports that differ from what is already on the server
jaspergo import -server http://prod/jasperserver -user admin -auto export.yml
```

---

## delete

Delete all resources in a server folder recursively. Always skips `/themes`. Sorts deletions so `reportUnit` resources are deleted before datasources, preventing "Operation cannot be performed" errors from the server when a datasource is still referenced by a report.

```
jaspergo delete [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-server` | (prompted) | Server base URL |
| `-user` | (prompted) | Username |
| `-password` | (prompted) | Password |
| `-folder` | `/` | Root folder to delete from |
| `-yes` | false | Delete without prompting |
| `-no` | false | Abort if any resources would be deleted (dry-run guard) |

The resources that would be deleted are printed before confirmation is requested.

**Examples**

```bash
# Delete everything under /reports (interactive confirmation)
jaspergo delete -server http://localhost:8080/jasperserver -user admin -folder /reports

# Delete without prompting
jaspergo delete -server http://localhost:8080/jasperserver -user admin -yes

# Dry-run: show what would be deleted, then abort
jaspergo delete -server http://localhost:8080/jasperserver -user admin -no
```

---

## mergeshared

Scan an exported YAML manifest for resource files that appear in multiple reports (detected by SHA-256 content hash), then:

1. Prompt for a single server URI to use for each group of duplicates.
2. Move the canonical copy to the new path inside the export directory tree.
3. Delete the duplicate local copies.
4. Update all manifest entries to point to the shared URI.
5. Optionally consolidate shared resources that ended up with different URIs but the same filename.
6. Optionally move shared resources that are not in their default folder.
7. Prompt to remove resources not referenced in the report's JRXML.

The manifest file is updated in-place. JRXML-type resources are never marked shared.

```
jaspergo mergeshared [flags] [manifest.yml]
```

| Flag | Description |
|------|-------------|
| `-input` | YAML manifest file (may also be a positional argument) |
| `-default type=/folder` | Default server folder for a resource type (repeatable) |

**Examples**

```bash
# Interactive deduplication
jaspergo mergeshared export.yml

# With default folders pre-set (no prompt for those types)
jaspergo mergeshared \
  -default img=/shared/images \
  -default jrtx=/shared/styles \
  export.yml
```

---

## YAML manifest format

```yaml
server: http://source-server/jasperserver   # recorded for reference; import -server overrides it

datasources:
  - uri: /datasources/MyDB
    label: My Database
    driver_class: org.postgresql.Driver
    connection_url: jdbc:postgresql://db:5432/mydb
    username: reporter
    password: secret          # optional — omit to prompt on import
    timezone: UTC             # optional

reports:
  - uri: /reports/hr/Headcount
    label: Headcount Report
    desc: Monthly headcount by department
    datasource: /datasources/MyDB
    jrxml: export/reports/hr/Headcount.jrxml
    resources:
      - name: logo_png
        type: img
        file: export/reports/hr/Headcount/logo.png
      - name: central_jrtx
        type: jrtx
        uri: /shared/styles/central.jrtx
        shared: true          # resource lives outside the report's own folder
```

All file paths in the manifest are relative to the directory containing the manifest file.

---

## Resource types

| Extension | Server type |
|-----------|-------------|
| `.png` `.jpg` `.jpeg` `.gif` `.svg` `.bmp` | `img` |
| `.jrxml` `.jasper` | `jrxml` |
| `.jrtx` | `jrtx` |
| `.ttf` `.otf` | `font` |
| `.jar` | `jar` |
| `.properties` | `resourceBundle` |

---

## Debug logging

Pass `-debug` to any command that supports it. HTTP request method, URL, body size, and response status are written to:

- **publish**: `logs/<report-name>.publish.debug.json` (payload) + stderr
- **export**: `logs/<output-basename>.log`
- **import**: `logs/<manifest-basename>.import.log`

The `logs/` directory is created automatically.

---

## Architecture

```
jaspergo/
├── main.go                        # thin dispatcher — parses os.Args[1], calls cmd.*
├── cmd/
│   ├── publish.go                 # RunPublish, refFlag, parseResourceRef, reportJob
│   ├── list.go                    # RunList
│   ├── delete.go                  # RunDelete
│   └── export.go                  # RunExport, RunImport, RunMergeShared, helpers
└── internal/
    ├── models/
    │   └── models.go              # all shared data types: ResourceFile, ServerResource,
    │                              #   ReportUnitDetail, DatasourceDetail, Export* types
    ├── client/
    │   └── client.go              # JasperClient — all HTTP calls to REST API v2,
    │                              #   private ru* JSON descriptor types, buildInputControls
    ├── jrxml/
    │   └── jrxml.go               # ParseParameters, ParseResources, ToResourceType
    └── ui/
        └── ui.go                  # Prompt, MustPrompt, PromptPassword, Fatalf, BaseName
```

**Import graph** (no cycles):

```
main.go          → cmd
cmd/*            → internal/client, internal/jrxml, internal/models, internal/ui
internal/client  → internal/jrxml, internal/models
internal/jrxml   → internal/models
internal/models  → (stdlib only)
internal/ui      → (stdlib only)
```

## Building

```bash
go build ./...          # build
go run .                # run without building
go vet ./...            # lint
go test ./...           # run all tests
go build -o jaspergo .  # build with explicit binary name
```
