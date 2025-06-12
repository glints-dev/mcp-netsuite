package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/glints-dev/mcp-netsuite/pkg/netsuite"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Config holds all configuration for the MCP server
type Config struct {
	NetSuiteOptions netsuite.ClientOptions
	RecordTypes     []string
}

// loadConfig reads configuration from environment variables and files
func loadConfig() (Config, error) {
	// Read private key from file
	privateKeyPath := os.Getenv("NETSUITE_PRIVATE_KEY_PATH")
	var privateKeyBytes []byte
	var err error

	if privateKeyPath != "" {
		privateKeyBytes, err = os.ReadFile(privateKeyPath)
		if err != nil {
			return Config{}, err
		}
	}

	// Read environment variables into ClientOptions
	options := netsuite.ClientOptions{
		AccountID:          os.Getenv("NETSUITE_ACCOUNT_ID"),
		ClientID:           os.Getenv("NETSUITE_CLIENT_ID"),
		ClientSecret:       os.Getenv("NETSUITE_CLIENT_SECRET"),
		CertificateID:      os.Getenv("NETSUITE_CERTIFICATE_ID"),
		PrivateKeyBytes:    privateKeyBytes,
		PrivateKeyPassword: os.Getenv("NETSUITE_PRIVATE_KEY_PASSWORD"),
	}

	// Read record types from environment variable
	var recordTypes []string
	recordTypesEnv := os.Getenv("NETSUITE_RECORD_TYPES")
	if recordTypesEnv != "" {
		// Split by comma and trim whitespace
		parts := strings.Split(recordTypesEnv, ",")
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				recordTypes = append(recordTypes, trimmed)
			}
		}
	}

	config := Config{
		NetSuiteOptions: options,
		RecordTypes:     recordTypes,
	}

	return config, nil
}

func main() {
	// Load configuration
	config, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create NetSuite client
	client, err := netsuite.NewClient(config.NetSuiteOptions)
	if err != nil {
		log.Fatalf("Failed to create NetSuite client: %v", err)
	}

	// Create MCP server
	s := server.NewMCPServer(
		"NetSuite MCP Server",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	// Add NetSuite metadata tool
	metadataTool := mcp.NewTool("netsuite_get_metadata",
		mcp.WithDescription("Get metadata (schema) for a NetSuite record type"),
		mcp.WithString("record_type",
			mcp.Required(),
			mcp.Description("The NetSuite record type to get metadata for (e.g., 'customer', 'item', 'transaction')"),
		),
		mcp.WithArray("included_fields",
			mcp.Description("Optional list of specific fields to include in the metadata. If not provided, all available fields will be returned."),
		),
	)

	// Add tool handler
	s.AddTool(metadataTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleGetMetadata(client, request)
	})

	// Add NetSuite SuiteQL tool
	suiteQLTool := mcp.NewTool("netsuite_run_suiteql",
		mcp.WithDescription("Execute a SuiteQL query against NetSuite and return the results"),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("The SuiteQL query to execute (e.g., 'SELECT id, companyname FROM customer LIMIT 10')"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results to return (default: 100, max: 1000)"),
		),
		mcp.WithNumber("offset",
			mcp.Description("Number of records to skip for pagination (default: 0)"),
		),
	)

	// Add SuiteQL tool handler
	s.AddTool(suiteQLTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleRunSuiteQL(client, request)
	})

	// Start the stdio server
	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// handleGetMetadata handles the netsuite_get_metadata tool request
func handleGetMetadata(client *netsuite.Client, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Get record type from arguments
	recordType, err := request.RequireString("record_type")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid record_type parameter: %v", err)), nil
	}

	// Get optional included fields
	var includedFields []string
	args := request.GetArguments()
	if fieldsArg, exists := args["included_fields"]; exists {
		if fieldsArray, ok := fieldsArg.([]interface{}); ok {
			for _, field := range fieldsArray {
				if fieldStr, ok := field.(string); ok {
					includedFields = append(includedFields, fieldStr)
				}
			}
		}
	}

	// Get metadata from NetSuite
	metadata, err := client.Metadata(recordType, includedFields)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get metadata for record type '%s': %v", recordType, err)), nil
	}

	// Create a structured response
	response := map[string]interface{}{
		"record_type":      recordType,
		"included_fields":  includedFields,
		"metadata_schema":  metadata,
		"metadata_summary": generateMetadataSummary(metadata),
	}

	responseJSON, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to marshal response to JSON: %v", err)), nil
	}

	return mcp.NewToolResultText(string(responseJSON)), nil
}

