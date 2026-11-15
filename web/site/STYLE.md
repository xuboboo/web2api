# Web2API 官网设计语言规范(STYLE.md)

> 参照站:**DeepSeek Harness**(https://www.deepseek.com/harness/)+ **zero.cat**(https://zero.cat/)。
> 本文档所有数值均来自 2026-11-15 对两站的**浏览器实测**(1440×900 视口,`getComputedStyle` 读取与截图取证),
> 证据截图存于 `qa/reference/site-*.png`。标注「视觉读数」的条目为截图目测,非 computed style,工程实现时可微调。

---

## 0. 两站路线与我们的取舍

| | DeepSeek Harness(暗夜产品流) | zero.cat(暖纸衬线流) |
|---|---|---|
| 底色 | `#0A0A0A` 纯黑页 | `#FAF9F5` 暖米白页 |
| 标题字体 | Montserrat / DM Sans(无衬线, 40px w400) | **Anthropic Serif / Lora / Noto Serif SC(衬线, 72px w500)** |
| 强调色 | 无彩色强调,靠白/灰层级 | **橙色 `#ED5B20`**(按钮实色,实测 `rgb(237,91,32)`) |
| 导航 | fixed 透明 → 滚动后变圆角胶囊条 | sticky 高 65px,**毛玻璃** `backdrop-filter: blur(12px)` |
| 区块留白 | 上下各 **160px**(实测 `pt-ds-11`) | 约 96–120px(视觉读数) |
| 动效 | 入场 opacity+translateY 渐显(JS 驱动),`prefers-reduced-motion` 降级 | `landing-fade-up` 关键帧 + **logo 无缝跑马灯 40s** |

**Web2API 官网采用:zero.cat 的「暖纸衬线」为主线(导航/Hero/正文),内嵌 DeepSeek 的「暗色产品窗口」语言展示 API/终端/代码。
即:纸面上放一台机器 —— 亮色页面承载叙事,深色窗口承载「产品感」。两站均默认亮色主题,同时各自提供完整暗色主题(见 §2)。**

---

## 1. 设计 Token 表

### 1.1 颜色(实测自 zero.cat CSS 变量)

亮色主题(默认,`:root`):

| Token | 值 | 用途 |
|---|---|---|
| `--background` | `#FAF9F5` | 页面底色(暖纸白) |
| `--foreground` | `#141413` | 主文字/标题墨色 |
| `--muted-foreground` | `#5E5D59` | 次级文字/正文灰 |
| `--muted` | `#EFEEE8` | 弱底色(标签、代码块底) |
| `--accent` | `#EAE8E0` | hover 底、icon 头部区 |
| `--border` | `#E2E0D8` | 全部 1px 描边 |
| `--card` | `#FAF9F5` | 卡片底(卡片靠 border 区分,不靠底色) |
| `--primary` | `#141413` | 主按钮深色态 |
| `--radius` | `0.625rem`(10px) | 全局圆角基准 |

强调橙(按钮实测 `rgb(237,91,32)`):

| Token | 值 | 用途 |
|---|---|---|
| `--brand` | 亮色态 `#ED5B20`;暗色态 `#FF8A3D` | 主 CTA、链接 hover、图表主色、状态点 |
| brand-foreground | `#141413`(深墨字) | **橙底上放深字,不放白字**(zero.cat 实测) |

暗色主题(zero.cat `.dark` 类,实测):
`--background:#141413` / `--foreground:#F3F1EB` / `--card:#1D1C19` / `--secondary:#262420` /
`--accent:#2E2B26` / `--muted-foreground:#A8A69F` / `--destructive:#E05A5A` / `--brand:#FF8A3D`。
图表序列色:`#FF8A3D #8089FF #00C386 #00B1FB #FFA824`。

DeepSeek 暗色组件语言(实测,用于「产品窗口」):
- 页面底 `#0A0A0A`;标题 `#FFFFFF`;正文 `rgba(255,255,255,.5)`(50% 白,16px/25.6px);
- 卡片底 `rgba(255,255,255,.02)` + 边框 `0.8px solid rgba(255,255,255,.06)`,radius 16px,padding 40px;
- 代码 diff:增 `+` 绿 / 删 `−` 红,行号灰(见 `qa/reference/site-deepseek-capabilities.png`)。

语义色:成功/在线 = 绿点(200 OK,视觉读数 `site-zero-code.png` 右下角);危险 = `#E05A5A`。

### 1.2 字体栈(实测)

| 角色 | 字体栈 | 出处 |
|---|---|---|
| 标题(衬线) | `"Anthropic Serif", "Lora Variable", Lora, "Source Serif Pro", "Source Serif 4", "Noto Serif SC", "Noto Serif TC", "Noto Serif JP", serif` | zero.cat h1/h2 computed font |
| 正文(无衬线) | `"Inter Variable", system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif` | zero.cat body |
| 数字/代码(mono) | `"Geist Mono", "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, monospace` | zero.cat 统计数字(36px w600) |
| 暗窗口内英文 | Montserrat/DM Sans 系为 DeepSeek 自用,**我们不引入**,窗口内一律 mono | — |

中文标题依赖 Noto Serif SC 兜底(思源宋体),需在 `<head>` 预载 `Noto Serif SC` 500/700 两档;正文中文由 Inter 的中文回退(system-ui)承担,不额外加载黑体,控制首屏字节。

> **实现取舍(官网工程师写回,2026-11-15)**:本站受「零依赖、零构建、禁止 CDN/外部字体」契约约束,**不预载任何字体文件**(§1.2:71 的预载要求如实降格)——标题衬线按上述栈在本机回退(Windows 下命中 Noto Serif SC 系统安装版或其他衬线中文回退),正文由 system-ui 承担,mono 由 ui-monospace/Consolas 承担。视觉在装有思源宋体的机器上为完整形态,裸机上标题仍可读(serif 回退)。

### 1.3 字阶(实测)

| 级别 | 值 | 用法 |
|---|---|---|
| Hero H1 | `72px / 70.5px`,w500,`letter-spacing:-1.44px`,衬线,`#141413` | 首页大标题(两行短句) |
| 区块 H2 | `36px/45px` w700 或 `30px/36px` w700,衬线 | 「为什么选择我们」「3 行代码,接入所有模型。」 |
| Eyebrow | `12px/16px` w600,`#5E5D59`,置于 H2 上方 | 「核心优势」「聚合全球领先 AI 厂商」 |
| Hero 副文 | `18px/28px`,`#5E5D59`,最大宽 ~489px | Hero 段落 |
| 卡片 H3 | `18–20px` w600(视觉读数),`#141413` | 特性卡标题 |
| 正文 | `16px/26px`(区块)/ `14px` 辅助 | — |
| 统计数字 | `36px` w600 mono | 61+ / 99.9% / 13+ / 24/7 |
| 导航链接 | `14px` w500,激活 `#141413`,其余 `#5E5D59` | — |
| 按钮 | `16px` w500 | 主/次 CTA |

> 参考:DeepSeek H1 为 `40px/60px` w400–500 `ls:-0.8px`(无衬线)。若做暗色「发布页」变体可沿用其克制的无衬线大标题;官网主站统一衬线。

### 1.4 布局与间距

- 内容容器:`max-width:1056px`(zero.cat 特性卡区实测宽 1056)+ 左右 24px;导航容器同宽。全宽区(跑马灯、超大水印)破格。
- 区块垂直节奏:大区块间距 `96px`(常规)/`160px`(hero 与首区块之间,借 DeepSeek 实测值)。
- 区块内标题→内容:`32px`;卡片间距:`24px`。
- 卡片:radius `10px`,border `1px solid var(--border)`,padding `24px`;DeepSeek 式暗窗口卡:radius `16px`,padding `32–40px`。
- 特性卡头部 icon 区:浅纹底(暗色主题点纹 `--canvas-dot:#f3f1eb0d`),高 ~96px,线性 icon 居中(视觉读数 `site-zero-features.png`)。
  **亮色值写回(官网工程师,2026-11-15)**:`--canvas-dot` 亮色取 `#1414130d`,来源为后台勘察 `web/admin/STYLE-v2.md` §3.1 实测成对值(亮 `#1414130d` / 暗 `#f3f1eb0d`),两主题点纹密度一致。

### 1.5 按钮(实测)

| 形态 | 规格 | 出处 |
|---|---|---|
| Primary(橙) | 高 `48px`,`padding:0 28px`,`radius:10px`,bg `#ED5B20`,字 `16px` w500 `#141413`,尾部 `→` 箭头 | zero.cat「开始使用」「创建一个账户」 |
| Secondary | 高 48px,白/纸底 + `1px solid var(--border)`,深字 | 「浏览模型」 |
| 深色底上的白胶囊(暗窗口内) | `radius:100px`,深字 `#0A0A0A`,14px w500;split 变体=主按钮+chevron 两段圆角拼接(`100px 0 0 100px` / `0 100px 100px 0`) | DeepSeek「下载 Windows 版」「下载 ⌄」 |
| 导航小按钮 | 高 `30px`,radius 100px | DeepSeek 滚动态导航 |
| 登录(描边) | 高 ~36px,`1px solid var(--border)`,radius 10px | zero.cat 导航右侧 |

---

## 2. 导航栏规范

**默认形态(zero.cat,实测):**
- `position: sticky; top:0`,高 **65px**;
- 背景 `rgba(250,249,245,0.95)` + **`backdrop-filter: blur(12px)`**(毛玻璃,实测 oklab 值 ≈ #FAF9F5/95%);
- 底部 `1px solid var(--border)`;暗色主题同构(bg #141413/95% + blur)。
- 结构:左 = cat logo(36px 见方,视觉读数)+「Web2API」wordmark(14px w400→品牌名 w500);
  右 = 链接组(14px w500,间距 32px)+ 分隔线 + 图标按钮(语言/主题切换/通知+数字徽标)+「开始使用」橙按钮(或描边「登录」)。
- 激活项 `#141413`,非激活 `#5E5D59`;hover 变 `#141413` + `background: var(--accent)` 圆角底(视觉读数)。

**可选变体(DeepSeek 滚动切换,实测):**首屏 fixed 透明无底;滚过 hero 后,导航内容包进一个 `rounded-full` 深灰胶囊条(z-index 50,宽 `min(100%-48px,1140px)` 居中,顶部留 8px),同时右侧从「语言切换」切换为「GitHub 深色胶囊 + 白色下载 split 胶囊」。
适合做「首屏纯净 → 滚动后行动入口突出」的发布页变体;主站先用 sticky 毛玻璃形态。

---

## 3. 官网区块逐个怎么用这套语言

按单页官网自上而下。每条 = [构图] + [用到的 token] + [动画]。

1. **导航** — §2 默认形态。滚动 >8px 时给底边框上色加深(`transition: border-color .3s`)。
2. **Hero(左文右图)** — 左:`eyebrow(12px 灰)→ H1 衬线两行(≤12 字/行)→ 18px 副文(489px 宽)→ [橙 CTA+→] [白 CTA]`;
   右:**负载均衡示意图**(zero.cat 构图,实测 `site-zero-hero.png`):一张「请求卡」(mono 12px,JSON 三行,border 卡)→ 实线连到**橙色枢纽圆点**(外圈呼吸光环,见 §4 脉冲)→ 虚线扇形分发到 5 张「站点卡」(站点名 + mono 小字接口路径)。Web2API 语境:请求卡=「GET /v1/lookup?domain=example.com」,站点卡=收录查询/批量校验/监控/MCP 等出口。
   入场:子元素依次 `landing-fade-up`(§4,递增 delay 240ms)。
3. **数据源跑马灯** — eyebrow「聚合权威收录数据源」+ 单行 logo/名称循环条:
   `w-max` 双份列表 + `animation: marquee-x 40s linear infinite`(实测),hover 暂停(`animation-play-state: paused`),两端渐隐 mask。灰度渲染,hover 单项恢复彩色。
4. **统计条** — 4 格横排,格间 1px 竖边框(不是 4 张卡),数字 mono 36px w600 + 12px 灰 label(已收录站点数 / 日均查询 / 数据源 / 可用性 99.9%)。数字进入视口时 rAF 计数(§4)。
5. **能力卡 ×4** — H2(eyebrow「核心优势」+「为什么选 Web2API」)→ 4 列卡:浅纹 icon 头部(线性图标,`--accent` 底)+ H3 + 16px 灰文。卡片纯 border 语言,**禁投影**。
6. **代码接入(左文右窗)** — 左:H2「3 行代码,接入所有模型。」+ 步骤 pill(①编号圆徽+文案,箭头连接)+ CTA 行;
   右:**暗色终端窗口**(DeepSeek 卡语言):radius 16px、`#0A0A0A` 底、顶栏 tab 胶囊(激活=橙底深字)、`请求`/`响应` 分块、mono 语法高亮(字符串/URL 橙)、窗口底栏 `POST /v1/chat/completions` + 绿点 `200 OK`。tab:cURL / Python / Node / MCP。
7. **开发者/自托管区(DeepSeek 语言,亮页放暗卡)** — 两列暗色卡(radius 16px,`rgba(255,255,255,.02)` 底 + `rgba(255,255,255,.06)` 边,置于亮页时直接用 `#161615` 实底更稳):「命令启动」`curl -O ... && ./web2api serve`、「源码安装」`git clone …`,代码块带「复制」按钮(点击变「已复制」1.5s)。
8. **生态 CTA 区** — 居中 H2「加入 Web2API 生态」+ 两行居中副文 + 按钮组(1 白胶囊主 + 3 深色胶囊次:API 文档 / MCP Server / GitHub)(DeepSeek `site-deepseek-dev.png` 构图)。
9. **页脚** — logo+tagline → `1px var(--border)` 分隔 → 版权行(12px 灰)+ 链接;最底部**超大淡橙品牌字水印**「Web2API」(8–10vw,`#FF8A3D` 8% 不透明度,截断出血,`site-zero-code.png` 页脚实测构图)。

---

## 4. 动画实现要点(IntersectionObserver 模式)

### 4.1 两站实测的动画词汇

zero.cat(直接读自其 stylesheet 的 @keyframes):
```
landing-fade-up  { 0% { opacity:0; transform: translateY(24px); } 100% { opacity:1; transform: translateY(0); } }
landing-fade-in  { 0% { opacity:0; } 100% { opacity:1; } }
app-loading-rise { 100% { opacity:1; transform: translateY(0); filter: blur(0px); } }  /* 模糊升起 */
app-loading-pulse{ 50% { transform: scale(1.02); } }          /* hub 呼吸 */
marquee-x        { translateX 无缝循环 }                        /* 实测 animation: 40s infinite */
skeleton-shimmer { background-position 位移 }
```
入场类:`landing-reveal`,duration `0.8s`,配 `[animation-delay:240ms]` 等递增延迟(实测内联 Tailwind 任意值)。

DeepSeek:区块容器带 `entrance` class,JS 驱动 opacity/translateY(实测中间态 opacity .857、translateY 2.7px),并配
```
@media (prefers-reduced-motion: reduce) { .entrance { opacity:1 !important; transform:none !important; } }
```
演示动画卡带「暂停演示」按钮(无障碍)。

### 4.2 统一实现模板(官网工程师照抄)

```css
/* 1) 初始态 & 过渡 */
.reveal { opacity: 0; transform: translateY(24px);
  transition: opacity .8s cubic-bezier(.16,1,.3,1), transform .8s cubic-bezier(.16,1,.3,1);
  transition-delay: var(--reveal-delay, 0ms); will-change: opacity, transform; }
.reveal.is-visible { opacity: 1; transform: none; }
/* 2) 无 JS 回退:html.no-js 下全部直接可见 */
/* 3) 降级 */
@media (prefers-reduced-motion: reduce) {
  .reveal { opacity: 1; transform: none; transition: none; }
  .marquee-track, .pulse { animation: none; }
}
```

```js
// 单例 IntersectionObserver;进场即解除,只播一次
const io = new IntersectionObserver((entries) => {
  for (const e of entries) {
    if (!e.isIntersecting) continue;
    e.target.classList.add('is-visible');
    io.unobserve(e.target);                    // 一次性,避免反复触发
  }
}, { threshold: 0.15, rootMargin: '0px 0px -10% 0px' });
document.querySelectorAll('.reveal').forEach(el => io.observe(el));
// no-js 回退:<noscript><style>.reveal{opacity:1;transform:none}</style></noscript>
```

要点:
- **stagger** 用 CSS 变量而非 JS 计时:子项内联 `style="--reveal-delay:240ms"`(每级 +240ms,同 zero.cat 实测);
- **首屏内容不交给 IO**:Hero 子元素直接在 load 后加 `.is-visible`(或 CSS animation + delay),避免白屏闪烁;
- **计数动画**:IO 触发后 `requestAnimationFrame` 做 800ms 线性/缓出计数,mount 时 `prefers-reduced-motion` 直接置终值;
- **枢纽呼吸光环**:外圈 `box-shadow` 或伪元素 `animation: pulse 2.4s ease-in-out infinite`(scale 1→1.02 + opacity 波动);
- **跑马灯**:`.marquee-track { display:flex; width:max-content; animation: marquee-x 40s linear infinite; }`
  内容渲染两份,`@keyframes marquee-x { to { transform: translateX(-50%); } }`;容器 `overflow:hidden` + 两端 `mask-image: linear-gradient(...)`,hover `animation-play-state: paused`;
- **tab/复制反馈** ≤150ms,不加渐显;
- 每个长演示动画区(如 Hero 示意图)提供暂停按钮(DeepSeek 无障碍模式);
- `will-change` 只加在 `.reveal` 与跑马灯轨道上,动画结束后随 `transitionend` 移除以释放合成层。

---

## 5. 交付自查清单

- [ ] 所有颜色取自 §1.1 变量,不新增裸 hex;
- [ ] 标题衬线、正文 Inter、数字/代码 mono,三栈不混用;
- [ ] 橙色按钮上文字为深墨 `#141413`,非白色;
- [ ] 卡片只有 1px `var(--border)` 边 + 10px 圆角,无阴影;
- [ ] 导航 sticky + `blur(12px)` 毛玻璃,高 65px;
- [ ] 每个内容区块 `.reveal` + 递增 `--reveal-delay`,且 `prefers-reduced-motion` 全降级;
- [ ] 跑马灯双份内容可无缝循环,hover 暂停;
- [ ] 暗色主题换 `.dark` 一组变量即可整体成立(实测值见 §1.1),代码窗口、diff、水印在两主题下均可用;
- [ ] 官方名称一律「Web2API 网站收录查询开放平台」,不得出现「收录情报」表述。

---

## 6. 实现补充(官网工程师写回,2026-11-15)

实现中在 §1.1 实测 token 之外**派生了少量组件 token**(均标注来源,集中在 `assets/style.css` :root,未散落裸 hex):

| Token | 值 | 来源 |
|---|---|---|
| `--border-strong` | 亮 `#C9C4B6` / 暗 `#3A3730` | 仓库 `logo.svg` 连接线灰;暗色为同明度派生 |
| `--ok` | `#00C386` | §1.1 实测暗色图表序列色 |
| `--warn` | `#FFA824` | §1.1 实测暗色图表序列色 |
| `--ink-num` | `#8089FF` | §1.1 实测暗色图表序列色 |
| `--canvas-dot` | 亮 `#1414130d` / 暗 `#f3f1eb0d` | `web/admin/STYLE-v2.md` §3.1 成对实测值 |
| `--ink-*` | `#0A0A0A` / `#161615` / rgba 白层叠 | §1.1 DeepSeek 暗色组件语言实测值 |

其余实现要点:
- Hero H1 两行取「一次 API 调用 / 判定四大引擎收录」(每行 ≤12 字);跑马灯为文字标(中文粗体 + mono 拉丁名),每组 16 项、双份轨道,40s 无缝循环;
- 统计条数字只取 README 事实:4 引擎 / 1 次调用(单请求最多 20 URL)/ 3 行 curl(README.md:41-43)/ 100% 可自托管(MIT);
- 收录判定暗窗四 tab 的请求示例与 README.md:41-44、docs/API.md §2.1 逐字段一致;响应示例中 `match="none"` 与 `meta.units=2` 按 API 语义构造,`meta.engines_cooldown` 在无冷却时按实现省略(`core/server.go:1213`);
- 架构卡端点经 `core/server.go:206` 核对:专引擎搜索为 `/{engine}/search`(如 `/web-tool/baidu/search`、`/web-tool/360/search`),不挂 `/v1` 前缀;
- 冷却区文案与 `web/admin/assets/app.js:909`、`config.yaml` `steps_minutes: [10,30,120]` / `reset_after_minutes: 240` 同口径;
- 动效基建按 §4.2:单例 IO(threshold .15 / rootMargin -10% / 命中即 unobserve)、`--reveal-delay` 240ms 递增 stagger、首屏 `.reveal-hero` 不交 IO、`html.no-js` + `<noscript>` 双回退、`prefers-reduced-motion` 全量 `!important` 降级、rAF 800ms 缓出计数、hub 呼吸 2.4s、跑马灯 40s、复制反馈 1.5s、Hero 示意图带「暂停演示」按钮;
- 主题切换键 `w2a_site_theme`,head 内联脚本在 CSS 前预置 `.dark` 防闪白;与面板 `w2a_*` 键互不干扰。
