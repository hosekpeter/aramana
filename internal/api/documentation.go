package api

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"

	"gopkg.in/yaml.v3"

	"aramana/docs"
	"aramana/internal/urlutils"

	swaggerui "aramana/third_party"
)

// Documentation serves the Swagger UI interface for interactive API documentation.
// @Summary API Documentation
// @Description Interactive API documentation with Swagger UI.
// @Tags documentation
// @Produce html
// @Success 200 {string} string "HTML page with Swagger UI"
// @Router /documentation [get].
func Documentation(docsPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, docsPath)

		if path == "/" || path == "" {
			serveSwaggerUI(w, r, docsPath)
			return
		}
		if path == "/swagger.json" {
			serveSwaggerJSON(w, r)
			return
		}
		if path == "/swagger.yaml" {
			serveSwaggerYAML(w, r)
			return
		}
		if path == "/openapi.json" {
			serveOpenAPIJSON(w, r)
			return
		}

		serveSwaggerAsset(w, path)
	}
}

func serveSwaggerJSON(w http.ResponseWriter, r *http.Request) {
	b, err := docs.SpecJSON()
	if err != nil {
		http.Error(w, "Failed to read swagger.json", http.StatusInternalServerError)
		return
	}

	var swaggerSpec map[string]any
	if err := json.Unmarshal(b, &swaggerSpec); err != nil {
		http.Error(w, "Failed to parse swagger.json", http.StatusInternalServerError)
		return
	}

	baseURL := urlutils.GetBaseURL(r)
	swaggerSpec["host"] = baseURL.Host
	swaggerSpec["schemes"] = []string{baseURL.Scheme}

	modifiedContent, err := json.MarshalIndent(swaggerSpec, "", "    ")
	if err != nil {
		http.Error(w, "Failed to marshal modified swagger.json", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(modifiedContent)
	if err != nil {
		http.Error(w, "Failed to write file content", http.StatusInternalServerError)
		return
	}
}

func serveSwaggerYAML(w http.ResponseWriter, r *http.Request) {
	b, err := docs.SpecYAML()
	if err != nil {
		http.Error(w, "Failed to read swagger.yaml", http.StatusInternalServerError)
		return
	}

	var swaggerSpec map[string]any
	if err := yaml.Unmarshal(b, &swaggerSpec); err != nil {
		http.Error(w, "Failed to parse swagger.yaml", http.StatusInternalServerError)
		return
	}

	baseURL := urlutils.GetBaseURL(r)
	swaggerSpec["host"] = baseURL.Host
	swaggerSpec["schemes"] = []string{baseURL.Scheme}

	modifiedContent, err := yaml.Marshal(swaggerSpec)
	if err != nil {
		http.Error(w, "Failed to marshal modified swagger.yaml", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, err = w.Write(modifiedContent)
	if err != nil {
		http.Error(w, "Failed to write file content", http.StatusInternalServerError)
		return
	}
}

func serveOpenAPIJSON(w http.ResponseWriter, r *http.Request) {
	b, err := docs.SpecJSON()
	if err != nil {
		http.Error(w, "Failed to read openapi.json", http.StatusInternalServerError)
		return
	}

	var openapiSpec map[string]any
	if err := json.Unmarshal(b, &openapiSpec); err != nil {
		http.Error(w, "Failed to parse openapi.json", http.StatusInternalServerError)
		return
	}

	baseURL := urlutils.GetBaseURL(r)
	openapiSpec["servers"] = []map[string]string{{"url": baseURL.String()}}

	modifiedContent, err := json.MarshalIndent(openapiSpec, "", "    ")
	if err != nil {
		http.Error(w, "Failed to marshal modified openapi.json", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(modifiedContent)
	if err != nil {
		http.Error(w, "Failed to write file content", http.StatusInternalServerError)
		return
	}
}

func serveSwaggerUI(w http.ResponseWriter, r *http.Request, docsPath string) {
	rawBaseURL := fmt.Sprintf("%s%s", urlutils.GetBaseURL(r), docsPath)

	content, err := swaggerui.SwaggerFS.ReadFile("swagger-ui/index.html")
	if err != nil {
		http.Error(w, "Failed to read Swagger UI", http.StatusInternalServerError)
		return
	}

	// HTML attribute contexts: html.EscapeString prevents attribute injection.
	htmlBase := html.EscapeString(rawBaseURL)

	// JavaScript string context: json.Marshal produces a properly quoted and escaped JS string
	// (handles quotes, backslashes and other special chars that html.EscapeString does not cover).
	jsSpecURL, err := json.Marshal(rawBaseURL + "/swagger.json")
	if err != nil {
		http.Error(w, "Failed to build documentation URL", http.StatusInternalServerError)
		return
	}

	pageHTML := string(content)
	pageHTML = strings.ReplaceAll(pageHTML, `href="./swagger-ui.css"`, fmt.Sprintf(`href="%s/swagger-ui.css"`, htmlBase))
	pageHTML = strings.ReplaceAll(pageHTML, `href="./favicon-32x32.png"`, fmt.Sprintf(`href="%s/favicon-32x32.png"`, htmlBase))
	pageHTML = strings.ReplaceAll(pageHTML, `href="./favicon-16x16.png"`, fmt.Sprintf(`href="%s/favicon-16x16.png"`, htmlBase))
	pageHTML = strings.ReplaceAll(pageHTML, `src="./swagger-ui-bundle.js"`, fmt.Sprintf(`src="%s/swagger-ui-bundle.js"`, htmlBase))
	pageHTML = strings.ReplaceAll(pageHTML, `src="./swagger-ui-standalone-preset.js"`, fmt.Sprintf(`src="%s/swagger-ui-standalone-preset.js"`, htmlBase))
	pageHTML = strings.ReplaceAll(pageHTML, `url: "openapi3.json"`, fmt.Sprintf(`url: %s`, jsSpecURL))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = w.Write([]byte(pageHTML))
	if err != nil {
		http.Error(w, "Failed to write file content", http.StatusInternalServerError)
		return
	}
}

func serveSwaggerAsset(w http.ResponseWriter, path string) {
	filePath := strings.TrimPrefix(path, "/")

	//nolint:gosec // G304: path is constructed from a stripped prefix, not direct user input
	content, err := swaggerui.SwaggerFS.ReadFile("swagger-ui/" + filePath)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	contentType := getContentType(filePath)
	w.Header().Set("Content-Type", contentType)

	//nolint:gosec // G705: content is read from compile-time embedded FS, not user-controlled
	_, err = w.Write(content)
	if err != nil {
		http.Error(w, "Failed to write file content", http.StatusInternalServerError)
		return
	}
}

func getContentType(filename string) string {
	switch {
	case strings.HasSuffix(filename, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(filename, ".css"):
		return "text/css"
	case strings.HasSuffix(filename, ".js"):
		return "application/javascript"
	case strings.HasSuffix(filename, ".png"):
		return "image/png"
	case strings.HasSuffix(filename, ".jpg"), strings.HasSuffix(filename, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(filename, ".gif"):
		return "image/gif"
	case strings.HasSuffix(filename, ".ico"):
		return "image/x-icon"
	case strings.HasSuffix(filename, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(filename, ".woff"):
		return "font/woff"
	case strings.HasSuffix(filename, ".woff2"):
		return "font/woff2"
	case strings.HasSuffix(filename, ".ttf"):
		return "font/ttf"
	case strings.HasSuffix(filename, ".eot"):
		return "application/vnd.ms-fontobject"
	default:
		return "application/octet-stream"
	}
}
