import type { Profile } from './types'

// Only reuse a transport for the same server and login identity.
export function connectionIdentity(profile: Profile): string {
  // Fold only ASCII case so distinct Unicode host spellings stay separate.
  const host = profile.host.replace(/[A-Z]/g, letter => letter.toLowerCase())
  const identity: (string | number | boolean)[] = [profile.id, host, profile.port, profile.username, profile.authType, profile.authType === 'key' ? profile.keyPath : '']
  if (profile.proxyAddress || profile.keyboardInteractive) identity.push(profile.proxyAddress || '', !!profile.keyboardInteractive)
  if (profile.jumpProfileId) identity.push(profile.jumpProfileId, profile.jumpIdentity || '')
  return JSON.stringify(identity)
}
