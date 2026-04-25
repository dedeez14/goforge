// Package openapi turns a programmatically registered route catalogue
// into an OpenAPI 3.1 document and serves it (plus a Swagger UI page)
// from the running application.
//
// The pattern is opt-in: handlers that want to appear in the spec
// register themselves with `openapi.Document.AddOperation(...)`. The
// payload structure is reflected from real Go DTO types so the
// generated schema matches the wire format byte-for-byte. Compared to
// `swag` comments this avoids a build-time generator step and stale
// docs caused by code drifting away from comments.
package openapi

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2"
)

// Info holds the document-level metadata.
type Info struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

// Server describes a server entry in the OpenAPI document.
type Server struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// SecurityScheme describes a security scheme. Only the bearer-JWT
// shape is wired into the document by default but custom apps can add
// more.
type SecurityScheme struct {
	Type         string `json:"type"`
	Scheme       string `json:"scheme,omitempty"`
	BearerFormat string `json:"bearerFormat,omitempty"`
	In           string `json:"in,omitempty"`
	Name         string `json:"name,omitempty"`
}

// Operation describes a single HTTP operation. RequestType/ResponseType
// are concrete Go types; reflection produces the JSON Schema for them.
type Operation struct {
	Method        string
	Path          string
	Summary       string
	Description   string
	Tags          []string
	RequestType   any
	ResponseType  any
	ResponseCode  int
	RequiresAuth  bool
}

// Document accumulates operations and renders them as OpenAPI 3.1.
type Document struct {
	Info    Info
	Servers []Server

	mu         sync.Mutex
	operations []Operation
	schemes    map[string]SecurityScheme
}

// New returns a new Document with sensible defaults.
func New(info Info) *Document {
	return &Document{
		Info: info,
		schemes: map[string]SecurityScheme{
			"bearerAuth": {Type: "http", Scheme: "bearer", BearerFormat: "JWT"},
		},
	}
}

// AddOperation registers op with the document. Method names should be
// upper-case (e.g. "POST"); paths should use OpenAPI parameter syntax
// (e.g. "/users/{id}").
func (d *Document) AddOperation(op Operation) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.operations = append(d.operations, op)
}

// AddSecurityScheme registers a custom security scheme.
func (d *Document) AddSecurityScheme(name string, s SecurityScheme) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.schemes == nil {
		d.schemes = make(map[string]SecurityScheme)
	}
	d.schemes[name] = s
}

// MarshalJSON renders the document as OpenAPI 3.1 JSON. The output is
// stable - paths and methods are sorted alphabetically.
func (d *Document) MarshalJSON() ([]byte, error) {
	d.mu.Lock()
	ops := append([]Operation(nil), d.operations...)
	schemes := make(map[string]SecurityScheme, len(d.schemes))
	for k, v := range d.schemes {
		schemes[k] = v
	}
	d.mu.Unlock()

	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return ops[i].Method < ops[j].Method
	})

	paths := make(map[string]map[string]any)
	for _, op := range ops {
		method := strings.ToLower(op.Method)
		path := op.Path
		entry, ok := paths[path]
		if !ok {
			entry = make(map[string]any)
			paths[path] = entry
		}

		body := map[string]any{
			"summary":     op.Summary,
			"description": op.Description,
			"tags":        op.Tags,
			"responses": map[string]any{
				responseCode(op.ResponseCode): map[string]any{
					"description": "Successful response",
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": schemaFor(op.ResponseType),
						},
					},
				},
			},
		}
		if op.RequestType != nil {
			body["requestBody"] = map[string]any{
				"required": true,
				"content": map[string]any{
					"application/json": map[string]any{
						"schema": schemaFor(op.RequestType),
					},
				},
			}
		}
		if op.RequiresAuth {
			body["security"] = []map[string][]string{{"bearerAuth": {}}}
		}
		entry[method] = body
	}

	doc := map[string]any{
		"openapi": "3.1.0",
		"info":    d.Info,
		"paths":   paths,
		"components": map[string]any{
			"securitySchemes": schemes,
		},
	}
	if len(d.Servers) > 0 {
		doc["servers"] = d.Servers
	}
	return json.Marshal(doc)
}

