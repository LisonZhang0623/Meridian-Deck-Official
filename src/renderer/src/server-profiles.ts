import type { Profile } from '../../shared/types'

export function filterProfiles(profiles: Profile[], query: string, tag: string, recent: boolean): Profile[] {
  const needle = query.trim().toLocaleLowerCase()
  const filtered = profiles.filter(profile => {
    if (recent && !profile.lastConnectedAt) return false
    if (tag && !(profile.tags || []).includes(tag)) return false
    return !needle || [profile.name, profile.host, profile.group, profile.username, ...(profile.tags || [])].join(' ').toLocaleLowerCase().includes(needle)
  })
  return recent ? filtered.sort((a, b) => b.lastConnectedAt! - a.lastConnectedAt! || a.name.localeCompare(b.name)) : filtered
}

export function profileTags(profiles: Profile[]): string[] {
  return [...new Set(profiles.flatMap(profile => profile.tags || []))].sort((a, b) => a.localeCompare(b))
}
