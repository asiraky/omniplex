package mcp

import (
	"reflect"
	"testing"
)

func TestParseTurnsPastesIntoDrafts(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Draft
	}{
		{
			name: "bare URL named after its host",
			in:   "  https://mcp.cloudflare.com/mcp \n",
			want: Draft{Name: "cloudflare", URL: "https://mcp.cloudflare.com/mcp"},
		},
		{
			name: "bare URL on a subdomain",
			in:   "https://observability.mcp.cloudflare.com/mcp",
			want: Draft{Name: "observability-cloudflare", URL: "https://observability.mcp.cloudflare.com/mcp"},
		},
		{
			name: "claude remote with headers, options after the positionals",
			in:   `claude mcp add sentry https://mcp.sentry.dev/mcp --transport http -H "X-Api-Key: abc" --header 'X-Org: acme'`,
			want: Draft{Name: "sentry", URL: "https://mcp.sentry.dev/mcp", Headers: map[string]string{"X-Api-Key": "abc", "X-Org": "acme"}},
		},
		{
			name: "claude stdio with env and a command after --",
			in:   "claude mcp add --transport stdio airtable --env AIRTABLE_API_KEY=YOUR_KEY -- npx -y airtable-mcp-server",
			want: Draft{Name: "airtable", Command: "npx", Args: []string{"-y", "airtable-mcp-server"}, Env: map[string]string{"AIRTABLE_API_KEY": "YOUR_KEY"}},
		},
		{
			name: "claude -e takes several pairs",
			in:   "claude mcp add -e A=1 B=x=y db -- ./server --port 3",
			want: Draft{Name: "db", Command: "./server", Args: []string{"--port", "3"}, Env: map[string]string{"A": "1", "B": "x=y"}},
		},
		{
			name: "claude command over continued lines, with a shell prompt",
			in:   "$ claude mcp add --transport http \\\n  linear \\\n  https://mcp.linear.app/mcp",
			want: Draft{Name: "linear", URL: "https://mcp.linear.app/mcp"},
		},
		{
			name: "claude add-json",
			in:   `claude mcp add-json weather '{"type":"stdio","command":"/opt/weather","args":["--units","metric"],"env":{"CACHE":"/tmp"}}'`,
			want: Draft{Name: "weather", Command: "/opt/weather", Args: []string{"--units", "metric"}, Env: map[string]string{"CACHE": "/tmp"}},
		},
		{
			name: "codex remote",
			in:   "codex mcp add docs --url https://developers.openai.com/mcp",
			want: Draft{Name: "docs", URL: "https://developers.openai.com/mcp"},
		},
		{
			name: "codex stdio with env",
			in:   "codex mcp add gh --env GITHUB_TOKEN=ghp_x -- npx -y @modelcontextprotocol/server-github",
			want: Draft{Name: "gh", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-github"}, Env: map[string]string{"GITHUB_TOKEN": "ghp_x"}},
		},
		{
			name: "mcpServers map yields its first server",
			in: `{"mcpServers": {
				"zeta": {"type": "http", "url": "https://z.example.com/mcp", "headers": {"Authorization": "Bearer t"}},
				"alpha": {"command": "alpha"}
			}}`,
			want: Draft{Name: "zeta", URL: "https://z.example.com/mcp", Headers: map[string]string{"Authorization": "Bearer t"}},
		},
		{
			name: "one server object, named from its URL",
			in:   `{"type": "http", "url": "https://api.githubcopilot.com/mcp/"}`,
			want: Draft{Name: "githubcopilot", URL: "https://api.githubcopilot.com/mcp/"},
		},
		{
			name: "a fragment cut out of a bigger file",
			in:   `"Linear Tools": {"serverUrl": "https://mcp.linear.app/mcp"},`,
			want: Draft{Name: "linear-tools", URL: "https://mcp.linear.app/mcp"},
		},
		{
			name: "an mcp-remote bridge becomes the remote server",
			in: `{"mcpServers": {"cf-observability": {"command": "npx",
				"args": ["-y", "mcp-remote@latest", "https://observability.mcp.cloudflare.com/mcp", "--header", "Authorization: Bearer ${TOKEN}"],
				"env": {"TOKEN": "secret"}}}}`,
			want: Draft{Name: "cf-observability", URL: "https://observability.mcp.cloudflare.com/mcp", Headers: map[string]string{"Authorization": "Bearer secret"}},
		},
		{
			name: "toml remote with headers codex reads from its env",
			in:   "[mcp_servers.figma]\nurl = \"https://mcp.figma.com/mcp\"\nhttp_headers = { \"X-Team\" = \"t1\" }\nenv_http_headers = { \"X-Key\" = \"FIGMA_KEY\" }\n",
			want: Draft{Name: "figma", URL: "https://mcp.figma.com/mcp", Headers: map[string]string{"X-Team": "t1", "X-Key": ""}},
		},
		{
			name: "toml stdio with an env table",
			in:   "[mcp_servers.fs]\ncommand = \"npx\"\nargs = [\"-y\", \"@modelcontextprotocol/server-filesystem\", \"/tmp\"]\n[mcp_servers.fs.env]\nDEBUG = \"1\"\n",
			want: Draft{Name: "fs", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"}, Env: map[string]string{"DEBUG": "1"}},
		},
		{
			name: "a bare command is named after the package it runs",
			in:   "npx -y @modelcontextprotocol/server-github@1.2.0",
			want: Draft{Name: "server-github", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-github@1.2.0"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.want
			if want.Env == nil {
				want.Env = map[string]string{}
			}
			if want.Headers == nil {
				want.Headers = map[string]string{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got  %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestParseRefusesWhatItCannotRead(t *testing.T) {
	for _, in := range []string{
		"",
		"{not json",
		`{"name": "x"}`,
		"claude mcp add onlyname",
		"claude mcp add --frobnicate x https://x.example.com",
		"codex mcp add nothing",
		"[mcp_servers.x]\nenabled = true\n",
		`claude mcp add x "unclosed`,
		"--flag first",
	} {
		if d, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = %+v, want an error", in, d)
		}
	}
}
