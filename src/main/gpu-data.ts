import type { GpuDevice } from '../shared/types'

export const GPU_QUERY_COMMAND = 'nvidia-smi --query-gpu=index,uuid,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw,power.limit,fan.speed,pstate --format=csv,noheader,nounits'

function csvRows(input: string): string[][] {
  const rows: string[][] = []
  let row: string[] = [], field = '', quoted = false, afterQuote = false
  for (let i = 0; i < input.length; i++) {
    const char = input[i]
    if (quoted) {
      if (char === '"') {
        if (input[i + 1] === '"') { field += '"'; i++ }
        else { quoted = false; afterQuote = true }
      } else field += char
    } else if (char === '"') {
      if (field.trim() || afterQuote) throw new Error('显卡查询返回了无效的 CSV 引号。')
      field = ''; quoted = true
    } else if (char === ',' || char === '\n' || char === '\r') {
      row.push(field.trim()); field = ''; afterQuote = false
      if (char !== ',') {
        if (row.some(value => value !== '')) rows.push(row)
        row = []
        if (char === '\r' && input[i + 1] === '\n') i++
      }
    } else {
      if (afterQuote && char.trim()) throw new Error('显卡查询返回了无效的 CSV 字段。')
      field += char
    }
  }
  if (quoted) throw new Error('显卡查询返回了不完整的 CSV 数据。')
  row.push(field.trim())
  if (row.some(value => value !== '')) rows.push(row)
  return rows
}
function metric(value: string, max = Number.MAX_SAFE_INTEGER): number | null {
  if (!/^\d+(?:\.\d+)?$/.test(value)) return null
  const number = Number(value)
  return Number.isFinite(number) && number <= max ? number : null
}
export function parseGpuDevices(output: string): GpuDevice[] {
  if (!output.trim() || /^No devices were found\.?$/i.test(output.trim())) return []
  const ids = new Set<number>(), uuids = new Set<string>()
  return csvRows(output).map(fields => {
    if (fields.length !== 11) throw new Error('显卡查询字段不完整，无法生成卡片。请检查 nvidia-smi 版本或服务器输出。')
    const [indexText, uuid, name, utilization, used, total, temperature, draw, limit, fan, performance] = fields
    const index = metric(indexText)
    if (index === null || !Number.isSafeInteger(index) || !/^GPU-\S+$/.test(uuid) || !name || ids.has(index) || uuids.has(uuid)) throw new Error('显卡查询返回了无效或重复的 GPU 标识。')
    ids.add(index); uuids.add(uuid)
    return {
      index, uuid, name, utilization: metric(utilization, 100), memoryUsedMiB: metric(used), memoryTotalMiB: metric(total),
      temperatureC: metric(temperature), powerDrawW: metric(draw), powerLimitW: metric(limit), fanPercent: metric(fan, 100),
      performanceState: /^P\d+$/.test(performance) ? performance : null
    }
  }).sort((a, b) => a.index - b.index)
}
