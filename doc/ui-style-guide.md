# Meridian Deck 前端界面样式规范（UI Style Guide）

本规范定义 Meridian Deck 前端（`src/renderer/src/`）的视觉语言、设计令牌、浮岛布局结构与通用组件范式。

**适用范围**：本次样式改造及**后续所有新增页面、侧栏面板、抽屉、弹窗、浮层与工具栏**。任何人类开发者或 AI 协作者新增前端界面时，都应直接套用本规范与 `src/renderer/src/tokens.css`，禁止在业务组件中硬编码色值、自造字体栈或引入不兼容的布局层级。

- **设计体系**：Obsidian Islands（浮岛科研舱）× 精简 4 组菜单栏 × 瓷白蓝（Porcelain Blue）品牌识别
- **运行载体**：Wails 3.1.0 + Vue 3.5 + TypeScript（需兼顾 Windows WebView2、macOS WKWebView、Linux WebKitGTK）

---

## 一、核心原则

1. **主画布优先**：终端与编辑器是工作台的绝对视觉中心，任何新增工具栏、状态条或弹窗都必须克制高度占用，避免在主画布上方堆叠多层通栏。
2. **令牌驱动（Zero Hardcoded Colors）**：除 `terminal-theme.ts` 等需要 JS 对象传参的 Canvas/xterm 主题外，所有 CSS 颜色、字体、圆角与间距一律引用 CSS 变量，保证深色、浅色与跟随系统三态自动一致。
3. **列表/状态先于表单**：运维与监控面板打开后应先展示当前活跃状态或列表，新建/配置表单优先通过「+ 新建」折叠展开或弹窗承载。
4. **数值稳定可读**：IP、端口、路径、显存、百分比、速率、行号、快捷键统一使用等宽字体与等宽数字（`tabular-nums`），避免实时轮询刷新时界面抖动。
5. **键盘与无障碍默认可达**：悬停渐显的操作按钮必须同步支持 `:focus-within`；所有交互控件必须有清晰的 `:focus-visible` 焦点环与语义化 `aria-label`。

---

## 二、设计令牌（`src/renderer/src/tokens.css`）

### 2.1 四级表面与双级边框

浮岛体系依靠**四级中性灰阶**建立层次，而非依赖厚重阴影或高对比硬边框：

| 令牌名 | 兼容旧变量 | 深色模式（Dark） | 浅色模式（Light） | 适用场景 |
| --- | --- | --- | --- | --- |
| `--bg-shell` | — | `#0a0c10` | `#e8edf5` | 窗口最底层底板（衬托浮岛间隙） |
| `--bg-canvas` | `--background` | `#12161f` | `#ffffff` | 终端/编辑器主画布、活跃胶囊、卡片底、输入框底 |
| `--bg-surface` | `--surface` | `#151a24` | `#f8fafd` | 顶栏、侧栏浮岛、46px 导航轨、弹窗页脚 |
| `--bg-surface-2` | — | `#1c2331` | `#eef2f8` | 段式切换槽底、进度条轨道、快捷键徽章底 |
| `--border` | `--border` | `#252e3f` | `#d5deeb` | 浮岛外描边、卡片边框、表单控件描边 |
| `--border-soft` | — | `#1f2736` | `#e6ecf5` | 面板内部分割线、行号分隔线、菜单分隔线 |

### 2.2 文本、品牌色与语义预警色