// JSONHandler returns a Fiber handler that serves the document at the
// path it is mounted on. Application code typically wires this onto
// `/openapi.json`.
func (d *Document) JSONHandler() fiber.Handler {
	return func(c *fiber.Ctx) error {
		raw, err := d.MarshalJSON()
		if err != nil {
			return err
		}
		c.Set("Content-Type", "application/json")
		return c.Send(raw)
	}
}

// SwaggerUIHandler returns a Fiber handler that serves a minimal HTML
// page rendering Swagger UI from the public CDN against the spec at
// specPath (e.g. "/openapi.json").
func (d *Document) SwaggerUIHandler(specPath string) fiber.Handler {
	html := `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8" />
<title>` + d.Info.Title + ` API</title>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css" />
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>
window.onload = () => SwaggerUIBundle({ url: "` + specPath + `", dom_id: "#swagger-ui" });
</script>
</body>
</html>`
	return func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/html; charset=utf-8")
		return c.SendString(html)
	}
}

func responseCode(code int) string {
	if code <= 0 {
		return "200"
	}
	if code < 100 || code > 599 {
		return "200"
	}
	return strings.TrimSpace(itoa(code))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// schemaFor reflects t into a JSON Schema fragment. It supports the
// types every typical DTO uses: structs, slices, maps, primitives and
// time.Time / uuid.UUID via duck typing.
func schemaFor(t any) map[string]any {
	if t == nil {
		return map[string]any{"type": "object"}
	}
	rt := reflect.TypeOf(t)
	return reflectType(rt)
}

func reflectType(rt reflect.Type) map[string]any {
	for rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	// Well-known types are checked before the kind switch so opaque
	// stand-ins like uuid.UUID ([16]byte) don't get reported as
	// arrays of bytes.
	switch rt.String() {
	case "time.Time":
		return map[string]any{"type": "string", "format": "date-time"}
	case "uuid.UUID":
		return map[string]any{"type": "string", "format": "uuid"}
	case "json.RawMessage":
		return map[string]any{"type": "object"}
	}
	switch rt.Kind() {
	case reflect.String:
		// Detect well-known stringly types by name.
		switch rt.String() {
		case "time.Time":
			return map[string]any{"type": "string", "format": "date-time"}
		case "uuid.UUID":
			return map[string]any{"type": "string", "format": "uuid"}
		}
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": reflectType(rt.Elem())}
	case reflect.Map:
		return map[string]any{
			"type":                 "object",
			"additionalProperties": reflectType(rt.Elem()),
		}
	case reflect.Struct:
		// time.Time / uuid.UUID flagged as their wire form, not the raw struct.
		switch rt.String() {
		case "time.Time":
			return map[string]any{"type": "string", "format": "date-time"}
		case "uuid.UUID":
			return map[string]any{"type": "string", "format": "uuid"}
		}
		props := make(map[string]any)
		var required []string
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if !f.IsExported() {
				continue
			}
			name, opts := jsonName(f)
			if name == "-" {
				continue
			}
			schema := reflectType(f.Type)
			props[name] = schema
			if !opts.omitempty && !opts.optional {
				if hasValidate(f, "required") {
					required = append(required, name)
				}
			}
		}
		out := map[string]any{
			"type":       "object",
			"properties": props,
		}
		if len(required) > 0 {
			out["required"] = required
		}
		return out
	default:
		return map[string]any{"type": "object"}
	}
}

type jsonOpts struct {
	omitempty bool
	optional  bool
}

func jsonName(f reflect.StructField) (string, jsonOpts) {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name, jsonOpts{}
	}
	parts := strings.Split(tag, ",")
	name := parts[0]
	if name == "" {
		name = f.Name
	}
	var opts jsonOpts
	for _, p := range parts[1:] {
		if p == "omitempty" {
			opts.omitempty = true
		}
	}
	return name, opts
}

func hasValidate(f reflect.StructField, rule string) bool {
	tag := f.Tag.Get("validate")
	if tag == "" {
		return false
	}
	for _, part := range strings.Split(tag, ",") {
		if part == rule || strings.HasPrefix(part, rule+"=") {
			return true
		}
	}
	return false
}
