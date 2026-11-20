import { shallowRef } from 'vue'

export interface Confirmation {
  title: string
  message: string
  action: string
  danger?: boolean
}

export const pendingConfirmation = shallowRef<Confirmation | null>(null)
let complete: ((accepted: boolean) => void) | undefined

export function confirmAction(request: Confirmation): Promise<boolean> {
  if (complete) return Promise.resolve(false)
  return new Promise(resolve => {
    complete = resolve
    pendingConfirmation.value = request
  })
}

export function resolveConfirmation(accepted: boolean): void {
  const resolve = complete
  complete = undefined
  pendingConfirmation.value = null
  resolve?.(accepted)
}