| 令牌名 | 深色模式（Dark） | 浅色模式（Light） | 适用场景 |
| --- | --- | --- | --- |
| `--text` | `#edf2fb` | `#111827` | 正文、主标题、主图标 |
| `--muted` | `#8a97ae` | `#5b677b` | 次要说明、路径前缀、默认状态图标、占位符 |
| `--blue` | `#5b9cf6` | `#2558b8` | 瓷白蓝品牌强调、聚焦框、活跃标签、文件夹图标 |
| `--highlight` | `rgba(91, 156, 246, 0.14)` | `#e6effe` | 列表悬停、选中底色、活跃导航图标底 |
| `--selection` | `#294b79` | `#bed0ee` | 文本与列表多选高亮 |
| `--primary` | `#3574e2` | `#1f4fa8` | 主操作按钮背景（保存、连接、执行、启动） |
| `--primary-text` | `#ffffff` | `#ffffff` | 主操作按钮文字（深浅统一高对比纯白） |
| `--green` | `#34d399` | `#16794c` | 已连接、监听中、正常下载/显存健康 |
| `--green-soft` | `rgba(52, 211, 153, 0.14)` | `#e6f7ef` | 在线/监听状态胶囊底 |
| `--amber` | `#fbbf24` | `#b4690e` | 未保存圆点、资源 ≥80% / GPU ≥75°C 预警 |
| `--amber-soft` | `rgba(251, 191, 36, 0.14)` | `#fef6e6` | 警告提示底 |
| `--danger` | `#fb7185` | `#c62f40` | 错误、断开连接、删除、资源 ≥90% / GPU ≥85°C |
| `--error-bg` | `rgba(251, 113, 133, 0.13)` | `#fef2f2` | 错误横幅、日志错误行底、错误提示框 |
| `--shadow` | `rgba(0, 0, 0, 0.42)` | `rgba(17, 24, 39, 0.14)` | 下拉菜单、右键菜单、Toast、弹窗投影 |

### 2.3 终端与代码高亮同步约定

- **xterm.js（`terminal-theme.ts`）**：深色终端底色使用 `#10141c`（略深于 `--bg-canvas`，突出终端聚焦），浅色使用 `#fcfdff`；保留 `minimumContrastRatio: 4.5` 与高饱和 ANSI 16 色。
- **CodeMirror 6（`typography.css` + `editor-highlight.ts`）**：所有语法色继续走 `--syntax-*` 变量，新增语言或预览组件（如 JSON / Markdown / CSV）必须复用 `--syntax-*` 与 `--bg-canvas` / `--border-soft`。

### 2.4 标准令牌声明模板（`tokens.css` 基准）

```css
.app-shell {
  --font-sans: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Microsoft YaHei', 'Noto Sans CJK SC', sans-serif;
  --font-mono: 'JetBrains Mono', 'Cascadia Code', 'SF Mono', Consolas, monospace;

  --radius-dialog: 12px;
  --radius-island: 10px;
  --radius-card: 8px;
  --radius-control: 6px;
  --radius-pill: 999px;

  --island-gap: 7px;
  --transition-fast: 140ms ease;

  /* Light Theme */
  --bg-shell: #e8edf5;
  --bg-canvas: #ffffff;
  --bg-surface: #f8fafd;
  --bg-surface-2: #eef2f8;
  --background: var(--bg-canvas);
  --surface: var(--bg-surface);
  --border: #d5deeb;
  --border-soft: #e6ecf5;
  --text: #111827;
  --muted: #5b677b;
  --blue: #2558b8;
  --highlight: #e6effe;
  --selection: #bed0ee;
  --primary: #1f4fa8;
  --primary-text: #ffffff;
  --green: #16794c;
  --green-soft: #e6f7ef;
  --amber: #b4690e;
  --amber-soft: #fef6e6;
  --danger: #c62f40;
  --error-bg: #fef2f2;
  --shadow: rgba(17, 24, 39, 0.14);
  color-scheme: light;
}

.app-shell[data-theme="dark"] {
  --bg-shell: #0a0c10;
  --bg-canvas: #12161f;
  --bg-surface: #151a24;
  --bg-surface-2: #1c2331;
  --border: #252e3f;
  --border-soft: #1f2736;
  --text: #edf2fb;
  --muted: #8a97ae;
  --blue: #5b9cf6;
  --highlight: rgba(91, 156, 246, 0.14);
  --selection: #294b79;
  --primary: #3574e2;
  --primary-text: #ffffff;
  --green: #34d399;
  --green-soft: rgba(52, 211, 153, 0.14);
  --amber: #fbbf24;
  --amber-soft: rgba(251, 191, 36, 0.14);
  --danger: #fb7185;
  --error-bg: rgba(251, 113, 133, 0.13);
  --shadow: rgba(0, 0, 0, 0.42);
  color-scheme: dark;
}
```

---

## 三、排版与字号体系

