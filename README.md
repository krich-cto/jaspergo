# jaspergo

A comprehensive command-line tool for managing JasperReports Server resources via REST API v2. Publish JRXML reports with auto-discovered resources, export entire folder hierarchies to portable YAML manifests, import them to any server, list and delete resources, and consolidate duplicate resources — all from one binary.

## Requirements

- **Go 1.26.2 or later** (module: `jasper/go`)
- **A running JasperReports Server** (Community or Commercial) reachable over HTTP/HTTPS
- **External dependencies:** `golang.org/x/term` (password input), `gopkg.in/yaml.v3` (manifest parsing)

## Installation

```bash
git clone <repo>
cd jaspergo
go build -o jaspergo .
```

Or install directly:
```bash
go install jasper/go@latest
```

Verify installation:
```bash
jaspergo -h
```

## Commands

```
jaspergo <command> [flags] [args]

Commands:
  publish      Publish one or more JRXML reports to the server (default)
  list         List resources in a server folder with filtering and sorting
  export       Export all report units and datasources from a folder to a YAML manifest
  import       Import report units and datasources from a YAML manifest to a server
  delete       Delete all resources in a folder (skips /themes, respects dependencies)
  mergeshared  Detect duplicate resources across reports and consolidate URIs
```

Run `jaspergo <command> -h` for detailed command-specific help.

### Interactive Prompts

All required flags not supplied on the command line are prompted interactively:
- **Server URL** — prompted with validation
- **Username** — prompted 
- **Password** — prompted with no-echo when stdin is a terminal (uses `golang.org/x/term`)
- **Overwrite confirmation** — yes/no prompts (skipped with `-yes` or `-no` flags)

This makes jaspergo easy to use in interactive shells and in scripting with `-yes`/`-no` flags.

---

## publish

Publish one or more JRXML files to a JasperReports Server folder. Auto-discovers embedded resources (images, sub-reports, style templates, fonts, resource bundles) from JRXML source files and uploads them inline. Resources already present on the server can be referenced by URI instead of re-uploaded. Supports both single and batch publishing with flexible overwrite handling.

```
jaspergo publish [flags] [report.jrxml ...]
```

**Shorthand**: `jaspergo report.jrxml` (no subcommand) invokes publish for backward compatibility.

### publish Flags

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
| `-yes` | false | Overwrite all existing reports without prompting |
| `-no` | false | Skip all existing reports without prompting |
| `-debug` | false | Log HTTP requests/responses to stderr and save payload to `logs/` |

### Resource Resolution Order

1. JRXML is parsed for embedded references: `imageExpression`, `subreportExpression`, `repo:/` URIs, and style template keys.
2. Referenced files searched in this order:
   - `-resources` directory (if specified)
   - JRXML file's own directory
   - JRXML file's parent directory
3. `repo:/absolute/path` references verified against the server; unresolved ones prompt for local file or server URI fallback.
4. Explicit `-resource-ref` flags appended last and take precedence by resource name.

### Upload Method: DELETE + PUT

jaspergo uses DELETE followed by PUT to the full resource URI (not POST + `?overwrite=true`). This approach:
- Ensures Jasper uses the URL path segment as the authoritative resource ID
- Avoids silent duplication (POST + overwrite creates duplicates under different IDs)
- Eliminates optimistic-lock conflicts
- Works around Jasper's inconsistent label-vs-URI-path handling

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

List resources in a server folder with optional filtering by type, recursive traversal, and sorting.

```
jaspergo list [flags] [folder-path]
```

`folder-path` defaults to `/` (server root) when omitted.

### list Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-server` | (prompted) | Server base URL |
| `-user` | (prompted) | Username |
| `-password` | (prompted) | Password |
| `-type` | (all types) | Filter by resource type (e.g., `reportUnit`, `folder`, `jdbcDataSource`, `img`, `jrxml`) |
| `-recursive` | false | Include resources in subfolders (recursive tree traversal) |
| `-sort` | `uri` | Sort by: `uri` (path), `label` (display name), or `type` (resource type) |

