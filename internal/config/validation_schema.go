package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/github/gh-aw-mcpg/internal/httputil"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/version"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// embeddedSchemaBytes holds the bundled MCP Gateway configuration JSON Schema.
// Embedding the schema in the binary eliminates the runtime network request that was
// previously needed to fetch it from GitHub, improving startup reliability and removing
// a potential point of failure in network-restricted environments.
//
// To update the schema to a newer version:
//  1. Download the new schema from https://github.com/github/gh-aw/releases
//  2. Replace internal/config/schema/mcp-gateway-config.schema.json
//  3. Update the version comment above
//  4. Run tests to ensure compatibility: make test
//
//go:embed schema/mcp-gateway-config.schema.json
var embeddedSchemaBytes []byte

const (
	// maxSchemaFetchRetries is the number of fetch attempts before giving up.
	maxSchemaFetchRetries = 3

	// maxSchemaFetchBytes bounds fetched schema size to avoid unbounded memory usage.
	maxSchemaFetchBytes = 10 * 1024 * 1024 // 10 MiB

	// embeddedSchemaID is the $id URL used when registering the embedded schema with
	// the JSON Schema compiler. It matches the $id field in the bundled schema file.
	embeddedSchemaID = "https://docs.github.com/gh-aw/schemas/mcp-gateway-config.schema.json"

	// maxRemoteRefDocuments bounds how many remote documents a single schema
	// compilation may load through $ref resolution.
	maxRemoteRefDocuments = 10

	// maxRemoteRefTotalBytes bounds the aggregate size of all remote documents loaded
	// during a single schema compilation.
	maxRemoteRefTotalBytes = 10 * 1024 * 1024 // 10 MiB
)

// requireHTTPSSchemaURLs gates the HTTPS-only policy applied to remotely resolved
// schema references, matching the policy enforced for top-level custom schema URLs.
// It is a variable so tests can serve fixtures from httptest's HTTP server.
var requireHTTPSSchemaURLs = true

// schemaFetchRetryDelay is the base delay between retry attempts using exponential
// backoff (1×, 2×, 4×, …). It is a variable so tests can override it to zero for
// fast execution.
var schemaFetchRetryDelay = time.Second

// schemaHTTPClientTimeout is the per-attempt HTTP request timeout. It is a variable
// so tests can shorten it to avoid long waits when testing timeout behaviour.
var schemaHTTPClientTimeout = 10 * time.Second

// schemaErrPrinter is used to format localized error messages from ValidationError kinds.
// language.English is required to construct the printer; without a registered catalog,
// it does not enable translations.
var schemaErrPrinter = message.NewPrinter(language.English)

var (
	// logSchema is the debug logger for schema validation
	logSchema = logger.ForFile()

	// Schema caching to avoid recompiling the JSON schema on every validation.
	// This improves performance by compiling the schema once and reusing it.
	//
	// schemaOnce intentionally provides no reset path: if compilation fails (e.g. due
	// to a corrupted embed), the error is cached and returned on every subsequent call
	// until the binary is replaced. A reset path would allow transient success after a
	// permanent structural failure and is therefore deliberately omitted.
	schemaOnce   sync.Once
	cachedSchema *jsonschema.Schema
	schemaErr    error
)