| 角色 | 字号 | 字重 | 字体 | 适用元素 |
| --- | --- | --- | --- | --- |
| 欢迎页主标题 | `22px` | `600` | `--font-sans` | 空工作区大标题 |
| 弹窗标题 | `16–17px` | `600` | `--font-sans` | `<dialog>` 标题 |
| 监控主指标数值 | `16–18px` | `600–700` | `--font-mono` (`tabular-nums`) | GPU 利用率、CPU/内存百分比 |
| 品牌 / 面板标题 / 默认正文 | `12.5–13px` | `500–600` / `400` | `--font-sans` | `.section-heading`、表单输入、正文 |
| 导航 / 菜单 / 列表主项 | `11.5–12px` | `400–500` | `--font-sans` | 顶栏菜单、会话胶囊、文件行、服务器行 |
| 辅助元信息 / 副标题 / 底栏 | `11px` | `400` | `--font-sans` / `--font-mono` | 路径、状态栏、表单注释、文件大小 |
| 微型角标 / 分组小标题 | `10px` | `500–600` | `--font-sans` / `--font-mono` | 标签徽章、快捷键 `<kbd>`、GPU 编号 |

> 约束：禁止正文字号小于 `11px`（仅状态徽章、快捷键和分组眉标可用 `10px`），以保障 80% 缩放时的可读性。

---

## 四、浮岛布局尺寸与层级（Layout & Z-Index）

### 4.1 固定区域尺寸红线

- **顶行菜单栏（`.topbar`）**：高度 `34px`，承载品牌标识 + 4 组菜单（`连接 / 终端 / 视图 / 帮助`）+ 右侧当前会话主机胶囊与高频连接动作（设置 / 断开 / 重连）。
- **会话胶囊行（`.session-tabs`）**：高度 `30px`（位于浮岛底板顶部），承载活跃/非活跃会话胶囊、`+` 新建终端及多会话切换器。
- **浮岛工作区（`.app-body`）**：底板为 `--bg-shell`，四周内边距与浮岛间距为 `var(--island-gap)`（默认 `7px`，`<= 750px` 窄窗降为 `5px`）。
  - **主画布浮岛**：`border-radius: var(--radius-island)`，`border: 1px solid var(--border)`，`background: var(--bg-canvas)`；顶边内嵌 `34px` 文档段式切换栏与终端工具图标。
  - **侧栏浮岛**：`border-radius: var(--radius-island)`，`border: 1px solid var(--border)`，`background: var(--bg-surface)`；默认宽 `280px`（服务器/文件）或 `360–380px`（工具/监控类），拖拽范围 `236px–640px`。
  - **导航轨浮岛（`.sidebar-rail`）**：固定宽 `46px`，图标 `17–18px`，分三段（核心面板 / 分割线 / 运维监控 / 贴底主题与设置）。
  - **侧栏拖拽条（`SidebarResize`）**：宽度与 `--island-gap` 对齐（`7px`），默认透明不画实线，悬停或拖拽时居中显示 `2px` 宽 `--blue` 引导线。
- **底部状态条（`.statusbar`）**：高度 `24–26px`，不单独在上方叠加常驻传输条；传输进度以右侧胶囊呈现，点击向上展开浮岛式传输抽屉。

### 4.2 Z-Index 分层约定

| 层级 | 值 | 元素 |
| --- | --- | --- |
| 局部浮层提示 | `3` | NAS 拖放目标底部条 |
| 面板拖拽遮罩 | `4` | 文件区 `.drop-overlay` |
| 主画布状态气泡 | `5` | `.file-loading`、传输展开抽屉 |
| 右键菜单 | `15` | `.context-menu`（文件树/标签右键） |
| 拖拽跟随标签 | `19` | `.nas-drag-label` |
| 全局 Toast | `20` | `.toast` |
| 顶栏下拉菜单 | `30` | `.workbench-menu-panel` |
| 模态弹窗 | Browser Top Layer | 原生 `<dialog>`（`showModal()`） |

---

## 五、通用组件样式范式（新增页面直接套用）

### 5.1 侧栏面板标准骨架

所有现有及**未来新增的侧栏面板**必须遵循同一四段骨架，保证切换面板时标题位置与边距零跳动：

