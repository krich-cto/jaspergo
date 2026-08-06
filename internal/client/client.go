package client

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"jasper/go/internal/jrxml"
	"jasper/go/internal/models"
)

// JasperClient is an HTTP client for the JasperReports Server REST API v2.
type JasperClient struct {
	baseURL     string
	user        string
	pass        string
	hc          *http.Client
	Debug       bool
	DebugWriter io.Writer
}

// NewJasperClient creates a client for the given server. baseURL should be the
// server root, e.g. "http://localhost:8080/jasperserver". A trailing slash and
// any "/rest_v2" suffix are stripped automatically.
func NewJasperClient(baseURL, user, pass string) *JasperClient {
	base := strings.TrimRight(baseURL, "/")
	base = strings.TrimSuffix(base, "/rest_v2")
	return &JasperClient{
		baseURL:     base,
		user:        user,
		pass:        pass,
		hc:          &http.Client{Timeout: 60 * time.Second},
		DebugWriter: os.Stderr,
	}
}

const repoAPI = "/resources"

func (c *JasperClient) url(path string) string {
	return c.baseURL + "/rest_v2" + path
}

// Ping verifies connectivity and authentication.
func (c *JasperClient) Ping() error {
	resp, err := c.do(http.MethodGet, repoAPI+"?limit=1", "application/json", nil, "")
	if err != nil {
		return err
	}
	defer drainClose(resp)

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("authentication failed — check username and password")
	case http.StatusForbidden:
		return fmt.Errorf("access denied — account may lack REST API permissions")
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned %s", resp.Status)
	}
	return nil
}

// ResourceExists returns true when a resource already exists at serverPath.
func (c *JasperClient) ResourceExists(serverPath string) (bool, error) {
	resp, err := c.do(http.MethodGet, repoAPI+serverPath, "*/*", nil, "")
	if err != nil {
		return false, err
	}
	defer drainClose(resp)

	switch resp.StatusCode {
	case http.StatusOK, http.StatusNotAcceptable:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	case http.StatusUnauthorized:
		return false, fmt.Errorf("authentication failed")
	default:
		return false, fmt.Errorf("unexpected status %s", resp.Status)
	}
}

// ListResources returns the resources inside folderPath.
func (c *JasperClient) ListResources(folderPath string, recursive bool, resourceType string) ([]models.ServerResource, error) {
	const pageSize = 100
	var all []models.ServerResource
	for offset := 0; ; offset += pageSize {
		page, total, err := c.listResourcesPage(folderPath, recursive, resourceType, offset, pageSize)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if (total > 0 && len(all) >= total) || len(page) < pageSize {
			break
		}
	}
	return all, nil
}

