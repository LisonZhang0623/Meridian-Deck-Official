import { reactive } from 'vue'

// File-tree actions may prefill a path without starting a remote command implicitly.
export const logTargets = reactive<Record<string, string>>({})
