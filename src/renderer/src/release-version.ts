const releaseVersionPattern = /^v?(?:0|[1-9]\d{0,8})(?:\.(?:0|[1-9]\d{0,8})){2,3}$/

function versionParts(version: string): number[] {
  if (!releaseVersionPattern.test(version)) throw new Error('无法识别发布版本。')
  return version.replace(/^v/, '').split('.').map(Number)
}

export function compareReleaseVersions(left: string, right: string): number {
  const leftParts = versionParts(left), rightParts = versionParts(right)
  for (let index = 0; index < 4; index++) {
    const difference = (leftParts[index] ?? 0) - (rightParts[index] ?? 0)
    if (difference !== 0) return difference
  }
  return 0
}
