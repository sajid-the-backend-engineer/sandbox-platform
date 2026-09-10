/*
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

/**
 * Restricts login to an explicit list of email addresses.
 *
 * WHY THIS AND NOT "DISABLE THE AUTH0 APPLICATION". Disabling the application
 * stops new tokens being issued, which sounds like the same thing and is not:
 * the operator's own session is a token with an expiry, and when it lapses the
 * refresh fails too. The result is a deployment nobody can log into, including
 * the person who locked it. This denies everyone except the allow list and
 * leaves that list's own login working indefinitely.
 *
 * It is also the control that survives a change of address. The ALB source-IP
 * rule keeps the login page off the public internet, but consumer IPs move and
 * some are shared behind carrier NAT. Identity is the durable boundary; the
 * network rule is the surface reduction in front of it.
 *
 * Fails CLOSED. A missing or empty ALLOWED_EMAILS denies every login rather
 * than falling back to permitting them -- a secret that did not load is
 * indistinguishable, from in here, from a secret that was deleted, and the
 * safe reading of "I cannot tell who is allowed" is "nobody".
 *
 * SETUP
 *   Auth0 Dashboard -> Actions -> Library -> Build Custom
 *     Name:    Restrict to allowed emails
 *     Trigger: Login / Post Login
 *   Paste this file, then add a secret:
 *     ALLOWED_EMAILS = comma-separated addresses, e.g. "you@example.com"
 *   Drag the action into the Login flow and Apply.
 *
 * Order it AFTER setCustomClaims if that action is also in the flow: this one
 * can end the transaction, and there is no reason to run it before the claims
 * another action needs are attached.
 *
 * @param {Event} event
 * @param {PostLoginAPI} api
 */
exports.onExecutePostLogin = async (event, api) => {
  const DENIAL = 'This account is not permitted to sign in.'

  const configured = (event.secrets && event.secrets.ALLOWED_EMAILS) || ''
  const allowed = configured
    .split(',')
    .map((entry) => entry.trim().toLowerCase())
    .filter(Boolean)

  if (allowed.length === 0) {
    // No list means no answer to "who is allowed", not "everyone".
    return api.access.deny(DENIAL)
  }

  const email = (event.user.email || '').trim().toLowerCase()
  if (!email) {
    return api.access.deny(DENIAL)
  }

  // A verified address only. Without this check, a provider that lets a user
  // set an arbitrary unverified email would let anyone claim an allowed one.
  if (event.user.email_verified !== true) {
    return api.access.deny(DENIAL)
  }

  if (!allowed.includes(email)) {
    return api.access.deny(DENIAL)
  }
}