### Output Format

Each resource displayed as:
```
<uri>  <label>  <type>  <updateDate>
```

### Examples

```bash
# List everything at server root
jaspergo list -server http://localhost:8080/jasperserver -user jasperadmin

# List only reports under /reports/hr recursively
jaspergo list -server http://js.example.com/jasperserver -user admin \
  -type reportUnit -recursive /reports/hr

# List all datasources, sorted by label
jaspergo list -server http://js.example.com/jasperserver -user admin \
  -type jdbcDataSource -sort label

# Dry-run: see all resources before delete
jaspergo list -server http://js.example.com/jasperserver -user admin \
  -recursive /reports
```

---

## export

Export all report units (and their JDBC datasources) from a server folder to a portable YAML manifest file and a local directory tree that mirrors the server folder structure. Useful for migrating between servers, version control, or backup.

```
jaspergo export [flags]
```

### export Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-server` | (prompted) | Server base URL |
| `-user` | (prompted) | Username |
| `-password` | (prompted) | Password |
| `-folder` | `/` | Server folder to export from (recursively) |
| `-output` | `export.yml` | Output YAML manifest file path |
| `-debug` | false | Log all HTTP requests/responses to `logs/<output-basename>.log` |

### Output Structure

Export creates two artifacts:

- **`<output>.yml`** — YAML manifest with metadata:
  - Server URL (for reference; can be overridden on import)
  - All JDBC datasources (driver, connection URL, credentials, timezone)
  - All reports with datasource references and resource metadata
  - Relative paths to local files (resolved from manifest directory)

- **`<output-basename>/`** — Directory tree mirroring server URI structure:
  - `reports/` — JRXML files organized by server folder hierarchy
  - Embedded resources (images, style templates, fonts, bundles) alongside JRXML files
  - Shared resources (resources outside report folder) in separate tree

### Details

- **Recursive**: Exports all reports and datasources under `-folder` and its subfolders
- **Themes skipped**: Resources under `/themes` are never exported (system resources)
- **Datasources included**: Only JDBC datasources referenced by reports are exported
- **All resources captured**: Inline resources (embedded in report) and URI references (shared resources) both exported

### Examples

```bash
# Export /reports folder to export.yml + export/ directory
jaspergo export -server http://localhost:8080/jasperserver -user admin \
  -folder /reports -output export.yml

# Export with debug logging
jaspergo export -server http://localhost:8080/jasperserver -user admin \
  -debug -output export.yml

# Export entire server (from root)
jaspergo export -server http://prod/jasperserver -user admin \
  -output full-backup.yml
```

---

## import

Import report units and datasources from a YAML manifest (produced by `export`). Datasources are imported first so reports can reference them. Shared resources are uploaded in a separate confirmation phase before report units that reference them. Supports smart change detection via `-auto` mode.

```
jaspergo import [flags] [manifest.yml]
```

### import Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-server` | manifest value | Target server base URL (overrides manifest `server` field) |
| `-user` | (prompted) | Username |
| `-password` | (prompted) | Password |
| `-input` | (prompted) | YAML manifest file (may also be a positional argument) |
| `-yes` | false | Overwrite all existing resources without prompting |
| `-no` | false | Skip all existing resources without prompting |
| `-auto` | false | Skip resources identical to server copy; prompt only on changes |
| `-debug` | false | Log all HTTP requests/responses to `logs/<manifest-basename>.import.log` |

### Import Order

1. **Datasources** — created or updated first (reports depend on them)
2. **Shared resources** — resources outside report folders uploaded in separate confirmation phase
3. **Report units** — uploaded last (now their URI references are valid)

This ordering prevents 400 Bad Request errors when importing reports with URI references to not-yet-created resources.

### Auto Mode (`-auto`)

When `-auto` is set, import compares each resource on target server with what would be imported and skips if identical:

**For datasources**: Compares label, driver class, connection URL, username, and timezone.
- Password omitted (server doesn't return it, can't compare)
- Any mismatch → prompt for confirmation

