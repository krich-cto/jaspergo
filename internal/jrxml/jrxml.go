package jrxml

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"jasper/go/internal/models"
)

// builtinJasperParams are internal JasperReports parameter names that should
// never become input controls.
var builtinJasperParams = map[string]bool{
	"REPORT_PARAMETERS_MAP": true, "REPORT_CONNECTION": true, "REPORT_DATA_SOURCE": true,
	"REPORT_MAX_COUNT": true, "REPORT_SORT_FIELDS": true, "REPORT_LOCALE": true,
	"REPORT_RESOURCE_BUNDLE": true, "REPORT_TIME_ZONE": true, "REPORT_FORMAT": true,
	"REPORT_VIRTUAL_IZER": true, "REPORT_FILE_RESOLVER": true, "REPORT_TEMPLATES": true,
	"REPORT_CONTEXT": true, "REPORT_URL_HANDLER_FACTORY": true, "REPORT_SCRIPTLET": true,
	"REPORT_CLASS_LOADER": true,
}

// ParseParameters extracts user-defined parameters from JRXML content.
// It skips built-in JasperReports parameters and those with isForPrompting="false".
func ParseParameters(data []byte) []models.JRXMLParameter {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = false

	var params []models.JRXMLParameter
	var cur *models.JRXMLParameter
	var inParamDesc bool

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "parameter":
				name := attrVal(t.Attr, "name")
				class := attrVal(t.Attr, "class")
				if class == "" {
					class = "java.lang.String"
				}
				forPrompting := attrVal(t.Attr, "isForPrompting")
				if name == "" || builtinJasperParams[name] || strings.ToLower(forPrompting) == "false" {
					cur = nil
				} else {
					cur = &models.JRXMLParameter{Name: name, Class: class, Label: name}
				}
			case "property":
				if cur == nil {
					continue
				}
				switch attrVal(t.Attr, "name") {
				case "com.jaspersoft.studio.js.ic.label":
					cur.Label = strings.Trim(attrVal(t.Attr, "value"), `"`)
				case "com.jaspersoft.studio.js.ic.description":
					cur.Description = strings.Trim(attrVal(t.Attr, "value"), `"`)
				case "com.jaspersoft.studio.js.ic.mandatory":
					cur.Mandatory = strings.ToLower(attrVal(t.Attr, "value")) == "true"
				}
			case "parameterDescription":
				if cur != nil {
					inParamDesc = true
				}
			}
		case xml.CharData:
			if inParamDesc && cur != nil && cur.Description == "" {
				cur.Description = strings.TrimSpace(string(t))
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "parameter":
				if cur != nil {
					params = append(params, *cur)
					cur = nil
				}
				inParamDesc = false
			case "parameterDescription":
				inParamDesc = false
			}
		}
	}
	return params
}

// knownExtRe matches quoted string literals that look like local file references.
var knownExtRe = regexp.MustCompile(`"([^"${}\\]+\.(?:png|jpg|jpeg|gif|bmp|svg|jrxml|jasper|ttf|otf|jar|properties))"`)

// repoRefRe matches repo references of the form "repo:/absolute/path" or "repo:relative.jrtx".
var repoRefRe = regexp.MustCompile(`"repo:([^"]+)"`)

// ParseResources parses the JRXML at jrxmlPath and returns all resources it references.
// Two kinds are returned:
//   - Local files (FilePath set): resolved from imageExpression / subreportExpression.
//   - Server repo refs (URI set): any CDATA containing "repo:/..." patterns.
//
// References that match a known file extension but cannot be resolved locally
// are returned separately as MissingRef so the caller can prompt the user.
func ParseResources(jrxmlPath, resourcesDir string) ([]models.ResourceFile, []models.MissingRef, error) {
	data, err := os.ReadFile(jrxmlPath)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", jrxmlPath, err)
	}

	jrxmlDir := filepath.Dir(jrxmlPath)
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	decoder.Strict = false

	var stack []string
	seen := make(map[string]bool)
	var results []models.ResourceFile
	var missing []models.MissingRef

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)

		case xml.CharData:
			text := string(t)

			for _, m := range repoRefRe.FindAllStringSubmatch(text, -1) {
				ref := m[1]
				if strings.HasPrefix(ref, "/") {
					name := filepath.Base(ref)
					if seen["repo:"+ref] {
						continue
					}
					seen["repo:"+ref] = true
					results = append(results, models.ResourceFile{
						Name: name,
						URI:  ref,
						Type: ToResourceType(name),
					})
				} else {
					key := toResourceKey(filepath.Base(ref))
					if seen[key] || seen["missing:"+key] {
						continue
					}
					localPath, ok := resolveFile(ref, resourcesDir, jrxmlDir)
					if ok {
						seen[key] = true
						results = append(results, models.ResourceFile{
							Name:     key,
							FilePath: localPath,
							Type:     ToResourceType(ref),
						})
					} else {
						seen["missing:"+key] = true
						missing = append(missing, models.MissingRef{
							Ref:  ref,
							Name: key,
							Type: ToResourceType(ref),
						})
					}
				}
			}

			if len(stack) == 0 {
				continue
			}
			parent := stack[len(stack)-1]
			if parent != "imageExpression" && parent != "subreportExpression" {
				continue
			}
			for _, ref := range extractRefs(text) {
				localPath, ok := resolveFile(ref, resourcesDir, jrxmlDir)
				if !ok {
					name := toResourceKey(filepath.Base(ref))
					if !seen["missing:"+name] {
						seen["missing:"+name] = true
						missing = append(missing, models.MissingRef{
							Ref:  ref,
							Name: name,
							Type: ToResourceType(ref),
						})
					}
					continue
				}
				key := toResourceKey(filepath.Base(ref))
				if seen[key] {
					continue
				}
				seen[key] = true
				results = append(results, models.ResourceFile{
					Name:     key,
					FilePath: localPath,
					Type:     ToResourceType(ref),
				})
			}

		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}

	return results, missing, nil
}

// ToResourceType maps a file extension to a JasperReports Server resource type string.
func ToResourceType(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".bmp":
		return "img"
	case ".jrxml", ".jasper":
		return "jrxml"
	case ".jrtx":
		return "jrtx"
	case ".ttf", ".otf":
		return "font"
	case ".jar":
		return "jar"
	case ".properties":
		return "resourceBundle"
	default:
		return "file"
	}
}

// toResourceKey converts a filename to a safe resource key for JasperReports Server.
// "logo.png" → "logo_png", "sub-report.jrxml" → "sub_report_jrxml"
func toResourceKey(filename string) string {
	return strings.NewReplacer(".", "_", " ", "_", "-", "_").Replace(filename)
}

func extractRefs(expr string) []string {
	var refs []string
	for _, m := range knownExtRe.FindAllStringSubmatch(expr, -1) {
		ref := m[1]
		if !strings.HasPrefix(ref, "http://") && !strings.HasPrefix(ref, "https://") {
			refs = append(refs, ref)
		}
	}
	return refs
}

func resolveFile(ref, resourcesDir, jrxmlDir string) (string, bool) {
	base := filepath.Base(ref)
	candidates := []string{
		filepath.Join(resourcesDir, base),
		filepath.Join(resourcesDir, ref),
		filepath.Join(jrxmlDir, base),
		filepath.Join(jrxmlDir, ref),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			abs, err := filepath.Abs(p)
			if err == nil {
				return abs, true
			}
			return p, true
		}
	}
	return "", false
}

func attrVal(attrs []xml.Attr, name string) string {
	for _, a := range attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
