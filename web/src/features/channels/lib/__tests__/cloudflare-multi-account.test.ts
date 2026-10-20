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

import { describe, expect, test } from 'vitest'

import { CHANNEL_TYPE_CLOUDFLARE_MULTI } from '../../constants'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  transformFormDataToCreatePayload,
  transformFormDataToUpdatePayload,
  type ChannelFormValues,
} from '../channel-form'
import {
  CLOUDFLARE_ACCOUNT_CREDENTIALS_ERROR,
  normalizeCloudflareAccounts,
} from '../cloudflare-multi-account'

function cloudflareForm(
  overrides: Partial<ChannelFormValues> = {}
): ChannelFormValues {
  return {
    ...CHANNEL_FORM_DEFAULT_VALUES,
    name: 'Cloudflare accounts',
    type: CHANNEL_TYPE_CLOUDFLARE_MULTI,
    key: '[{"account_id":"account-one","api_key":"key-one"},{"api_key":"key-two","account_id":"account-two"}]',
    models: 'public-model',
    model_mapping: '{"public-model":"@cf/test/model"}',
    multi_key_mode: 'multi_to_single',
    multi_key_type: 'polling',
    ...overrides,
  }
}

describe('Cloudflare account credentials', () => {
  test('creates one multi-key channel without requiring a shared Account ID', () => {
    const payload = transformFormDataToCreatePayload(
      channelFormSchema.parse(cloudflareForm())
    )
    expect(payload.mode).toBe('multi_to_single')
    expect(payload.multi_key_mode).toBe('polling')
    expect(payload.channel.key).toBe(
      '{"account_id":"account-one","api_key":"key-one"}\n{"account_id":"account-two","api_key":"key-two"}'
    )
    expect(payload.channel.models).toBe('public-model')
    expect(payload.channel.model_mapping).toBe(
      '{"public-model":"@cf/test/model"}'
    )
  })

  test('preserves legacy keys with their shared Account ID', () => {
    const payload = transformFormDataToCreatePayload(
      channelFormSchema.parse(
        cloudflareForm({
          type: 39,
          key: 'key-one\nkey-two',
          other: 'shared-account',
        })
      )
    )
    expect(payload.channel.key).toBe('key-one\nkey-two')
    expect(payload.channel.other).toBe('shared-account')
    expect(
      channelFormSchema.safeParse(
        cloudflareForm({ type: 39, key: 'legacy-key', other: '' })
      ).success
    ).toBe(false)
  })

  test('normalizes pretty JSON and newline account objects', () => {
    expect(
      normalizeCloudflareAccounts(
        '{\n"account_id":"account-one",\n"api_key":"key-one"\n}'
      )
    ).toEqual(['{"account_id":"account-one","api_key":"key-one"}'])
    expect(
      normalizeCloudflareAccounts(
        '{"api_key":"key-one","account_id":"account-one"}\n{"account_id":"account-two","api_key":"key-two"}'
      )
    ).toEqual([
      '{"account_id":"account-one","api_key":"key-one"}',
      '{"account_id":"account-two","api_key":"key-two"}',
    ])
  })

  test.each([
    'plain-key-without-an-account',
    '[]',
    '["plain-key"]',
    '{"account_id":"../account","api_key":"secret"}',
    '{"account_id":null,"api_key":"secret"}',
    '{"account_id":"account","api_key":null}',
    '{"account_id":"account","api_key":"secret\\r\\nInjected: yes"}',
    '{"account_id":"account","api_key":"secret","proxy":"http://proxy:8080"}',
  ])('rejects invalid credentials without exposing them: %s', (key) => {
    const result = channelFormSchema.safeParse(cloudflareForm({ key }))
    expect(result.success).toBe(false)
    if (result.success) return
    expect(result.error.issues).toContainEqual(
      expect.objectContaining({
        path: ['key'],
        message: CLOUDFLARE_ACCOUNT_CREDENTIALS_ERROR,
      })
    )
    expect(result.error.message).not.toContain('Injected: yes')
  })

  test('keeps unrevealed credentials untouched while editing', () => {
    const form = cloudflareForm({ key: '' })
    expect(channelFormSchema.safeParse(form).success).toBe(true)
    expect(transformFormDataToUpdatePayload(form, 7)).not.toHaveProperty('key')
  })

  test('requires account objects even if an old shared Account ID is supplied', () => {
    const result = channelFormSchema.safeParse(
      cloudflareForm({ key: 'plain-key', other: 'shared-account' })
    )
    expect(result.success).toBe(false)
  })

  test('normalizes replacement credentials for an existing channel', () => {
    const payload = transformFormDataToUpdatePayload(
      cloudflareForm({ key_mode: 'replace' }),
      7
    )
    expect(payload.key).toBe(
      '{"account_id":"account-one","api_key":"key-one"}\n{"account_id":"account-two","api_key":"key-two"}'
    )
  })
})