// fetchSchema fetches the JSON schema bytes from the remote URL with retry logic.
// This function is used for fetching custom server schemas from remote URLs.
func fetchSchema(url string) ([]byte, error) {
	startTime := time.Now()
	logSchema.Printf("Fetching schema from URL: %s", url)

	client := &http.Client{
		Timeout: schemaHTTPClientTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			// Never allow a redirect to downgrade an HTTPS request to plain HTTP.
			if len(via) > 0 && via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return fmt.Errorf("refusing insecure redirect from HTTPS to %q", req.URL.Scheme)
			}
			return nil
		},
	}

	var resp *http.Response
	var lastErr error

	for attempt := 1; attempt <= maxSchemaFetchRetries; attempt++ {
		if attempt > 1 {
			delay := schemaFetchRetryDelay << uint(attempt-2) // 1×, 2×, 4× base delay
			logSchema.Printf("Retrying schema fetch (attempt %d/%d) after %v: %v", attempt, maxSchemaFetchRetries, delay, lastErr)
			time.Sleep(delay)
		}

		fetchStart := time.Now()
		var err error
		resp, err = client.Get(url)
		if err != nil {
			logSchema.Printf("Schema fetch attempt %d failed after %v: %v", attempt, time.Since(fetchStart), err)
			lastErr = fmt.Errorf("failed to fetch schema from %s: %w", url, err)
			resp = nil
			continue
		}
		logSchema.Printf("HTTP request attempt %d completed in %v with status %d", attempt, time.Since(fetchStart), resp.StatusCode)

		if resp.StatusCode == http.StatusOK {
			lastErr = nil
			break
		}

		if httputil.IsTransientHTTPError(resp.StatusCode) {
			lastErr = fmt.Errorf("failed to fetch schema: HTTP %d", resp.StatusCode)
			logSchema.Printf("Schema fetch attempt %d returned transient error: HTTP %d, will retry", attempt, resp.StatusCode)
			resp.Body.Close()
			resp = nil
			continue
		}

		// Permanent HTTP error (404, 403, 401, etc.) — do not retry.
		lastErr = fmt.Errorf("failed to fetch schema: HTTP %d", resp.StatusCode)
		logSchema.Printf("Schema fetch returned permanent error: HTTP %d", resp.StatusCode)
		resp.Body.Close()
		resp = nil
		break
	}

	if resp == nil {
		return nil, lastErr
	}
	defer resp.Body.Close()
	logSchema.Printf("HTTP request completed in %v", time.Since(startTime))

	readStart := time.Now()
	limitedBody := io.LimitReader(resp.Body, maxSchemaFetchBytes+1)
	schemaBytes, err := io.ReadAll(limitedBody)
	if err != nil {
		return nil, fmt.Errorf("failed to read schema response: %w", err)
	}
	if len(schemaBytes) > maxSchemaFetchBytes {
		return nil, fmt.Errorf("schema response too large: exceeds %d bytes", maxSchemaFetchBytes)
	}
	logSchema.Printf("Schema read completed in %v (size: %d bytes)", time.Since(readStart), len(schemaBytes))

	return schemaBytes, nil
}

// schemaURLLoader adapts fetchSchema into jsonschema.URLLoader. Each instance carries
// a per-compilation budget so a chain of remote $ref documents cannot consume unbounded
// time or memory.
type schemaURLLoader struct {
	mu        sync.Mutex
	documents int
	bytesUsed int
}

// reserveDocument accounts for one additional remote document, returning an error when
// the per-compilation document budget is exhausted.
func (l *schemaURLLoader) reserveDocument() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.documents+1 > maxRemoteRefDocuments {
		return fmt.Errorf("too many remote schema references: limit is %d documents per schema compilation", maxRemoteRefDocuments)
	}
	l.documents++
	return nil
}

func (l *schemaURLLoader) Load(url string) (any, error) {
	if requireHTTPSSchemaURLs && !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("refusing to load schema reference '%s': remote schema references must use HTTPS", url)
	}
	// Reserve a document slot before fetching so a long $ref chain stops early.
	if err := l.reserveDocument(); err != nil {
		return nil, err
	}
	schemaBytes, err := fetchSchema(url)
	if err != nil {
		return nil, err
	}
	if err := l.reserveBytes(len(schemaBytes)); err != nil {
		return nil, err
	}
	return jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
}

// reserveBytes charges fetched bytes against the aggregate budget.
func (l *schemaURLLoader) reserveBytes(size int) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.bytesUsed+size > maxRemoteRefTotalBytes {
		return fmt.Errorf("remote schema references too large: limit is %d bytes per schema compilation", maxRemoteRefTotalBytes)
	}
	l.bytesUsed += size
	return nil
}

// newCompiler creates a JSON Schema compiler using the library defaults (Draft 2020-12).
func newCompiler() *jsonschema.Compiler {
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	return compiler
}

// newCompilerWithSchemaLoader creates a compiler that can resolve remote $ref URLs
// using fetchSchema (retrying and size-limiting remote loads consistently). Each
// compiler gets its own loader instance and therefore its own remote-reference budget.
func newCompilerWithSchemaLoader() *jsonschema.Compiler {
	compiler := newCompiler()
	compiler.UseLoader(&schemaURLLoader{})
	return compiler
}

type schemaCompileError struct {
	resourceID string
	compile    bool
	err        error
}

func (e *schemaCompileError) Error() string {
	return e.err.Error()
}

func (e *schemaCompileError) Unwrap() error {
	return e.err
}

func compileSchemaDocument(compiler *jsonschema.Compiler, schemaID string, schemaDoc any, resourceIDs ...string) (*jsonschema.Schema, *schemaCompileError) {
	for _, resourceID := range resourceIDs {
		if err := compiler.AddResource(resourceID, schemaDoc); err != nil {
			return nil, &schemaCompileError{resourceID: resourceID, err: err}
		}
	}

	schema, err := compiler.Compile(schemaID)
	if err != nil {
		return nil, &schemaCompileError{compile: true, err: err}
	}
	return schema, nil
}

