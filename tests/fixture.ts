import { Server, type Connection } from 'ssh2'
import { generateKeyPairSync, randomUUID } from 'node:crypto'
import { posix } from 'node:path'
import { createConnection, type AddressInfo } from 'node:net'

export interface MemoryFile { data: Buffer; mode: number; mtime: number }
export const GPU_COMMAND = 'nvidia-smi --query-gpu=index,uuid,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw,power.limit,fan.speed,pstate --format=csv,noheader,nounits'
export const GPU_SAMPLE = `0, GPU-test-0, NVIDIA RTX 4090, 78, 12288, 24564, 62, 245.5, 450, 45, P2
1, GPU-test-1, NVIDIA RTX 4090, 3, 512, 24564, 40, 28, 450, 30, P8`
export const RESOURCE_SAMPLE = `[cpu]
cpu 1000 0 500 8500 0 0 0 0 50 0
[memory]
MemTotal: 33554432 kB
MemFree: 1000000 kB
MemAvailable: 16777216 kB
[disk]
Filesystem 1024-blocks Used Available Capacity Mounted on
/dev/sda1 104857600 20971520 83886080 20% /
[network]
Inter-| Receive | Transmit
face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed
eth0: 100000 10 0 0 0 0 0 0 200000 10 0 0 0 0 0 0
lo: 500 10 0 0 0 0 0 0 500 10 0 0 0 0 0 0
[uptime]
3661.9 1000
[load]
1.20 0.80 0.50 1/100 1000`
export async function createFixture() {
  const privateKey = generateKeyPairSync('rsa', { modulusLength: 2048, privateKeyEncoding: { type: 'pkcs1', format: 'pem' }, publicKeyEncoding: { type: 'pkcs1', format: 'pem' } }).privateKey
  const files = new Map<string, MemoryFile>()
  const stamp = () => Math.floor(Date.now() / 1000)
  for (const path of ['/', '/home', '/home/test', '/home/test/configs', '/home/test/images']) files.set(path, { data: Buffer.alloc(0), mode: 0o40755, mtime: stamp() })
  files.set('/home/test/configs/config.json', { data: Buffer.from('{"message":"hello 世界","epochs":20}\n'), mode: 0o100644, mtime: stamp() })
  files.set('/home/test/hello.txt', { data: Buffer.from('Hello SSH\n'), mode: 0o100644, mtime: stamp() })
  files.set('/home/test/app.log', { data: Buffer.from('INFO training started\nERROR sample diagnostic\n'), mode: 0o100644, mtime: stamp() })
  files.set('/home/test/images/pixel.png', { data: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAAEUlEQVR4nAAEAPv/AhqMhwMAAfkBMBehCXUAAAAASUVORK5CYII=', 'base64'), mode: 0o100644, mtime: stamp() })
  files.set('/home/test/images/pixel-second.png', { data: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAAEUlEQVR4nAAEAPv/AplTvQMAAzoBrFsOlK4AAAAASUVORK5CYII=', 'base64'), mode: 0o100644, mtime: stamp() })
  const clients = new Set<Connection>()
  const metrics = { connections: 0, authentications: 0 }
  const behavior = { rejectNextShell: false }
  const shells = { opened: 0, commands: [] as { shell: number; command: string }[] }
  const gpu = { commands: [] as string[], output: GPU_SAMPLE, stderr: '', exitCode: 0, delayMs: 0, hang: false }
  const resources = { commands: [] as string[], output: RESOURCE_SAMPLE, samples: 0 }
  const logs = { commands: [] as string[], active: 0 }
  const server = new Server({ hostKeys: [privateKey] }, client => {
    metrics.connections++
    clients.add(client)
    client.on('error', () => {})
    client.on('close', () => clients.delete(client))
    client.on('tcpip', (accept, reject, info) => {
      const socket = createConnection({ host: info.destIP, port: info.destPort })
      socket.once('error', () => reject())
      socket.once('connect', () => { const stream = accept(); stream.on('error', () => socket.destroy()); socket.on('error', () => stream.destroy()); stream.on('close', () => socket.destroy()); socket.on('close', () => stream.destroy()); socket.pipe(stream); stream.pipe(socket) })
    })
    client.on('authentication', context => { if (context.method === 'password' && context.username === 'test' && context.password === 'test-password') { metrics.authentications++; context.accept() } else context.reject(['password']) })
    client.on('ready', () => client.on('session', accept => {
      const session = accept()
      session.on('pty', accept => accept?.())
      session.on('window-change', accept => accept?.())
      session.on('exec', (accept, reject, info) => {
        if (info.command.startsWith('LC_ALL=C; export LC_ALL\nif [ ! -r /proc/stat ]')) {
          resources.commands.push(info.command)
          const n = resources.samples++, stream = accept()
          stream.on('error', () => {})
          const output = resources.output.replace('cpu 1000 0 500 8500', `cpu ${1000 + n * 100} 0 ${500 + n * 50} ${8500 + n * 850}`).replace('eth0: 100000', `eth0: ${100000 + n * 2048}`).replace('200000 10', `${200000 + n * 4096} 10`)
          stream.write(output + '\n'); stream.exit(0); stream.end(); return
        }
        if (info.command.includes('exec tail -n ') && info.command.includes(' -F -- ')) {
          logs.commands.push(info.command); logs.active++
          const stream = accept(); let count = 0
          stream.on('error', () => {})
          stream.write('INFO training started\nERROR sample diagnostic\n')
          const timer = setInterval(() => stream.write(`INFO epoch ${++count} loss=0.1\n`), 1000)
          let cleaned = false
          const cleanup = () => { if (cleaned) return; cleaned = true; clearInterval(timer); logs.active-- }
          // ssh2 emits Session close as soon as CHANNEL_CLOSE arrives. Channel
          // close can wait for its readable EOF, so drain stdin and clean up on
          // either event just as a remote process exits with its SSH session.
          session.once('close', cleanup); stream.once('close', cleanup); stream.resume(); return
        }
        gpu.commands.push(info.command)
        if (info.command !== GPU_COMMAND) { reject?.(); return }
        const stream = accept()
        stream.on('error', () => {})
        if (gpu.hang) return
        const timer = setTimeout(() => {
          if (gpu.output) stream.write(gpu.output + '\n')
          if (gpu.stderr) stream.stderr.write(gpu.stderr)
          stream.exit(gpu.exitCode); stream.end()
        }, gpu.delayMs)
        stream.on('close', () => clearTimeout(timer))
      })
      session.on('shell', (accept, reject) => {
        if (behavior.rejectNextShell) { behavior.rejectNextShell = false; reject?.(); return }
        const shell = ++shells.opened
        const stream = accept()
        stream.write('Welcome to the local SSH test server.\r\ntest@fixture:~$ ')
        let input = ''
        stream.on('data', (data: Buffer) => {
          for (const char of data.toString()) {
            if (char === '\r' || char === '\n') { stream.write('\r\n'); if (input.trim()) shells.commands.push({ shell, command: input }); if (input.trim() === 'exit') { stream.exit(0); stream.end(); return } if (input.trim()) stream.write('received: ' + input + '\r\n'); input = ''; stream.write('test@fixture:~$ ') }
            else { input += char; stream.write(char) }
          }
        })
      })
      session.on('sftp', accept => {
        const sftp = accept()
        const handles = new Map<string, { path: string; listed: boolean }>()
        const resolve = (path: string) => posix.resolve('/home/test', path)
        const attrs = (file: MemoryFile) => ({ mode: file.mode, size: file.data.length, uid: 1000, gid: 1000, atime: file.mtime, mtime: file.mtime })
        const ok = (id: number) => sftp.status(id, 0)
        const fail = (id: number, code = 2) => sftp.status(id, code)
        sftp.on('REALPATH', (id, path) => { const resolved = resolve(path); const file = files.get(resolved); if (!file) { fail(id); return } sftp.name(id, [{ filename: resolved, longname: resolved, attrs: attrs(file) }]) })
        for (const event of ['STAT', 'LSTAT'] as const) sftp.on(event, (id: number, path: string) => { const file = files.get(resolve(path)); if (file) sftp.attrs(id, attrs(file)); else fail(id) })
        sftp.on('FSTAT', (id, handle) => { const item = handles.get(handle.toString()); const file = item && files.get(item.path); if (file) sftp.attrs(id, attrs(file)); else fail(id) })
        sftp.on('OPEN', (id, path, flags, attributes) => {
          path = resolve(path)
          let file = files.get(path)
          if (file && flags & 32) { fail(id, 4); return }
          if (!file && flags & 8) { file = { data: Buffer.alloc(0), mode: 0o100000 | (attributes.mode || 0o644), mtime: stamp() }; files.set(path, file) }
          if (!file) { fail(id); return }
          if (flags & 16) file.data = Buffer.alloc(0)
          const handle = Buffer.from(randomUUID()); handles.set(handle.toString(), { path, listed: false }); sftp.handle(id, handle)
        })
        sftp.on('READ', (id, handle, offset, length) => { const item = handles.get(handle.toString()); const file = item && files.get(item.path); if (!file) { fail(id); return } if (offset >= file.data.length) { fail(id, 1); return } sftp.data(id, file.data.subarray(offset, offset + length)) })
        sftp.on('WRITE', (id, handle, offset, data) => { const item = handles.get(handle.toString()); const file = item && files.get(item.path); if (!file) { fail(id); return } if (file.data.length < offset + data.length) { const grown = Buffer.alloc(offset + data.length); file.data.copy(grown); file.data = grown } data.copy(file.data, offset); file.mtime = stamp(); ok(id) })
        sftp.on('FSETSTAT', (id, handle, attributes) => {
          const item = handles.get(handle.toString())
          const file = item && files.get(item.path)
          if (!file) { fail(id); return }
          if (attributes.mode !== undefined) file.mode = (file.mode & 0o170000) | (attributes.mode & 0o7777)
          ok(id)
        })
        sftp.on('CLOSE', (id, handle) => { handles.delete(handle.toString()); ok(id) })
        sftp.on('OPENDIR', (id, path) => { path = resolve(path); if (!files.has(path)) { fail(id); return } const handle = Buffer.from(randomUUID()); handles.set(handle.toString(), { path, listed: false }); sftp.handle(id, handle) })
        sftp.on('READDIR', (id, handle) => { const item = handles.get(handle.toString()); if (!item) { fail(id); return } if (item.listed) { fail(id, 1); return } item.listed = true; const entries = [...files.entries()].filter(([path]) => path !== item.path && posix.dirname(path) === item.path).map(([path, file]) => ({ filename: posix.basename(path), longname: posix.basename(path), attrs: attrs(file) })); if (entries.length) sftp.name(id, entries); else fail(id, 1) })
        sftp.on('RENAME', (id, oldPath, newPath) => { oldPath = resolve(oldPath); newPath = resolve(newPath); const file = files.get(oldPath); if (!file) { fail(id); return } files.set(newPath, file); files.delete(oldPath); ok(id) })
        sftp.on('REMOVE', (id, path) => { if (files.delete(resolve(path))) ok(id); else fail(id) })
        sftp.on('MKDIR', (id, path) => { path = resolve(path); if (files.has(path)) { fail(id, 4); return } files.set(path, { data: Buffer.alloc(0), mode: 0o40755, mtime: stamp() }); ok(id) })
        sftp.on('RMDIR', (id, path) => { path = resolve(path); if ([...files.keys()].some(key => key !== path && key.startsWith(path + '/'))) { fail(id, 4); return } if (files.delete(path)) ok(id); else fail(id) })
        sftp.on('EXTENDED', id => fail(id, 8))
      })
    }))
  })
  await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  const port = (server.address() as AddressInfo).port
  return { port, files, metrics, behavior, shells, gpu, resources, logs, disconnectClients() { for (const client of clients) client.end() }, async close() { for (const client of clients) client.end(); await new Promise<void>(resolve => server.close(() => resolve())) } }
}