// generateMetadataSummary creates a human-readable summary of the metadata
func generateMetadataSummary(metadata interface{}) map[string]interface{} {
	summary := map[string]interface{}{
		"description": "NetSuite record metadata schema",
	}

	// Try to extract useful information from the metadata structure
	if metadataMap, ok := metadata.(map[string]interface{}); ok {
		if properties, exists := metadataMap["properties"]; exists {
			if propsMap, ok := properties.(map[string]interface{}); ok {
				fieldCount := len(propsMap)
				summary["total_fields"] = fieldCount

				// List first few field names as examples
				fieldNames := make([]string, 0, 10)
				count := 0
				for fieldName := range propsMap {
					if count >= 10 {
						break
					}
					fieldNames = append(fieldNames, fieldName)
					count++
				}
				summary["sample_fields"] = fieldNames
				if fieldCount > 10 {
					summary["note"] = fmt.Sprintf("Showing first 10 fields out of %d total fields", fieldCount)
				}
			}
		}

		if schemaType, exists := metadataMap["type"]; exists {
			summary["schema_type"] = schemaType
		}
	}

	return summary
}

// handleRunSuiteQL handles the netsuite_run_suiteql tool request
func handleRunSuiteQL(client *netsuite.Client, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Get query from arguments
	query, err := request.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid query parameter: %v", err)), nil
	}

	// Get optional limit and offset from arguments
	args := request.GetArguments()
	limit := 100 // default limit
	offset := 0  // default offset

	if limitArg, exists := args["limit"]; exists {
		if limitFloat, ok := limitArg.(float64); ok {
			limit = int(limitFloat)
			// Validate limit (max 1000 as mentioned in description)
			if limit > 1000 {
				limit = 1000
			}
		}
	}

	if offsetArg, exists := args["offset"]; exists {
		if offsetFloat, ok := offsetArg.(float64); ok {
			offset = int(offsetFloat)
		}
	}

	// Execute SuiteQL query
	results, err := client.SuiteQL(query, limit, offset)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to execute SuiteQL query: %v", err)), nil
	}

	// Create a structured response
	response := map[string]interface{}{
		"query":        query,
		"limit":        limit,
		"offset":       offset,
		"count":        results.Count,
		"totalResults": results.TotalResults,
		"hasMore":      results.HasMore,
		"items":        results.Items,
		"summary":      generateSuiteQLSummary(results),
	}

	responseJSON, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to marshal response to JSON: %v", err)), nil
	}

	return mcp.NewToolResultText(string(responseJSON)), nil
}

// generateSuiteQLSummary creates a human-readable summary of the SuiteQL results
func generateSuiteQLSummary(results *netsuite.SuiteQLResponse) map[string]interface{} {
	summary := map[string]interface{}{
		"description": "NetSuite SuiteQL query results",
		"count":       results.Count,
		"offset":      results.Offset,
		"total":       results.TotalResults,
		"hasMore":     results.HasMore,
	}

	// Try to extract useful information from the first result item
	if len(results.Items) > 0 {
		// Parse the first item to see what fields are available
		var firstItemMap map[string]interface{}
		if err := json.Unmarshal(results.Items[0], &firstItemMap); err == nil {
			fieldCount := len(firstItemMap)
			summary["total_fields"] = fieldCount

			// List first few field names as examples
			fieldNames := make([]string, 0, 10)
			count := 0
			for fieldName := range firstItemMap {
				if count >= 10 {
					break
				}
				fieldNames = append(fieldNames, fieldName)
				count++
			}
			summary["sample_fields"] = fieldNames
			if fieldCount > 10 {
				summary["note"] = fmt.Sprintf("Showing first 10 fields out of %d total fields", fieldCount)
			}
		}
	}

	return summary
}