// getOrCompileSchema retrieves the cached compiled schema or compiles it on first use.
// This function uses sync.Once to ensure thread-safe, one-time schema compilation,
// which significantly improves performance by avoiding repeated schema compilation
// on every validation call.
//
// The schema is bundled in the binary via go:embed and read on first call.
// If schema compilation fails, the error is also cached to avoid repeated attempts.
//
// Returns:
//   - Compiled JSON schema on success
//   - Error if schema compilation fails
func getOrCompileSchema() (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		logSchema.Print("Compiling JSON schema for the first time")

		// Parse the embedded schema bytes into a JSON document
		schemaDoc, parseErr := jsonschema.UnmarshalJSON(bytes.NewReader(embeddedSchemaBytes))
		if parseErr != nil {
			schemaErr = fmt.Errorf("failed to process embedded schema: %w", parseErr)
			logSchema.Printf("Schema compilation failed: %v", schemaErr)
			return
		}

		// Extract $id from the parsed document to use as the schema URL
		schemaID := embeddedSchemaID
		if schemaObj, ok := schemaDoc.(map[string]any); ok {
			if id, ok := schemaObj["$id"].(string); ok && id != "" {
				schemaID = id
			}
		}

		// Compile the schema
		compiler := newCompiler()

		// Add the schema using its $id URL so internal $ref references resolve correctly.
		var compileErr *schemaCompileError
		cachedSchema, compileErr = compileSchemaDocument(compiler, schemaID, schemaDoc, schemaID)
		if compileErr != nil {
			if compileErr.compile {
				schemaErr = fmt.Errorf("failed to compile schema: %w", compileErr)
				logSchema.Printf("Schema compilation failed: %v", schemaErr)
			} else {
				schemaErr = fmt.Errorf("failed to add schema resource: %w", compileErr)
			}
			return
		}

		logSchema.Print("Schema compiled and cached successfully")
	})

	return cachedSchema, schemaErr
}

// validateJSONSchema validates the raw JSON configuration against the JSON schema
func validateJSONSchema(data []byte) error {
	startTime := time.Now()
	logSchema.Printf("Starting JSON schema validation: data_size=%d bytes", len(data))

	// Get the cached compiled schema (or compile it on first use)
	schemaStart := time.Now()
	schema, err := getOrCompileSchema()
	if err != nil {
		return err
	}
	logSchema.Printf("Schema compilation/retrieval took: %v", time.Since(schemaStart))

	// Parse the configuration using jsonschema.UnmarshalJSON for proper number handling
	parseStart := time.Now()
	configObj, parseErr := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if parseErr != nil {
		return fmt.Errorf("failed to parse configuration JSON: %w", parseErr)
	}
	logSchema.Printf("JSON parsing took: %v", time.Since(parseStart))

	// Validate the configuration
	validationStart := time.Now()
	if err := schema.Validate(configObj); err != nil {
		logSchema.Printf("Schema validation failed after %v: %v", time.Since(validationStart), err)
		return formatSchemaError(err)
	}
	logSchema.Printf("Schema validation took: %v", time.Since(validationStart))

	logSchema.Printf("Total validation completed successfully in %v", time.Since(startTime))
	return nil
}

// formatSchemaError formats JSON schema validation errors to be user-friendly
func formatSchemaError(err error) error {
	if err == nil {
		return nil
	}

	// The jsonschema library returns a ValidationError type with detailed info
	var ve *jsonschema.ValidationError
	if errors.As(err, &ve) {
		var sb strings.Builder
		fmt.Fprintf(&sb, "Configuration validation error (MCP Gateway version: %s):\n\n", version.Get())

		// Recursively format all errors
		formatValidationErrorRecursive(ve, &sb, 0)

		AppendConfigDocsFooter(&sb)

		return errors.New(sb.String())
	}

	return fmt.Errorf("configuration validation error (version: %s): %s", version.Get(), err.Error())
}

// formatValidationErrorRecursive recursively formats validation errors with proper indentation
func formatValidationErrorRecursive(ve *jsonschema.ValidationError, sb *strings.Builder, depth int) {
	indent := strings.Repeat("  ", depth)

	// Format location and message
	location := "<root>"
	if len(ve.InstanceLocation) > 0 {
		location = "/" + strings.Join(ve.InstanceLocation, "/")
	}
	fmt.Fprintf(sb, "%sLocation: %s\n", indent, location)
	fmt.Fprintf(sb, "%sError: %s\n", indent, ve.ErrorKind.LocalizedString(schemaErrPrinter))

	// Add detailed context based on the error kind
	context := formatErrorContext(ve, indent)
	if context != "" {
		sb.WriteString(context)
	}

	// Recursively process nested causes
	if len(ve.Causes) > 0 {
		for _, cause := range ve.Causes {
			formatValidationErrorRecursive(cause, sb, depth+1)
		}
	}

	// Add spacing between sibling errors at the same level
	if depth == 0 {
		sb.WriteString("\n")
	}
}