**For reports**: Compares label, description, JRXML content, datasource URI, and resource list.
- Changed content → prompt for confirmation
- Shows detailed metrics: resources added/removed/changed

**Behavior**: No fields overwritten silently; user always decides on mismatch or new resource.

### Existence Checks

Before importing, all parent folders are listed once to build a `map[string]bool`. This avoids per-resource GET requests, which can return HTTP 406 Not Acceptable for typed resources, causing false negatives in existence checks.

### Examples

```bash
# Import to different server than export source
jaspergo import -server http://prod/jasperserver -user admin export.yml

# Overwrite all without prompting (useful in automation)
jaspergo import -server http://prod/jasperserver -user admin -yes export.yml

# Smart import: only update reports that changed
jaspergo import -server http://prod/jasperserver -user admin -auto export.yml

# Import with debug logging to diagnose issues
jaspergo import -server http://prod/jasperserver -user admin \
  -debug -auto export.yml
```

---

## delete

Delete all resources in a server folder recursively. Respects resource dependencies by deleting report units before datasources, preventing "Operation cannot be performed" errors. Always skips `/themes` (system resources). Shows what will be deleted before requesting confirmation.

```
jaspergo delete [flags]
```

### delete Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-server` | (prompted) | Server base URL |
| `-user` | (prompted) | Username |
| `-password` | (prompted) | Password |
| `-folder` | `/` | Root folder to delete from (recursively) |
| `-yes` | false | Delete all without prompting |
| `-no` | false | Dry-run: show what would be deleted, then abort |

### Deletion Order

Resources are deleted in this order to respect dependencies:
1. **Report units** — deleted first (they reference datasources)
2. **Other types** — folders, images, fonts, bundles, etc.
3. **JDBC datasources** — deleted last (after reports that reference them)

This prevents "Operation cannot be performed" errors from the server.

### Safety Features

- Lists all resources that will be deleted before confirmation
- Requires explicit confirmation unless `-yes` flag set
- `-no` flag performs dry-run: shows what would be deleted, then aborts

### Examples

```bash
# Delete /reports interactively (shows list first, then confirms)
jaspergo delete -server http://localhost:8080/jasperserver -user admin -folder /reports

# Dry-run: preview what would be deleted
jaspergo delete -server http://localhost:8080/jasperserver -user admin \
  -folder /reports -no

# Delete everything under /reports without prompting (automation)
jaspergo delete -server http://localhost:8080/jasperserver -user admin \
  -folder /reports -yes

# Delete entire server (careful!)
jaspergo delete -server http://prod/jasperserver -user admin -yes
```

---

## mergeshared

Scan an exported YAML manifest for duplicate resource files (detected by SHA-256 content hash), consolidate them into shared resources, and update the manifest. Reduces server storage, improves maintenance (one copy to update), and enables dependency management.

```
jaspergo mergeshared [flags] [manifest.yml]
```

### mergeshared Flags

| Flag | Description |
|------|-------------|
| `-input` | YAML manifest file (may also be a positional argument) |
| `-default type=/folder` | Default server folder for a resource type (repeatable) |

### Workflow

mergeshared processes duplicates in this order:

1. **Scan for duplicates** — compute SHA-256 hash of all resource files, group by hash
2. **Prompt for consolidation** — for each duplicate group, ask for single shared URI
3. **Move files** — move canonical copy to new shared path in local export tree
4. **Delete duplicates** — remove local copies that are now references
5. **Update manifest** — rewrite all entries to point to shared URI with `shared: true`
6. **Consolidate by filename** (optional) — if multiple shared resources have same filename but different URIs, prompt to consolidate
7. **Relocate** (optional) — move shared resources outside default folder to default folder
8. **Remove unreferenced** (optional) — scan JRXML for resource references, prompt to delete unused resources

### Details

- **JRXML resources never shared** — `.jrxml` files are always per-report (even if identical)
- **SHA-256 deduplication** — detects identical content across all reports
- **Interactive** — prompts for each decision (can skip with automated flags if added)
- **In-place updates** — manifest file rewritten with new URIs and `shared: true` flags
- **Default folders** — `-default type=/folder` pre-answers prompts for specific types (img, jrtx, font, etc.)

