import { spawnSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { basename, dirname, resolve } from 'node:path'

const { version, build: { productName } } = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8'))
const { platform, arch } = process
const goarch = { x64: 'amd64', arm64: 'arm64' }[arch]
const goos = { win32: 'windows', darwin: 'darwin', linux: 'linux' }[platform]
if (!goos || !goarch || (platform === 'win32' && arch !== 'x64')) throw new Error(`当前平台或架构没有 ${productName} 构建配置`)

const directory = resolve('release', version)
const productSlug = productName.replaceAll(' ', '-')
const name = `${productSlug}-${version}`
const bundle = resolve(directory, `${productName}.app`)
const linuxFolder = `${name}-linux-${arch}`
const executable = platform === 'win32' ? resolve(directory, `${name}-x64.exe`)
  : platform === 'darwin' ? resolve(bundle, 'Contents/MacOS', productSlug)
  : resolve(directory, linuxFolder, productSlug)

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { stdio: 'inherit', ...options })
  if (result.error) throw result.error
  if (result.status !== 0) throw new Error(`${command} 执行失败（${result.signal || result.status}）`)
}

function buildMacIcon(output) {
  const temporaryDirectory = mkdtempSync(resolve(tmpdir(), 'meridian-deck-icon-'))
  try {
    const iconset = resolve(temporaryDirectory, `${productSlug}.iconset`)
    const source = resolve('assets', 'meridian-deck-icon-porcelain-blue.png')
    mkdirSync(iconset)
    mkdirSync(dirname(output), { recursive: true })
    for (const size of [16, 32, 128, 256, 512]) {
      const name = `icon_${size}x${size}`
      run('sips', ['-z', size, size, source, '--out', resolve(iconset, `${name}.png`)])
      run('sips', ['-z', size * 2, size * 2, source, '--out', resolve(iconset, `${name}@2x.png`)])
    }
    run('iconutil', ['-c', 'icns', '-o', output, iconset])
  } finally {
    rmSync(temporaryDirectory, { recursive: true, force: true })
  }
}

mkdirSync(resolve(executable, '..'), { recursive: true })
const backendTags = platform === 'darwin' ? ['-tags', 'purego'] : []
run('go', ['build', '-trimpath', ...backendTags, platform === 'win32' ? '-ldflags=-H windowsgui -s -w' : '-ldflags=-s -w', '-o', executable, '.'], {
  env: { ...process.env, GOOS: goos, GOARCH: goarch, ...(arch === 'x64' ? { GOAMD64: 'v1' } : {}), CGO_ENABLED: platform === 'linux' ? '1' : '0' }
})

let artifact = executable
if (platform === 'win32') {
  writeFileSync(resolve(directory, 'README.txt'), `${productName} ${version} — Windows x64\r\n\r\n运行 ${basename(executable)}，需要 Microsoft Edge WebView2 Evergreen Runtime。\r\nhttps://developer.microsoft.com/microsoft-edge/webview2/\r\n\r\n配置目录为 %APPDATA%/SSHTool/。当前未配置代码签名。\r\n`, 'utf8')
} else if (platform === 'darwin') {
  const iconName = `${productSlug}.icns`
  // macOS requires a three-component short version. The app and archive use the full release version.
  const shortVersion = version.split('.').slice(0, 3).join('.')
  buildMacIcon(resolve(bundle, 'Contents/Resources', iconName))
  writeFileSync(resolve(bundle, 'Contents/Info.plist'), `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleExecutable</key><string>${productSlug}</string>
  <key>CFBundleName</key><string>${productName}</string>
  <key>CFBundleIconFile</key><string>${iconName}</string>
  <key>CFBundleIdentifier</key><string>local.sshtool.workbench</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${shortVersion}</string>
  <key>CFBundleVersion</key><string>${version}</string>
  <key>NSHighResolutionCapable</key><true/>
</dict></plist>
`, 'utf8')
  run('codesign', ['--force', '--sign', '-', bundle])
  artifact = resolve(directory, `${name}-macos-${arch}.zip`)
  run('ditto', ['-c', '-k', '--keepParent', bundle, artifact])
} else {
  writeFileSync(resolve(directory, linuxFolder, 'README.txt'), `${productName} ${version} — Linux ${arch}\n\n解压后在桌面会话中运行 ./${productSlug}。需要 GTK 4 与 WebKitGTK 6.0。\nUbuntu 24.04 可安装 libgtk-4-1 和 libwebkitgtk-6.0-4。\n`, 'utf8')
  artifact = resolve(directory, `${linuxFolder}.tar.gz`)
  run('tar', ['-czf', artifact, '-C', directory, linuxFolder])
}

console.log(`Wails 构建完成：${artifact}`)