// detailForKeyword returns the addDetail arguments (key and detail lines) for a
// recognised JSON Schema keyword. Returns "", nil for unknown keywords.
// This is used by formatErrorContext to centralise the guidance text so it is
// only written in one place and always stays in sync between the keyword-location
// switch and the message-fallback checks.
func detailForKeyword(keyword string) (string, []string) {
	switch keyword {
	case "additionalProperties":
		return "additionalProperties", []string{
			"Details: Configuration contains field(s) that are not defined in the schema",
			"  → Check for typos in field names or remove unsupported fields",
		}
	case "type":
		return "type", []string{
			"Details: Type mismatch - the value type doesn't match what's expected",
			"  → Verify the value is the correct type (string, number, boolean, object, array)",
		}
	case "enum":
		return "enum", []string{
			"Details: Invalid value - the field has a restricted set of allowed values",
			"  → Check the documentation for the list of valid values",
		}
	case "required":
		return "required", []string{
			"Details: Required field(s) are missing",
			"  → Add the required field(s) to your configuration",
		}
	case "pattern":
		return "pattern", []string{
			"Details: Value format is incorrect",
			"  → The value must match a specific format or pattern",
		}
	case "range":
		return "range", []string{
			"Details: Value is outside the allowed range",
			"  → Adjust the value to be within the valid range",
		}
	case "oneOf":
		return "oneOf", []string{
			"Details: Configuration doesn't match any of the expected formats",
			"  → Review the structure and ensure it matches one of the valid configuration types",
		}
	case "allOf":
		return "allOf", []string{
			"Details: Configuration must satisfy all required constraints",
			"  → Review each related field and ensure every required rule is satisfied together",
		}
	case "if":
		return "conditional", []string{
			"Details: Configuration failed conditional schema evaluation",
			"  → Check the fields used by the conditional rule and verify the expected branch applies",
		}
	case "then":
		return "conditional", []string{
			"Details: Configuration matched a conditional rule but failed its required follow-up constraints",
			"  → Update the related fields so the conditional requirements are satisfied",
		}
	case "else":
		return "conditional", []string{
			"Details: Configuration did not match the conditional rule and also failed the alternate constraints",
			"  → Update the related fields so the alternate conditional requirements are satisfied",
		}
	case "not":
		return "not", []string{
			"Details: Value matches a constraint that it must not match",
			"  → Remove or change the value so it does not satisfy the prohibited condition",
		}
	case "contains":
		return "contains", []string{
			"Details: Array does not satisfy the required contains constraint",
			"  → Ensure the array contains at least one item matching the required schema",
		}
	case "minContains":
		return "minContains", []string{
			"Details: Array does not meet the minimum number of matching items",
			"  → Add items matching the required schema until the minimum is satisfied",
		}
	case "maxContains":
		return "maxContains", []string{
			"Details: Array exceeds the maximum number of matching items",
			"  → Remove items matching the required schema until the maximum is satisfied",
		}
	case "uniqueItems":
		return "uniqueItems", []string{
			"Details: Array items must be unique — duplicate values are not allowed",
			"  → Remove duplicate entries from the array",
		}
	case "format":
		return "format", []string{
			"Details: Value does not match the required format",
			"  → Update the value so it satisfies the configured format",
		}
	case "propertyNames":
		return "propertyNames", []string{
			"Details: Object contains a property name that violates the propertyNames schema",
			"  → Rename the field so it satisfies the configured property name constraints",
		}
	}
	return "", nil
}