### Use Cases

- **Storage optimization** — reduce server disk by storing one copy of shared images
- **Maintenance** — update logo in one place instead of every report
- **Dependency tracking** — know which reports use which resources
- **Migration** — organize shared resources into dedicated folders before importing to new server

### Examples

```bash
# Interactive deduplication with prompts for each duplicate group
jaspergo mergeshared export.yml

# Pre-set default folders (no prompts for these types)
jaspergo mergeshared \
  -default img=/shared/images \
  -default jrtx=/shared/styles \
  -default font=/shared/fonts \
  export.yml

# Interactive, with defaults for images but prompts for other types
jaspergo mergeshared \
  -default img=/assets/images \
  export.yml
```

---

## YAML manifest format

Manifests are portable, human-readable YAML files produced by `export` and consumed by `import`. All file paths are relative to the directory containing the manifest.

### Full Example

```yaml
server: http://source-server/jasperserver   # for reference; import -server overrides

datasources:
  - uri: /datasources/MyDB
    label: My Database
    desc: Production database
    driver_class: org.postgresql.Driver
    connection_url: jdbc:postgresql://db:5432/mydb
    username: reporter
    password: secret              # optional — omit to prompt on import
    timezone: UTC                 # optional

reports:
  - uri: /reports/hr/Headcount
    label: Headcount Report
    desc: Monthly headcount by department
    datasource: /datasources/MyDB
    jrxml: export/reports/hr/Headcount.jrxml
    resources:
      # Inline resource: uploaded with report
      - name: logo_png
        type: img
        file: export/reports/hr/Headcount/logo.png
        shared: false
      
      # Shared resource: reference to resource outside report's folder
      - name: central_jrtx
        type: jrtx
        uri: /shared/styles/central.jrtx
        shared: true
      
      # Shared resource with local file (uploaded during import)
      - name: standard_font
        type: font
        file: export/shared/fonts/Arial.ttf
        uri: /shared/fonts/Arial.ttf
        shared: true
```

### Field Reference

**Top-level:**
- `server` — Source server URL (informational; import `-server` flag overrides)

**Datasources:**
- `uri` — Server URI path (e.g., `/datasources/MyDB`)
- `label` — Display name
- `desc` — Description (optional)
- `driver_class` — Java JDBC driver class
- `connection_url` — JDBC connection URL
- `username` — Database user
- `password` — Database password (optional; omit to prompt on import, or leave blank for no auth)
- `timezone` — Timezone identifier (optional, e.g., `UTC`, `America/New_York`)

**Reports:**
- `uri` — Server URI path (e.g., `/reports/hr/Headcount`)
- `label` — Display name
- `desc` — Description (optional)
- `datasource` — Server URI of JDBC datasource (optional, e.g., `/datasources/MyDB`)
- `jrxml` — Relative path to JRXML file from manifest directory
- `resources` — Array of resource attachments (optional)

**Resources:**
- `name` — Key used in report's JRXML (e.g., `logo_png`, `central_jrtx`)
- `type` — JasperReports Server resource type (see Resource Types section)
- `file` — Relative path to resource file from manifest directory (for inline/shared with local copy)
- `uri` — Server URI path (for reference-only resources)
- `shared` — `true` if resource lives outside report's folder; `false` if inline

### Path Resolution

When importing:
- `file` paths in manifest are resolved relative to manifest directory
- `uri` paths must be absolute server paths (e.g., `/shared/images/logo.png`)
- For resources with both `file` and `uri`: file is uploaded to URI if URI doesn't exist
- For resources with only `uri`: server URI is referenced directly (no upload)

---

## Resource Types

JasperReports Server supports these resource types. jaspergo auto-detects type from file extension:

