/*
 * Copyright 2025 Daytona Platforms Inc.
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */
import { isIPv4 } from 'net'

/**
 * Validates network allow list to ensure valid CIDR network addresses are allowed
 * @param networkAllowList - Comma-separated string of network addresses
 * @returns null if valid, error message string if invalid
 */
export function validateNetworkAllowList(networkAllowList: string): void {
  const networks = networkAllowList.split(',').map((net: string) => net.trim())

  for (const network of networks) {
    if (!network) continue // Skip empty entries

    const [ipAddress, prefixLength] = network.split('/')

    if (!isIPv4(ipAddress)) {
      throw new Error(`Invalid IP address: "${ipAddress}" in network "${network}". Must be a valid IPv4 address`)
    }

    if (!prefixLength) {
      throw new Error(`Invalid network format: "${network}". Missing CIDR prefix length (e.g., /24)`)
    }

    // Validate CIDR prefix length (0-32 for IPv4)
    const prefix = parseInt(prefixLength, 10)
    if (prefix < 0 || prefix > 32) {
      throw new Error(`Invalid CIDR prefix length: ${network}. Prefix must be between 0 and 32`)
    }
  }

  if (networks.length > 10) {
    throw new Error(`Network allow list cannot contain more than 10 networks`)
  }
}

/**
 * The public-internet posture: any public hostname is permitted by NAME.
 *
 * This is not "unrestricted". The runner still places such a sandbox behind its
 * egress proxy and resolver, still runs it unprivileged, and still vets the address
 * each hostname resolves to before dialling — so cloud metadata, private ranges,
 * runner services and other tenants remain unreachable. Permitting every name does
 * not affect a check made on the address.
 */
export const ANY_HOST = '*'

/**
 * Validates domain allow list to ensure valid domain names are allowed
 * @param domainAllowList - Comma-separated string of domains (optionally prefixed with a `*.` wildcard),
 *                          or the single token `*` for the public-internet posture
 * @throws Error if any domain is invalid or the list is too long
 */
export function validateDomainAllowList(domainAllowList: string): void {
  const domains = domainAllowList.split(',').map((domain: string) => domain.trim())

  // `*` on its own is the public-internet posture and is accepted as the WHOLE list.
  // It is deliberately not accepted alongside named entries: "everything, plus these
  // in particular" reads as though the named ones were doing something, and a policy
  // that looks narrower than it is, is worse than one that is plainly broad.
  const meaningful = domains.filter((domain) => domain !== '')
  if (meaningful.length === 1 && meaningful[0] === ANY_HOST) {
    return
  }
  if (meaningful.includes(ANY_HOST)) {
    throw new Error(
      `Invalid domain allow list: "${ANY_HOST}" permits every host and cannot be combined with named domains`,
    )
  }

  // Hostname label format, optionally prefixed with a single `*.` wildcard (e.g. "*.northrays.com")
  const domainRegex = /^(\*\.)?([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$/

  for (const domain of domains) {
    if (!domain) continue // Skip empty entries

    if (!domainRegex.test(domain)) {
      throw new Error(`Invalid domain: "${domain}". Must be a valid domain name`)
    }
  }

  if (domains.length > 10) {
    throw new Error(`Domain allow list cannot contain more than 10 domains`)
  }
}
