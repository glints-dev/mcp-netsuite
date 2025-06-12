package netsuite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/glints-dev/mcp-netsuite/pkg/jsonschematree"
	"github.com/golang-jwt/jwt/v4"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// Client is the type representing a NetSuite REST client
type Client struct {
	*http.Client
}

type netsuiteAPIHTTPTransport struct {
	accountID string
}

func (transport *netsuiteAPIHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	fullURL, err := url.Parse(fmt.Sprintf(
		"https://%s.suitetalk.api.netsuite.com/services/rest%s",
		transport.accountID,
		req.URL.String(),
	))
	if err != nil {
		return nil, fmt.Errorf("unable to parse URL: %w", err)
	}

	req.URL = fullURL

	return http.DefaultTransport.RoundTrip(req)
}

type ClientOptions struct {
	AccountID          string
	ClientID           string
	ClientSecret       string
	CertificateID      string
	PrivateKeyBytes    []byte
	PrivateKeyPassword string
}

func NewClient(options ClientOptions) (*Client, error) {
	tokenEndpoint := "/auth/oauth2/v1/token"

	key, err := jwt.ParseRSAPrivateKeyFromPEM(
		options.PrivateKeyBytes,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	// NetSuite supports multiple signing methods, but PS256 is recommended
	// over RS256. See https://www.scottbrady91.com/jose/jwts-which-signing-algorithm-should-i-use
	token := jwt.NewWithClaims(jwt.SigningMethodPS256, jwt.MapClaims{
		"iss":   options.ClientID,
		"scope": []string{"rest_webservices"},
		"aud":   tokenEndpoint,
		"iat":   time.Now().UTC().Unix(),
		"exp":   time.Now().Add(time.Hour).UTC().Unix(),
	})
	token.Header["kid"] = options.CertificateID

	signedToken, err := token.SignedString(key)
	if err != nil {
		return nil, fmt.Errorf("failed to get signed token: %w", err)
	}

	clientConfig := clientcredentials.Config{
		ClientID:     options.ClientID,
		ClientSecret: options.ClientSecret,
		TokenURL:     tokenEndpoint,
		EndpointParams: url.Values{
			"client_assertion_type": []string{
				"urn:ietf:params:oauth:client-assertion-type:jwt-bearer",
			},
			"client_assertion": []string{signedToken},
		},
	}

	ctx := context.WithValue(
		context.Background(),
		oauth2.HTTPClient,
		&http.Client{
			Transport: &netsuiteAPIHTTPTransport{
				accountID: options.AccountID,
			},
		},
	)

	return &Client{
		Client: clientConfig.Client(ctx),
	}, nil
}

var metadataCache = map[string]*jsonschematree.Schema{}

// Metadata returns the schema for a given record type.
// https://docs.oracle.com/en/cloud/saas/netsuite/ns-o
func (c *Client) Metadata(recordType string, includedFields []string) (*jsonschematree.Schema, error) {
	if cachedMetadata, ok := metadataCache[recordType]; ok {
		return cachedMetadata, nil
	}

	parsedBody, _ := c.getMetadata(recordType)
	if _, ok := parsedBody.Components.Schemas[recordType]; !ok {
		parsedBody, _ = c.schemaForSchemaless(recordType, includedFields)
	}

	for recordType, schema := range parsedBody.Components.Schemas {
		metadataCache[recordType] = schema
	}

	return metadataCache[recordType], nil
}

type metadataCatalogResponse struct {
	Components struct {
		Schemas map[string]*jsonschematree.Schema `json:"schemas"`
	} `json:"components"`
}

// SuiteQL executes a SuiteQL query and returns the result of the query.
// https://docs.oracle.com/en/cloud/saas/netsuite/ns-online-help/section_157909186990.html
func (c *Client) SuiteQL(q string, limit int, offset int) (*SuiteQLResponse, error) {
	requestBody := make(map[string]interface{})
	requestBody["q"] = q

	requestBodyJSON, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	endpoint, _ := url.Parse("/query/v1/suiteql")
	query := endpoint.Query()

	if limit != 0 {
		query.Add("limit", strconv.Itoa(limit))
	}

	if offset != 0 {
		query.Add("offset", strconv.Itoa(offset))
	}

	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequest(
		http.MethodPost,
		endpoint.String(),
		bytes.NewReader(requestBodyJSON),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	request.Header.Add("Prefer", "transient")
	response, err := c.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to get list of records: %w", err)
	}
	defer response.Body.Close()

	bodyBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to get body bytes: %w", err)
	}

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"invalid HTTP response status %d: %s",
			response.StatusCode,
			string(bodyBytes),
		)
	}

	var parsedBody SuiteQLResponse
	if err := json.Unmarshal(bodyBytes, &parsedBody); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	return &parsedBody, nil
}

type SuiteQLResponse struct {
	Count        int               `json:"count"`
	Offset       int               `json:"offset"`
	TotalResults int               `json:"totalResults"`
	HasMore      bool              `json:"hasMore"`
	Items        []json.RawMessage `json:"items"`
}

func (c *Client) getMetadata(recordType string) (*metadataCatalogResponse, error) {
	catalogEndpoint := fmt.Sprintf(
		"/record/v1/metadata-catalog/%s",
		url.PathEscape(recordType),
	)

	request, err := http.NewRequest(http.MethodGet, catalogEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	request.Header.Add("Accept", "application/swagger+json")

	response, err := c.Do(request)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to GET /record/v1/metadata-catalog: %w",
			err,
		)
	}

	defer response.Body.Close()

	bodyBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var parsedBody metadataCatalogResponse
	if err := json.Unmarshal(bodyBytes, &parsedBody); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	return &parsedBody, nil
}

func (c *Client) getSingleRow(recordType string) (*SuiteQLResponse, error) {
	query := fmt.Sprintf("SELECT * FROM %s", recordType)
	return c.SuiteQL(query, 1, 0)
}

func (c *Client) schemaForSchemaless(recordType string, includedFields []string) (*metadataCatalogResponse, error) {
	var singleRow *SuiteQLResponse
	var columnMap map[string]json.RawMessage
	var columnStruct map[string]*jsonschematree.Schema
	var schemaStruct *jsonschematree.Schema
	var Schemas map[string]*jsonschematree.Schema

	singleRow, _ = c.getSingleRow(recordType)
	json.Unmarshal(singleRow.Items[0], &columnMap)

	columnStruct = make(map[string]*jsonschematree.Schema)
	dummyType := []string{"string", "null"}
	for _, includedField := range includedFields {
		columnStruct[includedField] = jsonschematree.PrepareDummySchema(dummyType)
	}

	for columnName := range columnMap {
		columnStruct[columnName] = jsonschematree.PrepareDummySchema(dummyType)
	}

	dummyType = []string{"object"}
	schemaStruct = jsonschematree.PrepareDummySchema(dummyType)
	schemaStruct.Properties = columnStruct

	Schemas = make(map[string]*jsonschematree.Schema)
	Schemas[recordType] = schemaStruct
	return &metadataCatalogResponse{Components: struct {
		Schemas map[string]*jsonschematree.Schema `json:"schemas"`
	}{Schemas: Schemas}}, nil

}
