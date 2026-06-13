#!/usr/bin/env python3
"""Test the Granola MCP token by listing available tools."""
import json
import urllib.request

# Read token
with open("/home/jeoleary/.hermes/granola_token.json") as f:
    token = json.load(f)

access_token = token["access_token"]

req = urllib.request.Request(
    "https://mcp.granola.ai/mcp",
    data=json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {}}).encode(),
    headers={
        "Content-Type": "application/json",
        "Accept": "application/json, text/event-stream",
        "Authorization": f"Bearer ***    },
)

try:
    with urllib.request.urlopen(req, timeout=15) as resp:
        data = json.loads(resp.read())
        print("=== Full Response ===")
        print(json.dumps(data, indent=2))
except urllib.error.HTTPError as e:
    print(f"HTTP {e.code}: {e.read().decode()[:500]}")
except Exception as e:
    print(f"Error: {e}")
