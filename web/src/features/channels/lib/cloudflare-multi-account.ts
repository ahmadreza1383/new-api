/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

export const CLOUDFLARE_ACCOUNT_CREDENTIALS_ERROR =
  'Invalid Cloudflare account credentials: use JSON objects with account_id and api_key'

// Match the host's credential format; the host remains the authority for all
// validation, including updates that keep the saved (unrevealed) credentials.
export function normalizeCloudflareAccounts(value: string): string[] {
  const raw = value.trim()
  let entries: unknown[]
  if (raw.startsWith('[')) {
    try {
      const parsed: unknown = JSON.parse(raw)
      if (!Array.isArray(parsed) || parsed.length === 0) {
        throw new Error(CLOUDFLARE_ACCOUNT_CREDENTIALS_ERROR)
      }
      entries = parsed
      if (entries.some((entry) => typeof entry !== 'object' || entry === null)) {
        throw new Error(CLOUDFLARE_ACCOUNT_CREDENTIALS_ERROR)
      }
    } catch {
      throw new Error(CLOUDFLARE_ACCOUNT_CREDENTIALS_ERROR)
    }
  } else if (raw.startsWith('{')) {
    try {
      entries = [JSON.parse(raw) as unknown]
    } catch {
      entries = raw
        .split('\n')
        .map((entry) => entry.trim())
        .filter(Boolean)
    }
  } else {
    entries = raw
      .split('\n')
      .map((entry) => entry.trim())
      .filter(Boolean)
  }
  if (entries.length === 0) {
    throw new Error(CLOUDFLARE_ACCOUNT_CREDENTIALS_ERROR)
  }

  return entries.map((entry) => {
    let credential: unknown = entry
    if (typeof entry === 'string') {
      try {
        credential = JSON.parse(entry) as unknown
      } catch {
        throw new Error(CLOUDFLARE_ACCOUNT_CREDENTIALS_ERROR)
      }
    }
    if (
      typeof credential !== 'object' ||
      credential === null ||
      Array.isArray(credential)
    ) {
      throw new Error(CLOUDFLARE_ACCOUNT_CREDENTIALS_ERROR)
    }
    const fields = credential as Record<string, unknown>
    if (
      Object.keys(fields).length !== 2 ||
      typeof fields.account_id !== 'string' ||
      typeof fields.api_key !== 'string'
    ) {
      throw new Error(CLOUDFLARE_ACCOUNT_CREDENTIALS_ERROR)
    }
    const accountId = fields.account_id.trim()
    const apiKey = fields.api_key.trim()
    if (
      !/^[A-Za-z0-9_-]+$/.test(accountId) ||
      !apiKey ||
      /[^\x21-\x7e]/.test(apiKey) ||
      ['{', '}', '[', ']', '"'].some((character) => apiKey.includes(character))
    ) {
      throw new Error(CLOUDFLARE_ACCOUNT_CREDENTIALS_ERROR)
    }
    return JSON.stringify({ account_id: accountId, api_key: apiKey })
  })
}
