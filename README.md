# groq-go

A CLI AI assistant for software engineering tasks, powered by Groq API.

## Installation

```bash
go install groq-go@latest
```

Or build from source:

```bash
make build
```

## Configuration

Set your Groq API key:

```bash
export GROQ_API_KEY="your-api-key"
```

Optionally set a different model:

```bash
export GROQ_MODEL="llama-3.1-8b-instant"
```

## Usage

### CLI Mode

```bash
./bin/groq-go
```

### Web Mode

```bash
./bin/groq-go -web
```

Then open http://localhost:8080 in your browser.

Options:
- `-web` - Start web server instead of CLI
- `-addr :3000` - Custom port (default: :8080)

### Commands

- `/help` - Show available commands
- `/clear` - Clear conversation history
- `/model [name]` - Show or change the current model
- `/exit` - Exit the REPL

### Available Tools

- **Read** - Read file contents with line numbers
- **Write** - Create or overwrite files
- **Edit** - Replace exact strings in files
- **Glob** - Find files by pattern (e.g., `**/*.go`)
- **Grep** - Search file contents with regex
- **Bash** - Execute shell commands
- **WebFetch** - Fetch content from URLs (fast, no JS)
- **Browser** - Control browser with Playwright (screenshots, JS-rendered content, PDFs)

## Examples

```
> Read the file main.go
> Find all Go files in this project
> What does the Config struct do?
> Run the tests
> What's the top story on Hacker News?
> Take a screenshot of https://example.com
```

## MCP Support

groq-go supports MCP (Model Context Protocol) servers. Create a `mcp.json` file:

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/dir"]
    }
  }
}
```

## Supported Models

- `llama-3.3-70b-versatile` (default)
- `llama-3.1-8b-instant`
- `llama-3.2-90b-vision-preview`
- `mixtral-8x7b-32768`

## License

MIT
