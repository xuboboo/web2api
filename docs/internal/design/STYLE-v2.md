# Web2API 管理控制台 · 设计规范 V2(zero.cat 语言)

> 参照站:[zero.cat](https://zero.cat) — new-api 系 AI API 网关,公开层为 shadcn/ui + Tailwind + Radix 体系,设计语言可概括为「Anthropic 暖纸 × Vercel Geist Mono × Linear 键盘优先」。
>
> 本规范所有色值/字号/间距/圆角均为 2026-11-15 在 1440×900 视口下,对 zero.cat 登录页(暗/亮两主题)、公开主页(hero/统计带/特性卡/代码区)、/pricing(卡片视图+表格视图)、/rankings(图表页)做浏览器截图 + `getComputedStyle` + `:root` CSS 变量**实测**提取,非主观推断。截图存于 `qa/reference/console-zero-*.png`(10 张)。

---

## 0. 勘察方法与降级声明(如实记录)

1. **目标页登录墙**:打开 `https://zero.cat/dashboard/overview` 被重定向到 `https://zero.cat/sign-in?redirect=%2Fdashboard%2Foverview` —— 本环境会话未登录,**登录后的控制台内页未能直接勘察**。
2. **按预案降级**:完整勘察了 zero.cat 全部公开设计面:
   - 登录页 `/sign-in`(暗色 + 亮色两套主题,含主题切换菜单交互);
   - 公开主页 `/`(hero、厂商 marquee、核心优势统计带 + 特性卡、3 行代码终端卡、页脚);
   - 模型广场 `/pricing`(卡片视图 + **表格视图**,左筛选栏 + 工具栏 + 搜索 ⌘K);
   - 排行分析 `/rankings`(下划线 Tab、图表卡、KPI 大数字、排行列表)。
   这些公开面本身即是「后台式布局」(筛选侧栏/工具栏/数据表格/图表卡),足以完整提取设计语言。
3. **登录后壳层的间接证据**:公开页 `:root` 中实测到成套 shadcn Sidebar 与 App-Header 变量(`--sidebar`/`--sidebar-border`/`--sidebar-foreground`/`--sidebar-accent`/`--sidebar-primary`/`--app-header-height: 3.75rem`),据此推断登录后壳层为「左侧 shadcn Sidebar + 60px 顶栏」;**其视觉形态未经直接验证**,本文涉及处均已标注。
4. **WebSearch 佐证**:zero.cat 本身无设计文章(仅"界面友好"类用户评价);检索了 Linear(键盘优先/Cmd+K/暗色优先/低铬密度)与 Vercel Geist(高对比黑白/减法/hairline+shadow-as-border/Geist Mono)与 Anthropic(暖纸 #f5f4ed/黄棕调灰/衬线标题+无衬线 UI+等宽数字/8px 网格)的设计系统文章,与实测结论互相印证。来源见文末。

---

## 1. 设计基因(一段话)

**暖纸色工作台 + 反色单色 CTA + 等宽数字**。亮色主题是带黄棕底调的"羊皮纸"暖白(#faf9f5),暗色主题是暖炭黑(#141413),两套主题共用一套语义结构;唯一强调色是品牌绿·松柏(亮 #177e5b / 暗 #3ecf8e,营销 CTA 同绿系,2026-11-15 领导拍板由橙/蓝改为山系绿),其余界面全靠暖灰阶 + 0.8px hairline 分层,卡片无投影(shadow-as-border);标题层用衬线字(Anthropic Serif → Lora → 思源宋体,中文标题呈宋体质感),UI 正文用 Inter,一切数字/代码/价格用 Geist Mono;大数字 + 小灰标签是数据表达的唯一范式;微交互全部 150–200ms,按下有 `scale(0.96)` 反馈,焦点是 2px 蓝色 30% 环。

---

## 2. 布局骨架

### 2.1 认证后壳层(推断,证据为 CSS 变量)

- 左侧 **shadcn Sidebar**:`--sidebar`(亮 `var(--card)`=#faf9f5 / 暗 #191816)、边框 `--sidebar-border`(亮 #e2e0d8 / 暗 #393730)、文字 `--sidebar-foreground`(亮 #30302e / 暗 #d8d6d0)、悬停底 `--sidebar-accent`(亮 #eae8e0 / 暗 #2e2b26)、强调 `--sidebar-primary` = 品牌橙。宽度取 shadcn 默认 `16rem`(256px,未实测,可收敛到 220–240px)。
- 顶栏高度 `--app-header-height: 3.75rem`(60px,实测变量)。
- **本面板落位**:保留现版左侧栏 + 主内容骨架(`index.html` 的 `.sidebar/.main`),只换皮肤;不需要改成 zero.cat 营销页那种无侧栏结构。

### 2.2 页面骨架(直接实测自 /pricing、/rankings)

```
┌─ 顶栏 60px:logo(左)⋯⋯ 导航/图标按钮/登录(右),sticky,backdrop-blur,bg background/95 ─┐
│ ┌─ 页头块(在内容区内部,不在顶栏)────────────────────────────── │
│ │ h1 衬线 30px/600(如「模型广场」)+ 副标题 14px muted(左)      │
│ │ 搜索框 h40 + ⌘K kbd(右)                                     │
│ ├─ 工具栏:结果计数(左)| 视图分段控件 + 单位切换 + 排序 + 网格/表格切换(右)│
│ ├─ 主体:左筛选栏(白卡容器,宽约 264px,可折叠分组) + 右内容(卡片网格/表格)│
│ └─ 内容卡片间 gap 16px,网格 3 列(1280px 下)                     │
└─ 内容左右 gutter 24px,区块纵向间距 64–96px,区块间用 hairline 分隔 ──┘
```

- **页头永远在内容区里**,顶栏不重复放页面标题 —— 这是 zero.cat 与现版面板最大的结构差异(现版标题在顶栏),V2 改之。
- 区块之间用 `border-t`(hairline)而非大色块分隔;hero 区可用略深一档的纸色(#fbfaf7)与页面底色区分(实测)。

---

## 3. 设计 Token 表(实测)

### 3.1 主题色板(亮 / 暗成对)

| Token | 亮色实测 | 暗色实测 | 用途 |
|---|---|---|---|
| `--bg` | `#faf9f5` 暖纸白 | `#141413` 暖炭黑 | 页面底色 |
| `--fg` | `#141413` 近黑 | `#f3f1eb` 暖白 | 主文字 |
| `--card` | `#faf9f5`(与底同色,靠 0.8px 边框成卡) | `#1d1c19` | 卡片/表格行/侧栏底 |
| `--popover` | `#faf9f5` | `#262420` | 下拉菜单/浮层 |
| `--muted` | `#efeee8` | `#262420` | 次级底(kbd、表头、骨架) |
| `--muted-fg` | `#5e5d59` | `#a8a69f` | 次级文字/占位/表头字 |
| `--accent` | `#eae8e0` | `#2e2b26` | 悬停底、选中底 |
| `--accent-fg` | `#141413` | `#f3f1eb` | 悬停态文字 |
| `--secondary` | `#efeee8` / 字 `#30302e` | `#262420` / 字 `#f3f1eb` | 次级按钮底 |
| `--primary` | `#141413`(反色黑钮) | `#3ecf8e`(蓝) | 主按钮/选中态;亮暗语义相反 |
| `--primary-fg` | `#faf9f5` | `#141413` | 主按钮文字 |
| `--brand` | `#177e5b`(深蓝) | `#3ecf8e`(亮蓝) | 链接/焦点环 `--ring`/图表主色/动态计费字 |
| `--brand-cta` | `#177e5b`(蓝底)hover `#0f6b4b`,字 `#ffffff` | `#3ecf8e` hover `#55d9a4`,字 `#141413` | 营销级 CTA(登录页/hero) |
| `--border` | `#e2e0d8` | `#393730` | 全站 hairline |
| `--input` | `#e2e0d8` | `#44413a` | 输入框边框 |
| `--ring` | `var(--brand)` | `#3ecf8e` | 焦点环(用 30% 透明度) |
| `--canvas-dot` | `#1414130d` | `#f3f1eb0d` | 点阵纹理(特性卡图标区/登录右侧背景,1px 圆点平铺) |
| `--skeleton` | base `#edece6` / highlight `#f6f5f0` | base `#292825` / highlight `#383632` | 骨架屏 |

> **暗色 toast/系统级**实测:`--normal-bg: #000`、`--normal-border: hsl(0 0% 20%)`(sonner)。浮层(菜单/popover)用 `--popover` 底 + hairline 边;zero.cat 卡片与按钮的 box-shadow 实测全为 `none`。

### 3.2 语义色(成对;暗色另有 bg/border/text 三件套)

| Token | 亮色实测 | 暗色实测 | 用途(面板映射) |
|---|---|---|---|
| `--success` | `#15803d` | `#82ad6a` | 启用/2xx/正常;暗色三件套 `bg hsl(150,100%,6%) / border hsl(147,100%,12%) / text hsl(150,86%,65%)` |
| `--warning` | `#b45309` | `#eca851` | 冷却/限流/4xx;暗色三件套 `bg hsl(64,100%,6%) / border hsl(60,100%,9%) / text hsl(46,87%,65%)` |
| `--destructive` | `#cc3d3d`(字 `#faf9f5`) | `#ff6367`(字 `#fafafa`) | 熔断/5xx/删除;暗色三件套 `bg hsl(358,76%,10%) / border hsl(357,89%,16%) / text hsl(358,100%,81%)` |
| `--info` | `#0084cc` | `#79aad8` | 提示;暗色三件套 `bg hsl(215,100%,6%) / border hsl(223,43%,17%) / text hsl(216,87%,65%)` |

> 现版 STYLE.md 的 `--warn #a16207` / `#c23b3b` 拓展在 V2 中由上表原位替换,语义一一对应(冷却→warning、危险→destructive),无需第三套色。

### 3.3 图表色板(最多同屏 5 系)

| 系 | 亮色实测 | 暗色实测 |
|---|---|---|
| chart-1 | `#3ecf8e` | `#55d9a4` |
| chart-2 | `#625fff` | `#82ad6a` |
| chart-3 | `#00bb7f` | `#79aad8` |
| chart-4 | `#00a5ef` | `#fb93b4` |
| chart-5 | `#d66bb8` | `#b6cdc6` |

- /rankings 堆叠柱状图实测:分段间留 1px 白缝(亮)/暗缝(暗),无渐变、无圆角或 2px 微圆角;仅横向网格线;轴标签 10–12px muted。
- 状态进度条(模型卡健康度):**分段块状**(约 14–16 个小方块一行),绿 #00bb7f 系为健康,掺红段表示异常百分比(实测截图)。

### 3.4 字体三族(实测 font-family 原串)

| 族 | 实测栈 | 面板落地建议 |
|---|---|---|
| UI 无衬线 | `"Inter Variable", system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif` | 自托管 Inter Variable(`@fontsource-variable/inter`),中文回退 PingFang SC/Microsoft YaHei |
| 展示衬线 | `"Anthropic Serif", "Lora Variable", Lora, "Source Serif Pro", "Source Serif 4", "Noto Serif SC", "Noto Serif TC", …, "Source Han Serif SC", "Songti SC", …, Georgia, serif` | Anthropic Serif 为品牌专有,自托管用 **Lora + Noto Serif SC(思源宋体)**——即照抄 zero.cat 自己的回退链,中文标题自然呈现宋体 |
| 等宽 | `"Geist Mono", "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace` | 自托管 Geist Mono(Vercel 开源)或 JetBrains Mono;所有数字加 `tabular-nums` |

### 3.5 字阶(实测值;serif 仅用于标题层)

| 级别 | 实测 | 用途 |
|---|---|---|
| Hero Display | 衬线 72px/500,行高 1.1 | 登录页左区字标下的大标语(可选) |
| Wordmark | 衬线 48px/600,`letter-spacing: -0.03em` | 登录页品牌字标 |
| Page-Title | 衬线 30px/600,`tracking -0.6px`,行高 1.25 | 页头 h1(模型广场/模型榜单实测 30px) |
| Section-Title | 衬线 30px/700,`tracking -0.36px`,行高 1.2 | 卡组区块标题(为什么选择我们) |
| Card-Title | 衬线 16px/600,`tracking -0.192px`,行高 1.5 | 卡片标题、弹窗标题 |
| Body | Inter 14px/400,行高 1.5(21px) | 正文/表格 |
| Strong | Inter 14px/500 | 按钮字、导航字、Tab 字 |
| Eyebrow | Inter 12px/600,大写,`letter-spacing: 1.2px` | 区块小眉题(核心优势/TOKEN) |
| Caption | Inter 12px/400 | 帮助文字、法律行 |
| KPI 大数字 | Geist Mono **36px/600**,`tracking -0.72px`(统计带)/ **24px/600** `tabular-nums`(卡内 KPI、登录页统计) | 一切数字 |
| Mono-Body | Geist Mono 14px/500 | 模型名、密钥、IP、代码 |
| kbd | Geist Mono 10px | ⌘K 快捷键帽 |

- 数字**永远 mono**;衬线只出现在标题层;UI 层零衬线。

### 3.6 间距(4px 基网格,实测锚点)

`4 / 8 / 12 / 16 / 20 / 24 / 32 / 48 / 64 / 96`

- 组件内:按钮 `px 12–28px`、输入 `0 12px`、卡片 `16px`(px-4 实测)~24px、筛选 chip `4px 8px`、统计带单元 `24px`。
- 页面:内容 gutter 24px;区块纵向 `64px`(py-16)~`96px`(py-24);卡片网格 gap 16px;表单 label 与输入间 8px,字段间 16px。
- 首页内容最大宽 `1120px`(实测 max-w-[1120px]),管理台表格页可放宽到 1280–1440px 满幅。

### 3.7 圆角 / 边框 / 阴影 / 点阵

| Token | 实测值 | 用途 |
|---|---|---|
| `--radius-sm` | `4px` | kbd、微标签 |
| `--radius-md` | `8px` | 小按钮(h32)、chip、导航项、Tab 焦点环底 |
| `--radius-lg` | `10px` | 主按钮/输入/搜索/分段控件(变量 `--radius: .625rem`;暗色变量为 1rem,但组件实测仍 10px,以实测为准) |
| `--radius-xl` | `14px` | 卡片(实测 `rounded-xl` = 14px) |
| 边框 | **`0.8px solid`**(全部组件实测一致) | 按钮/输入/卡片/表格行/kbd;1px hairline 的收敛写法 |
| 阴影 | 卡片与按钮 `box-shadow: none`(实测) | 层级只靠 hairline + 底色差;仅浮层(菜单/弹窗/toast)允许 `0 8px 24px rgba(20,20,19,.12)` |
| 点阵 | `--canvas-dot` 平铺 | 特性卡图标区、登录页右区、空态插画底纹 |

### 3.8 动效(实测 class/计算值)

| 项 | 实测 | 面板落地 |
|---|---|---|
| 颜色过渡 | `transition-colors duration-200`(color/background-color/border-color/opacity,200ms) | 全部按钮/导航/行悬停 |
| 按压反馈 | CTA 实测 `active:scale-[0.96]`,transition 含 transform | 主按钮按下缩到 96%,100ms 回弹 |
| 焦点环 | `focus-visible: ring-2 ring-brand/30 + border-brand` | 键盘焦点统一 2px 橙环 |
| Logo/图标 hover | `hover:opacity-80` + `transition-opacity 150ms` | 图标钮、链接 |
| 输入 hover/focus | `hover:border-border`,`focus:border-primary/50` + ring | 边框加深→橙 |
| 时长域 | 150ms(透明度)/ 200ms(颜色)/ 无弹性动画 | 禁 bounce;菜单用 Radix 默认 150ms zoom+fade |

---

## 4. 组件规范

### 4.1 按钮体系(全部实测)

| 变体 | 几何 | 亮色 | 暗色 |
|---|---|---|---|
| Primary-solid(反色 CTA) | h36 / px16 / r10 / 14px·500 | 底 `#141413` 字 `#faf9f5` | 底 `#f3f1eb` 字 `#141413`(登录页「登录」钮实测两态) |
| Brand-CTA(蓝,仅营销面) | h48 / px28 / r10 / 16px·500 | 底 `#177e5b` 字 `#ffffff`,hover `#0f6b4b` | 底 `#3ecf8e` 字 `#141413`,hover `#55d9a4` |
| Outline(次操作) | h32–48 / px12–28 / r8–10 / `0.8px solid var(--border)` | 底 transparent~background,hover 底 `--accent` 字 `--accent-fg` | 底 transparent,`dark:border-input` |
| Ghost/Text | 无底,字 `--muted-fg`,hover 变 `--fg` | 图标钮/工具栏钮;顶部登录钮实测即 outline h32 | 同 |
| Danger | outline + `--destructive` 字与边框 | 危险确认 | 暗色字 `#ff6367` |

- 全部按钮 `border: 0.8px solid`;`disabled: opacity-50 pointer-events-none`;焦点 `ring-2 ring-brand/30`。

### 4.2 输入 / 搜索

- 输入框:h36(默认)/ r10 / `0.8px solid var(--input)` / 底 background(暗色为 30% 透明叠层)/ 字 14px / `padding: 4px 12px`;focus 边框 `primary/50` + 橙环;placeholder `muted-fg/50`。
- 搜索框(页头级):h40 / 左置搜索图标(pl-40px)/ 右置 **⌘K kbd 帽**(pr-64px)/ 边框 `border/60` hover 加深。**键盘行为按 Linear 惯例:全局 `/` 或 ⌘K 聚焦搜索**。
- 标签在输入框上方,14px/400,与框距 8px;错误态边框 `--destructive` + 12px 错误文字。

### 4.3 pill / chip / 分段控件 / Tab(三种选择器形态,实测)

1. **chip(筛选值)**:h30 / r8 / `0.8px` 边 / 底 background / 12px·500 / gap 6px;计数后缀 `×1.5` 用 muted 小字;选中态底 `--accent` 或橙。
2. **toggle-group(分段,标准/充值、/1M /1K)**:h32 / 组整体 r10、成员间 r0 / 成员底 `--muted`(#f1f0eb)/ `0.8px` 边 / 14px·500;激活成员底 `--background` 或橙字(实测视图切换激活为橙)。
3. **underline tabs(今天/本周/本月/今年)**:px12 py8 / 14px·500 / 激活项 `--fg` + 底部 2px `--fg` 下划线(`relative -mb-px`)/ 非激活 `--muted-fg`。
- **状态 pill**(表格):r-full(30px)/ `2px 10px` / 12px·600;启用=实心 success(暗色 `text hsl(150,86%,65%)`);冷却=warning 描边+同色字;熔断=destructive 描边;停用=灰字无框;「动态计费」类=蓝字(var(--brand))+ 品牌蓝圆点。

### 4.4 卡片 / 统计带(实测两个范式)

- **普通卡**:`rounded-xl(14px)` + `0.8px solid var(--border)` + 无阴影;内部 padding 16–24px;标题衬线 16px/600,描述 14px muted;卡头可带图标(线性 icon 20px,stroke 1.5)。
- **特性卡(总览页 hero 卡/空态卡可用)**:上下两段——上段底 `--muted` + 点阵纹理 + 居中线性图标,下段白(卡底)+ 衬线标题 + 14px 描述;`overflow-hidden`。
- **统计带(总览页 KPI 行的直接范式)**:**一个** hairline 容器,内部 4–6 格以竖 hairline 分隔(`divide-x`),**不是独立卡片**;每格居中,数字 Geist Mono 36px/600,标签 14px muted 于数字下方;单元 padding 24px。暗色同构(底 #141413,格间 #393730)。
- **KPI 卡内角标**(图表卡右上):mono 24px/600 `tabular-nums` + 10px 大写宽字距 caption(TOKEN)。大数字纪律沿用现版 STYLE.md:数字 ≥24px、mono、600。

### 4.5 表格(/pricing 表格视图实测,暗色)

- 列头:12px·500 `--muted-fg`,行底 hairline,可排序列名后置 ⇅ 图标。
- 行:高 88–96px(信息两级:主行 14px + 副行 10–11px muted)或紧凑 44px(单级);行分隔 `0.8px`;hover 底 `--accent`(实测第二行悬停呈提亮)。
- 单元格:模型名/密钥/IP/价格 **mono 14px**;副行(每次调用 / CNY)10–11px muted;端点、标签用 **小 chip**(暗色底 `--secondary`,字 11px muted);状态列 = 4.3 的状态 pill;供应商列 = 图标 + 12px 字。
- 分页:文字钮 + hairline;当前页实心 `--primary`(亮=黑底白字,暗=橙底黑字)。
- 空数据:点阵底纹 + 14px muted 居中,不放插画。

### 4.6 代码块 / 终端卡(3 行代码区实测)

- 容器:r14 卡,顶部条 `--muted`:左红黄绿三点(8px 圆点)+ 右侧 pill Tab(Chat=品牌蓝实心白字,其余文字态);主体 padding 16–24px,Geist Mono 13–14px;字符串/URL 高亮 `--brand`(亮 #177e5b);底部状态条 hairline 上边:`POST /v1/chat/completions` mono 11px muted + 右侧 `● 200 OK`(success 圆点 8px + mono 11px)。
- 面板落位:在线调试页的请求/响应窗、Agent 集成页的代码示例、API 文档页,全部用此终端卡范式;**不用**现版的 `#09093f` 深蓝代码底(见 §6 取舍)。

### 4.7 筛选侧栏(/pricing 左栏实测)

- 独立白卡容器(r14 + 0.8px 边),宽约 264px,内部分组:组头(14px·600 + 右侧 chevron,可折叠,`py-10px`)+ chip 列表(纵向 wrap,gap 8px)+ 分隔线;供应商组 = 图标 + 名称 + 数量徽标(muted 小圆片);组头右上有「重置」文字钮。
- 侧栏与内容区间距 16px;移动端折叠为「筛选」下拉。

### 4.8 顶栏 / 导航(实测)

- 顶栏:sticky,h60(`--app-header-height`)、`bg background/95 + backdrop-blur-md`、底 hairline;左 logo(24px 图 + 衬线 16px 字标,`hover:opacity-80`);右导航项(h32 / px12 / r8 / 14px·500,激活 `--fg`,非激活 `--muted-fg`,hover `--accent` 底)+ 图标钮(语言/主题/通知,通知带右上角橙点数字徽标)+ 登录 outline 钮。
- 面板落位:现版侧栏导航项改为同规格(h36 / r8 / 图标 16px + 字 14px·500;激活底 `--accent` + 字 `--fg`,或左侧 2px 橙条,二选一**取前者**——zero.cat 语言是"底色高亮"而非"指示条")。

### 4.9 排行列表(rankings 实测,密钥用量 Top 榜可直接套用)

- 双栏网格,行内:序号(muted)+ 图标 + **mono 名称(下划线链接:`decoration-foreground/30 underline-offset-4`,hover 实线)** + "by xxx" 衬线斜体 muted;右侧 mono 大数(20–24px/600)+ 单位 muted + 涨跌 `+54.4%`(success)/ `-53.0%`(destructive)。
- 行距 32px,行间无框线(靠间距分层)。

---

## 5. 面板 8 页 + 登录页落地(逐页)

> 通用:每页 = 页头块(h1 衬线 30px/600 + 副标题 14px muted + 页头动作区)+ 区块纵向间距 32px;区块标题用 14px·600 或 eyebrow;所有数字 mono。

| 页 | 布局 | 关键组件落位 |
|---|---|---|
| **登录页** | 整页两栏(实测范式):左 45% 品牌区——暗色摄影底 + 暖黑遮罩(两主题都保持暗),衬线 48px 字标 + 一句副标 + hairline + 3 个 mono 24px 统计(61+ 模型数 / 99.9% 可用性 / 24×7);右 55% 表单区——底 `--bg` + `--canvas-dot` 点阵,居中 384px 列:「登录」标题(衬线 24px)+ 注册链接(橙)→ Passkey/SSO ghost 钮 h44(可选)→ 「或继续使用」细线分隔 → 两输入(h36)→ **反色主按钮**(亮=黑底白字 / 暗=白底黑字)→ 协议勾选(放 `--muted` 圆片行)→ 12px 法律行居中。顶部两个图标钮(语言/主题)。错误提示:红 12px + `--destructive` 边框输入 |
| **总览 overview** | 页头块 + 统计带(4 格 hairline 容器:今日请求 / 活跃密钥 / 平均延迟 P50 / 成功率)+ 两列卡:左「24h 用量」堆叠柱图卡(§4.6 终端卡式卡头 + chart 色板,卡右上 KPI mono 24px),右「引擎健康」列表(行 = 引擎名 mono + 状态 pill + 迷你分段进度条);下方「最近日志」hairline 表 5 行 + 「查看全部」text 钮 |
| **引擎状态 engines** | 页头 + 工具栏(引擎分组 toggle-group:全部/搜索/ review…+ 视图切换)+ 卡片网格(每卡:引擎 icon+名 mono、状态 pill、延迟/吞吐 mono 小字、分段进度条、冷却中显示 mono 倒计时大数 24px);表格视图切换后=§4.5 表格 |
| **API 密钥 keys** | 页头 + 主按钮「新建密钥」(反色 solid)+ 搜索框 ⌘K + §4.5 表格(名称、`sk-…` mono、配额进度细条、2xx 率、创建时间、状态 pill、行尾 icon 钮组:复制/编辑/吊销=destructive ghost);新建/吊销走弹窗(§5.1) |
| **使用日志 usage** | 页头 + 筛选行(chip:级别 全部/2xx/4xx/5xx + 时间范围 underline-tabs 今天/7天/30天)+ 表格(时间 mono 12px、密钥名、模型 mono、引擎 pill、状态 pill、耗时/Token mono 右对齐);行展开显示请求详情(终端卡) |
| **在线调试 playground** | 两栏:左参数表单(模型 select、分组 chip、系统提示 textarea,输入§4.2)+ 右终端卡(§4.6:顶部 pill Tab=chat/responses,主体请求/响应 JSON,底部状态条 `POST /v1/chat/completions ● 200 OK`);底部主按钮「发送」反色 |
| **Agent 集成 agents** | 页头 + 左侧锚点小卡列表(或直接纵向):每集成一卡(OpenAI 兼容 / Claude / MCP / llms.txt):衬线标题 + 14px 描述 + 终端卡代码块(§4.6,含复制钮)+ 右上「复制 base_url」ghost 钮 |
| **API 文档 docs** | 页头 + 两栏:左目录(14px,激活 `--fg` + `--accent` 底 r8)+ 右 prose(衬线 h2/h3、正文 Inter 14、代码块终端卡、参数表 = hairline 表) |
| **系统设置 settings** | 页头 + 左分组锚点(运行/数据/系统)+ 右表单卡:每卡衬线标题 + 字段(label 上、输入下、间距 8/16)+ 卡底部 hairline 上「保存」反色钮;危险区(重置缓存)单独卡 + destructive ghost |

### 5.1 弹窗 / Toast(暗色变量实测,形态照 shadcn 惯例)

- 弹窗:底 `--popover`、r14、`0.8px` 边、`0 8px 24px rgba(20,20,19,.12)`、宽 ≤480、padding 24;标题衬线 16px/600;按钮右下 Primary+Ghost;遮罩 `rgba(20,20,19,.5)`。
- Toast:sonner——亮色卡底 + hairline;暗色 `#000` 底 + `hsl(0 0% 20%)` 边(实测变量);右上角关闭钮(`translate(35%,-35%)` 惯例);语义用 §3.2 三件套(暗色 bg/border/text)。

---

## 6. 与现版 high-flyer 黑白基因的取舍(给重构工程师的明确建议)

现版基因(STYLE.md):纯黑/纯白、品牌蓝 `#177e5b`、亮蓝 `#2997ff`、大数字 mono、hairline 分层、无阴影、圆角 4–8px、零装饰。zero.cat 语言与之**同构**的是"克制、hairline、mono 数字、无重投影",**分歧**在底色温度、强调色、字体层、圆角与微交互。逐项裁决:

### 6.1 保留(黑白基因里依然成立的部分)

1. **大数字纪律**:数字一律 mono ≥24px/600 + `tabular-nums`,标签小灰字——zero.cat 完全同款,继续执行。
2. **hairline 分层、无重阴影**:0.8px 边框 + 底色差,卡片阴影继续禁用(仅浮层一档)。
3. **信息密度优先、装饰近零**:不因换暖色而引入插画/渐变;点阵纹理是唯一被允许的"装饰"(且有实测出处)。
4. **语义色最小扩展原则**:延续"冷却是最低饱和的琥珀"思路,但值换成 zero.cat 实测档(§3.2),不再自造。

### 6.2 替换(黑白基因 → zero.cat 语言)

| 项 | 现版 | V2 裁决 | 理由 |
|---|---|---|---|
| 亮色底 | `#ffffff` | **`#faf9f5` 暖纸白** | zero.cat 全站实测;"冷机房感"→"文档纸面感",与"网站收录/数据情报"的产品叙事更贴 |
| 暗色底 | `#000` | **`#141413` 暖炭黑**(卡片 `#1d1c19`) | 纯黑在长时间后台工作中过于生硬;zero.cat 暗 hero/表格实测 |
| 强调色 | 蓝 `#177e5b`/`#2997ff` | 蓝:亮 `#177e5b`(链接/焦点环)/ 暗 `#3ecf8e`;CTA 同蓝系 | 与参照同为蓝系(2026-11-15 应用户要求由橙改蓝);同时反色 CTA(亮黑/暗白)承接了 Vercel 黑白基因中"单色主按钮"的克制 |
| 标题字体 | 全无衬线 700 | **页头 h1 / 卡片标题 / 登录字标用衬线(Lora + Noto Serif SC),UI/表格全 Inter** | zero.cat 签名;中文呈宋体,回退链照抄其自带配置;仅标题层,信息区不受影响 |
| 等宽字体 | Roboto Mono | **Geist Mono**(回退 JetBrains Mono) | 实测栈;数字观感更"票据化" |
| 圆角 | 4–8px | **8 / 10 / 14 三档**(按钮输入 10、卡片 14、kbd 4) | 实测;4px 过硬,与暖纸气质冲突 |
| 页头位置 | 顶栏放标题 | **页头块移入内容区**(h1+副标+动作),顶栏只留品牌/主题/用户 | /pricing、/rankings 实测结构 |
| 代码块底 | `#09093f` 深蓝 | **终端卡范式**(亮=纸底+边框,暗=`--card`),语言高亮用 `--brand` | 深蓝是 high-flyer 页脚的遗留,与新色系脱节 |
| 状态高亮 | 左侧 2px 蓝指示条 | **选中项 `--accent` 底色高亮**(侧栏项、Tab、行选中) | zero.cat 语言靠底色不靠指示条(实测 nav/Tab 无左条) |

### 6.3 新增(zero.cat/Linear 有而现版没有)

1. **双主题**:亮暖纸为默认,暗暖炭一键切换(变量成对,见 §3.1);`color-scheme` 同步。
2. **微交互底座**:全组件 `transition-colors 200ms`;主按钮 `active:scale-[0.96]`;焦点环 `ring-2 ring-brand/30`——现版无按压/焦点反馈,V2 必须补齐。
3. **⌘K**:页头搜索框带 kbd 帽 + 全局快捷键(`/` 聚焦搜索、`⌘K` 命令面板)——Linear/zero.cat 双重实证。
4. **分段进度条 / 分段 toggle-group**:引擎健康、筛选工具栏直接套实测组件。

### 6.4 禁止(延续并加严)

1. 禁止蓝橙同屏混用:蓝退役后全站唯一强调=橙;图表多色只允许 §3.3 色板。
2. 禁止衬线进入表格/按钮/表单;禁止 mono 进入标题层。
3. 禁止圆角 >14px(仅状态 pill 允许 full)。
4. 禁止重阴影/渐变按钮/大面积色块;CTA 橙仅登录页与页头主按钮可用,单屏 ≤1 处。
5. 禁止 >200ms 的界面动画与弹性缓动。

---

## 7. 证据清单

| 结论 | 依据(本次会话实际执行) |
|---|---|
| 登录墙、降级决策 | 打开 `https://zero.cat/dashboard/overview` → 302 `https://zero.cat/sign-in?redirect=%2Fdashboard%2Foverview`(浏览器 URL 实录) |
| 暗/亮全量 token(§3.1–3.3) | 登录页 `:root` CSS 变量提取:亮 `--background:#faf9f5`/`--primary:#141413`/`--brand:#c2410c`/`--radius:.625rem`…;暗 `--background:#141413`/`--card:#1d1c19`/`--primary:#ff8a3d`/`--border:#393730`/`--sidebar:#191816`… |
| 字体三族(§3.4) | body/wordmark/统计数字 `getComputedStyle` font-family 原串(Inter Variable / Anthropic Serif→Lora / Geist Mono) |
| 字阶(§3.5) | hero h1 72px/500;字标 48px/600 -0.03em;页 h1 30px/600 -0.6px;区块 h2 30px/700;卡题 16px/600;统计数字 36px/600 -0.72px;KPI 24px/600 tabular-nums;eyebrow 12px/600 1.2px |
| 按钮(§4.1) | CTA class 实录 `bg-[#ed5b20] text-[#141413] hover:bg-[#f06b37] active:scale-[0.96]` + h48/px28/r10;登录钮亮=黑底白字、暗=白底黑字(两主题分别计算);outline h32 r8 hover accent |
| 输入/搜索/kbd(§4.2) | 输入 h36 r10 `0.8px`;搜索 h40 + `focus:border-primary/50`;kbd mono 10px bg #f1f0eb r4 |
| 统计带/特性卡(§4.4) | 主页实测:容器 hairline 分格、单元 py-24px、数字 mono 36px;特性卡 r14 0.8px 边无阴影、上段 muted+点阵 |
| 表格(§4.5) | /pricing 表格视图截图(console-zero-pricing-table.png,暗色):mono 模型名、双级行、小 chip、状态 pill |
| 图表/KPI/排行(§4.9) | /rankings 截图 + 计算:KPI mono 24px、排行名 mono 下划线 `decoration-foreground/30`、涨跌绿/红 |
| 微交互(§3.8) | transition 计算值实测:`0.2s color, background-color, transform`、`0.15s opacity`、focus ring class 实录 |
| 主题切换交互 | 页面 menu(浅色/深色/系统)DOM 点击实录;暗色切回 `html.dark` class 实证 |
| WebSearch 佐证 | Linear(键盘优先/Cmd+K/暗色优先/低铬)、Vercel Geist(黑白减法/hairline+shadow-as-border/Geist Mono)、Anthropic(暖纸/黄棕调灰/衬线标题+Inter UI+mono 代码/8px 网格);zero.cat 无专门设计文章(仅"界面友好"评价),其页脚自述「基于 new-api」 |
| 截图 | `qa/reference/console-zero-signin-dark.png`、`console-zero-signin-light.png`、`console-zero-home-hero.png`、`console-zero-home-hero-dark.png`、`console-zero-home-features.png`、`console-zero-home-features-dark.png`、`console-zero-home-code.png`、`console-zero-pricing-top.png`、`console-zero-pricing-table.png`、`console-zero-rankings-top.png`(共 10 张,均本次会话生成) |

**外部来源**:[Geist 官方介绍](https://vercel.com/geist/introduction) · [Vercel Geist 设计系统解析](https://www.designsystems.one/design-systems/vercel-geist) · [Linear Design System(Figma Community)](https://www.figma.com/community/file/1222872653732371433/linear-design-system) · [Linear design system on OpenDesign](https://open-design.ai/plugins/design-system-linear-app) · [Anthropic 设计系统解析(shadcn.io)](https://www.shadcn.io/design/anthropic) · [Refero: Claude 界面风格](https://styles.refero.design/style/d469cba4-c448-4a43-a033-883f8bfcdc42)

---

## 8. 交接备注(给面板重构工程师)

1. 本文件只定义语言与落位,**不改任何现有代码**;实现时新建变量层(建议 `assets/style.css` 顶部 `:root` / `.dark` 两段),旧 token(`--hf-*`)逐个映射删除。
2. 圆角/边框/过渡先全局铺(0.8px、r8/10/14、200ms),再逐页对表 §5;每页对照 `qa/reference/console-zero-*.png` 验收。
3. 主题切换实现 `<html class="dark">` + localStorage,登录页右上放主题图标钮(zero.cat 同款三态菜单可简化为双态)。
4. 字体自托管:Inter Variable、Geist Mono(Vercel OFL)、Lora + Noto Serif SC(Google Fonts OFL);全部 `font-display: swap`,管理台禁外部 CDN(与现版自托管约束一致)。
5. 未验证项(实现时自行定夺):登录后壳层侧栏的可视宽度与折叠行为、暗色下大表格的斑马纹禁令沿用现版、移动端断点沿用现版 1024/768。
