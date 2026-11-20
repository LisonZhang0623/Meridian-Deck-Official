import { ChangeSet, Text } from '@codemirror/state'

const supported = new Set('py go js mjs cjs jsx ts tsx sh bash c h cpp hpp rs java css html vue json yaml yml'.split(' '))
export function codeExtension(path: string) {
  const match = /[^/.]\.([a-z0-9]+)$/i.exec(path)
  const ext = match?.[1].toLowerCase()
  return ext && supported.has(ext) ? ext : undefined
}

// Preserve selections outside the edited range when another view changes the draft.
export function textChange(previous: string, next: string) {
  let start = 0, oldEnd = previous.length, newEnd = next.length
  while (start < oldEnd && start < newEnd && previous[start] === next[start]) start++
  while (oldEnd > start && newEnd > start && previous[oldEnd - 1] === next[newEnd - 1]) { oldEnd--; newEnd-- }
  return ChangeSet.of({ from: start, to: oldEnd, insert: Text.of(next.slice(start, newEnd).split('\n')) }, previous.length)
}

// BroadcastChannel preserves each sender's order. Rebase incoming edits over local
// edits still awaiting acknowledgement. The workbench wins ties at one position.
export function draftReplica(workbench: boolean) {
  const pending: ChangeSet[] = []
  return {
    change(before: string, after: string) {
      const changes = textChange(before, after)
      pending.push(changes)
      return changes.toJSON()
    },
    receive(value: unknown, current: string) {
      let incoming = ChangeSet.fromJSON(value)
      for (let i = 0; i < pending.length; i++) {
        const local = pending[i]
        pending[i] = local.map(incoming, workbench)
        incoming = incoming.map(local, !workbench)
      }
      return incoming.apply(Text.of(current.split('\n'))).toString()
    },
    acknowledge() { pending.shift() },
  }
}
