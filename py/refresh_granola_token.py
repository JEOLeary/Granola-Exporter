#!/usr/bin/env python3
"""
Refresh the Granola MCP access token using the refresh token.

Reads the saved token from ~/.hermes/granola_token.json,
refreshes it, writes the new token, and updates config.yaml.

Can be run as a cron job or manually.

Usage:
    uv run python3 refresh_granola_token.py
"""

import json
import os
import sys
import urllib.request
import urllib.parse

TOKEN_FILE = os.path.expanduser("~/.hermes/granola_token.json")
CONFIG_FILE = os.path.expanduser("~/.hermes/config.yaml")
AUTH_SERVER = "https://mcp-auth.granola.ai"
CLIENT_ID = "client_01KTN4Q7CZZJ7JGZ7WXBQ0813C"


def main():
    # Read current token
    if not os.path.exists(TOKEN_FILE):
        print("❌ Token file not found. Run granola_mcp_auth.py first.")
        return 1

    with open(TOKEN_FILE) as f:
        token = json.load(f)

    refresh_token = token.get("refresh_token")
    if not refresh_token:
        print("❌ No refresh token available. Re-authenticate with granola_mcp_auth.py")
        return 1

    # Refresh the token
    data = urllib.parse.urlencode({
        "grant_type": "refresh_token",
        "refresh_token": refresh_token,
        "client_id": CLIENT_ID,
    }).encode()

    req = urllib.request.Request(
        f"{AUTH_SERVER}/oauth2/token",
        data=data,
        headers={"Content-Type": "application/x-www-form-urlencoded"},
    )

    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            new_token = json.loads(resp.read())
    except urllib.error.HTTPError as e:
        print(f"❌ Refresh failed: {e.code} {e.read().decode()[:200]}")
        print("   Re-authenticate with: uv run python3 granola_mcp_auth.py")
        return 1

    # If no new refresh token, keep the old one
    if "refresh_token" not in new_token:
        new_token["refresh_token"] = refresh_token

    # Save
    with open(TOKEN_FILE, "w") as f:
        json.dump(new_token, f, indent=2)

    # Update config.yaml
    access_token = new_token["access_token"]
    try:
        import yaml
        with open(CONFIG_FILE) as f:
            config = yaml.safe_load(f)

        if "mcp_servers" in config and "granola" in config["mcp_servers"]:
            config["mcp_servers"]["granola"]["headers"]["Authorization"] = f"Bearer {access_token}"
            with open(CONFIG_FILE, "w") as f:
                yaml.dump(config, f, default_flow_style=False, sort_keys=False)
            print(f"✅ Token refreshed and config updated! Expires in {new_token.get('expires_in', '?')}s")
        else:
            print(f"✅ Token refreshed, but granola not in config.yaml. Add manually.")
    except Exception as e:
        print(f"⚠️  Token refreshed but config update failed: {e}")
        print(f"   Manually update your config with the new token.")
        print(f"   New access_token: {access_token[:30]}...")

    return 0


if __name__ == "__main__":
    sys.exit(main())