```html
<section id="sidebar-panel-xxx" class="side-panel" aria-label="面板名称">
  <!-- 1. 统一标题栏：高度 36px，底部分割线 --border-soft -->
  <header class="section-heading">
    <span><Icon :size="15" />面板名称</span>
    <div class="sidebar-heading-actions">
      <!-- 最多放 2~3 个高频轻量按钮（如 + 新建 / 刷新 / 折叠） -->
      <button class="icon-button" aria-label="收起"><X :size="15" /></button>
    </div>
  </header>

  <!-- 2. 可选单行上下文条（当前服务器、刷新状态或路径） -->
  <div class="side-context">
    <span>gpu-node-04</span>
    <span class="mono">每 2s · 实时</span>
  </div>

  <!-- 3. 主滚动内容区（padding: 8px, gap: 8px） -->
  <div class="side-scroll">...</div>

  <!-- 4. 可选底部轻量说明条（单行 10.5px muted，顶部分割线 --border-soft） -->
  <footer class="side-footnote">...</footer>
</section>
```

### 5.2 按钮层级规范

| 类型 | 类名 | 外观 | 使用原则 |
| --- | --- | --- | --- |
| 主操作 | `.primary` | `--primary` 底 + `--primary-text` 字 | 每个卡片/表单/弹窗**最多 1 个**（保存、连接、启动、执行） |
| 次操作 | 默认 `<button>` | `--bg-canvas` 底 + `1px solid var(--border)` | 取消、停止、复制、打开页面等常规操作 |
| 轻量/图标按钮 | `.icon-button` / `.ghost-button` | 透明底无边框，悬停 `--highlight` | 标题栏图标、工具条图标、行尾快捷按钮 |
| 危险操作 | `.danger-button` / `.danger` | 删除确认用实心 `--danger`；菜单/行内用文字 `--danger` | 递归删除、替换指纹、断开连接 |

- 常规按钮圆角 `var(--radius-control)`（`6px`），最小点击高度 `28px`（紧凑卡片内可至 `24–26px`），图标与文字间距 `5–6px`。

### 5.3 段式切换器（Segmented Control）

用于 2–3 个互斥视图/范围切换（如「终端 / 文件」、「全部服务器 / 最近连接」、「当前与全局 / 全部收藏」）：

```css
.segmented {
  display: inline-flex;
  padding: 2px;
  gap: 2px;
  border-radius: 7px;
  background: var(--bg-surface-2);
}
.segmented > button {
  flex: 1;
  min-height: 24px;
  padding: 3px 9px;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--muted);
  font-size: 11px;
}
.segmented > button.active {
  background: var(--bg-canvas);
  color: var(--blue);
  font-weight: 500;
}
```

### 5.4 数据卡片、状态胶囊与阈值进度条

- **卡片**：`background: var(--bg-canvas)`、`border: 1px solid var(--border)`、`border-radius: var(--radius-card)`（`8px`）、`padding: 10px 11px`、卡片间距 `8px`。
- **状态胶囊（Pill）**：`border-radius: var(--radius-pill)`、`padding: 1px 8px`、`font-size: 10.5px`，配 `5–7px` 语义圆点：
  - 正常/在线/监听：`background: var(--green-soft); color: var(--green)`
  - 告警/高负载：`background: var(--amber-soft); color: var(--amber)`
  - 失败/离线报错：`background: var(--error-bg); color: var(--danger)`
  - 信息/传输中：`background: var(--highlight); color: var(--blue)`
- **进度条轨道**：高度 `4–5px`、圆角 `999px`、槽底 `var(--bg-surface-2)`；**阈值语义变色规则**：
  - 常规负载（`< 80%`）：`var(--blue)`（显存常规可用 `var(--green)`）
  - 高负载预警（`80%–89%` 或温度 `75°C–84°C`）：`var(--amber)`
  - 过载告警（`≥ 90%` 或温度 `≥ 85°C`）：`var(--danger)`

### 5.5 列表行与悬停渐显操作

- 列表行圆角 `6px`（不在列表内使用尖锐 0px 通栏选中条），选中态 `background: var(--highlight); color: var(--blue)`。
- 行尾超过 1 个管理图标（如编辑、删除）时，默认降低视觉权重（`opacity: 0.25` 或隐藏），在 `.row:hover` 与 `.row:focus-within` 时恢复 `opacity: 1`，既降低噪声又不破坏键盘 Tab 可达性。

