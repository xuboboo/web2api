# Web2API 管理控制台 · 设计规范

> 设计参考:[幻方量化官网](https://www.high-flyer.cn/)。气质关键词:**极简、强科技感、大数字数据排版、克制硬核工程师审美**。
>
> 本规范中的 token 均为 2026-11-15 在 1280×720 视口下,对参考站首页(`/`)与内容页(`/blog/`)通过浏览器计算样式(getComputedStyle)与 `:root` CSS 变量实测提取,非主观推断。原始截图见 `qa/reference/highflyer-home.png`(首页首屏)与 `qa/reference/highflyer-sub.png`(技术博客列表页)。

---

## 1. 设计基因(一段话)

以纯黑/纯白为底,高对比排版;品牌蓝 `#2e55a5` 只做"稳重主色"(按钮、选中态、卡片标题),亮蓝 `#2997ff` 只做深色底上的激活/链接色;数据永远以"超大加粗数字 + 小号灰色标签"的形式出现;全站无重阴影、无渐变按钮、无花哨圆角——分隔靠 1px 细线(hairline),层级靠字重(400/600/700)与字号跳变,装饰几乎为零,信息密度让位于留白。

---

## 2. 设计 Token 表

### 2.1 色板

| Token | 值 | 实测出处 | 用途 |
|---|---|---|---|
| `--hf-primary` | `#2e55a5` | 参考站 `--theme-color` / `--color-primary`;博客页选中 pill 的 bg `rgb(46,85,165)`,侧栏子页底色,卡片标题色 | **主色**:主按钮、选中态、图表主色线、深色链接标题 |
| `--hf-active` | `#2997ff` | 参考站 `--active-color`;侧栏激活项文字 `rgb(41,151,255)`,「数据来源>」链接色 | **激活/链接色**(深色底上必用它而非主色;浅色底链接可用) |
| `--hf-gold` | `#ffcf63` | 参考站 `--hf-gold` | 极少量点缀(徽标、高亮徽章),页面出现 ≤1 处 |
| `--hf-navy` | `#09093f` | 页脚实测 `rgb(9,9,63)` | 页脚 / 深色横幅(比纯黑柔一档的深蓝黑) |
| `--hf-dark` | `#000000` | 首页侧栏实测 `rgb(0,0,0)`(nav.main-nav fixed) | 首屏/侧栏/深色区块底色 |
| `--hf-dark-panel` | `#282828` | 参考站 `--index-dark-layout` | 深色区块内的次级面板 |
| `--hf-dark-tint` | `rgba(126,168,255,0.13)` | 首页大数字统计面板 `arch-data-number-container` 实测 | 深色底上的"数据面板"底色(蓝色 13% 透明 tint) |
| `--hf-pill-dark` | `rgba(135,135,140,0.5)` | 首页标签 pill(3FS)实测 | 深色底上的选中 pill |
| `--hf-bg` | `#ffffff` | 博客列表页背景(白) | 内容区底色 |
| `--hf-bg-soft` | `#f5f5f7` | 参考站 `--index-main-content-color`(同时是深底主文字色,黑白复用) | 浅色底上的卡片/表头底 |
| `--hf-hairline` | `#e8e8ed` | Apple 系 hairline(参考站 `#d2d2d7`≈偏深变体,浅底分隔线建议此值) | 表格行线、卡片描边、分割线 |
| `--hf-heading` | `#1d1d1f` | 参考站 `--index-main-content-color-reverse`(深底文字的反色,即浅底标题色);`--color-heading: #1a202c` 近似 | 浅色底标题 |
| `--hf-text` | `#2e353f` | 参考站 `--color-text` | 浅色底正文 |
| `--hf-text-light` | `#4f5969` | 参考站 `--color-text-light` | 次级正文 |
| `--hf-muted` | `#86868b` | 参考站 `--index-sub-content-color-2`;页脚 ICP 链接实测 `rgb(134,134,139)` | 弱化文字、占位符、表头、时间戳 |
| `--hf-on-dark` | `#f5f5f7` | 参考站 `--index-main-content-color`;首页大标题实测 `rgb(255,255,255)`、正文 `rgb(245,245,247)` | 深色底主文字 |
| `--hf-on-dark-2` | `#d2d2d7` | 参考站 `--index-sub-content-color`;首页 hero h3 实测 `rgb(210,210,215)` | 深色底次级文字 |
| `--hf-tip` | `#677b8c` | 参考站 `--tip-color`;博客页「分类」「最新发布」label、pill 描边实测 | 浅色底上的说明文字、pill 描边 |
| `--hf-meta` | `#ccd1d4` | 参考站 `--meta-color`;博客卡片日期实测 | 元信息(浅灰,仅浅底) |

> 语义色补充:参考站**没有红色/绿色语义色**。管理台需要错误/危险操作时,允许低饱和引入 `#c23b3b`(错误文字/危险按钮),成功态用主色 `#2e55a5` 表达;这是对参考语言的最小扩展,不得扩大使用范围。

### 2.2 字体

| Token | 值 | 实测出处 |
|---|---|---|
| `--hf-font-sans` | `-apple-system, "Noto Sans", "Helvetica Neue", Helvetica, Arial, "PingFang SC", "Hiragino Sans GB", "Noto Sans CJK SC", "Source Han Sans SC", "Microsoft YaHei", SimHei, sans-serif` | 参考站 body 实测 font-family |
| `--hf-font-mono` | `"Roboto Mono", "HFRoboto Mono", ui-monospace, "SF Mono", Consolas, monospace` | 参考站终端代码行实测 `"HFRoboto Mono"`(自定义 Roboto Mono);管理台用开源 Roboto Mono 等价替换 |

- 正文字号 `16px`/行高 `1.5`(参考站 `--fontSize-root: 16px`, body 16px/24px 实测);管理台信息密度更高,正文可降到 **14px/1.5**,但数字与代码必须用 mono。
- 代码/密钥/时间戳/数字列一律 mono,可加 `letter-spacing: 0.02em`(参考站终端行实测 `letter-spacing: 2px` 的收敛版)。

### 2.3 字号 / 字重阶梯(实测值)

| 级别 | 参考站实测 | 管理台映射 |
|---|---|---|
| Display(大数字) | `56px / 700`(首页 96%、85% 实测) | 总览大数字卡:36–56px/700 |
| Title-1 | `40px / 700 / #f5f5f7`(深色区标题 h3 实测) | 登录页主标题 40px/700 |
| Title-2 | `36px / 400 / #fff`(hero h2 实测,行高 1.5) | 深色横幅标语 |
| Title-3 | `20px / 700 / #1d1d1f`(博客详情卡标题,近黑) | 页面标题(topbar) |
| Title-4 | `18px / 700 / #2e55a5`(博客列表卡标题实测) | 卡片标题、弹窗标题 |
| Body | `16px / 400` | 正文默认 14–16px |
| Small | `14px / 600 / #fff`(侧栏项实测) | 表格正文、按钮文字 |
| Caption | `12px / 400 / #86868b`(页脚 ICP 实测) | 表头、时间戳、帮助文字 |
| Unit | `22px / 700 / #d2d2d7`(大数字的单位 %、TB/s 实测) | 数字后缀单位 |

- 字重只用三档:`400 / 600 / 700`(参考站 `--fontWeight-*` 变量表)。
- 行高:`1 / 1.1 / 1.5`(参考站 `--lineHeight-none/tight/normal`);大数字用 1,标题用 1.1–1.3,正文用 1.5。

### 2.4 间距节奏(参考站 `--spacing-*` 变量表,4px 基网格)

`4 / 8 / 12 / 16 / 20 / 24 / 28 / 32 / 36 / 64 / 80 / 96 / 128`

- 组件内边距:小(pill/按钮)`0 10px`(pill 实测)~`4px 12px`;卡片 `20–24px`;区块 `32–64px`;页面级留白 `64–96px`。
- 内容最大宽度:参考站 `--maxWidth-wrapper: 60rem(960px)`;管理台为满幅工作台,内容区给 `max-width: none`,但表格与卡片间距遵守同一 4px 网格。

### 2.5 圆角 / 边框 / 阴影

| Token | 值 | 实测出处 |
|---|---|---|
| `--hf-radius-sm` | `4px` | 「快速体验」按钮实测 `border-radius: 4px` |
| `--hf-radius-pill` | `30px`(即 full) | 博客分类 pill 实测 `border-radius: 30px` |
| `--hf-radius-md` | `8px` | 管理台卡片/弹窗(参考站整体趋平,取 4–8px 区间收敛值) |
| `--hf-border` | `0.8px solid`(hairline) | 按钮与 pill 实测 `0.8px solid`;浅底用 `1px solid var(--hf-hairline)` |

- **阴影几乎禁用**:全站卡片无 box-shadow(博客卡片实测 `box-shadow: none`)。管理台最多允许 `0 1px 2px rgba(0,0,0,.04)`,弹窗允许 `0 8px 24px rgba(0,0,0,.12)`。
- 层级感靠**细线 + 底色差**,不靠投影。

### 2.6 按钮样式(实测)

| 类型 | 样式(实测基准) |
|---|---|
| Primary(实心) | 参考 pill 选中态:bg `#2e55a5`、白字 `16px/400`、`radius 4px`(pill 为 30px)、`padding 8px 16px`,hover `#25468a`,无渐变无边框 |
| Ghost(描边) | 参考「快速体验」按钮:bg 透明、`border 0.8px solid #f5f5f7`(浅底改 `#677b8c`)、字色同边框、`14px/400`、`radius 4px` |
| Text(链接) | `#2997ff`(深底)/ `#2e55a5`(浅底),可带 `>`,hover 加下划线 |
| Danger | `#c23b3b` 描边 ghost(扩展语义色,见 2.1 备注) |

---

## 3. 映射到管理控制台(组件级实现指引)

参考站有两种"界面语言",按页面性质取用:

- **深色语言**(首页 index 区):黑底 + 白大字 + 大数字 + 蓝色 tint 面板 → 用于**总览页头部横幅、登录页**;
- **浅色语言**(博客/内容区):白底 + 黑标题 + 蓝色标题/选中 + hairline 表格 → 用于**所有日常工作区**(表格、表单、弹窗)。

### 3.1 侧边栏(对应参考站 110px 固定左侧栏)

- 固定左侧、`width: 200px`(管理台需容纳图标+文字,取参考站"窄黑条"的放宽版)、`bg: #000`、全高。
- 品牌区:顶部 logo + 名称,白字 `16px/700`,与菜单间以 1px `rgba(255,255,255,.12)` 细线分隔。
- 菜单项:`14px / 600`,白字,图标 16px 同色;**间距 `padding: 12px 16px`**。
- 激活态:文字与图标变 `#2997ff`,左侧 **2px 蓝色指示条**(参考站首页侧栏激活项实测样式);hover:白字降为 `#d2d2d7`。
- 备选形态(登录后首屏/深色主题):整条侧栏可用 `#2e55a5`(参考站子页面侧栏的蓝色形态),激活项改白底蓝字或白字+深一档蓝。

### 3.2 顶栏

- 白底、`height: 56px`、**无投影**,仅 `border-bottom: 1px solid var(--hf-hairline)`。
- 页面标题:`20px / 700 / #1d1d1f`,左对齐;右侧操作区(用户名/退出)用 `14px` + ghost 按钮。

### 3.3 统计卡(对应首页大数字排版——本套设计最重要的组件)

- 浅色版:白底卡片、`radius 8px`、`padding 24px`;**数字 `36–56px / 700 / #1d1d1f / line-height 1`**,单位跟随数字但 `16–22px / 700 / #86868b`,标签 `14px / 400 / #677b8c` 置于数字下方。
- 深色版(总览页横幅):`bg #000`(或 `--hf-navy`),数字 `#f5f5f7` 56px/700,标签 `#d2d2d7`,多指标并排时放入 `rgba(126,168,255,0.13)` tint 面板(`padding: 12px`,无圆角,如参考站实测)。
- 每卡只表达**一个指标**;辅助趋势用 12px 灰字,不加迷你图装饰。

### 3.4 表格

- 表头:`12px / 600 / #86868b`,`bg: #f5f5f7` 或白底+`border-bottom: 1px hairline`,不加粗黑底。
- 行:`14px / 400 / #2e353f`,`height: 44px`,`border-bottom: 1px solid var(--hf-hairline)`,行 hover `#f5f5f7` 底,**选中行左边 2px `#2e55a5` 指示条**。
- ID/密钥/IP/时间戳列用 mono 字体;数字列右对齐;状态列用 pill(见 3.7)。
- 分页器:纯文字按钮 + hairline 描边,选中页实心 `#2e55a5`,风格同 pill。

### 3.5 图表

- 主线 `#2e55a5`,对比线 `#2997ff`,最多两条;无面积渐变,如需填充用 `rgba(46,85,165,0.08)`。
- 网格线 `1px var(--hf-hairline)` 横向 only;坐标轴文字 `12px / #86868b`,**数字轴用 mono**。
- 柱状图圆角 2px;tooltip:白底、1px hairline、无阴影或极浅阴影。

### 3.6 弹窗

- 遮罩 `rgba(0,0,0,.5)`;面板白底、`radius 8px`、`width ≤ 480px`、`padding 24px`。
- 标题 `18px / 700 / #1d1d1f`;正文 `14px / #4f5969`;按钮右下角,Primary + Ghost 组合;危险确认按钮用 `#c23b3b` 描边。
- 关闭按钮 `#86868b`,hover 变黑。

### 3.7 表单 / 输入 / pill

- 输入框:白底、`border: 1px solid #d2d2d7`、`radius 4px`、`height: 36px`、`14px`;focus 边框 `#2997ff` + `box-shadow: none`;错误态边框 `#c23b3b`,错误文字 `12px / #c23b3b`。
- 状态 pill:`radius 30px`、`padding: 2px 10px`、`12px/600`;激活/正常=实心 `#2e55a5` 白字;待处理=描边 `#677b8c`(参考站未选中 pill 实测:border `0.8px solid #677b8c`、字同色、bg 透明);停用=`#86868b` 文字无框。
- 筛选 pill 行(如日志级别筛选):照抄博客页分类栏——「分类」label `16px/600 #677b8c` + pill 组,选中实心蓝、未选中描边灰。

### 3.8 页脚(可选)

- `bg: #09093f`、`padding: 40px 0 60px`、白标题 + `#86868b 12px` 说明与版本号;一行细线(`rgba(255,255,255,.12)`)分隔版权区。

### 3.9 登录页

- 整页 `bg #000`(可用星空/噪点暗纹,克制);居中白卡 `radius 8px`、`padding 40px`、宽 `360px`。
- 主标题 `40px/700 #1d1d1f` 可简化为 `28px/700`;副标题 `14px #86868b`;输入框与主按钮照 3.7/2.6。

---

## 4. 禁止事项(保持"克制硬核"气质)

1. 禁止渐变按钮、彩色大底块、重投影卡片。
2. 禁止超过两种的强调色同屏(`#2e55a5` + `#2997ff` 已足够;金色 `#ffcf63` 全页 ≤1 处)。
3. 禁止圆角 >8px 的卡片(仅 pill 允许 full)。
4. 禁止给表格加斑马纹——层级靠 hairline 与字重。
5. 大数字组件的数字禁止小于 36px 或使用非 mono/非 700 字重。
6. 深色区块上的链接/激活态一律 `#2997ff`,浅色区块一律 `#2e55a5`,不得混用。

---

## 5. 证据清单

| 结论 | 依据 |
|---|---|
| 全部色值/字号/字重/间距 | 2026-11-15 浏览器实测 `https://www.high-flyer.cn/` 与 `/blog/` 的 getComputedStyle 与 `:root` CSS 变量(如 `--theme-color:#2e55a5`、`--active-color:#2997ff`、`--hf-gold:#ffcf63`、`--tip-color:#677b8c`、标题 56/40/36px 等) |
| 侧栏布局/激活态 | 首页 `nav.main-nav` 实测:fixed、110px、bg #000;激活项 `#2997ff` |
| 大数字排版 | 首页 `arch-data-number-container` 实测:56px/700 数字、22px 单位、面板 `rgba(126,168,255,.13)` |
| 按钮/描边风格 | 「快速体验」按钮实测:border `0.8px solid rgb(245,245,247)`、radius 4px、bg 透明 |
| 卡片/pill | 博客页实测:卡片标题 `18px/700 #2e55a5`;pill radius 30px、选中实心蓝/未选中描边 `#677b8c` |
| 页脚 | 实测 `bg rgb(9,9,63)`、ICP 链接 `12px #86868b` |
| 截图 | `qa/reference/highflyer-home.png`(首页首屏)、`qa/reference/highflyer-sub.png`(技术博客页) |

---

## 6. 管理台实现补记(2026-11-15 二期面板交付)

按本规范完成 `web/admin/` 全站改造时,做了两处**已记录的最小扩展**与组件落地说明:

1. **语义色扩展(在 2.1 备注之上新增一档)**:管理台需要表达"冷却 / 4xx"这类"临时不可用"状态,引入低饱和琥珀 `--warn: #a16207`(冷却 pill、冷却引擎卡描边、登录锁定提示框)。与既有的错误色 `#c23b3b` 同属最小扩展,成功态仍用主色 `#2e55a5` 表达,禁止再扩大。
2. **pill 语义映射(3.7 的落地)**:正常/启用/2xx = 实心主色白字(`.pill-green`,类名沿用旧代码);停用 = 灰字无框(`.pill-gray`);冷却/4xx = 描边 `--warn`(`.pill-amber`);熔断/5xx/危险 = 描边 `#c23b3b`(`.pill-red`);中性强调 = 描边主色(`.pill-indigo`)。
3. **代码块底色**复用页脚深蓝黑 `#09093f`(2.1 `--hf-navy`),替代通用的 slate 深灰,保持品牌一致性;代码文字 `#d2d2d7`。
4. **大数字组件**:总览统计卡数字 `40px/700/mono/line-height 1`,标签在数字上方(`14px #677b8c`)、辅助信息在下(`12px #86868b`);引擎冷却卡的倒计时数字 `36px/700/mono`。
5. **favicon**:SVG data-uri(品牌蓝圆角方块 + 白色闪电,与侧栏 logo 同形),`index.html` 内联,无额外文件请求。