func (c *JasperClient) listResourcesPage(folderPath string, recursive bool, resourceType string, offset, limit int) ([]models.ServerResource, int, error) {
	q := url.Values{}
	q.Set("folderUri", folderPath)
	q.Set("offset", strconv.Itoa(offset))
	q.Set("limit", strconv.Itoa(limit))
	if recursive {
		q.Set("recursive", "true")
	}
	if resourceType != "" {
		q.Set("type", resourceType)
	}

	resp, err := c.do(http.MethodGet, repoAPI+"?"+q.Encode(), "application/json", nil, "")
	if err != nil {
		return nil, 0, err
	}
	defer drainClose(resp)

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, 0, fmt.Errorf("authentication failed")
	case http.StatusNotFound:
		return nil, 0, fmt.Errorf("folder not found: %s", folderPath)
	case http.StatusOK:
	default:
		body, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("server returned %s: %s", resp.Status, truncate(string(body), 200))
	}

	var result struct {
		ResourceLookup []models.ServerResource `json:"resourceLookup"`
		TotalCount     int                     `json:"totalCount"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, 0, fmt.Errorf("decoding response: %w", err)
	}
	return result.ResourceLookup, result.TotalCount, nil
}

// GetReportUnit fetches the full report unit descriptor for serverPath.
// When the JRXML or any resource is stored as a separate repository file,
// it fetches the file resource automatically and fills in Content and Type.
func (c *JasperClient) GetReportUnit(serverPath string) (*models.ReportUnitDetail, error) {
	resp, err := c.do(http.MethodGet, repoAPI+serverPath,
		"application/repository.reportUnit+json", nil, "")
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("report not found: %s", serverPath)
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("authentication failed")
	default:
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %s: %s", resp.Status, truncate(string(body), 200))
	}

	var detail models.ReportUnitDetail
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return nil, fmt.Errorf("decoding report unit: %w", err)
	}

	if detail.JRXML != nil {
		var jrxmlURI string
		if detail.JRXML.JRXMLFileReference != nil {
			jrxmlURI = detail.JRXML.JRXMLFileReference.URI
		} else if detail.JRXML.JRXMLFile != nil && detail.JRXML.JRXMLFile.Content == "" && detail.JRXML.JRXMLFile.URI != "" {
			jrxmlURI = detail.JRXML.JRXMLFile.URI
		}
		if jrxmlURI != "" {
			raw, err := c.fetchRawFile(jrxmlURI)
			if err != nil {
				return nil, fmt.Errorf("fetching jrxml %s: %w", jrxmlURI, err)
			}
			if detail.JRXML.JRXMLFile == nil {
				detail.JRXML.JRXMLFile = &struct {
					URI     string `json:"uri"`
					Content string `json:"content"`
				}{}
			}
			detail.JRXML.JRXMLFile.URI = jrxmlURI
			detail.JRXML.JRXMLFile.Content = base64.StdEncoding.EncodeToString(raw)
		}
	}

	if detail.Resources != nil {
		for i := range detail.Resources.Resource {
			res := &detail.Resources.Resource[i]
			var fileURI string
			var knownType string
			if res.File.FileReference != nil {
				fileURI = res.File.FileReference.URI
			} else if res.File.FileResource != nil && res.File.FileResource.Content == "" && res.File.FileResource.URI != "" {
				fileURI = res.File.FileResource.URI
				knownType = res.File.FileResource.Type
			}
			if fileURI == "" {
				continue
			}
			raw, err := c.fetchRawFile(fileURI)
			if err != nil {
				return nil, fmt.Errorf("fetching resource %s: %w", fileURI, err)
			}
			res.File.FileReference = nil
			res.File.FileResource = &struct {
				URI     string `json:"uri"`
				Type    string `json:"type"`
				Content string `json:"content"`
			}{
				URI:     fileURI,
				Type:    coalesce(knownType, jrxml.ToResourceType(fileURI)),
				Content: base64.StdEncoding.EncodeToString(raw),
			}
		}
	}

	return &detail, nil
}

func (c *JasperClient) fetchRawFile(fileURI string) ([]byte, error) {
	resp, err := c.do(http.MethodGet, repoAPI+fileURI, "application/octet-stream", nil, "")
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %s: %s", resp.Status, truncate(string(body), 200))
	}
	return io.ReadAll(resp.Body)
}

// UploadSharedFile uploads a local file to a specific server repository URI.
func (c *JasperClient) UploadSharedFile(serverURI, resourceType, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	// Check if resource exists and get its version for optimistic locking
	var version int64
	resp, err := c.do(http.MethodGet, repoAPI+serverURI, "application/repository.file+json", nil, "")
	if err == nil && resp.StatusCode == http.StatusOK {
		defer drainClose(resp)
		var existing struct {
			Version int64 `json:"version"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&existing); err == nil {
			version = existing.Version
		}
	} else if resp != nil {
		defer drainClose(resp)
	}

	descriptor := struct {
		URI          string `json:"uri"`
		Label        string `json:"label"`
		ResourceType string `json:"resourceType"`
		Type         string `json:"type"`
		Version      int64  `json:"version"`
		Content      string `json:"content"`
	}{
		URI:          serverURI,
		Label:        filepath.Base(serverURI),
		ResourceType: "file",
		Type:         resourceType,
		Version:      version,
		Content:      base64.StdEncoding.EncodeToString(data),
	}

	body, err := json.Marshal(descriptor)
	if err != nil {
		return fmt.Errorf("marshaling file descriptor: %w", err)
	}

	resp, err = c.do(
		http.MethodPut,
		repoAPI+serverURI+"?createFolders=true",
		"application/repository.file+json",
		bytes.NewReader(body),
		"application/repository.file+json",
	)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %s: %s", resp.Status, truncate(string(b), 300))
	}
	return nil
}

