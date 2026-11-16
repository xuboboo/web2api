# Web2API 管理控制台 · 设计规范 V4(设计基准:zero.cat 2026-11-15 实测)

> **取代声明**:本规范(V4)自 2026-11-15 起取代 `docs/design/STYLE-v2.md`,成为 Web2API 管理控制台(web/admin/)的**现行设计规范**;`STYLE-v2.md` 保留为历史文件,不再作为验收基准。V2 → V4 的主要变化:强调色确认映射为品牌松柏绿(亮 `#177e5b` / 暗 `#3ecf8e`)、暗色语义色四值全量更新、新增侧栏/表格/滚动条/骨架实测 token(§3.4/§3.5/§9.16)、动效时长表精确化(§8)、按压曲线 scale(0.95)+400ms 过冲回弹。`STYLE-v3.md` 为官网(web/site)规范,与面板无关。冲突裁决:实施阶段若发现本文与施工要点摘要不一致,以本文为准。

> 本规范所有色值/字号/几何/动效均来自 2026-11-15 1440×900 视口下对 zero.cat 的浏览器实测(`getComputedStyle` + `:root`/`.dark` 全量 CSS 变量提取 + 样式表规则扫描),截图存于 `qa/panel-ref/`(17 张)。凡未经直接验证的部分一律标注【推断】;与 zero.cat 原值的差异一律标注【映射】。

---

## 0. 勘察纪要与降级声明(如实记录)

1. **登录墙**:打开 `https://zero.cat/dashboard/overview` 被 302 到 `https://zero.cat/sign-in?redirect=%2Fdashboard%2Foverview`——本环境(应用内浏览器)会话未登录,用户侧也无已打开的 zero.cat 标签页。按纪律**不输入凭据、不绕过**,登录墙已截图(`qa/panel-ref/00-login-wall.png`)。
2. **降级勘察范围**(全部为只读浏览,未登录/登出、未创建/修改/删除任何数据、未改站点设置;仅用站点自带的主题菜单切换显示主题,不动任何服务端数据):
   - 登录页 `/sign-in`(暗+浅两套完整提取);
   - 首页 `/`(hero/厂商 marquee/统计带/特性卡/终端卡/页脚,浅+暗);
   - 模型广场 `/pricing`(筛选侧栏/工具栏/搜索/卡片视图/表格视图/分页/详情抽屉,浅+暗);
   - 排行分析 `/rankings`(下划线 Tab/堆叠柱图卡/KPI/排行榜,浅+暗);
   - 主题切换菜单(浅色开态截图、暗色开态 DOM 提取)。
