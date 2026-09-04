// Copyright Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: Apache-2.0

// Externalize the SDK and its sibling client packages so Next.js does NOT
// bundle them via webpack. Node loads them as ESM at runtime, which is the
// failure mode reported in issue #4771.
module.exports = {
  serverExternalPackages: ['@northrays/sdk', '@northrays/api-client', '@northrays/toolbox-api-client'],
}