// PublishReport creates or updates the report unit at serverPath.
// It deletes any existing resource first, then PUTs to the full resource URI.
func (c *JasperClient) PublishReport(
	serverPath, label, description, datasourceURI, jrxmlPath string,
	resources []models.ResourceFile,
) error {
	jrxmlData, err := os.ReadFile(jrxmlPath)
	if err != nil {
		return fmt.Errorf("reading jrxml: %w", err)
	}

	resEntries := make([]ruResourceEntry, 0, len(resources))
	for _, r := range resources {
		if r.URI != "" && (r.FilePath == "" || r.Shared) {
			resEntries = append(resEntries, ruResourceEntry{
				Name: r.Name,
				File: ruFileWrapper{FileReference: &ruFileReference{URI: r.URI}},
			})
			continue
		}
		data, err := os.ReadFile(r.FilePath)
		if err != nil {
			return fmt.Errorf("reading resource %s: %w", r.Name, err)
		}
		resEntries = append(resEntries, ruResourceEntry{
			Name: r.Name,
			File: ruFileWrapper{
				FileResource: &ruFileResource{
					Label:   r.Name,
					Type:    r.Type,
					Content: base64.StdEncoding.EncodeToString(data),
				},
			},
		})
	}

	params := jrxml.ParseParameters(jrxmlData)
	inputControls := buildInputControls(params)

	descriptor := ruDescriptor{
		URI:                  serverPath,
		Label:                label,
		Description:          description,
		ResourceType:         "reportUnit",
		AlwaysPromptControls: false,
		ControlsLayout:       "popupScreen",
		JRXML: ruJRXML{
			JRXMLFile: ruJRXMLFile{
				Type:    "jrxml",
				Label:   "main_jrxml",
				Content: base64.StdEncoding.EncodeToString(jrxmlData),
			},
		},
	}
	if len(inputControls) > 0 {
		descriptor.InputControls = inputControls
	}
	if datasourceURI != "" {
		descriptor.DataSource = &ruDataSource{
			DataSourceReference: ruDataSourceRef{URI: datasourceURI},
		}
	}
	if len(resEntries) > 0 {
		descriptor.Resources = &ruResources{Resource: resEntries}
	}

	descJSON, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling descriptor: %w", err)
	}

	if c.Debug {
		_ = os.MkdirAll("logs", 0o755)
		debugFile := filepath.Join("logs", strings.TrimSuffix(filepath.Base(jrxmlPath), ".jrxml")+".publish.debug.json")
		_ = os.WriteFile(debugFile, descJSON, 0o644)
		fmt.Fprintf(c.DebugWriter, "\n%s\n", strings.Repeat("─", 80))
		fmt.Fprintf(c.DebugWriter, "[%s] PUBLISH %s\n", time.Now().Format("15:04:05.000"), serverPath)
		fmt.Fprintf(c.DebugWriter, "   JRXML:     %s (%d bytes)\n", jrxmlPath, len(jrxmlData))
		fmt.Fprintf(c.DebugWriter, "   Payload:   %d bytes → %s\n", len(descJSON), debugFile)
		fmt.Fprintf(c.DebugWriter, "   Resources: %d\n", len(resEntries))
		for _, r := range resEntries {
			if r.File.FileReference != nil {
				fmt.Fprintf(c.DebugWriter, "     • %s (ref → %s)\n", r.Name, r.File.FileReference.URI)
			} else if r.File.FileResource != nil {
				fmt.Fprintf(c.DebugWriter, "     • %s (inline %s, %d chars b64)\n", r.Name, r.File.FileResource.Type, len(r.File.FileResource.Content))
			}
		}
	}

	if err := c.deleteResource(serverPath); err != nil {
		return fmt.Errorf("deleting existing resource before recreate: %w", err)
	}

	resp, err := c.do(
		http.MethodPut,
		repoAPI+serverPath+"?createFolders=true",
		"application/repository.reportUnit+json",
		bytes.NewReader(descJSON),
		"application/repository.reportUnit+json",
	)
	if err != nil {
		return err
	}
	defer drainClose(resp)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %s: %s", resp.Status, truncate(string(body), 300))
	}
	return nil
}

// GetDatasource fetches the JDBC datasource descriptor at serverPath.
func (c *JasperClient) GetDatasource(serverPath string) (*models.DatasourceDetail, error) {
	resp, err := c.do(http.MethodGet, repoAPI+serverPath,
		"application/repository.jdbcDataSource+json", nil, "")
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("datasource not found: %s", serverPath)
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("authentication failed")
	default:
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %s: %s", resp.Status, truncate(string(body), 200))
	}

	var ds models.DatasourceDetail
	if err := json.NewDecoder(resp.Body).Decode(&ds); err != nil {
		return nil, fmt.Errorf("decoding datasource: %w", err)
	}
	return &ds, nil
}

