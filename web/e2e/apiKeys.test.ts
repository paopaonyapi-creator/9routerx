// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
/**
 * The API-key row menu (#224).
 *
 * The destructive-verb guards are the point of this file. Collapsing seven
 * per-row controls into one menu dropped the `confirm()` on delete and
 * regenerate, and no gate in the repo noticed: `tsc` reads types, `vite build`
 * bundles, the svelte-check ratchet counts diagnostics, and `go test` exercises
 * the HTTP API without rendering a component. Each assertion below therefore
 * checks an *observable* consequence — the row survives a dismissal, and the
 * dialog is the one an operator expects — rather than that some function calls
 * `confirm`.
 */

import { afterAll, beforeAll, describe, expect, test } from 'bun:test'

import { createKey, keyRow, openRowMenu, startDashboard, type Dashboard } from './harness'

let app: Dashboard
let page: Dashboard['page']

beforeAll(async () => {
  app = await startDashboard(20411)
  page = app.page
})
afterAll(async () => {
  await app.stop()
})

describe('api key row menu', () => {
  beforeAll(async () => {
    await page.getByRole('button', { name: 'Create Key' }).waitFor()
  })

  test('dismissing the delete dialog leaves the key intact', async () => {
    const key = await createKey(page, 'E2E Delete Guard')

    page.once('dialog', (d) => d.dismiss())
    await openRowMenu(page, key)
    await page.getByRole('menuitem', { name: 'Delete key' }).click()
    await page.waitForTimeout(500)

    // The row must still be there: a dismissed confirmation is a no-op.
    await keyRow(page, key).waitFor()
  })

  test('accepting the delete dialog removes the key', async () => {
    const key = await createKey(page, 'E2E Delete Confirm')

    page.once('dialog', (d) => d.accept())
    await openRowMenu(page, key)
    await page.getByRole('menuitem', { name: 'Delete key' }).click()

    await keyRow(page, key).waitFor({ state: 'detached' })
  })

  test('dismissing the regenerate dialog keeps the current secret', async () => {
    const key = await createKey(page, 'E2E Rotate Guard')

    const secretBefore = await keyRow(page, key).locator('code').innerText()

    page.once('dialog', (d) => d.dismiss())
    await openRowMenu(page, key)
    await page.getByRole('menuitem', { name: 'Regenerate secret' }).click()
    await page.waitForTimeout(500)

    const secretAfter = await keyRow(page, key).locator('code').innerText()

    expect(secretAfter).toBe(secretBefore)
  })

  test('the delete confirmation names the key it is about to destroy', async () => {
    const key = await createKey(page, 'E2E Named Prompt')

    const message = Promise.withResolvers<string>()
    page.once('dialog', (d) => {
      void message.resolve(d.message())
      void d.dismiss()
    })

    await openRowMenu(page, key)
    await page.getByRole('menuitem', { name: 'Delete key' }).click()

    // A prompt reading "this key" would be true of every key and useless.
    expect(await message.promise).toContain(key)
  })
})

describe('a key whose secret is stored hashed', () => {
  // Item 5 reversed issue #199: an issued key keeps only an argon2id verifier,
  // so its secret exists in the create/rotate response and nowhere else. The
  // row therefore has nothing to reveal or copy, and the controls say so
  // instead of vanishing — a missing button reads as a broken row.
  //
  // The mask is matched by its bullets rather than by a specific character: the
  // cell masks whatever the server sent, so the assertion survives a change of
  // mask style but not the removal of masking.
  test('offers no copy or reveal, and explains why', async () => {
    const key = await createKey(page, 'E2E Hash-Only Row')
    const row = keyRow(page, key)

    const displayed = await row.locator('code').innerText()
    expect(displayed).toContain('•')
    expect(displayed.startsWith('sk-')).toBe(true)

    const copy = row.getByRole('button', { name: `Copy key ${key}` })
    const reveal = row.getByRole('button', { name: `Key ${key} cannot be revealed` })
    await copy.waitFor()
    await reveal.waitFor()
    expect(await copy.isDisabled()).toBe(true)
    expect(await reveal.isDisabled()).toBe(true)
    // The reason travels with the control, so a dead button is not a mystery.
    expect(await reveal.getAttribute('title')).toContain('hashed')
  })

  test('shows the secret once, at creation, and it still authenticates', async () => {
    await page.getByRole('button', { name: 'Create Key' }).click()
    await page.getByLabel('Key Name').fill('E2E One-Time Secret')
    await page.getByLabel('Key Name').press('Enter')

    const modal = page.locator('.fixed.inset-0').filter({ hasText: 'API Key Created' })
    const created = modal.locator('input[readonly]').first()
    await created.waitFor({ timeout: 10_000 })
    const secret = (await created.inputValue()).trim()
    expect(secret.startsWith('sk-')).toBe(true)
    await page.getByRole('button', { name: 'Done' }).click()

    // The row must not carry it: what the list gives back is the mask.
    const shown = await keyRow(page, 'E2E One-Time Secret').locator('code').innerText()
    expect(shown).not.toBe(secret)
    expect(shown).toContain('•')

    // The end of the user story: the value copied at creation still works.
    const res = await fetch(`${app.baseURL}/v1/models`, {
      headers: { Authorization: `Bearer ${secret}` },
    })
    expect(res.status).toBe(200)
  })

  test('rotating hands back the replacement once and stops there', async () => {
    const key = await createKey(page, 'E2E Rotate Once')

    page.once('dialog', (d) => d.accept())
    await openRowMenu(page, key)
    await page.getByRole('menuitem', { name: 'Regenerate secret' }).click()

    // The table reveals the replacement it just received; that is the only
    // moment the new secret is readable from the dashboard.
    let rotated = ''
    const deadline = Date.now() + 10_000
    while (Date.now() < deadline) {
      const text = (await keyRow(page, key).locator('code').innerText()).trim()
      if (/^sk-[a-f0-9]{32}$/.test(text)) {
        rotated = text
        break
      }
      await page.waitForTimeout(100)
    }
    expect(rotated).not.toBe('')

    // A reload drops the one-time value: the row goes back to its mask.
    await page.reload()
    const after = await keyRow(page, key).locator('code').innerText()
    expect(after).not.toBe(rotated)
    expect(after).toContain('•')

    const ok = await fetch(`${app.baseURL}/v1/models`, {
      headers: { Authorization: `Bearer ${rotated}` },
    })
    expect(ok.status).toBe(200)
  })
})

describe('api key batch bar', () => {
  test('pauses every selected key at once', async () => {
    const a = await createKey(page, 'E2E Batch A')
    const b = await createKey(page, 'E2E Batch B')

    await page.getByRole('checkbox', { name: 'Select all keys' }).check()
    await page.getByRole('button', { name: 'Pause' }).click()

    for (const key of [a, b]) {
      await keyRow(page, key).getByText('Paused').waitFor()
    }
  })
})