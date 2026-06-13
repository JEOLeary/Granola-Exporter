#!/usr/bin/env python3
"""
Granola MCP OAuth Authentication Script

Uses the Device Authorization Grant flow (no browser redirect server needed):

1. Requests a device code from Granola's auth server
2. Shows the user a URL + code to authenticate
3. Polls for the token
4. Saves the token to JSON
5. Shows the Hermes config.yaml snippet

Usage:
    uv run python3 granola_mcp_auth.py
"""

import asyncio
import json
import os
import sys
import time

import httpx

# Configuration
SERVER_URL = "https://mcp.granola.ai/mcp"
AUTH_SERVER = "https://mcp-auth.granola.ai"
TOKEN_FILE = os.path.expanduser("~/.hermes/granola_token.json")
CLIENT_INFO_FILE = os.path.expanduser("~/.hermes/granola_client_info.json")

# Dynamically registered client ID from registration endpoint
CLIENT_ID = "client_01KTN4Q7CZZJ7JGZ7WXBQ0813C"

# MCP scopes for the resource
MCP_SCOPE = "mcp"


async def main():
    print("=" * 60, flush=True)
    print("  Granola MCP OAuth Authentication (Device Flow)", flush=True)
    print("=" * 60, flush=True)
    print(flush=True)

    async with httpx.AsyncClient() as client:
        # Step 1: Request device code
        print("  [Step 1/4] Requesting device code...", flush=True)
        print(flush=True)

        device_resp = await client.post(
            f"{AUTH_SERVER}/oauth2/device_authorization",
            data={
                "client_id": CLIENT_ID,
                "scope": MCP_SCOPE,
            },
        )

        if device_resp.status_code != 200:
            print(f"  ❌ Device auth request failed: {device_resp.status_code}", flush=True)
            print(f"  Response: {device_resp.text}", flush=True)
            return 1

        device_data = device_resp.json()
        device_code = device_data["device_code"]
        user_code = device_data["user_code"]
        verification_uri = device_data.get("verification_uri", device_data.get("verification_url"))
        verification_uri_complete = device_data.get("verification_uri_complete")
        interval = device_data.get("interval", 5)
        expires_in = device_data.get("expires_in", 600)

        print(f"  ✓ Device code received!", flush=True)
        print(flush=True)
        print("  ╔════════════════════════════════════════════════════════╗", flush=True)
        print("  ║          GRANOLA MCP AUTHORIZATION REQUIRED           ║", flush=True)
        print("  ╚════════════════════════════════════════════════════════╝", flush=True)
        print(flush=True)
        print(f"  Step 2: Open this URL in your browser:", flush=True)
        print(flush=True)

        if verification_uri_complete:
            print(f"  {verification_uri_complete}", flush=True)
        else:
            print(f"  {verification_uri}", flush=True)
            print(flush=True)
            print(f"  Step 3: Enter this code:", flush=True)
            print(flush=True)
            print(f"  {user_code}", flush=True)

        print(flush=True)
        print(f"  (Code expires in {expires_in} seconds)", flush=True)
        print(flush=True)
        print(f"  Waiting for you to authenticate... (polling every {interval}s)", flush=True)
        print(flush=True)

        # Step 2: Poll for token
        start_time = time.time()
        token_data = None

        while time.time() - start_time < expires_in:
            await asyncio.sleep(interval)

            token_resp = await client.post(
                f"{AUTH_SERVER}/oauth2/token",
                data={
                    "grant_type": "urn:ietf:params:oauth:grant-type:device_code",
                    "device_code": device_code,
                    "client_id": CLIENT_ID,
                },
            )

            if token_resp.status_code == 200:
                token_data = token_resp.json()
                break
            elif token_resp.status_code == 400:
                error_data = token_resp.json()
                error = error_data.get("error", "")

                if error == "authorization_pending":
                    print("  .", end="", flush=True)
                    continue
                elif error == "slow_down":
                    interval += 5
                    continue
                elif error == "expired_token":
                    print(flush=True)
                    print(f"\n  ❌ Device code expired. Please re-run.", flush=True)
                    return 1
                else:
                    print(flush=True)
                    print(f"\n  ❌ Error: {error}", flush=True)
                    return 1
            else:
                print(flush=True)
                print(f"\n  ❌ Token request failed: {token_resp.status_code}", flush=True)
                print(f"  Response: {token_resp.text}", flush=True)
                return 1

        if not token_data:
            print(flush=True)
            print(f"\n  ❌ Timed out waiting for authorization.", flush=True)
            return 1

        print(flush=True)
        print(flush=True)
        print(f"  ✅ Authentication successful!", flush=True)
        print(flush=True)

        # Save token
        with open(TOKEN_FILE, "w") as f:
            json.dump(token_data, f, indent=2)
        print(f"  ✓ Token saved to {TOKEN_FILE}", flush=True)
        print(flush=True)

        # Show token details
        access_token = token_data.get("access_token", "")
        token_type = token_data.get("token_type", "Bearer")
        expires_in_val = token_data.get("expires_in", 3600)
        refresh_token = token_data.get("refresh_token")
        scope = token_data.get("scope", "")

        print(f"  Token type: {token_type}", flush=True)
        print(f"  Expires in: {expires_in_val}s", flush=True)
        print(f"  Has refresh token: {bool(refresh_token)}", flush=True)
        print(f"  Scopes: {scope}", flush=True)
        print(flush=True)

        # Step 3: Test the token by making an MCP request
        print(f"  [Step 4/4] Testing token against MCP server...", flush=True)
        print(flush=True)

        test_resp = await client.post(
            SERVER_URL,
            json={"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {}},
            headers={
                "Content-Type": "application/json",
                "Accept": "application/json",
                "Authorization": f"Bearer {access_token}",
            },
        )

        if test_resp.status_code == 200:
            result = test_resp.json()
            tools = result.get("result", {}).get("tools", [])
            print(f"  ✅ Token works! Found {len(tools)} tools:", flush=True)
            if tools:
                for tool in tools:
                    desc = tool.get("description", "")[:80]
                    print(f"    • {tool.get('name')}: {desc}", flush=True)
                    input_schema = tool.get("inputSchema", {})
                    props = input_schema.get("properties", {})
                    if props:
                        for prop_name, prop_info in props.items():
                            print(f"        - {prop_name} ({prop_info.get('type', 'any')}): {prop_info.get('description', '')[:60]}", flush=True)
            else:
                print(f"    (no tools returned)", flush=True)
        else:
            print(f"  ⚠️  Token test returned {test_resp.status_code}", flush=True)
            print(f"  Response: {test_resp.text[:300]}", flush=True)

        print(flush=True)
        print("=" * 60, flush=True)
        print("  NEXT STEPS: Hermes Configuration", flush=True)
        print("=" * 60, flush=True)
        print(flush=True)
        print("  Add to ~/.hermes/config.yaml under mcp.servers:", flush=True)
        print(flush=True)
        print("  granola:", flush=True)
        print("    transport: http", flush=True)
        print(f"    url: {SERVER_URL}", flush=True)
        print(f"    headers:", flush=True)
        print(f'      Authorization: "Bearer {access_token}"', flush=True)
        print(flush=True)
        if refresh_token:
            print("  ⚠️  Token expires in {expires_in_val}s. When it does, re-run this script.", flush=True)
        print(flush=True)

    return 0


if __name__ == "__main__":
    try:
        exit_code = asyncio.run(main())
        sys.exit(exit_code)
    except KeyboardInterrupt:
        print(flush=True)
        print("\n  Interrupted by user.", flush=True)
        sys.exit(1)
    except Exception as e:
        print(f"\n  ❌ Error: {e}", flush=True)
        import traceback
        traceback.print_exc()
        sys.exit(1)