// CreateOrUpdateDatasource creates or updates the JDBC datasource at ds.URI.
func (c *JasperClient) CreateOrUpdateDatasource(ds *models.DatasourceDetail, overwrite bool) error {
	if overwrite {
		existing, err := c.GetDatasource(ds.URI)
		if err != nil {
			return fmt.Errorf("fetching current datasource version: %w", err)
		}
		ds.Version = existing.Version

		body, err := json.Marshal(ds)
		if err != nil {
			return fmt.Errorf("marshaling datasource: %w", err)
		}
		resp, err := c.do(
			http.MethodPut,
			repoAPI+ds.URI,
			"application/repository.jdbcDataSource+json",
			bytes.NewReader(body),
			"application/repository.jdbcDataSource+json",
		)
		if err != nil {
			return err
		}
		defer drainClose(resp)
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			b, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("server returned %s: %s", resp.Status, truncate(string(b), 300))
		}
		return nil
	}

	body, err := json.Marshal(ds)
	if err != nil {
		return fmt.Errorf("marshaling datasource: %w", err)
	}
	lastSlash := strings.LastIndex(ds.URI, "/")
	folderPath := ds.URI[:lastSlash]
	if folderPath == "" {
		folderPath = "/"
	}
	resp, err := c.do(
		http.MethodPost,
		repoAPI+folderPath+"?createFolders=true",
		"application/repository.jdbcDataSource+json",
		bytes.NewReader(body),
		"application/repository.jdbcDataSource+json",
	)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %s: %s", resp.Status, truncate(string(b), 300))
	}
	return nil
}

func (c *JasperClient) deleteResource(serverPath string) error {
	resp, err := c.do(http.MethodDelete, repoAPI+serverPath, "application/json", nil, "")
	if err != nil {
		return err
	}
	defer drainClose(resp)
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %s: %s", resp.Status, truncate(string(body), 200))
	}
	return nil
}

// DeleteAllResources deletes the given resources in dependency-safe order:
// reportUnits first, then other resource types, datasources last.
func (c *JasperClient) DeleteAllResources(resources []models.ServerResource, progress func(uri string)) (int, []error) {
	deleteTypeOrder := func(t string) int {
		switch t {
		case "reportUnit":
			return 0
		case "jdbcDataSource":
			return 2
		default:
			return 1
		}
	}
	sorted := make([]models.ServerResource, len(resources))
	copy(sorted, resources)
	sort.Slice(sorted, func(i, j int) bool {
		return deleteTypeOrder(sorted[i].ResourceType) < deleteTypeOrder(sorted[j].ResourceType)
	})

	deleted := 0
	var errs []error
	for _, r := range sorted {
		if progress != nil {
			progress(r.URI)
		}
		if err := c.deleteResource(r.URI); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.URI, err))
		} else {
			deleted++
		}
	}
	return deleted, errs
}

// --- internal HTTP helpers ---

func (c *JasperClient) do(method, path, accept string, body io.Reader, contentType string) (*http.Response, error) {
	fullURL := c.url(path)

	var bodyBytes []byte
	if body != nil {
		bodyBytes, _ = io.ReadAll(body)
		body = bytes.NewReader(bodyBytes)
	}

	if c.Debug {
		fmt.Fprintf(c.DebugWriter, "\n%s\n", strings.Repeat("─", 80))
		fmt.Fprintf(c.DebugWriter, "[%s] → %s %s\n", time.Now().Format("15:04:05.000"), method, fullURL)
		if contentType != "" {
			fmt.Fprintf(c.DebugWriter, "   Content-Type: %s\n", contentType)
		}
		if len(bodyBytes) > 0 {
			fmt.Fprintf(c.DebugWriter, "   Body: %d bytes — %s\n", len(bodyBytes), sanitizeDebug(bodyBytes, 120))
		}
	}

	req, err := http.NewRequest(method, fullURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.user, c.pass)
	req.Header.Set("Accept", accept)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	start := time.Now()
	resp, err := c.hc.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		if c.Debug {
			fmt.Fprintf(c.DebugWriter, "   ERROR: %v\n", err)
		}
		return nil, err
	}

	if c.Debug {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		fmt.Fprintf(c.DebugWriter, "← %s (%dms)\n", resp.Status, elapsed.Milliseconds())
		if len(raw) > 0 {
			if pretty := prettyJSON(raw); pretty != "" {
				fmt.Fprintln(c.DebugWriter, pretty)
			} else {
				fmt.Fprintf(c.DebugWriter, "%s\n", sanitizeDebug(raw, 500))
			}
		}
	}

	return resp, nil
}

