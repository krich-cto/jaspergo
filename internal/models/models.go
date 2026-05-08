package models

// ResourceFile is a file resource included in a published report unit.
//   - FilePath set, URI empty: upload the file inline.
//   - URI set, FilePath empty: reference an existing server repository resource.
//   - Both set, Shared true: upload to URI first, then reference by URI.
//   - Both set, Shared false: upload inline.
type ResourceFile struct {
	Name     string // resource key in the server descriptor
	FilePath string // local filesystem path
	URI      string // server URI
	Type     string // JasperReports Server type: "img", "jrxml", "jrtx", etc.
	Shared   bool   // true when the resource lives outside the report's own folder
}

// JRXMLParameter is a user-defined parameter extracted from a JRXML file.
type JRXMLParameter struct {
	Name        string
	Class       string // Java class, e.g. "java.lang.String"
	Label       string // display label (falls back to Name)
	Description string
	Mandatory   bool
}

// MissingRef is a resource referenced in a JRXML expression that could not be
// resolved to a local file. The caller should prompt the user for a replacement.
type MissingRef struct {
	Ref  string // original reference string as it appears in the JRXML
	Name string // suggested resource key (safe for JasperReports Server)
	Type string // JasperReports Server resource type
}

// ServerResource is a single entry returned by the resources listing endpoint.
type ServerResource struct {
	URI          string `json:"uri"`
	Label        string `json:"label"`
	Description  string `json:"description"`
	ResourceType string `json:"resourceType"`
	UpdateDate   string `json:"updateDate"`
}

// ReportUnitDetail holds the full report unit descriptor returned by the server.
type ReportUnitDetail struct {
	URI          string `json:"uri"`
	Label        string `json:"label"`
	Description  string `json:"description"`
	ResourceType string `json:"resourceType"`
	DataSource   *struct {
		DataSourceReference struct {
			URI string `json:"uri"`
		} `json:"dataSourceReference"`
	} `json:"dataSource,omitempty"`
	JRXML *struct {
		JRXMLFile *struct {
			URI     string `json:"uri"`
			Content string `json:"content"`
		} `json:"jrxmlFile,omitempty"`
		JRXMLFileReference *struct {
			URI string `json:"uri"`
		} `json:"jrxmlFileReference,omitempty"`
	} `json:"jrxml,omitempty"`
	Resources *struct {
		Resource []struct {
			Name string `json:"name"`
			File struct {
				FileResource *struct {
					URI     string `json:"uri"`
					Type    string `json:"type"`
					Content string `json:"content"`
				} `json:"fileResource,omitempty"`
				FileReference *struct {
					URI string `json:"uri"`
				} `json:"fileReference,omitempty"`
			} `json:"file"`
		} `json:"resource"`
	} `json:"resources,omitempty"`
}

// DatasourceDetail holds the JDBC datasource descriptor from the server.
type DatasourceDetail struct {
	URI           string `json:"uri"`
	Label         string `json:"label"`
	Description   string `json:"description"`
	ResourceType  string `json:"resourceType"`
	Version       int    `json:"version"`
	DriverClass   string `json:"driverClass"`
	ConnectionURL string `json:"connectionUrl"`
	Username      string `json:"username"`
	Password      string `json:"password,omitempty"`
	Timezone      string `json:"timezone,omitempty"`
}

// ExportManifest is the root of an exported YAML file.
type ExportManifest struct {
	Server      string             `yaml:"server,omitempty"`
	Datasources []ExportDatasource `yaml:"datasources,omitempty"`
	Reports     []ExportReport     `yaml:"reports"`
}

// ExportDatasource holds a JDBC datasource definition.
type ExportDatasource struct {
	URI           string `yaml:"uri"`
	Label         string `yaml:"label"`
	Description   string `yaml:"desc,omitempty"`
	DriverClass   string `yaml:"driver_class"`
	ConnectionURL string `yaml:"connection_url"`
	Username      string `yaml:"username"`
	Password      string `yaml:"password,omitempty"`
	Timezone      string `yaml:"timezone,omitempty"`
}

// ExportReport holds all data for one exported report unit.
type ExportReport struct {
	URI         string           `yaml:"uri"`
	Label       string           `yaml:"label"`
	Description string           `yaml:"desc,omitempty"`
	Datasource  string           `yaml:"datasource,omitempty"`
	JRXML       string           `yaml:"jrxml"` // relative path to the .jrxml file
	Resources   []ExportResource `yaml:"resources,omitempty"`
}

// ExportResource holds one resource attached to a report unit.
type ExportResource struct {
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`
	File   string `yaml:"file,omitempty"`   // relative path to the resource file
	URI    string `yaml:"uri,omitempty"`    // server URI of the resource
	Shared bool   `yaml:"shared,omitempty"` // true when the resource lives outside the report's folder
}
