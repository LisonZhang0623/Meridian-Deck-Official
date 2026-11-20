import { afterEach, expect, it } from 'vitest'
import { confirmAction, pendingConfirmation, resolveConfirmation } from '../src/renderer/src/confirmation'

const request = { title: '关闭？', message: '未保存的修改将丢失。', action: '关闭' }

afterEach(() => resolveConfirmation(false))

it('keeps one confirmation active and never accepts an overlapping request', async () => {
  const first = confirmAction(request)
  await expect(confirmAction({ ...request, title: '删除？' })).resolves.toBe(false)
  expect(pendingConfirmation.value).toBe(request)
  resolveConfirmation(true)
  await expect(first).resolves.toBe(true)
  expect(pendingConfirmation.value).toBeNull()
})

it('dismisses safely and permits the next action', async () => {
  const first = confirmAction(request)
  resolveConfirmation(false)
  await expect(first).resolves.toBe(false)

  const next = confirmAction(request)
  resolveConfirmation(true)
  await expect(next).resolves.toBe(true)
})
