# Web2API 官网设计语言规范 V3(STYLE-v3.md)

> 对标站:**ZCode 官网**(https://zcode.z.ai/cn)+ **macOS 设计语言**(Apple HIG / apple.com)。
> 本文所有「实测」值来自 2026-11-15 浏览器实测:1440×900 视口、`getComputedStyle` 逐组件读取,
> 证据截图存 `qa/style-ref/01-top.png` ~ `04-footer.png`。macOS 侧数值来自 Apple HIG 与 apple.com
> 公开设计体系(见文末「证据来源」)。标注「沿 V2」的值沿用 `STYLE.md`/`assets/style.css` 既有实现。

---

## 0. V3 一句话路线

**画布与中性色全部 macOS 化,松柏绿是全站唯一的强调色;克制描边 + 柔和阴影;加大留白。**

| | V2(现行) | V3(本规范) |
|---|---|---|
| 色相 | 浅暗两套都带绿味(#F5F8F5 / #0B1210) | **纯中性**:浅 `#F5F5F7` 系 / 暗 `#0B0B0D` 系 |
| 强调色 | 松柏绿 #177E5B / 亮绿 #3ECF8E | **保留不动**(唯一强调色,绿系一族) |
| 字体 | 标题 serif(Lora)正文 Inter | **全站无衬线**,SF 气质系统栈(Hero 衬线取消) |
| 页面底 | 三层绿色 radial 光晕 | **纯平画布**,零装饰渐变 |
| 圆角 | 基准 10px | 6 / 10 / 16 / 20 / 胶囊 五档,CAC 按钮**胶囊化** |
| 阴影 | 卡片禁投影 | 卡片静止无影,hover 柔影;产品窗常驻柔影 |
| Hero | 左右分栏 + 全屏暗色 hero 段 | **左右分栏保留(硬性)**,hero 回到画布色,暗色只存在于右侧产品窗 |
| 留白 | 区块 88px | 区块 **112px**,容器加宽到 1120px |

不改动:品牌名、吉祥物「笃笃」、松柏绿/亮绿、`w2a_site_theme` 主题键与默认暗夜、零依赖零构建零外部字体契约、`/web-tool` 路由与 go:embed 构建方式。

---

## 1. 证据:ZCode 官网实测值(1440×900,2026-11-15)

直接决定 V3 取向的实测读数(Tailwind v4 neutral 体系 + Geist 字体,shadcn 变量):

| 组件 | 实测值(getComputedStyle) | 证据 |
|---|---|---|
| 页面画布 | `--background:#161616`(body 实测 rgb(22,22,22)),纯色无渐变(`bodyBgImage:none`) | 01-top.png |
| 前景/弱化 | `--foreground` oklch(0.985 0 0)=`#FAFAFA`;`--muted-foreground` =`#A1A1A1`;链接弱化 `white/40` | 01-top.png |
| 卡片 | `bg neutral-900`=`#171717`,边 `0.8px solid rgba(255,255,255,0.1)`,radius **16px**,padding 24px,阴影 `0 20px 60px rgba(0,0,0,0.35)` | 03/04 截图 |
| 强调卡(Pro) | 边加重 `white/25`,阴影 `0 24px 70px`;高亮卡边 `amber-300/25` | 03-pricing-cards.png |
| 导航 | sticky top-0 z-50,总高 **60.8px**(py-3 + 36 内容),`bg #161616/40` + **`blur(8px)`**,底边 `white/5` 0.8px,**上下滚动状态同一组 CSS**,过渡 `0.2s cubic-bezier(0.4,0,0.2,1)`;容器 `max-w-7xl`(1280px) | 01/02/03 截图 |
| 导航链接 | 14px w400 `#A1A1A1`,间距 gap 32px,hover → `#FAFAFA`(仅变色,无底色),过渡 0.15s | — |
| Ghost 胶囊(EN) | h 36px,min-w 84px,`rounded-full`,边 `white/20`,底 `white/5`,字白 | 01-top.png |
| 主按钮(登录) | h 36px,**`bg #FFFFFF` + 黑字**,`rounded-full`,px 24px,`transition:all .3s ease-out` | 01-top.png |
| 徽标 pill | h 34px,`rounded-full`,底 `white/8`,边 `white/15` 0.8px,padding 6px 16px,14px w500 | 01-top.png |
| 状态 pill(推荐/高频/Beta) | h 26px,`rounded-full`,12px w500,padding 4px 12px;**tint 底 + 同色系边**(白/amber/sky 三色) | 03-pricing-cards.png |
| Hero H1 | **60px/75px,w700,letter-spacing normal**,`#FAFAFA` | 01-top.png |
| Hero 副文 | 16px/24px `#A1A1A1` | — |
| 主 CTA(下载卡) | **白底黑字**,radius 16px,70×204px,padding 16px 24px,icon 32px + 两行(标题 16px w600 + 说明灰),过渡 0.3s | 01-top.png |
| 文字链接 | 12px `white/40`,underline offset-4,hover `white/60`,0.15s | — |
| 产品窗 | 1248×768,radius **20px**,底 `rgba(0,0,0,0.7)`,边 `white/20` 0.8px,内部 chrome 条 h 48px + 底边 hairline | 01/02 截图 |
| 下载行 | h 52px,radius **8px**,底 `white/5`,padding 16px 20px,hover `white/10` | 04-footer.png |
| Eyebrow | 14px w600,**letter-spacing 0.2em**,强调色(sky-400) | 02/03 截图 |
| H2 | **36px w600**,白 | — |
| 特性卡标题/正文 | 标题 18px w600 `#FAFAFA`;正文 14px/20px w400 `#A1A1A1` | 04-footer.png |
| 页脚 | `max-w-7xl`,py-10(40px),text-sm(14px)`#A1A1A1`;链接 `muted/50` → hover 白 | 04-footer.png |
| 字体 | `GeistSans`(无衬线)/ `GeistMono, ui-monospace, SFMono-Regular, "Roboto Mono", Menlo, Monaco…`(UI 内代码 13px) | — |
| 动效 | 全站统一 `cubic-bezier(0.4,0,0.2,1)`;**0.15s**(色彩微交互)/ **0.2s**(导航)/ **0.3s**(按钮卡片);keyframes 仅 spin/pulse/bounce/enter/exit/accordion;滚动条走浏览器默认(仅 `.hide-scrollbar` 工具类) | 样式表扫描 |

**V3 对 ZCode 的取舍**:学它的中性灰阶、玻璃导航、pill 按钮、tint pill、0.15/0.2/0.3 三档动效、
「强调色只做 eyebrow/状态点缀、CTA 用反差色(白/黑)」的克制;不学它的多强调色(sky 蓝 + amber 黄 + 黑白三套并存)
——我们只有绿。Sky-400/amber-300 在本站**禁止出现**。

---

## 2. macOS 设计语言 → 本项目映射

| macOS 原则 | 本项目落地 |
|---|---|
| Clarity / Deference / Depth(HIG 三原则) | 内容优先、界面后退:纯平画布,层级靠「画布 < 卡片(白/浮起)< 产品窗(反色暗)」三层表达,不靠渐变光晕 |
| 材质(materials/vibrancy) | 只用在**导航**:半透明画布色 + `backdrop-filter: blur(20px) saturate(1.8)`(macOS 工具栏材质);正文区禁玻璃拟态 |
| 中性色 | 浅色画布 `#F5F5F7`(apple.com 签名灰)交替 `#FFFFFF` 卡片;文字 `#1D1D1F` / `#6E6E73`(apple.com 双灰);暗色画布 `#0B0B0D`,卡片 `#17171A` |
| 单一强调色(apple.com:一色只给可操作元素) | 松柏绿 `#177E5B` 只用于:主按钮、链接/文字按钮、图标 tile、状态点、选中态;**不作装饰** |
| 控件形态系统级一致 | 全站按钮统一胶囊(macOS 侧按钮/apple.com CTA 同形态),输入控件统一 10px 圆角 |
| 柔和阴影 | `0 8px 24px rgba(0,0,0,.06)`(亮)/ `0 8px 24px rgba(0,0,0,.4)`(暗)一级;产品窗 `0 24px 64px` 一级;再无更重的影 |
| hairline 分割 | 1px 半透明描边;`0.8px` 仅用于产品窗内侧 hairline(高分屏更细腻,ZCode 实测同款) |
| SF 字体气质 | 系统字体栈优先命中 SF Pro / PingFang;Windows 落 Segoe UI / 微软雅黑;数字一律 tabular-nums |
| 细遮罩滚动条 | macOS overlay 式:8px 圆角滑块 + 透明轨道,hover 加深;Firefox `scrollbar-width: thin`(完整规格见 §7) |

---

## 3. CSS 自定义属性 Token 表(可直接照抄)

> 放 `assets/style.css` `:root` / `html.dark`,整组替换 V2 的 §1 段。变量名尽量与 V2 对齐,
> 已有组件改值即可生效;新增 token 标注「新增」。状态页 `status/index.html` 引同一份 CSS,自动级联。

```css
:root {
  /* —— 画布与层级(浅色:macOS #F5F5F7 系)—— */
  --background: #F5F5F7;          /* 页面画布(原 #F5F8F5) */
  --card: #FFFFFF;                /* 卡片/浮起面 */
  --muted: #E8E8ED;               /* 弱底:icon tile、tag 底、代码 chip 底(新增,apple.com 三级面) */
  --accent: #EAEAEE;              /* hover 底(原 #E2EFE7 绿味 → 中性) */
  --canvas-dot: rgba(29, 29, 31, 0.06);   /* icon tile 点纹 */

  /* —— 前景(apple.com 双灰)—— */
  --foreground: #1D1D1F;          /* 标题/正文主色(原 #1A2B24) */
  --muted-foreground: #6E6E73;    /* 次级文字(原 #5E7265) */
  --foreground-faint: #86868B;    /* 三级:占位符、脚注、水印字(新增) */

  /* —— 描边(macOS hairline)—— */
  --border: rgba(0, 0, 0, 0.10);          /* 全站 1px 描边(原绿味) */
  --border-strong: rgba(0, 0, 0, 0.16);   /* hover/重描边 */
  --border-faint: rgba(0, 0, 0, 0.06);    /* 分组内 hairline(新增) */

  /* —— 唯一强调色:松柏绿(不动)—— */
  --brand: #177E5B;
  --brand-hover: #12634A;         /* 新增:主按钮 hover 预计算值(替代 filter:brightness) */
  --brand-active: #0E503B;        /* 新增:主按钮 active */
  --brand-tint: rgba(23, 126, 91, 0.10);  /* 新增:品牌 tint 底(pill/选中态) */
  --brand-foreground: #FFFFFF;
  --brand-dim: #3ECF8E;           /* 亮绿:仅暗色主题的 --brand 文字色与图示强调,见下 */

  /* —— 语义色(功能性,非装饰)—— */
  --ok: #00A86B;                  /* 在线/成功(原 #00C386,微调贴近 macOS systemGreen 亮色) */
  --warn: #C97A0E;                /* 冷却/告警(原 #FFA824 提深,浅底可读) */
  --destructive: #D64545;         /* 错误/封禁(原 #E05A5A) */

  /* —— 主按钮/反差色(macOS:可操作面用最大反差)—— */
  --primary: #1D1D1F;             /* 反色主按钮(secondary CTA、代码窗按钮) */
  --primary-foreground: #FFFFFF;

  /* —— 产品暗窗(两主题恒定的「窗口材质」,ZCode 实测同构)—— */
  --ink-bg: #16161A;              /* 窗底(暗主题下比画布 #0B0B0D 浮一档) */
  --ink-card: #1D1D22;            /* 窗内卡片 */
  --ink-border-soft: rgba(255, 255, 255, 0.07);
  --ink-border: rgba(255, 255, 255, 0.14);
  --ink-title: #F5F5F7;
  --ink-text: rgba(245, 245, 247, 0.60);
  --ink-dim: rgba(245, 245, 247, 0.78);
  --ink-faint: rgba(245, 245, 247, 0.38);
  --ink-accent: #3ECF8E;          /* 窗内强调 = 亮绿(绿系一族,非第二强调色) */
  --ink-green: #3ECF8E;           /* 代码字符串高亮(原 #9ECE6A → 收拢到品牌绿系) */
  --ink-num: #8FB8E8;             /* 代码数字(原 #8089FF 紫 → 降饱和冷灰蓝,去 AI 味) */

  /* —— 导航材质(macOS 工具栏)—— */
  --nav-glass: rgba(245, 245, 247, 0.72);   /* 滚动后 */
  --nav-glass-top: rgba(245, 245, 247, 0.55); /* 顶部(新增) */

  /* —— 圆角五档(macOS)—— */
  --radius-sm: 6px;               /* chip、inline-code、mini 元素 */
  --radius: 10px;                 /* 输入控件、统计条容器(原值保留) */
  --radius-lg: 16px;              /* 卡片、代码窗 */
  --radius-xl: 20px;              /* 产品暗窗(ZCode 实测同款) */
  --radius-pill: 999px;           /* 按钮、徽标、状态 pill */

  /* —— 阴影(macOS 柔和,两级封顶)—— */
  --shadow-1: 0 8px 24px rgba(0, 0, 0, 0.06);      /* 卡片 hover(新增) */
  --shadow-window: 0 24px 64px rgba(11, 11, 13, 0.28), 0 2px 8px rgba(11, 11, 13, 0.10); /* 产品窗 */

  /* —— 字体(零外部加载,SF 气质系统栈)—— */
  --font-sans: -apple-system, BlinkMacSystemFont, "SF Pro Text", "Segoe UI Variable Text",
               "Segoe UI", "PingFang SC", "HarmonyOS Sans SC", "MiSans", "Microsoft YaHei UI",
               "Microsoft YaHei", system-ui, sans-serif;
  --font-mono: ui-monospace, "SF Mono", "Cascadia Code", Consolas, "JetBrains Mono",
               Menlo, "Liberation Mono", monospace;
  /* 删除 --font-serif / --font-display:V3 全站无衬线 */

  /* —— 布局 —— */
  --container: 1120px;            /* 原 1056px,加宽换呼吸感 */
  --nav-h: 64px;                  /* 原 65px */

  /* —— 动效(新增统一档位;ZCode 实测三档 + V2 揭示缓动)—— */
  --motion-fast: 150ms;           /* 色彩/透明度微交互 */
  --motion-base: 250ms;           /* 导航、卡片位移 */
  --motion-reveal: 700ms;         /* 入场渐显 */
  --ease-standard: cubic-bezier(0.4, 0, 0.2, 1);  /* ZCode 全站实测同款 */
  --ease-out: cubic-bezier(0.16, 1, 0.3, 1);      /* 入场/位移(V2 保留) */
}

/* —— 暗色主题(macOS #0B0B0D 系;默认主题,html.dark 预置于 head 内联脚本)—— */
html.dark {
  --background: #0B0B0D;          /* 页面画布(原 #0B1210 绿味) */
  --card: #17171A;                /* 卡片/浮起面 */
  --muted: #1E1E22;               /* 弱底 */
  --accent: #232329;              /* hover 底 */
  --canvas-dot: rgba(245, 245, 247, 0.08);

  --foreground: #F5F5F7;
  --muted-foreground: #A1A1A6;
  --foreground-faint: #73737A;

  --border: rgba(255, 255, 255, 0.10);
  --border-strong: rgba(255, 255, 255, 0.18);
  --border-faint: rgba(255, 255, 255, 0.06);

  /* 暗色下唯一强调色仍属绿系:文字/图标/状态用亮绿保证对比(ZCode 白底黑字 CTA 同思路) */
  --brand: #3ECF8E;
  --brand-hover: #5BDC9F;
  --brand-active: #2FB97C;
  --brand-tint: rgba(62, 207, 142, 0.12);
  --brand-foreground: #062B1C;    /* 亮绿底上的深绿字(如 tint pill) */

  /* 主按钮在暗色下用反差白(V2 手法保留,ZCode「登录」同款) */
  --primary: #F5F5F7;
  --primary-foreground: #0B0B0D;

  --ok: #3ECF8E;                  /* 暗色 systemGreen(macOS 同策略:暗模式提亮) */
  --warn: #F0B429;
  --destructive: #E5645C;

  --nav-glass: rgba(11, 11, 13, 0.66);
  --nav-glass-top: rgba(11, 11, 13, 0.45);

  --shadow-1: 0 8px 24px rgba(0, 0, 0, 0.40);
  --shadow-window: 0 24px 64px rgba(0, 0, 0, 0.50), 0 2px 8px rgba(0, 0, 0, 0.30);
}
```

> 暗色 `--ink-bg` 与亮色共用同一值(`#16161A`):亮主题里它是反差产品窗,暗主题里它是比
> `#0B0B0D` 浮一档的浮起窗——一套值两主题都成立,不必切。
> `::selection`:`background: var(--brand-tint)`(V2 的实色绿改为 macOS 式半透明 tint,文字色不变)。

---

## 4. 字号阶梯(全站无衬线;行高、字重、字距一次给死)

| 级别 | 规格 | 用法 / 现有类 |
|---|---|---|
| Display | `clamp(40px, 5vw, 60px)` / 1.25,w700,`letter-spacing:-0.015em` | `.hero-title`(取消 0.12em 宽字距与 serif;ZCode H1 实测 60px w700) |
| H2 | `clamp(30px, 3.6vw, 36px)` / 1.3,w700,`letter-spacing:-0.01em` | `h2`(ZCode 36px w600 实测) |
| H3(卡标题) | `19px / 28px`,w600 | `.f-body h3`、`.dark-card h3`(ZCode 18px w600) |
| H4(小标题) | `15px / 22px`,w600 | `.step h4` 等 |
| Lead(Hero 副文) | `17px / 28px`,w400,`--muted-foreground`,max-width 560px | `.hero-lead`(apple.com 正文 17px 气质) |
| Body | `16px / 26px`,w400 | body 默认 |
| Secondary | `14px / 22px`,w400 | 卡片正文(ZCode 特性卡正文 14px/20px) |
| Caption | `12.5px / 18px`,w400 | 辅助说明、页脚版权 |
| Eyebrow | `12px`,w700,`letter-spacing:0.14em`,`--brand` 色 | `.eyebrow`(ZCode 用强调色 + 0.2em;我们收窄到 0.14em 并用品牌绿,中文不 uppercase,附拉丁文可 `text-transform:uppercase`) |
| Mono UI | `13px / 22px`,w400 | 代码块(ZCode UI 内 mono 13px 实测) |
| Mono inline | `12.5px`,w500,`--radius-sm` 6px、`--muted` 底 + 1px `--border` | `.inline-code` |
| 统计数字 | `36px`,w600,`--font-mono`,`tabular-nums`,字距 -0.02em | `.stat-num`(沿 V2) |

规则:同一屏最多出现 3 个字号级;标题一律 `--font-sans`;**数字(统计/版本号/配额)一律 mono + tabular-nums**。

---

## 5. 间距阶梯与区块节奏

```css
/* 4px 基(新增为变量,亦可直接写数值) */
--space-1: 4px;  --space-2: 8px;   --space-3: 12px;  --space-4: 16px;
--space-5: 20px; --space-6: 24px;  --space-8: 32px;  --space-10: 40px;
--space-12: 48px; --space-14: 64px; --space-16: 80px; --space-18: 96px;
--space-20: 112px; --space-22: 128px;
```

| 场景 | 值 | 变化 |
|---|---|---|
| 容器 | `max-width: calc(var(--container) + 48px)` + 两侧 24px | 1056 → **1120px** |
| 区块 `.section` | **`112px 0`**(≤980px:80px;≤760px:64px) | 88 → 112px,呼吸感主力 |
| 区块紧 `.section-tight` | `80px 0` | 72 → 80px |
| 标题组 `.sec-head` | `margin-bottom: 48px`,max-width 640px | 40 → 48px |
| eyebrow → H2 | 14px | — |
| H2 → 首行内容 | 40px | — |
| 卡片网格 gap | 24px(4 列)/ 24px(3 列)/ 24px(2 列) | — |
| Hero 上下 | `padding: calc(72px + var(--nav-h)) 0 96px` | 取消 `-margin-top` 暗段上移 hack |
| Hero 分栏 gap | `56px`(1100px 以下 44px) | 48 → 56px |
| 卡片内边距 | `28px 28px 32px` | 22 → 28px,留白换呼吸 |
| 代码窗内边距 | 头部 `12px 20px`,代码 `16px 24px 8px` | — |
| 统计格 | `36px 32px` | — |
| 页脚 | `padding: 80px 0 40px`;版权行区隔 `margin-top:48px; padding:20px 0` | — |

---

## 6. 组件规格

### 6.1 按钮(primary / secondary / ghost 三态;胶囊化)

```css
.btn {
  display: inline-flex; align-items: center; justify-content: center; gap: 8px;
  height: 44px; padding: 0 24px;
  border-radius: var(--radius-pill);            /* V2 的 10px → 胶囊(macOS 控件同形态) */
  border: 1px solid transparent;
  font: 500 15px/1 var(--font-sans);
  text-decoration: none; white-space: nowrap; cursor: pointer;
  transition: background-color var(--motion-fast) var(--ease-standard),
              border-color var(--motion-fast) var(--ease-standard),
              color var(--motion-fast) var(--ease-standard),
              transform var(--motion-fast) var(--ease-standard);
}
.btn:active { transform: scale(0.97); }          /* macOS 按压回弹 */
.btn:disabled { opacity: 0.4; cursor: not-allowed; pointer-events: none; }
.btn .arrow { transition: transform var(--motion-fast) var(--ease-standard); }
.btn:hover .arrow { transform: translateX(3px); }

/* Primary:松柏绿实底(唯一强调色给可操作面) */
.btn-primary { background: var(--brand); color: var(--brand-foreground); }
.btn-primary:hover { background: var(--brand-hover); }
.btn-primary:active { background: var(--brand-active); }
html.dark .btn-primary { background: var(--primary); color: var(--primary-foreground); }
html.dark .btn-primary:hover { background: #FFFFFF; }
html.dark .btn-primary:active { background: #E2E2E7; }

/* Secondary:卡面 + hairline(macOS 推钮) */
.btn-secondary { background: var(--card); color: var(--foreground); border-color: var(--border); }
.btn-secondary:hover { border-color: var(--border-strong); background: var(--muted); }
.btn-secondary:active { background: var(--accent); }

/* Ghost:透明,弱化字(macOS 工具栏按钮) */
.btn-ghost { background: transparent; color: var(--muted-foreground); }
.btn-ghost:hover { background: var(--accent); color: var(--foreground); }
.btn-ghost:active { color: var(--foreground); background: var(--muted); }
```

尺寸档:`.btn`(44px,默认)、`.btn-lg`(**52px**,仅 Hero 主 CTA 对)、`.btn-sm`(36px,导航/窗内,
padding 0 16px,14px)、`.btn-xs`(30px,代码窗内 copy 等,13px)。
Danger 保留 V2 `.btn-danger`(bg `--destructive`,白字),hover 用预计算 `#C23B3B`。
反差 CTA(暗窗内/生态区):`.pill-white`(bg #FFFFFF、深字 `#0B0B0D`、h 44 胶囊)与
`.pill-dark`(bg rgba(255,255,255,0.07)、边 `--ink-border`、字 `--ink-title`)沿 V2,h 统一 44px。

### 6.2 导航(两态)

```css
.site-nav {
  position: sticky; top: 0; z-index: 50;
  height: var(--nav-h);
  background: var(--nav-glass-top);                 /* 态 A:顶部 */
  -webkit-backdrop-filter: blur(20px) saturate(1.8);
  backdrop-filter: blur(20px) saturate(1.8);        /* macOS 工具栏材质 */
  border-bottom: 1px solid transparent;
  color: var(--foreground);
  transition: background-color var(--motion-base) var(--ease-standard),
              border-color var(--motion-base) var(--ease-standard);
}
.site-nav.scrolled {                                 /* 态 B:滚过 hero 后(app.js 阈值不变) */
  background: var(--nav-glass);
  border-bottom-color: var(--border-faint);
}
```

- 容器与页面同宽(`--container` 1120 + 24px),高度 64px,内容垂直居中。
- 品牌:啄木鸟方章 28px + 「Web2API」16px w600(`-0.01em`)+ 分隔 hairline + 副标 12px w500 `--foreground-faint`。
- 链接:14px w500,padding `6px 12px`,radius `--radius-sm`,颜色 `--muted-foreground`;
  hover:色变 `--foreground` + 底 `--accent`;active(当前节):同 hover 保持。
  (沿 V2 结构;V2 的暗段白字随 `.site-nav.scrolled` 切换的逻辑**整段删除**——V3 画布恒定,只有一套配色。)
- 右侧:主题切换 icon-btn(36px,同链接 hover 语言)+ `.btn-sm.btn-primary`「进入控制台」。
- 移动端抽屉(≤920px)沿用 V2 结构,材质同态 B。

### 6.3 卡片

```css
.f-card {
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);                 /* 16px */
  background: var(--card);
  overflow: hidden; min-width: 0;
  transition: border-color var(--motion-fast) var(--ease-standard),
              box-shadow var(--motion-base) var(--ease-standard),
              transform var(--motion-base) var(--ease-standard);
}
.f-card:hover { border-color: var(--border-strong);
  box-shadow: var(--shadow-1); transform: translateY(-2px); }
.f-body { padding: 28px 28px 32px; }
.f-body h3 { font: 600 19px/28px var(--font-sans); color: var(--foreground); text-wrap: balance; }
.f-body p  { margin-top: 10px; font-size: 14px; line-height: 22px; color: var(--muted-foreground); }

.f-badge {  /* icon tile:macOS 风格圆角方 */
  width: 44px; height: 44px; border-radius: 12px;
  display: grid; place-items: center;
  background: var(--muted);
  background-image: radial-gradient(var(--canvas-dot) 1px, transparent 1px);
  background-size: 10px 10px;
  color: var(--brand);
}
```

静止无阴影(靠 hairline 与画布差分层);hover 才出 `--shadow-1` + 上浮 2px。
`.dark-card`(Agent 接入)与 `.cmd-card` 同构,材质换 `--ink-*`,padding 32px,radius `--radius-lg`。
统计条 `.stats`:hairline 容器 + `--radius` 10px + `--card` 底,格间 1px `--border` 竖线,格 padding `36px 32px`。

### 6.4 徽标 pill(状态/标签)

```css
.pill {
  display: inline-flex; align-items: center; gap: 6px;
  height: 24px; padding: 0 10px;
  border-radius: var(--radius-pill);
  font: 500 12px/1 var(--font-sans);
}
.pill::before { content: ""; width: 6px; height: 6px; border-radius: 50%; background: currentColor; }

.pill-neutral  { background: var(--muted);            color: var(--muted-foreground); }
.pill-brand    { background: var(--brand-tint);       color: var(--brand); }          /* 推荐/重点 */
.pill-ok       { background: rgba(0, 168, 107, 0.12); color: var(--ok); }
.pill-warn     { background: rgba(240, 180, 65, 0.14);color: var(--warn); }
.pill-error    { background: rgba(214, 69, 69, 0.12); color: var(--destructive); }
/* 暗色主题:语义 pill 的背景 alpha ×1.2,文字色换 html.dark 的语义变量 */
```

无描边(macOS 系统徽标形态:tint 底 + 同色字);Hero 顶部徽标用 `.pill-brand` 加大到 h28 / 13px / padding 0 14px。
`--ink-*` 窗内 pill 用 `.chip`(V2 保留,半径 `--radius-pill`,边 `--ink-border`)。

### 6.5 代码块 / 产品暗窗

```css
.code-window {
  background: var(--ink-bg);
  border: 1px solid var(--ink-border-soft);
  border-radius: var(--radius-lg);
  overflow: hidden; color: var(--ink-text); min-width: 0;
  box-shadow: var(--shadow-window);
}
.cw-head {
  display: flex; align-items: center; gap: 10px;
  padding: 10px 16px; min-height: 44px;
  border-bottom: 1px solid var(--ink-border-soft);
}
.cw-dots i { width: 12px; height: 12px; border-radius: 50%; }
/* macOS 红绿灯(替代 V2 灰/橙点) */
.cw-dots i:nth-child(1) { background: #FF5F57; }
.cw-dots i:nth-child(2) { background: #FEBC2E; }
.cw-dots i:nth-child(3) { background: #28C840; }

.code-tab {
  height: 28px; padding: 0 12px; border-radius: var(--radius-pill);
  font: 500 12px/1 var(--font-mono); color: var(--ink-dim); border: 1px solid transparent;
  transition: background-color var(--motion-fast) var(--ease-standard),
              color var(--motion-fast) var(--ease-standard);
}
.code-tab:hover { color: var(--ink-title); background: rgba(255, 255, 255, 0.06); }
.code-tab.active, .code-tab[aria-selected="true"] {
  background: rgba(62, 207, 142, 0.16); color: var(--ink-accent); font-weight: 600;
}   /* V2 的实色绿 tab → tint 形态,与全站 pill 统一 */

.code-block {
  margin: 0; padding: 16px 24px 8px;
  font: 400 13px/22px var(--font-mono);
  color: var(--ink-dim); overflow-x: auto; white-space: pre;
  scrollbar-width: thin; scrollbar-color: rgba(255,255,255,0.22) transparent;
}
.code-block::-webkit-scrollbar { height: 8px; }
.code-block::-webkit-scrollbar-thumb { background: rgba(255,255,255,0.22); border-radius: 999px; }
```

语法高亮沿 V2 八类 `.tk-*`,仅 `--ink-green/--ink-num` 换新值;窗底 `.cw-foot`(端点 + `200 OK`)保留。
Hero 右侧 `.dark-window` 升级为 `--radius-xl`(20px)+ `--shadow-window`,**删除山形 SVG 底图**,
保留「请求卡 → 枢纽 → 扇形分发」图示;图示动效节奏沿 V2(§9)。

### 6.6 表单(输入/选择/开关)

```css
.field-label { display:block; margin-bottom:8px; font: 500 13px/1 var(--font-sans); color: var(--foreground); }
.input, .select, .textarea {
  width: 100%; background: var(--card);
  border: 1px solid var(--border); border-radius: var(--radius);   /* 10px */
  color: var(--foreground); font: 400 14px/1.4 var(--font-sans);
}
.input, .select { height: 40px; padding: 0 14px; }
.textarea { padding: 10px 14px; min-height: 88px; resize: vertical; }
.input::placeholder, .textarea::placeholder { color: var(--foreground-faint); }
.input:hover, .select:hover, .textarea:hover { border-color: var(--border-strong); }
.input:focus, .select:focus, .textarea:focus {
  outline: none; border-color: var(--brand);
  box-shadow: 0 0 0 3px var(--brand-tint);      /* macOS focus ring:强调色 3px 光环 */
}
.input:disabled, .select:disabled { opacity: 0.4; cursor: not-allowed; background: var(--muted); }
```

 checkbox/radio:14px,`accent-color: var(--brand)`(两主题自动成立);
 校验错误:`border-color: var(--destructive)` + ring 同色 3px,错误文案 12.5px `--destructive`。
 状态页/控制台新表单一律走此规格。

### 6.7 分割线

```css
hr { border: 0; border-top: 1px solid var(--border); }        /* 区块间 */
.hairline-faint { border-top: 1px solid var(--border-faint); } /* 卡内分组、列表行间 */
/* 列表行分割(引擎面板、route-list、platform-list)一律 .hairline-faint */
```

- 区块级:1px `--border`(页脚上边、跑马灯上下边保留)。
- 组件内分组:1px `--border-faint`(比区块级更轻,macOS 分组列表层级)。
- 统计条竖向分割:相邻格 `border-left: 1px solid var(--border)`(沿 V2)。
- **禁止用粗线(>1px)与有色分割线**(描边一律中性)。

### 6.8 滚动条(macOS overlay 式细滚动条;两主题各自成立)

```css
/* —— 全局:Firefox —— */
html { scrollbar-width: thin; scrollbar-color: rgba(0, 0, 0, 0.30) transparent; }
html.dark { scrollbar-color: rgba(255, 255, 255, 0.24) transparent; }

/* —— 全局:WebKit(Chrome/Edge/Safari)—— */
::-webkit-scrollbar { width: 8px; height: 8px; }          /* macOS overlay 宽度档 */
::-webkit-scrollbar-track { background: transparent; }     /* 透明轨道 */
::-webkit-scrollbar-thumb {
  background: rgba(0, 0, 0, 0.30);
  border-radius: 999px;
  border: 2px solid transparent;           /* 内缩 2px,滑块视觉宽 4px,macOS 质感关键 */
  background-clip: padding-box;
}
::-webkit-scrollbar-thumb:hover { background: rgba(0, 0, 0, 0.45); }
::-webkit-scrollbar-thumb:active { background: rgba(0, 0, 0, 0.55); }
::-webkit-scrollbar-corner { background: transparent; }

/* —— 暗色主题(各自成立,不共值)—— */
html.dark ::-webkit-scrollbar-thumb { background: rgba(255, 255, 255, 0.24); }
html.dark ::-webkit-scrollbar-thumb:hover { background: rgba(255, 255, 255, 0.38); }
html.dark ::-webkit-scrollbar-thumb:active { background: rgba(255, 255, 255, 0.48); }

/* —— 窗口内(代码窗/下拉面板):横滚同规格,色阶用 ink —— */
.code-block { scrollbar-width: thin; scrollbar-color: rgba(255,255,255,0.22) transparent; }
.code-block::-webkit-scrollbar { height: 8px; }
.code-block::-webkit-scrollbar-thumb { background: rgba(255,255,255,0.22); border-radius: 999px; border: 2px solid transparent; background-clip: padding-box; }
.code-block::-webkit-scrollbar-thumb:hover { background: rgba(255,255,255,0.36); }
/* V2 的 .mini-term code 滚动条隐藏规则保留 */
```

要点:8px 槽宽 + `border: 2px solid transparent; background-clip: padding-box` 产生 macOS
「两端内缩圆棒」;轨道恒透明(overlay 感);hover/按下两档加深;Firefox 走
`scrollbar-width: thin` + `scrollbar-color` 双值;暗浅两套色阶各自独立给出,不做颜色叠加推导。

### 6.9 焦点与选区(无障碍,macOS 语义)

```css
:focus-visible { outline: 2px solid var(--brand); outline-offset: 2px; border-radius: 4px; }  /* 沿 V2 */
::selection { background: var(--brand-tint); }   /* V3:半透明 tint,不反色 */
```

---

## 7. 主题机制(不变项 + 变化项)

- 默认暗夜:`html.dark` 预置内联脚本(head、CSS 之前)与 `localStorage['w2a_site_theme']` 逻辑**原样保留**(`assets/app.js:21-34`、`status/index.html:11-18`)。
- `html { color-scheme: light }` / `html.dark { color-scheme: dark }` 保留(原生控件、滚动条跟随)。
- 所有颜色一律走 token;**唯一允许主题分支**的地方:`html.dark` 的 token 组、暗窗内组件(恒定 ink 材质)、滚动条两套(§6.8)。
- 过渡:主题切换时 body 仅 `color` 过渡 0.3s(沿 V2);**禁止**给背景加大过渡(避免两屏闪变)。

---

## 8. 动效规范

| 档位 | 时长 | 缓动 | 用途 |
|---|---|---|---|
| fast | **150ms** | `var(--ease-standard)` | 颜色/描边/底色微交互、按钮 hover、链接 hover、复制反馈出现 |
| base | **250ms** | `var(--ease-standard)` | 导航两态、卡片 hover 阴影/位移、移动端抽屉 |
| reveal | **700ms** | `var(--ease-out)`(0.16,1,0.3,1) | `.reveal` 入场渐显(stagger 240ms 递增,沿 V2) |
| 演示循环 | 不变 | — | 跑马灯 40s linear、hub-pulse/dot-pulse/packet-y/rail-packet/stub-drop/card-lit 沿 V2 节奏;`live-blink` 2s |

- 唯一缓动族:色彩用 `cubic-bezier(0.4,0,0.2,1)`(ZCode 实测同款),位移/入场用 `cubic-bezier(0.16,1,0.3,1)`;**不再引入其他缓动**。
- `transition-property` 列举式书写(V2 按钮即此风格),禁 `transition: all`(仅 `.pill-white` 保留 transform+bg 例外)。
- `will-change` 只加在 `.reveal/.reveal-hero` 与 `.marquee-track`(沿 V2)。
- `prefers-reduced-motion: reduce` 全量降级块沿 V2(style.css §18)原样保留。
- 复制反馈:图标+文字变 `--ok`,1.5s 复原(沿 V2)。

---

## 9. 逐节布局注意点(自上而下)

1. **导航** — §6.2 两态。V3 后无暗段,`.site-nav` 初始即浅/暗画布玻璃;`app.js` 滚动阈值(`scrollY > heroEnd - 64`)不变。
2. **Hero(硬性要求:左右分栏,不许改回居中)** — `.hero-grid` 保持 `grid-template-columns: minmax(0,10fr) minmax(0,11fr); gap:56px; align-items:center`。
   - 左列(自上而下):`.pill.pill-brand` 徽标(如「单二进制 · 四引擎 · MCP 就绪」)→ `.hero-title` Display 两行 → `.hero-lead` 17px → `.hero-ctas`(`.btn-lg.btn-primary` + `.btn-lg.btn-secondary`)。
   - 右列:`.dark-window` 产品窗(radius 20px,`--shadow-window`),内部结构不变:请求卡 → 茎 → 枢纽(亮绿,脉冲光环)→ 扇形虚线 → 4 张出口卡。**删除山形 SVG**(`.mtn`)与 `.hero-dark` 整段暗底/绿色 radial——hero 段背景就是画布色。
   - 删除 `.hero { margin-top: calc(-1*var(--nav-h)) }` 与 `min-height:100vh` 暗屏逻辑;新 padding 见 §5。
   - 入场:`.reveal-hero` + 240ms 递增 stagger 沿 V2。
3. **数据源跑马灯** — 上下 hairline 保留;eyebrow 改品牌绿;`mq-item` 颜色 `--muted-foreground` → hover `--foreground`;mask 渐隐保留(功能性遮罩,不算装饰渐变)。
4. **统计条** — hairline 容器 4 格;数字 mono 36px;label 12px。数字色 `--foreground`,强调(如 100%)不加色。
5. **核心优势(4 卡)** — §6.3 卡片;icon tile `--muted` 底 + 点纹 + 品牌绿线性图标。
6. **收录判定 split(左步骤右代码窗)** — 布局沿 V2(5fr/7fr,gap 56px);步骤卡换新 token;右侧 `.code-window` 按 §6.5。
7. **架构(3 卡)** — 端点清单 route-list 分割线换 `--border-faint`;mono 端点色 `--foreground`。
8. **Agent 接入(2 暗卡)** — `.dark-card` padding 32px;mini-term 保留;chips 换 `--radius-pill`;亮绿链接 `--ink-accent` 只在此暗语境。
9. **服务器状态(引擎面板)** — 面板 radius `--radius-lg`、`--card` 底;状态 pill 换 §6.4(红黄绿语义色新值);live-dot `--ok` + 2s 呼吸保留。
10. **快速开始** — `.cmd-card` 三张;`.cmd-step` 编号圆改 `--brand` 底白字(原 `--ink-accent`);pre 换 §6.5 滚动条规格。
11. **生态 CTA** — **删除整段 radial 绿光与渐变底**;改为「画布上的第二层」:`background: var(--muted)`(暗:`var(--card)`),上下 hairline,padding 112px 0;按钮组:白/黑反差 `.pill-white` ×1 + `.pill-dark` ×3(h 44)。
12. **页脚** — 上 hairline;品牌列 + 链接列;版权行 `--foreground-faint` 12.5px;**不做超大水印**(V2 已移除);链接 hover 仅变色。

---

## 10. 不许做清单(硬性)

1. **禁止渐变滥用** — 全站禁止装饰性背景渐变:body radial 光晕、区块绿雾、渐变文字、渐变按钮一律删除。唯一保留:跑马灯两端功能性 mask、icon tile 点纹(radial-dot,非渐变观感)、暗窗内 8% 以内的 vignette(若需要)。山形 SVG 删除。
2. **禁止发光标题** — 任何标题/文字不得带 `text-shadow`、glow、霓虹。图示动效的光学反馈仅限:枢纽外圈脉冲(scale+opacity,无 blur 光晕)与状态点 6px halo;`.diag-hub` 的 `box-shadow 0 0 24px` 降为 `0 0 12px rgba(62,207,142,.30)`。
3. **禁止多强调色混用** — 强调色只有绿系一族(#177E5B / #3ECF8E 及其 tint/hover 派生)。ZCode 的 sky-400 蓝、amber 金等**不得引入**;红黄绿仅限语义状态(成功/冷却/错误),不作装饰、不作标题色、不作第二按钮色。
4. **禁止 AI 味空洞文案** — 禁词:「赋能」「引领」「极致」「一站式」「深耕」「闭环」「生态级」「智能、高效、强大」类排比;禁三连排比句;禁每段以「随着…的发展」开头;禁 emoji 装饰正文(ZCode 徽标内 🎉 不学);文案以数字与事实为主语(引擎数、URL 上限 20、unit 计费、MIT 自托管),短句,动宾开头。
5. **禁止居中 Hero** — hero 左右分栏为硬性要求,任何断点下 980px 以上不得改单栏居中。
6. **禁止描边换色** — 描边/分割线一律中性灰阶 alpha,不得用绿/蓝描边。
7. **禁止阴影堆叠** — 阴影只用 §3 两档;卡片静止态零阴影;禁止多重彩色投影(V2 `.exit-card::after` 的蓝投影一并删除)。
8. **禁止新字体加载** — 零依赖契约不变;标题 serif 字体栈整体退役,不得为 V3 引入 webfont。

---

## 11. V2 → V3 迁移对照(改哪里)

| V2 资产(style.css) | V3 处置 |
|---|---|
| `:root` 绿味色板 | 整组替换为 §3 token(变量名对齐,组件零改动生效) |
| `--font-serif/--font-display` 引用(h2/hero-title/f-body h3/split-title/dark-card h3) | 全部改 `var(--font-sans)` 并按 §4 改字号字距 |
| `body` 三层 radial 光晕(style.css:88-91) | 删除,`background-color: var(--background)` |
| `.btn` 48px/10px | 44px/胶囊 + `:active scale(0.97)`;新增 `.btn-lg 52px` |
| `.site-nav` 暗段白字双态语言(style.css:243-297) | 简化为 §6.2 单配色玻璃两态;`.scrolled` 只加底边 hairline |
| `.hero-dark` 暗段 + `.mtn` 山形 | 段落底改画布;山形删除;`--radius-xl` 给 `.dark-window` |
| `.diag-hub` 24px 绿光 | 降 12px(§10.2) |
| `.code-tab.active` 实色绿 | tint 形态(§6.5) |
| `.cw-dots` 灰/橙 | macOS 红绿灯 #FF5F57/#FEBC2E/#28C840 |
| `.st-ok/.st-cool/.st-cb`(引擎面板实底 pill) | 换 §6.4 tint pill;状态页同步 |
| `.eco` 渐变暗带(style.css:1131-1137) | 改第二层中性面 + hairline(§9.11) |
| 无全局滚动条规格 | 新增 §6.8 完整规格 |
| `--ease-out` 0.16,1,0.3,1 | 保留,新增 `--ease-standard` 与三档时长 |

## 12. 交付自查清单

- [ ] 亮/暗两主题逐节截图过:画布 `#F5F5F7`/`#0B0B0D`,绿味残留 0
- [ ] 强调色只有绿系;sky/amber/紫 grep 为 0
- [ ] 装饰性渐变 grep(linear-gradient/radial-gradient 非 mask/点纹用途)为 0
- [ ] 标题无 text-shadow;H1/hero/lead 无衬线,负字距
- [ ] hero 左右分栏 980px+ 保持;暗窗在右列
- [ ] 按钮:primary 绿(暗:白)/secondary/ghost 三态 + :active 回弹;全站胶囊
- [ ] 导航 64px 两态过渡 250ms;blur(20px) saturate(1.8)
- [ ] 卡片静止无影、hover 柔影 +2px;产品窗 `--shadow-window`
- [ ] 滚动条:WebKit 8px 内缩圆棒 + Firefox thin,暗浅两套各自成立
- [ ] 焦点环 2px brand、选区 tint、`prefers-reduced-motion` 全降级
- [ ] 主题键 `w2a_site_theme` 行为不变;状态页级联正常
- [ ] 文案过 §10.4 禁词检查

## 13. 证据来源

- ZCode 官网实测:浏览器 1440×900,`tab.playwright.evaluate(getComputedStyle…)` 逐组件读取 + 样式表扫描;截图 `qa/style-ref/01-top.png`(首屏:导航/H1/徽标/下载卡)、`02-product-window.png`(产品窗 20px 圆角)、`03-pricing-cards.png`(卡片 16px/阴影/pill)、`04-footer.png`(下载行/页脚)。实测数值明细见 §1 表。
- macOS 侧:Apple HIG(Materials/Typography/Color,developer.apple.com/design/human-interface-guidelines;Clarity/Deference/Depth、材质分层、系统色随外观切换);apple.com 设计体系实测值(画布 #F5F5F7 / 文字 #1D1D1F / 辅助灰 #6E6E73-#86868B / 单一蓝 #0071E3 只给可操作面 / 17px 正文 / 980px 胶囊按钮),检索佐证:Refero Styles(apple.com token 分解)与 Apple Design Resources(SF 字体)。检索日期 2026-11-15。
- 本项目现状:`web/site/assets/style.css`(token :8-71、按钮 :133-198、导航 :237-331、hero :333-438、代码窗 :828-927、eco :1130-1142、动效基建 :1178-1280)、`web/site/index.html`(hero 分栏 :62-138)、`web/site/assets/app.js`(主题键 :21-34、导航阈值 :63、IO 渐显 :141-181)、`web/site/status/index.html`(内联样式 :19-123,引同一 CSS)。