func prettyJSON(b []byte) string {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return ""
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	const max = 4000
	s := string(out)
	if len(s) > max {
		return s[:max] + "\n  … (truncated)"
	}
	return s
}

func drainClose(resp *http.Response) {
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func sanitizeDebug(b []byte, n int) string {
	if len(b) > n {
		b = b[:n]
	}
	return strings.Map(func(r rune) rune {
		if r < 32 {
			return '·'
		}
		return r
	}, string(b))
}

func coalesce(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// --- JSON descriptor types for reportUnit REST API ---

type ruDescriptor struct {
	URI                       string                `json:"uri"`
	Label                     string                `json:"label"`
	Description               string                `json:"description"`
	ResourceType              string                `json:"resourceType"`
	Query                     interface{}           `json:"query"`
	InputControlRenderingView string                `json:"inputControlRenderingView"`
	ReportRenderingView       string                `json:"reportRenderingView"`
	AlwaysPromptControls      bool                  `json:"alwaysPromptControls"`
	ControlsLayout            string                `json:"controlsLayout"`
	DataSource                *ruDataSource         `json:"dataSource,omitempty"`
	JRXML                     ruJRXML               `json:"jrxml"`
	Resources                 *ruResources          `json:"resources,omitempty"`
	InputControls             []ruInputControlEntry `json:"inputControls,omitempty"`
}

type ruInputControlEntry struct {
	InputControl ruInputControl `json:"inputControl"`
}

type ruInputControl struct {
	Label     string             `json:"label"`
	Mandatory bool               `json:"mandatory"`
	ReadOnly  bool               `json:"readOnly"`
	Visible   bool               `json:"visible"`
	Type      int                `json:"type"`
	DataType  *ruDataTypeWrapper `json:"dataType,omitempty"`
}

type ruDataTypeWrapper struct {
	DataType ruDataType `json:"dataType"`
}

type ruDataType struct {
	Label     string `json:"label"`
	Type      string `json:"type"`
	StrictMax bool   `json:"strictMax"`
	StrictMin bool   `json:"strictMin"`
}

func buildInputControls(params []models.JRXMLParameter) []ruInputControlEntry {
	var controls []ruInputControlEntry
	for _, p := range params {
		label := p.Label
		if label == "" {
			label = p.Name
		}
		if p.Class == "java.lang.Boolean" {
			controls = append(controls, ruInputControlEntry{
				InputControl: ruInputControl{
					Label: label, Mandatory: p.Mandatory, Visible: true, Type: 1,
				},
			})
			continue
		}
		var dtType string
		switch p.Class {
		case "java.lang.Integer", "java.lang.Long", "java.lang.Short",
			"java.lang.Double", "java.lang.Float",
			"java.math.BigDecimal", "java.math.BigInteger":
			dtType = "number"
		case "java.util.Date", "java.sql.Date", "java.sql.Timestamp",
			"java.time.LocalDate", "java.time.LocalDateTime",
			"java.time.OffsetDateTime", "java.time.ZonedDateTime":
			dtType = "date"
		default:
			dtType = "text"
		}
		controls = append(controls, ruInputControlEntry{
			InputControl: ruInputControl{
				Label:     label,
				Mandatory: p.Mandatory,
				Visible:   true,
				Type:      2,
				DataType: &ruDataTypeWrapper{
					DataType: ruDataType{
						Label:     p.Name + "_datatype",
						Type:      dtType,
						StrictMax: false,
						StrictMin: false,
					},
				},
			},
		})
	}
	return controls
}

type ruDataSource struct {
	DataSourceReference ruDataSourceRef `json:"dataSourceReference"`
}

type ruDataSourceRef struct {
	URI string `json:"uri"`
}

type ruJRXML struct {
	JRXMLFile ruJRXMLFile `json:"jrxmlFile"`
}

type ruJRXMLFile struct {
	Type    string `json:"type"`
	Label   string `json:"label"`
	Content string `json:"content"`
}

type ruResources struct {
	Resource []ruResourceEntry `json:"resource,omitempty"`
}

type ruResourceEntry struct {
	Name string        `json:"name"`
	File ruFileWrapper `json:"file"`
}

type ruFileWrapper struct {
	FileResource  *ruFileResource  `json:"fileResource,omitempty"`
	FileReference *ruFileReference `json:"fileReference,omitempty"`
}

type ruFileResource struct {
	Label   string `json:"label"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

type ruFileReference struct {
	URI string `json:"uri"`
}