// formatErrorContext provides additional context about what caused the validation error.
// It uses type-switching on ErrorKind for robust, version-stable keyword dispatch.
func formatErrorContext(ve *jsonschema.ValidationError, prefix string) string {
	var sb strings.Builder
	added := map[string]bool{}

	addDetail := func(key string, lines ...string) {
		if added[key] {
			return
		}
		for _, line := range lines {
			_, _ = fmt.Fprintf(&sb, "%s%s\n", prefix, line)
		}
		added[key] = true
	}

	addFromKeyword := func(keyword string) {
		if key, lines := detailForKeyword(keyword); key != "" {
			addDetail(key, lines...)
		}
	}

	// Dispatch on ErrorKind type for robust keyword identification.
	// Each case extracts concrete field data from the kind struct where available
	// to produce actionable error messages. The default branch provides a generic
	// fallback so every validation error gets at least some guidance.
	switch k := ve.ErrorKind.(type) {
	case *kind.AdditionalProperties:
		if len(k.Properties) > 0 {
			quotedProperties := make([]string, len(k.Properties))
			for i, p := range k.Properties {
				quotedProperties[i] = strconv.Quote(p)
			}
			addDetail("additionalProperties",
				fmt.Sprintf("Details: Unexpected field(s): %s", strings.Join(quotedProperties, ", ")),
				"  → Check for typos in field names or remove unsupported fields",
			)
		} else {
			addFromKeyword("additionalProperties")
		}
	case *kind.AdditionalItems:
		addFromKeyword("additionalProperties")
	case *kind.Type:
		if len(k.Want) > 0 {
			addDetail("type",
				fmt.Sprintf("Details: Type mismatch - expected %s, got %s", strings.Join(k.Want, " or "), k.Got),
				"  → Verify the value is the correct type (string, number, boolean, object, array)",
			)
		} else {
			addFromKeyword("type")
		}
	case *kind.Enum:
		if len(k.Want) > 0 {
			values := make([]string, len(k.Want))
			for i, v := range k.Want {
				values[i] = fmt.Sprintf("%v", v)
			}
			addDetail("enum",
				fmt.Sprintf("Details: Invalid value - allowed values: %s", strings.Join(values, ", ")),
				"  → Use one of the allowed values listed above",
			)
		} else {
			addFromKeyword("enum")
		}
	case *kind.Const:
		addFromKeyword("enum")
	case *kind.Required:
		if len(k.Missing) > 0 {
			addDetail("required",
				fmt.Sprintf("Details: Missing required field(s): %s", strings.Join(k.Missing, ", ")),
				"  → Add the required field(s) to your configuration",
			)
		} else {
			addFromKeyword("required")
		}
	case *kind.DependentRequired:
		if len(k.Missing) > 0 {
			addDetail("required",
				fmt.Sprintf("Details: Missing required field(s) for %q: %s", k.Prop, strings.Join(k.Missing, ", ")),
				"  → Add the required field(s) to your configuration",
			)
		} else {
			addFromKeyword("required")
		}
	case *kind.Pattern:
		addFromKeyword("pattern")
	case *kind.Format:
		addFromKeyword("format")
	case *kind.PropertyNames:
		if k.Property != "" {
			addDetail("propertyNames",
				fmt.Sprintf("Details: Invalid property name %q", k.Property),
				"  → Rename the field so it satisfies the configured property name constraints",
			)
		} else {
			addFromKeyword("propertyNames")
		}
	case *kind.AllOf:
		addFromKeyword("allOf")
	case *kind.OneOf, *kind.AnyOf:
		addFromKeyword("oneOf")
	case *kind.Not:
		addFromKeyword("not")
	case *kind.Contains:
		addFromKeyword("contains")
	case *kind.MinContains:
		addDetail("minContains",
			fmt.Sprintf("Details: Array must contain at least %d matching item(s), found %d", k.Want, len(k.Got)),
			"  → Add items matching the required schema until the minimum is satisfied",
		)
	case *kind.MaxContains:
		addDetail("maxContains",
			fmt.Sprintf("Details: Array must contain at most %d matching item(s), found %d", k.Want, len(k.Got)),
			"  → Remove items matching the required schema until the maximum is satisfied",
		)
	case *kind.UniqueItems:
		addFromKeyword("uniqueItems")
	case *kind.Minimum, *kind.Maximum, *kind.ExclusiveMinimum, *kind.ExclusiveMaximum,
		*kind.MinLength, *kind.MaxLength, *kind.MinItems, *kind.MaxItems,
		*kind.MinProperties, *kind.MaxProperties:
		addFromKeyword("range")
	default:
		// Fall back to the schema keyword path when the concrete ErrorKind type
		// is not handled directly but still maps to known keyword guidance.
		// Unknown keywords intentionally add no detail here and continue to the
		// generic fallback below.
		if keywordPath := ve.ErrorKind.KeywordPath(); len(keywordPath) > 0 {
			addFromKeyword(keywordPath[0])
		}

		if sb.Len() > 0 {
			return sb.String()
		}

		// Generic fallback for any ErrorKind not specifically handled above.
		// This ensures every validation error gets at least some context rather
		// than silently producing no detail. The LocalizedString already gives
		// the specific reason; this hint directs users to the documentation.
		addDetail("generic",
			"Details: See the error message above for more information",
			"  → Review the MCP Gateway configuration documentation for valid values",
		)
	}

	return sb.String()
}
