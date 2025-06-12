# MCP NetSuite Server

A Model Context Protocol (MCP) server that provides AI assistants with access to NetSuite data through SuiteQL queries and metadata retrieval.

## Tools

This MCP server provides two main tools:

- **`netsuite_get_metadata`** - Retrieve schema information for NetSuite record types
- **`netsuite_run_suiteql`** - Execute SuiteQL queries to fetch NetSuite data

## Setup

### 1. Prerequisites

- NetSuite account with API access
- Certificate-based authentication set up in NetSuite
- Private key file for the certificate

### 2. Environment Variables

Configure the following environment variables:

```bash
NETSUITE_ACCOUNT_ID=your_account_id
NETSUITE_CLIENT_ID=your_client_id
NETSUITE_CLIENT_SECRET=your_client_secret
NETSUITE_CERTIFICATE_ID=your_certificate_id
NETSUITE_PRIVATE_KEY_PATH=/path/to/your/private_key.pem
NETSUITE_PRIVATE_KEY_PASSWORD=your_private_key_password  # Optional
NETSUITE_RECORD_TYPES=customer,item,transaction         # Optional
```

## Usage

### Running the Server

```bash
go run github.com/glints-dev/mcp-netsuite@latest
```

The server will start and communicate via stdio, following the MCP protocol.

## Configuration with MCP Clients

### Claude Desktop

Add to your `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "netsuite": {
      "command": "go",
      "args": ["run", "github.com/glints-dev/mcp-netsuite@latest"],
      "env": {
        "NETSUITE_ACCOUNT_ID": "your_account_id",
        "NETSUITE_CLIENT_ID": "your_client_id",
        "NETSUITE_CLIENT_SECRET": "your_client_secret",
        "NETSUITE_CERTIFICATE_ID": "your_certificate_id",
        "NETSUITE_PRIVATE_KEY_PATH": "/path/to/private_key.pem",
        "NETSUITE_RECORD_TYPES": "customer,item,transaction"
      }
    }
  }
}
```

## NetSuite Authentication Setup

1. Go to Setup → Integration → Manage Integrations
2. Create a new integration record
3. Enable "Token-Based Authentication" and "Certificate Authentication"
4. Generate or upload your certificate
5. Note the Client ID and Client Secret
6. Assign appropriate permissions to the integration

## License

This project is licensed under the MIT License. 