3. **登录后壳层(侧栏/总览/设置)未能直接勘察**。证据:`:root` 中实测到成套 shadcn Sidebar 变量(`--sidebar*` 全套)、`--app-header-height: 3.75rem`、以及样式表中的 sidebar menu-button 交互规则(`data-active`→`bg-sidebar-accent + font-semibold`)。据此按 shadcn Sidebar 标准形态【推断】壳层,文中涉及处均标注。
4. **V2 文档差异警示**:`docs/design/STYLE-v2.md` §3.1 表格中 `--brand` 一行写的是 `#177e5b/#3ecf8e`,但那是当时"映射后"的裁决值;本轮实测 zero.cat 原值为 **橙系**:亮 `--brand:#c2410c`、暗 `#ff8a3d`、营销 CTA `#ed5b20`(hover #f06b37)。本轮全部以实测原值为基准,再按品牌约束做绿色映射(§4)。

---

## 1. 设计基因(一段话)

**暖纸工作台 + hairline 分层 + 反色主按钮 + mono 数字**。亮色主题是带黄棕底调的"羊皮纸"暖白 `#faf9f5`,暗色是暖炭黑 `#141413`,两套共用一套语义结构;层级不靠阴影靠 **0.8px hairline 边框 + 底色差**(卡片实测 box-shadow 全为 none);唯一强调色=品牌绿·松柏【映射,原站为橙】,其余全靠暖灰阶;标题层用衬线(Lora→中文思源宋体),UI 正文 Inter+系统中文栈,一切数字/代码/名称用 Geist Mono;大数字+小灰标签是数据表达的唯一范式;微交互 100–200ms,按压 `scale(0.95)`,焦点是 2.4px 品牌色 50% 外环;**全站细滚动条**(Firefox `scrollbar-width: thin`,全局规则实测)。

---

## 2. 主题机制(实测)

- 切换载体:`<html>` 上的 class——`font-inter light` / `font-inter dark`(+ `color-scheme` 同步);浅色默认。
- 用户入口:右上角图标钮 → 三态菜单「浅色 / 深色 / 系统」,当前项右侧 ✓。
- 防闪白:加载前同步主题(html 预置 class),现版面板的 `w2a_theme` 预置脚本机制与此一致,**保留**。
- 加载屏:`#app-loading .brand` 用 Manrope/橙色字标,`app-loading-appear/vanish/rise/pulse` 四个 keyframes 做入场淡出——面板可保留现有加载屏,配色换绿。

---

## 3. Token 总表(CSS 自定义属性,暗/浅两套完整值)

> 「原值」= zero.cat 2026-11-15 实测;「落地值」= V4 面板采用的值。除 §4 标注的绿色映射外,落地值=原值,照抄即可。

### 3.1 基础面(亮 / 暗成对)

| Token | 亮色落地 | 暗色落地 | 用途 |
|---|---|---|---|
| `--background` | `#faf9f5` 暖纸白 | `#141413` 暖炭黑 | 页面/抽屉底 |
| `--foreground` | `#141413` | `#f3f1eb` 暖白 | 主文字 |
| `--card` | `#faf9f5`(与底同色,靠边框成卡) | `#1d1c19` | 卡片/侧栏/表格底 |
| `--card-foreground` | `#141413` | `#f3f1eb` | |
| `--popover` | `#faf9f5` | `#1d1c19`(=card) | 下拉/浮层 |
| `--popover-foreground` | `#141413` | `#f3f1eb` | |
| `--primary` | `#141413` | `#f3f1eb` | 反色主按钮底(亮暗语义相反) |
| `--primary-foreground` | `#faf9f5` | `#141413` | 主按钮文字 |
| `--secondary` | `#efeee8` / 字 `#30302e` | `#262420` / 字 `#f3f1eb` | 次级按钮/嵌套卡底 |
| `--muted` | `#efeee8` | `#262420` | 次级底(kbd/表头/骨架/特性卡上段) |
| `--muted-foreground` | `#5e5d59` | `#a8a69f` | 次级文字/占位/表头字/标签 |
| `--accent` | `#eae8e0` | `#2e2b26` | 悬停底/选中底(侧栏项/行 hover 用 30% 透明) |
| `--accent-foreground` | `#141413` | `#f3f1eb` | |
| `--border` | `#e2e0d8` | `#322f28` | 全站 hairline(暗色表格行实测计算值 `#393730`,见 §9.7) |
| `--input` | `#e2e0d8` | `#4a473d` | 输入框边框(暗色搜索框用 `--input/60`) |
| `--ring` | `var(--brand)` | `var(--brand)` | 焦点环(50% 透明度实测) |

### 3.2 品牌与语义色

| Token | 亮色落地 | 暗色落地 | 说明 |
|---|---|---|---|
| `--brand` | `#177e5b` 【映射,原 #c2410c】 | `#3ecf8e` 【映射,原 #ff8a3d】 | 链接/焦点环/chart-1/选中行染色/动态提示字 |
| `--brand-foreground` | `#faf9f5` | `#141413` | |
| `--brand-cta` | `#177e5b`,hover `#0f6b4b`,字 `#ffffff` | `#3ecf8e`,hover `#55d9a4`,字 `#141413` | 营销级 CTA【映射,原橙 #ed5b20 hover #f06b37 字 #141413】 |
| `--success` | `#15803d` / 字 `#faf9f5` | `#35c07a` / 字 `#141413` | 启用/2xx/正常 |
| `--warning` | `#b45309` / 字 `#faf9f5` | `#e0a53a` / 字 `#141413` | 冷却/限流/4xx/动态提示图标 |
| `--destructive` | `#cc3d3d` / 字 `#faf9f5` | `#e05a5a` / 字 `#141413` | 熔断/5xx/删除/涨跌红 |
| `--info` | `#0084cc` / 字 `#fafafa` | `#00a4ed` / 字 `#0a0a0a` | 提示 |
| `--neutral` | `#a1a1a1` / 字 `#0a0a0a` | `#b1b1b1` / 字 `#0a0a0a` | 中性徽标 |

### 3.3 图表色板(同屏最多 5 系)

| 系 | 亮色落地 | 暗色落地 |
|---|---|---|
| chart-1(=brand) | `#177e5b` 【映射,原 #ff8a3d】 | `#3ecf8e` 【映射,原 #ff8a3d】 |
| chart-2 | `#625fff` | `#8089ff` |
| chart-3 | `#00bb7f` | `#00c386` |
| chart-4 | `#00a5ef` | `#00b1fb` |
| chart-5 | `#f99c00` | `#ffa824` |

- 堆叠柱状图实测形态:分段间 1px 底色缝、无渐变、无圆角或 ≤2px 微圆角;仅横向网格线;轴标签 mono 10–11px muted。

### 3.4 侧栏 / 表格 / 骨架 / 纹理(实测变量)

| Token | 亮色 | 暗色 |
|---|---|---|
| `--sidebar` | `var(--card)` = #faf9f5 | #1d1c19 |
| `--sidebar-foreground` | `#30302e` | `#d8d6d0` |
| `--sidebar-primary` / `-foreground` | `var(--brand)` / `var(--brand-foreground)` | 同 |
| `--sidebar-accent` / `-foreground` | `#eae8e0` / `#141413` | `#2e2b26` / `#f3f1eb` |
| `--sidebar-border` / `--sidebar-ring` | `var(--border)` / `var(--ring)` | 同 |
| `--table-row` / `--table-header` | `var(--card)` | 同 |
| `--table-header-hover` | `var(--foreground)` | 同 |
| `--table-disabled(-hover/-border)` | `var(--foreground)` | 同 |
| `--skeleton-base` / `--skeleton-highlight` | `#edece6` / `#f6f5f0` | `#292825` / `#383632` |
| `--canvas-dot` | `#1414130d`(1px 圆点平铺) | `#f3f1eb0d` |
| `--app-header-height` | `3.75rem`(60px) | 同 |
| `--radius` | `.625rem`(10px) | 同 |

### 3.5 细滚动条 token(V4 新增,规格见 §9.16)

| Token | 亮色 | 暗色 |
|---|---|---|
| `--scrollbar-thumb` | `#e2e0d8` | `#322f28` |
| `--scrollbar-thumb-hover` | `#c9c7bd`(加深) | `#4a473d`(提亮=对比加深) |

---

## 4. 品牌映射规则(松柏绿,一次说清)

zero.cat 的唯一强调色是**橙系**;V4 面板整体对齐 zero.cat 的中性色/结构/密度/动效/组件形态,**仅把强调色相换成松柏绿**:

1. 凡 `--brand`:亮 `#c2410c`→`#177e5b`,暗 `#ff8a3d`→`#3ecf8e`。影响面:链接、焦点环、chart-1、表格选中行染色(`color-mix(in oklch, var(--brand) 7%, var(--table-row))`,公式不变)、品牌染色的提示 chip(如"动态计费")、通知徽标、视图切换激活态。
2. 凡营销 CTA 橙 `#ed5b20`:亮改 `#177e5b` 白字,暗改 `#3ecf8e` 黑字(沿用 V2 裁决);CTA 仅登录页与页头主按钮可用,单屏 ≤1 处。
3. 语义色(success/warning/destructive/info)**保持 zero.cat 原值不映射**——它们是状态语义不是品牌色;其中 chart-3 `#00bb7f`/`#00c386` 与绿系同相,多系图表并排时注意与 chart-1 拉开明度(必要时对调 chart-3 与 chart-5 位次)。
4. 反色主按钮(亮=黑底白字/暗=白底黑字)**保持原值**——这是 zero.cat/Geist 语言的"单色主按钮",不因绿色映射而改动。

---

## 5. 字体(中文界面栈,明确选择)

| 族 | 落地栈(自托管,`font-display: swap`,管理台禁外部 CDN) | 用途 |
|---|---|---|
| UI 无衬线 | `"Inter Variable", "Inter", system-ui, -apple-system, "Segoe UI", Roboto, "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Noto Sans SC", sans-serif` | 全部 UI/表格/表单。中文回退:macOS 苹方 / Windows 雅黑;可选自托管 Noto Sans SC 兜底。zero.cat 原栈无中文族,V4 明确写入防止中文渲染漂移 |
| 展示衬线(仅标题层) | `"Lora Variable", Lora, "Noto Serif SC", "Source Han Serif SC", "Songti SC", SimSun, serif`(照抄 zero.cat 回退链,拉丁/数字 Lora,中文思源宋) | 页头 h1、卡组/卡片标题、登录字标 |
| 等宽 | `"Geist Mono", "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace`;数字一律加 `tabular-nums` | 一切数字、模型名/密钥/IP、价格、代码、轴标签 |
| Manrope(可选) | `"Manrope Variable", "Inter Variable", system-ui, sans-serif` | 仅登录页表单标题与加载屏字标(zero.cat 实测如此,原样保留;若减负可并入衬线/Inter,标注为可选项) |

实测原串:`--font-ui-sans: "Inter Variable", system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif`;衬线实测含 Anthropic Serif 头(品牌专有,自托管从 Lora 起);加载字标 `Manrope Variable`。

---

## 6. 字阶(实测)

| 级别 | 实测值 | 用途 |
|---|---|---|
| Hero 字标 | 衬线 48px/600,`letter-spacing:-0.03em`(实测 -1.44px) | 登录页品牌字标 |
| Hero 标语 | 衬线 72px/500,-1.44px,行高 ~0.98 | 营销页(面板登录页可选) |
| 页头 h1 | 衬线 30px/600,`tracking:-0.02em`(-0.6px),行高 1.25 | 每页页头("模型广场"/"模型榜单"实测) |
| 登录 h2 | Manrope 24px/600,-0.6px | 登录表单标题 |
| 卡片标题 | 衬线 16px/600 | 卡片/弹窗标题 |
| Body | Inter 14px/400,行高 1.5 | 正文/表格 |
| Strong | Inter 14px/500 | 按钮/导航/Tab/标签字 |
| 副标 | Inter 16px/400 muted(登录页实测) / 14px muted(页头副标) | 说明文字 |
| Caption | Inter 12px/400 | 帮助/法律行/统计带标签(实测 12px) |
| Eyebrow | 10px 大写,宽字距(≈1.2px) | KPI 角标单位(TOKEN) |
| KPI 大数字 | Geist Mono 36px/600,`-0.02em`(统计带实测)/ 24px/600(卡内 KPI "1.96T" 实测) | 一切数字 |
| Mono-Body | Geist Mono 14px/500 | 模型名/密钥/代码 |
| Mono-Small | Geist Mono 12px/400–500(延迟/吞吐/总计) | 表格副级 |
| kbd | Geist Mono 10px | ⌘K 快捷键帽 |

纪律:数字永远 mono+tabular;衬线只出现在标题层;UI 层零衬线。

---

## 7. 间距 / 圆角 / 边框 / 阴影(实测)

### 7.1 间距(4px 基网格,实测锚点)

`4 / 8 / 12 / 16 / 20 / 24 / 32 / 48 / 64`

- 组件内:按钮 `px-12~28`,输入 `4px 12px`,卡片 `16~24px`,chip `0 6~8px`,统计带格 `24px`。
- 页面:内容左右 gutter 24px;页头块与内容 24px;卡网格 gap 16px;表单 label↔框 8px、字段间 16px;抽屉内容 `24px`(顶部 48px)。

### 7.2 圆角(`--radius:10px` 基准,全实测)

| 档 | 值 | 用途 |
|---|---|---|
| sm | `calc(var(--radius)*0.6)`≈6px | 菜单项、Tab 焦点底 |
| md | `calc(var(--radius)*0.8)`≈8px | 小按钮(h32)、chip(类型染色 chip)、排序钮 |
| lg | `var(--radius)`=10px | 主按钮/输入/搜索/分段控件/下拉菜单容器 |
| xl | 14px | 卡片/筛选侧栏/终端卡(实测 rounded-xl) |
| full | 30px+/50% | 状态 pill、端点小 chip、toast 关闭钮 |
| 0 | — | 表格容器、Sheet 抽屉(贴边) |

### 7.3 边框与阴影

- **全部组件统一 `0.8px solid var(--border)`**(按钮/输入/卡片/表格行/kbd/菜单实测一致;唯一例外 solid 主按钮边框透明)。
- **卡片与按钮 box-shadow:none(实测)**;层级只靠 hairline+底色差。
- 允许阴影的仅浮层一档:下拉/弹窗 `0 4px 12px rgba(0,0,0,.1)`(菜单实测)/`0 8px 24px rgba(20,20,19,.12)`(弹窗,V2 实测沿用);toast `rgba(0,0,0,.1) 0 4px 12px`(sonner 实测)。
- 焦点环:`outline: 2.4px solid color-mix(in oklab, var(--ring) 50%, transparent)`(实测 outline-color=brand/50、width 2.4px、仅 `:focus-visible`;边框同步 `border-color: var(--ring)/60` 级别)。

---

## 8. 动效规范(时长/缓动/触发点,全实测)

| 触发点 | 时长/缓动 | 出处 |
|---|---|---|
| 按钮/导航/卡片颜色·背景·边框过渡 | `200ms cubic-bezier(0.4,0,0.2,1)` | 登录页按钮 transition 实测 |
| 输入框颜色/边框过渡 | `150ms cubic-bezier(0.4,0,0.2,1)` | 输入 transition 实测 |
| 表格行 hover 底色 | `120ms`(background-color) | 行 transition 实测 |
| 按钮按压 | 按下 `scale(0.95)`,80ms ease-out;松开回弹 `scale(1)`,400ms `cubic-bezier(0.34,1.56,0.64,1)`(过冲) | `.btn-squish` 规则实测 |
| 下拉/Popover 打开 | `150ms` enter:fade + `zoom-in-95`(0.95→1)+ 上滑 8px(`slide-in-from-top-2`) | Radix/Base 规则实测 |
| 下拉关闭 | `75~150ms` exit(fade + zoom-out-95) | 同上 |
| 菜单快速态 | open `100ms` / close `75ms`(`data-open:duration-100`/`data-closed:duration-75`) | 同上 |
| Accordion(侧栏分组/筛选组)展开收起 | `200ms ease-out` 高度动画(`accordion-down/up`) | keyframes 实测 |
| Sheet 抽屉 | 本体 `200ms ease-in-out`:进入 translate-x-40px+fade→0;退出反向 | sheet class 实测 |
| Sheet/Dialog 遮罩 | `150ms` opacity;遮罩色 `bg-black/60` | 遮罩 class 实测 |
| Toast(sonner) | in:fade+scale .8→1;out:scale→.8;可划走(swipe-out) | keyframes 实测 |
| 骨架 shimmer | `skeleton-shimmer`:background-position 100%→-100% 循环(实现取 1.5–2s linear infinite,标注:循环时长未在 zero.cat 实测到具体值) | keyframes 实测 |
| 表格行进入(翻页/刷新) | `tableRowEnter`:opacity 0→1 + translateY 4px→0,约 150ms | keyframes 实测 |
| 折叠面板 | `slideDown/slideUp`:height 0↔var(--collapsible-panel-height),200ms | keyframes 实测 |
| 图表柱生长 | 进入视口时生长(观测到动画过程);建议 300–500ms ease-out【时长未精确实测,标注】 | 观测 |
| 营销页入场 | `landing-fade-up` 24px 淡入上移等(仅登录页可选) | keyframes 实测 |
| 主题切换 | 无全局过渡(即时切换);面板可加 150ms 颜色过渡,不得加全屏白闪动画 | 观测 |

**统一约束**:界面动画上限 200ms(按钮回弹 400ms 与抽屉 200ms 是仅有的例外);禁 bounce/弹簧曲线进面板(按钮回弹的过冲曲线是唯一指定弹性);实现时全局加 `@media (prefers-reduced-motion: reduce){ *{animation:none!important; transition:none!important} }`。

---

## 9. 组件规格

### 9.1 按钮体系

| 变体 | 几何(实测) | 亮色 | 暗色 |
|---|---|---|---|
| Primary-solid(反色) | h36 / px16 / r10 / 14px·500 / 边框透明 / 按 `.btn-squish` | 底 `#141413` 字 `#faf9f5`,hover 底色微降不透明度或加深 | 底 `#f3f1eb` 字 `#141413` |
| Brand-CTA(仅登录页/页头主 CTA) | h48 / px28 / r10 / 16px·500 / gap 8px | 底 `#177e5b` 字 `#fff`,hover `#0f6b4b`【映射】 | 底 `#3ecf8e` 字 `#141413`,hover `#55d9a4`【映射】 |
| Outline(次操作) | h44(登录 SSO)/ h32(工具栏)/ px12-16 / r8-10 / `0.8px solid var(--border)` | 底 background,hover 底 `--accent` 字 `--accent-fg` | 同;边框用 `--input` |
| Ghost/图标钮 | 36×36 / r10 / 无底无边框 | hover 底 `--accent` | 同 |
| Danger | outline 几何 + `--destructive` 字与边框 | 危险操作(吊销/重置) | 暗字 `#e05a5a` |
| Text(行内) | 无底无边框,字 `--muted-fg`,hover `--fg` | 「查看全部」「重置」 | 同 |

- 全部按钮:0.8px 边框体系;`disabled: opacity-50 pointer-events-none`(登录页 SSO 禁用态实测 opacity 0.5);焦点=§7.3 焦点环;按 `.btn-squish` 按压曲线。
- 顶栏导航项(公开页):h32 / px12 / r8 / 14px·500;非激活 `--muted-foreground`,激活 `--foreground`,hover 底 accent。【控制台侧栏项规格不同,见 §9.8】

### 9.2 输入 / 搜索 / textarea

- **输入框**:h36 / r10 / `0.8px solid var(--input)` / 字 14px / `padding:4px 12px`;亮底=background,暗底=`--input/30` 叠层(实测 oklab 0.3 透明)。placeholder `--muted-foreground`。hover 边框加深至 `--input` 全量;focus-visible=§7.3 焦点环(实测 outline 2.4px ring/50)。
- **页头搜索框**:h40 / r10 / 边框 `--input/60`(实测)/ 左置搜索图标(左 36px)/ 右置 ⌘K kbd(右 64px);快捷键 `/` 或 ⌘K 聚焦【⌘K 帽实测,快捷键行为沿用 V2 约定】。
- **textarea**:同几何,padding 12px,最小高 96px。
- **label**:输入上方,14px/500,与框距 8px;错误态:`--destructive` 边框 + 12px 错误文字(V2 实测沿用)。
- **密度**:暗色搜索框边框实测 `--input/60`;登录输入框为全量 `--input`。

### 9.3 select / 分段控件 / toggle-group

- **select**:同输入几何(h36 r10 0.8px),右 chevron;「每页行数」实测为带上下 chevron 的紧凑 select。
- **分段控件(toggle-group)**:成员 h32 / px8 / 14px·500;组外角 r10(实测 `10px 0 0 10px`);激活成员底 `--muted`(#f1f0eb)+ `0.8px` 边框,非激活透明;工具栏实例:标准/充值、/1M /1K;视图切换(网格/表格)激活=brand 色【映射,原橙】。
- **分段 Tab(Sheet 内三等分)**:容器 `--muted` 底 r10,成员等宽 h36;激活成员底 `--background`+hairline 边框(shadcn default 变体:`data-active:shadow-sm + bg-background`,暗 `bg-input/30`)。

### 9.4 checkbox / switch / radio

- **switch(shadcn)**:checked 底 `--primary` + thumb `--primary-foreground`,thumb 平移 `calc(100%-2px)`;unchecked 底 `--input`(暗 `--input/80`);尺寸 default(h-5 w-9 ≈20×36)【shadcn 默认值,标注推断】。
- **checkbox**:登录页协议勾选实测为**圆形圆点样式**(radio 观感)置于 `--muted` 圆片行;面板表单用 shadcn 方形勾选框(默认 r4,checked 底 primary)【推断】。
- 选中卡/选中分组 chip:`data-checked` → 底 `--primary/5~10%`(暗)+ 边框 `--primary/30~20%`(实测规则);文字回 `--foreground`。

### 9.5 chip / pill / badge / kbd

| 类型 | 实测规格 |
|---|---|
| 端点小 chip | h20 / `0 6px` / r-full / 12px muted / 底 `#f1f0eb`(=muted 系) |
| 类型染色 chip(动态计费) | h20 / `0 6px` / r8 / 12px·500 / 底 brand 8% + `0.8px` 边 brand 30% + brand 字【映射为绿】 |
| 筛选 chip(侧栏) | h30 / r8 / `0.8px` 边 / 底 background / 12px·500 / 计数后缀 muted(`x1.5`);pressed/选中 底 `--accent` |
| 状态 pill(表格) | r-full / `2px 10px` / 12px·600;启用=success;冷却=warning 描边;熔断=destructive;停用=灰字无框(V2 实测沿用) |
| 通知徽标 | 右上角圆点数字,底 brand【映射,原橙】字白 10px |
| kbd | Geist Mono 10px / 底 `#f1f0eb` / `0.8px` 边 / r4 / `2px 6px` |

### 9.6 卡片 / 统计带 / KPI

- **普通卡**:r14 + `0.8px` 边 + 无阴影;pad 16–24;标题衬线 16px/600,描述 14px muted。
- **模型卡(数据卡范式)**:头部 icon 28 + mono 名 16/600 + 厂商 icon+12px;描述 14 muted;定价区:eyebrow 12px(每次调用/输入)+ mono 价格 16/600 + `/1M` muted;分组/端点 chip 行;底部 hairline 上方状态带:label 12 muted + **分段进度条**(约 14–16 个小块 h8,绿=健康,掺红=异常)+ 延迟/吞吐 mono 12px 右侧两栏。
- **特性卡**:上下两段——上段 `--muted` 底+`--canvas-dot` 点阵+居中线性 icon(20–24px,stroke 1.5),下段卡底+衬线标题+14px 描述;`overflow-hidden`(实测截图)。
- **统计带(总览 KPI 行范式)**:**单个** hairline 容器(h≈110px),内部 4 格竖 hairline 分隔(`divide-x`),不是独立卡片;每格居中,数字 Geist Mono 36px/600/-0.02em,标签 12px muted 于数字下方;格 pad 24px;暗色同构。
- **KPI 卡角标**:右上 mono 24px/600 + 10px 大写宽字距 eyebrow(TOKEN)。

### 9.7 表格 / 分页 / 空态(核心,全部实测)

- 容器:无外框无圆角无阴影(表格裸排在页底色上);`border-collapse: collapse`。
- **表头**:h40;th 14px/500 `--muted-foreground`,`padding:0 8px`,text-align:start;可排序列名右置 **⇅ 图标**;thead 底 hairline `0.8px`。表头无底色(实测 transparent)。
- **行**:两级行高 **85px**(主行 14px + 副行 10–12px muted),紧凑单级 **44px**;td `padding:12px 8px`,vertical-align:middle;行分隔 `0.8px`——亮 `#e2e0d8`,**暗实测计算值 `#393730`**(≈--border 系;实现取 `--border` 即可,标注与 token 的细微差异)。
- **hover**:底 `--accent/30`(亮暗同公式,实测 120ms 过渡);**选中行**:底 `color-mix(in oklch, var(--brand) 7%, var(--table-row))` + 选中单元格边框 `--brand/40`【公式实测,brand 换绿】;禁用行:`--table-disabled` 系列。
- **单元格内容纪律**:模型名/密钥/IP/价格一律 **Geist Mono 14px/500**;副级(单位/CNY/耗时)mono 10–12px muted;端点/标签用小 chip;状态列=状态 pill;供应商=icon+12px。
- **分页(实测)**:底栏 h36;左侧 `总计: 61`(数字 mono 600)+ `每页行数` label + select(20/50/100);右侧页码钮 h32:当前页**实心 `--primary`**(亮黑底白字/暗白底黑字)+反色字,其余 ghost 文字钮,« ‹ › » 同规格;卡视图分页为 `第 1 页,共 4 页` + 上一页/下一页(disabled opacity 50)。
- **横向滚动**:表格区横向可滚时用全局细滚动条(§9.16),实测横向细条出现于表格底部。
- **空态**:点阵底纹(`--canvas-dot`)+ 14px muted 居中,不放插画【V2 实测沿用;本轮未观测到空表格,标注】。
- **排序态**:列头 ⇅ 图标(默认 muted);激活排序方向=fg 色+箭头方向化【图标形态实测,排序点击未逐一枚举,标注】。

### 9.8 侧栏(登录后壳层)【推断——实测仅到变量与交互规则】

- 形态:shadcn Sidebar,左侧固定,**默认宽 16rem(256px)**【shadcn 默认,未实测】,可折叠为 3rem 图标列(带 `cursor-e-resize/w-resize` 拖拽把手,实测规则);`variant="inset"` 折叠时主区 `ml-2`(实测规则)。
- 底色 `--sidebar`(亮=卡同色/暗 #1d1c19),右边框 `--sidebar-border` hairline。
- 分组标题:12px 大写宽字距 `--muted-foreground`;分组可折叠(accordion 200ms)。
- **菜单项**:h36 / r8 / icon 16px + 14px·500 / gap 8px;**激活态=`bg-sidebar-accent` + `font-semibold` + 字 `--sidebar-accent-foreground`**(实测规则,zero.cat 语言是"底色高亮",无左指示条);hover 同底色;图标激活染 `--brand`(实测 `[&_svg]:text-brand` 规则)。
- 折叠态:仅图标,hover 弹 tooltip(150ms fade)。
- 面板落位:沿用现版 `.sidebar/.main` 骨架换肤;移动端 ≤1024px 折叠为抽屉。

### 9.9 顶栏(登录后)

- 高度 `--app-header-height: 3.75rem`(60px,实测变量);sticky;底 hairline;底色 `background/95 + backdrop-blur`(公开页顶栏实测 `blur(12px)` bg 95%)。
- 内容:左=侧栏折叠钮(图标)+ 面包屑/页标题(可选);右=主题切换、语言、通知(带 brand 徽标)【通知徽标=brand 染色,原橙,映射】、用户菜单。
- 页标题**不放在顶栏**——页头块在内容区(见 §10)。

### 9.10 下拉菜单 / Popover(实测)

- 容器:底 `--popover`(亮 #faf9f5 / 暗 #1d1c19)/ `0.8px` 边框 / r10 / `padding:4px` / 阴影 `0 4px 12px rgba(0,0,0,.1)` / 宽随内容(主题菜单实测 128px)。
- 菜单项:h32 / r6 / `6px 8px` / 14px·400 字 `--foreground`;hover/激活 底 `--accent`;当前选中项右侧 ✓;危险项字 `--destructive`。
- 动画:150ms zoom-95+fade+上滑 8px,关闭 75–100ms。
- 触发钮:36×36 ghost 图标钮(hover accent 底,实测)。

### 9.11 Dialog / Sheet(抽屉实测)

- **Sheet(右侧抽屉,详情页范式,实测)**:宽 672px(≤90vw);贴上下右三边,radius 0,**无阴影**;`border-left: 0.8px` hairline;底 `--background`;内容 `48px 24px 24px`;头部:标题(mono 20px/600)+ 左上关闭 ×(ghost 32px);结构=内嵌分段 Tab(三等分)→ KPI 带(3 格 hairline 容器)→ 嵌套次级卡(底 `--secondary` r10 pad16)+ 表格化明细;底部 hint 12px muted。
- 进入:`200ms ease-in-out` translate-x-40px+fade;退出反向;遮罩 `black/60` 150ms fade(实测 class)。
- **Dialog(居中弹窗)【shadcn 形态,V2 实测沿用】**:底 `--popover` / r14 / `0.8px` 边 / 阴影 `0 8px 24px rgba(20,20,19,.12)` / 宽 ≤480 / pad 24;标题衬线 16px/600;按钮右下 Primary+Ghost;遮罩 `rgba(20,20,19,.5)`→统一改 `black/60` 与 Sheet 一致;动画 150ms zoom-95+fade。

### 9.12 Toast(sonner,变量实测)

- 几何:13px / pad 16 / r10 / `1px` 边框 / 阴影 `rgba(0,0,0,.1) 0 4px 12px` / 默认宽 356px / 位置右下(sonner 默认【位置未直接观测,标注】);关闭钮 20×20 右上 `translate(35%,-35%)`。
- 亮色:`--normal-bg:#fff`、`--normal-border:var(--gray4)`、`--normal-text:var(--gray12)`;暗色:`--normal-bg:#000`、`--normal-border:hsl(0 0% 20%)`、text `gray1`。
- 语义三件套(亮):success `hsl(143 85% 96%)/hsl(145 92% 87%)/hsl(140 100% 27%)`;info `hsl(208 100% 97%)/hsl(221 91% 93%)/hsl(210 92% 45%)`;warning `hsl(49 100% 97%)/hsl(49 100% 88%)/hsl(36 84% 30%)`级;error `hsl(358 76% 96%)/hsl(357 89% 89%)/hsl(358 69% 44%)`级。(暗为对应 6%/12%/65% 深底公式,实现照 sonner dark 主题值。)
- 行为:4s 自动消失、可划走;成功复制类操作优先用**图标内联反馈**(zero.cat 实测:复制钮变 ✓,不弹 toast),toast 留给失败/异步结果。

### 9.13 终端卡 / 代码块(实测)

- 容器:r14 卡 + `0.8px` 边,顶部条 `--muted`:左红黄绿三点(8px 圆点)+ 右侧 pill Tab(Chat=brand 实心白字【映射,原橙】,其余文字态);主体 pad 16–24,Geist Mono 13–14px;字符串/URL 高亮 `--brand`【映射】;底部状态条 hairline 上边:`POST /v1/chat/completions` mono 11px muted + 右侧 `● 200 OK`(success 圆点 8px + mono 11px)。
- 用于:在线调试请求/响应窗、集成代码示例、行展开详情。

### 9.14 图表(实测)

- 柱状/堆叠柱:分段色板 §3.3,分段 1px 底色缝,无渐变无圆角(≤2px);仅横向网格线(hairline);y 轴标签 mono 10–11px muted;x 轴 mono 11px muted;卡头=icon+14px/600 标题+14px muted 副标;右上 KPI mono 24px/600+eyebrow。
- 分段进度条(健康度):约 14–16 块 h8,绿 success 系,异常掺 destructive 红。
- 禁:渐变填充、3D、发光、双轴花哨装饰。

### 9.15 骨架 / 加载

- 骨架:底 `--skeleton-base`,高亮 `--skeleton-highlight` shimmer 扫过(shimmer keyframes 实测);r8。
- 路由加载屏:品牌字标(Manrope)+ `app-loading-*` 序列,绿色化。

### 9.16 滚动条(完整规格,必须全局实现;系统默认宽滚动条=缺陷)

zero.cat 实测全局规则:**`* { scrollbar-width: thin; scrollbar-color: var(--border) transparent; }`**(所有元素,Firefox 直接生效)。V4 完整规格(WebKit+Firefox 双轨):

```css
/* Firefox(全局,含 hover 加深由 color-mix 静态近似) */
* {
  scrollbar-width: thin;
  scrollbar-color: var(--scrollbar-thumb) transparent;
}
/* WebKit(全局) */
::-webkit-scrollbar { width: 8px; height: 8px; }
::-webkit-scrollbar-track { background: transparent; }
::-webkit-scrollbar-thumb {
  background: var(--scrollbar-thumb);
  border-radius: 4px;
}
::-webkit-scrollbar-thumb:hover {
  background: var(--scrollbar-thumb-hover); /* hover 加深:亮 #c9c7bd / 暗 #4a473d */
}
::-webkit-scrollbar-corner { background: transparent; }

/* 隐藏型(横向 chip 带) */
.no-scrollbar { scrollbar-width: none; }
.no-scrollbar::-webkit-scrollbar { display: none; }
```

- track 恒透明(浮于内容上);滑块 `var(--scrollbar-thumb)`(亮 #e2e0d8 / 暗 #322f28)≈ 与 hairline 同色;hover 加深至 `--scrollbar-thumb-hover`。
- 需要隐藏滚动条的区域(筛选 chip 横条等)用 `.no-scrollbar`(zero.cat 同款类名实测存在)。
- 禁:`scrollbar-width: auto` 默认宽条、彩色滑块、粗于 8px、出现系统原生双轨。

---

## 10. 逐页布局规格

通用:每页=页头块(在内容区内部,h1 衬线 30px/600/-0.6px + 副标 14px muted + 右侧动作区)+ 区块纵向间距 24–32px;所有数字 mono;顶栏不放页标题。

### 10.1 登录页(实测,暗浅两套)

整页两栏:左 45% 品牌区——暗色摄影底(两主题恒暗)+ 暖黑遮罩,衬线 48px/600/-0.03em 字标 + 一句副标(浅色文字)+ hairline + 3 个统计(mono 24px/600 数字 + 12px muted 标签:模型数/厂商数/可用性);右 55% 表单区——底 `--background` + `--canvas-dot` 点阵,居中 384px 列:顶部右上两枚 36×36 ghost 图标钮(语言/主题);「登录」(Manrope 24/600/-0.6px)+ 16px muted 副行含注册链接(brand 色);SSO/Passkey outline 钮 h44(禁用时 opacity 50);「或继续使用」细线分隔(两侧 hairline);两输入(label 14/500 上置 + h36 输入);**反色主按钮** h36 r10(亮=黑底白字/暗=白底黑字);协议行(`--muted` 圆片容器 + 圆形勾选 + 12px 链接);12px 法律行居中。错误态:输入框 `--destructive` 边框 + 12px 错误文字。【zero.cat 原样;SSO 行按面板需求裁剪】

### 10.2 侧栏壳层【推断】

左 Sidebar 256px(§9.8)+ 主列;主列顶部 AppHeader 60px sticky(§9.9);主列内容 gutter 24px。路由切换内容区滚动,侧栏/顶栏固定。

### 10.3 总览(范式=统计带+图表卡+状态列表)

页头块(总览 h1+副标+右上时间段 underline-tabs 今天/本周/本月)→ 统计带(单个 hairline 容器 4 格:今日请求/活跃密钥/P50 延迟/成功率;mono 36 数字+12 标签)→ 两列卡(16px gap):左「24h 用量」堆叠柱图卡(chart 色板,卡右上 KPI mono 24px),右「引擎/渠道健康」列表(行=mono 名+状态 pill+分段进度条,行高 44px hairline 分隔)→ 「最近日志」hairline 表 5 行 + 「查看全部」text 钮。

### 10.4 表格页(范式=/pricing 表格视图,全实测)

页头块(h1+副标,右上搜索 h40+⌘K)→ 工具栏(左:计数 mono;右:分段控件/排序钮 h32/视图切换)→ §9.7 表格 → §9.7 分页底栏。筛选需求多时左侧加 264px 筛选白卡(r14,可折叠分组+chip,见 §9.5)。

### 10.5 表单页(在线调试/新建资源弹窗)

两栏:左参数表单(label 上 14/500,输入 h36,字段间 16px;模型 select、分组 chip 单选、系统提示 textarea)→ 右终端卡(§9.13);底部主按钮 solid 反色。弹窗表单(新建密钥):Dialog ≤480px,标题衬线 16/600,字段同上,底部 hairline 上 Primary+Ghost 双钮。嵌套分组用 `--secondary` 底 r10 pad16 次级卡(实测形态)。

### 10.6 设置页

页头块 + 左分组锚点(14px,激活 `--foreground`+`--accent` 底 r8)或纵向分组卡;每卡=衬线标题 16/600 + 14px muted 描述 + 字段(label 上、输入下、8/16 间距)+ 卡底 hairline 上「保存」solid 钮;危险区(重置/删除)独立卡 + destructive outline 钮 + 二次确认 Dialog。开关行:14px 标签+13px muted 描述,右置 switch(§9.4)。

---

## 11. 禁止清单

1. **渐变滥用**:禁止渐变按钮、渐变大色块、渐变文字、图表渐变填充;唯一允许的"装饰"=`--canvas-dot` 点阵纹理与数据本体(图表分段色)。
2. **发光标题**:标题禁 text-shadow/glow/霓虹;标题层禁止出现任何发光、描边、彩色字(标题一律 fg 色)。
3. **多强调色混用**:全站唯一强调色相=松柏绿(亮 `#177e5b`/暗 `#3ecf8e`);蓝/橙/紫等其他色相禁止进入 UI(链接/选中/徽标/图表主系);语义色(success/warning/destructive/info)只用于状态语义,不得当装饰、链接或选中底色;图表多色仅限 §3.3 色板;CTA 绿色单屏 ≤1 处。
4. **系统默认宽滚动条**:视为缺陷;必须按 §9.16 全局实现细滚动条(Firefox `scrollbar-width:thin`+`scrollbar-color`,WebKit 8px 滑块/hover 加深/track 透明);`no-scrollbar` 只用于明确隐藏区。
5. 圆角 >14px 禁止(仅状态 pill/端点 chip 允许 full)。
6. 重阴影禁止(卡片/按钮 shadow 必须为 none;仅浮层一档阴影);禁止 border>0.8px 的粗边框(状态 pill 描边除外)。
7. 时长 >200ms 的界面动画与弹性缓动禁止(例外:按钮回弹 400ms 过冲、抽屉 200ms——均为指定曲线,不得自造)。
8. 衬线禁止进入表格/按钮/表单/代码;mono 禁止进入标题层;中文标题不得出现黑体加粗替代衬线。
9. 左侧 2px 指示条式选中态禁止(侧栏项/Tab/行选中一律底色高亮)。
10. 面板文案不得出现 config.yaml 配置键(既有红线);深蓝代码底(#09093f 类)禁止,一律终端卡范式。
11. 禁止斑马纹表格(行分隔只用 hairline);禁止表头大底色块。

---

## 12. 证据清单与未验证项

| 结论 | 依据(本次会话实际执行) |
|---|---|
| 登录墙 | 打开 `/dashboard/overview` → 302 `/sign-in?redirect=…`;截图 `00-login-wall.png` |
| 全量 token(§3) | 浏览器提取 `:root` 与 `.dark` 声明块原文(本报告记录了完整 cssText) |
| 品牌橙原值 | 同上(`--brand:#c2410c/#ff8a3d`;CTA `#ed5b20` 实测于首页 CTA computed style) |
| 字体三族+Manrope | body/标题 computed font-family 原串;`#app-loading .brand` 规则 |
| 字阶/几何(§5–7) | 登录页、首页、pricing、rankings 各元素 computed style(字标 48/600/-1.44px;hero 72/500;CTA h48/px28/r10;导航项 h32/r8;输入 h36/r10/4px12px;统计带 mono 36/600/-0.72px 等) |
| 表格全指标(§9.7) | pricing 表格视图:thead h40、行 h85、td 12×8、hover `--accent/30`(120ms)、行框 亮#e2e0d8/暗#393730、端点 chip h20 r-full、类型 chip r8 染色 8%/30%、分页形态、选中行 brand 7% 混色规则(样式表规则) |
| 交互态 | CUA 悬停实测行 hover 底色(亮 accent/30、暗 #2e2b26/30);`focus-visible` outline 2.4px ring/50(样式表+computed);菜单开合、Sheet 开合实测 |
| Sheet 抽屉(§9.11) | class 原文:`duration-200 ease-in-out … data-starting-style:translate-x-[2.5rem] border-l bg-background`;遮罩 `bg-black/60 duration-150`;宽 672 实测 |
| 细滚动条(§9.16) | 样式表实测 `* { scrollbar-width: thin; scrollbar-color: var(--border) transparent }` + `.hover-scrollbar` WebKit 8px 规则 |
| 动效(§8) | 40 个 keyframes 与 transition 规则扫描(`.btn-squish`、`accordion-down/up`、`enter/exit` 150ms、sonner、`tableRowEnter` 等) |
| 主题切换(§2) | 三态菜单 DOM 点击实录;html class `font-inter light/dark` 切换实证 |
| 未验证项(如实) | ① 登录后壳层侧栏可视宽度/折叠交互/总览页真实布局——【推断】,实现时按 shadcn Sidebar 默认落地并留调整余地;② sonner toast 位置与自动消失时长——默认值;③ 骨架 shimmer 循环时长、图表生长动画时长——未精确实测;④ 表格排序点击后的箭头方向态——未逐一枚举;⑤ 空表格空态——本轮未观测到,沿用 V2 点阵范式 |
| 截图 | `qa/panel-ref/`:00-login-wall / 01-signin-dark / 02-signin-light(含浅色主题菜单开态) / 03-home-light-top / 04-home-light-stats-features / 04b-home-light-code-footer / 06-pricing-light-top / 07-pricing-light-table / 08-pricing-light-table-bottom / 09-rankings-light-top / 10-pricing-light-detail-drawer / 11-pricing-light-sheet-full / 12-pricing-dark-top / 13-pricing-dark-table / 14-rankings-dark-top / 15-home-dark-hero / 16-topbar-dark(共 17 张,均本次会话生成;编号 05 缺号为失败的滚动截图,已弃用) |

**交接备注**:本文件为阶段产出(将来落盘 `web/admin/STYLE-v4.md`),本轮未写仓库任何文件;实现时新建 `:root`/`.dark` 变量层整体替换旧 token,先全局铺 0.8px 边框/r 档/200ms/细滚动条,再逐页对表 §10,验收对照 `qa/panel-ref/` 截图。
