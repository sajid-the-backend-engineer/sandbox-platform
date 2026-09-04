#!/bin/sh
# Copyright Daytona Platforms Inc.
# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

set -e

# Validate NORTHRAYS_BASE_API_URL is a well-formed URL
if ! echo "$NORTHRAYS_BASE_API_URL" | grep -Eq '^https?://[a-zA-Z0-9.:/?=_-]*$'; then
    echo "Error: NORTHRAYS_BASE_API_URL is not a valid URL."
    exit 1
fi

# Escape characters that could break sed replacement
escape_sed() {
    printf '%s' "$1" | sed -e 's/[\/&|\\]/\\&/g'
}
NORTHRAYS_BASE_API_URL_ESCAPED=$(escape_sed "$NORTHRAYS_BASE_API_URL")

# Replace %NORTHRAYS_BASE_API_URL% with actual environment variable value
find /usr/share/nginx/html -type f \( -name "*.js" -o -name "*.html" \) -exec sed -i "s|%NORTHRAYS_BASE_API_URL%|${NORTHRAYS_BASE_API_URL_ESCAPED}|g" {} +

# Start nginx
exec nginx -g "daemon off;"