| Server Type | Extensions | Purpose |
|-------------|-----------|---------|
| `img` | `.png`, `.jpg`, `.jpeg`, `.gif`, `.svg`, `.bmp` | Images embedded in reports |
| `jrxml` | `.jrxml`, `.jasper` | Report JRXML definitions |
| `jrtx` | `.jrtx` | Style templates for report styling |
| `font` | `.ttf`, `.otf` | TrueType/OpenType fonts for report rendering |
| `jar` | `.jar` | Java libraries for custom report components |
| `resourceBundle` | `.properties` | Localization/i18n resource bundles |
| `reportUnit` | (constructed) | Report definition (container for JRXML + resources + datasource) |
| `jdbcDataSource` | (constructed) | JDBC datasource definition |
| `folder` | (constructed) | Hierarchical folder |

**Note**: `reportUnit`, `jdbcDataSource`, and `folder` are server resource types that don't map to file extensions — they're managed by jaspergo commands.

---

## Debug Logging

Pass `-debug` flag to any command that supports it for detailed HTTP traffic logging. Useful for troubleshooting connectivity, authentication, or API issues.

### Supported Commands

Commands supporting `-debug`:
- **publish** — logs request/response for each report and resource upload
- **export** — logs all list and GET requests for reports/datasources/resources
- **import** — logs all requests for datasource creation/update and report upload
- **delete** — logs all delete requests

### Log Output

Debug logs are written to:
- **Directory**: `logs/` (auto-created in current working directory)
- **export**: `logs/<output-basename>.log`
- **import**: `logs/<manifest-basename>.import.log`
- **publish**: Request/response details to stderr
- **delete**: Request/response details to stderr

Each log entry includes:
- HTTP method (GET, PUT, POST, DELETE)
- Full request URL
- Request body (if present)
- Response status code
- Response body (if present)

### Examples

```bash
# Export with debug logging
jaspergo export -server http://prod/jasperserver -user admin \
  -output export.yml -debug
# Produces: logs/export.log

# Import with debug logging to diagnose import failures
jaspergo import -server http://prod/jasperserver -user admin \
  -debug export.yml
# Produces: logs/export.import.log

# Publish with debug to diagnose upload issues
jaspergo publish -server http://localhost:8080/jasperserver \
  -user admin -debug report.jrxml
# Logs to stderr
```

---

## Architecture

jaspergo is organized into 5 packages (~3100 lines total):

```
jaspergo/
├── main.go                        # dispatcher: routes os.Args[1] to cmd package
├── cmd/
│   ├── publish.go                 # RunPublish: upload JRXML + resources
│   ├── export.go                  # RunExport, RunImport, RunMergeShared
│   ├── list.go                    # RunList: list and filter resources
│   ├── delete.go                  # RunDelete: remove resources safely
│   └── (go.mod, go.sum)
└── internal/
    ├── client/
    │   └── client.go              # JasperClient: HTTP wrapper for REST API v2
    │                              # Methods: Ping, ListResources, GetReportUnit,
    │                              # GetDatasource, PublishReport, DeleteResource,
    │                              # CreateOrUpdateDatasource
    ├── jrxml/
    │   └── jrxml.go               # JRXML XML parsing: parameters, embedded resources
    │                              # Functions: ParseParameters, ParseResources,
    │                              # ToResourceType, resolveResourcePath
    ├── models/
    │   └── models.go              # Public types: ResourceFile, ServerResource,
    │                              # ReportUnitDetail, DatasourceDetail,
    │                              # ExportManifest, ExportReport, ExportResource
    └── ui/
        └── ui.go                  # Interactive prompts: Prompt, PromptPassword,
                                   # Confirm, Fatalf, BaseName
```

### Dependency Graph (acyclic)

```
main.go              → cmd
cmd/*                → internal/{client, jrxml, models, ui}
internal/client      → internal/{jrxml, models}
internal/jrxml       → internal/models
internal/models      → (stdlib only)
internal/ui          → (stdlib only)
```

### Package Responsibilities

**main.go**: CLI dispatcher. Parses `os.Args[1]` and routes to `cmd` package subcommands.

**cmd/publish.go**: `RunPublish` orchestrates report upload — parses flags, discovers JRXML resources, handles overwrite prompts, builds multipart request via `client.PublishReport`.

