import { expect, it } from 'vitest'
import { compareReleaseVersions } from '../src/renderer/src/release-version'

it('compares three- and four-component release versions numerically', () => {
  expect(compareReleaseVersions('v0.5.8.1', '0.5.8')).toBeGreaterThan(0)
  expect(compareReleaseVersions('0.5.8.10', '0.5.8.2')).toBeGreaterThan(0)
  expect(compareReleaseVersions('0.5.8.1', '0.5.9')).toBeLessThan(0)
  expect(compareReleaseVersions('0.5.8.0', '0.5.8')).toBe(0)
})

it.each(['0.5', '0.5.8.1.2', '0.5.8.01', '0.5.8.1-beta'])('rejects invalid release version %s', version => {
  expect(() => compareReleaseVersions(version, '0.5.8')).toThrow('无法识别发布版本')
})