### 5.6 表单、可折叠新建区与日志/代码视口

- **表单控件**：`background: var(--bg-canvas)`、`border: 1px solid var(--border)`、`border-radius: var(--radius-control)`；聚焦时 `border-color: var(--blue)` 并配轻量焦点框。
- **可折叠表单原则**：面板同时包含「创建表单」与「活跃列表/实时流」时（如端口转发、日志追踪），活跃态下默认收起表单，仅在标题栏提供明确切换按钮（如「+ 新建」/「更换文件」）。
- **代码/预览/日志视口**：使用 `--font-mono` 与终端同源深/浅底色；**渲染后待执行命令预览**左侧加 `2px solid var(--blue)`；**日志错误行**使用 `background: var(--error-bg); color: var(--danger); border-left: 2px solid var(--danger)`，行号列右侧加 `1px solid var(--border-soft)`。

### 5.7 模态弹窗、空态与错误态

- **模态弹窗（`<dialog>`）**：圆角 `var(--radius-dialog)`（`12px`），背景 `var(--bg-canvas)`，边框 `1px solid var(--border)`，遮罩 `background: rgba(10, 12, 16, 0.56); backdrop-filter: blur(2px)`；结构固定为 `dialog-header`（底边 `--border-soft`）+ 可滚动 `dialog-body` + `dialog-footer`（底色 `--bg-surface`，重置/辅助动作居左，取消与主确认居右）。
- **空态（`.side-empty`）**：居中或左对齐轻量文案，字号 `11.5–12px`，颜色 `var(--muted)`，不堆砌无关装饰插画。
- **内联错误（`.error-box`）**：`background: var(--error-bg)`、`color: var(--danger)`、`border-radius: 6px`、`padding: 8px 10px`、`role="alert"`。

---

## 六、动效、滚动条、响应式与平台底线

1. **轻量动效**：仅对 `background-color`、`border-color`、`color`、`opacity` 使用 `var(--transition-fast)`（`140ms ease`）；禁止给布局宽高等高开销属性加动画；必须保留 `@media (prefers-reduced-motion: reduce)` 关闭非必要动效与旋转。
2. **统一细滚动条**：所有可滚动区域（侧栏、日志、弹窗、传输列表）统一 `6px` 宽透明轨道 + `var(--border)` 圆角滑块（悬停加深至 `var(--muted)`），消除 Windows WebView2 深色模式下的刺眼原生灰白滚动条。
3. **响应式与缩放**：
   - 必须通过 `80% / 100% / 125% / 150%` 工作台缩放验收；
   - `<= 960px` 时顶栏右侧主机胶囊自动截断地址；
   - `<= 750px` 时浮岛间距降至 `5px`，顶栏隐藏地址文字仅保留连接圆点与关键动作图标。
4. **宿主与无障碍红线**：
   - 不得破坏 `files-dropped` 原生拖放路径、`data-file-drop-target` 属性及 NAS 拖拽 `document.elementFromPoint` 命中；
   - 新增面板/按钮必须具备准确 `aria-label`、`aria-expanded` / `aria-pressed` 与可见 `:focus-visible`（`2px solid var(--blue)`）。

---

## 七、新增页面 / 功能样式自检清单

在提交任何新页面、新侧栏面板或新弹窗前，逐项核对：

- [ ] **零硬编码颜色**：全部颜色均来自 `tokens.css` 变量，深色与浅色模式均已切换目检；
- [ ] **浮岛与圆角一致**：主区/侧栏使用 `10px`、卡片 `8px`、控件 `6px`、弹窗 `12px`、胶囊 `999px`；
- [ ] **字体与数值对齐**：路径、命令、IP/端口、监控数值均已套用 `--font-mono` 与 `tabular-nums`；
- [ ] **复用标准组件范式**：切换项已用段式控件，状态已用语义胶囊，超 80%/90% 负载已走阈值预警色；
- [ ] **垂直空间克制**：未在终端上方新增通栏；实时流/列表面板的表单可折叠，不挤压核心内容区；
- [ ] **交互与缩放完整**：悬停操作支持 `:focus-within`，键盘可达，100%/125%/150% 缩放与 750px 窄窗无溢出。