**cmd/export.go**: Three commands:
- `RunExport` — lists all reports/datasources recursively, downloads content to manifest + local tree
- `RunImport` — reads manifest, creates datasources, uploads shared resources, creates reports; `-auto` mode compares vs server
- `RunMergeShared` — deduplicates resources by SHA-256, consolidates URIs in manifest

**cmd/list.go**: `RunList` — calls `client.ListResources` with optional `-recursive`, `-type` filter, sorting.

**cmd/delete.go**: `RunDelete` — lists resources to delete, sorts by type (reportUnit before datasources), prompts for confirmation, deletes via `client.DeleteResource`.

**internal/client/client.go**: `JasperClient` — HTTP wrapper around JasperReports REST API v2.
- `Ping()` — verify connectivity/auth
- `ListResources(path, recursive, type)` — paginated list (100/page)
- `ResourceExists(path)` — existence check (handles 406 for typed resources)
- `GetReportUnit(uri)` — fetch full report descriptor
- `GetDatasource(uri)` — fetch datasource descriptor
- `PublishReport(job)` — DELETE + PUT (ensures atomic, idempotent publish)
- `CreateOrUpdateDatasource(uri, desc)` — GET version, PUT with optimistic lock
- `DeleteResource(uri)` — delete single resource
- Private `ruReportUnit`, `ruDatasource` types for JSON unmarshaling
- Debug logging via `DebugWriter` (stderr by default)

**internal/jrxml/jrxml.go**: JRXML XML parsing.
- `ParseParameters` — extracts user-defined input controls from JRXML
- `ParseResources` — finds embedded resource references (images, sub-reports, fonts, style templates)
- `ToResourceType` — maps file extension → server resource type
- Returns `MissingRef` structs for unresolved `repo:/` references

**internal/models/models.go**: Public data types shared across packages.
- `ResourceFile` — one resource (name, filepath, server URI, type, shared flag)
- `JRXMLParameter` — user-defined parameter (name, class, label, description, mandatory)
- `MissingRef` — unresolved resource reference
- `ServerResource` — list entry (URI, label, type, description, date)
- `ReportUnitDetail` — full report descriptor (JRXML, datasource ref, resources)
- `DatasourceDetail` — datasource descriptor (driver, URL, credentials, version for optimistic lock)
- `ExportManifest`, `ExportDatasource`, `ExportReport`, `ExportResource` — YAML manifest structure

**internal/ui/ui.go**: Interactive CLI helpers.
- `Prompt(label)` — read line from stdin
- `MustPrompt(label)` — read line, fatal if empty
- `PromptPassword(label)` — read without echo on TTY
- `Confirm(message)` — yes/no prompt
- `Fatalf(format, args)` — print to stderr and exit
- `BaseName(path)` — filename without extension

### Key Design Patterns

**DELETE + PUT for publish**: Ensures idempotent, atomic uploads. Avoids silent duplication with POST + `?overwrite=true`.

**Optimistic locking for datasource updates**: GET version field first, PUT with version to handle concurrent updates safely.

**Bulk existence mapping**: List all folders once before loop, build map. Avoids per-resource GETs that can return 406 false negatives.

**Shared resource phase**: Upload shared resources (outside report folder) before report units, preventing 400 Bad Request from broken URI references.

**Debug logging**: Optional `-debug` flag logs all HTTP traffic to `logs/` directory for troubleshooting.

## Building & Testing

```bash
go build ./...          # build all packages
go run .                # run CLI without building
go vet ./...            # lint (staticcheck not configured)
go test ./...           # run all tests (if any)
go build -o jaspergo .  # build with explicit binary name
```

Compiled binary is `jaspergo` in the current directory.

## Dependencies

External packages (from `go.mod`):
- `golang.org/x/term` — password input without echo (cross-platform)
- `gopkg.in/yaml.v3` — YAML manifest parsing and serialization

Standard library only for HTTP, JSON, XML, flag parsing, filesystem I/O.
