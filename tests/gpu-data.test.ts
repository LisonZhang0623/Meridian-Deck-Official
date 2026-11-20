import { describe, expect, it } from 'vitest'
import { parseGpuDevices } from '../src/main/gpu-data'

describe('structured NVIDIA GPU data', () => {
  it('keeps one record per GPU, sorts indexes and preserves the metric units', () => {
    const devices = parseGpuDevices('2, GPU-b, NVIDIA A100, 99, 20000, 40000, 61, 199.5, 250, N/A, P0\r\n0, GPU-a, NVIDIA RTX 4090, 0, 0, 24564, 38, 20, 450, 0, P8\r\n')
    expect(devices.map(d => d.index)).toEqual([0, 2])
    expect(devices[0]).toMatchObject({ uuid: 'GPU-a', utilization: 0, memoryUsedMiB: 0, fanPercent: 0 })
    expect(devices[1]).toMatchObject({ uuid: 'GPU-b', memoryTotalMiB: 40000, temperatureC: 61, powerDrawW: 199.5, fanPercent: null, performanceState: 'P0' })
  })
  it('handles quoted names, commas and escaped quotes without shifting metrics', () => {
    const [gpu] = parseGpuDevices('0, GPU-a, "NVIDIA, ""测试卡""", 10, 256, 1024, 45, 42.5, 75, 20, P2')
    expect(gpu.name).toBe('NVIDIA, "测试卡"')
    expect(gpu.utilization).toBe(10)
    expect(gpu.memoryUsedMiB).toBe(256)
  })
  it('keeps unavailable and invalid readings distinct from zero', () => {
    const [gpu] = parseGpuDevices('0, GPU-a, NVIDIA, [N/A], N/A, [Not Supported], -1, NaN, Infinity, 101, N/A')
    expect(gpu).toMatchObject({ utilization: null, memoryUsedMiB: null, memoryTotalMiB: null, temperatureC: null, powerDrawW: null, powerLimitW: null, fanPercent: null, performanceState: null })
  })
  it('rejects malformed rows, duplicate IDs and truncated quotes instead of showing partial cards', () => {
    const row = '0, GPU-a, NVIDIA, 0, 0, 100, 40, 10, 20, 0, P8'
    for (const input of [row + '\n' + row, row + '\n' + row.replace('0, GPU-a', '1, GPU-a'), '0, GPU-a, card', row.replace('0, GPU-a', '-1, GPU-a'), row.replace('NVIDIA', '"NVIDIA')]) {
      expect(() => parseGpuDevices(input)).toThrow()
    }
  })
  it('recognizes an empty device list', () => {
    expect(parseGpuDevices(' \r\n')).toEqual([])
    expect(parseGpuDevices('No devices were found\n')).toEqual([])
  })
})
