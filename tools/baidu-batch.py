#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
百度收录小批量查询（无代理、控节奏版）

用途：不买代理的前提下，把百度查询拆成小批、按间隔执行，并在百度冷却时自动等待，
避免一次压 40+ 条触发验证码后被动冷却 30 分钟。

用法：
  python3 baidu-batch.py --urls urls.txt                 # 默认每批 8 条、批间隔 300s、日上限 200
  python3 baidu-batch.py --urls urls.txt --batch 5 --gap 600
  python3 baidu-batch.py --url https://a.com --url https://b.com --batch 2 --gap 5

接口：POST /web-tool/v1/index/check（engines=["baidu"]，同步 ≤20 条）
依赖：仅 Python3 标准库；API Key 从 --api-key、环境变量 W2A_KEY、
      或 /root/.web2api-e2e-key 读取。
"""
import argparse, datetime, json, os, random, sys, time, urllib.error, urllib.parse, urllib.request

DEFAULT_BASE = "https://radar.ydsdoa.cn/web-tool"
STATE_DEFAULT = "/var/tmp/w2a-baidu-state.json"
OUT_DEFAULT = "/var/tmp/w2a-baidu-results.jsonl"


def now():
    return datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")


def log(msg):
    print(f"[{now()}] {msg}", flush=True)


def parse_ts(s):
    if not s:
        return None
    try:
        return datetime.datetime.fromisoformat(s.replace("Z", "+00:00")).timestamp()
    except Exception:
        return None


def http_json(method, url, key=None, payload=None, timeout=300):
    data = json.dumps(payload).encode("utf-8") if payload is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    if key:
        req.add_header("X-API-Key", key)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            body = r.read()
            return r.status, (json.loads(body) if body else {}), dict(r.headers)
    except urllib.error.HTTPError as e:
        body = e.read()
        try:
            j = json.loads(body) if body else {}
        except Exception:
            j = {"raw": body[:200].decode("utf-8", "replace")}
        return e.code, j, dict(e.headers)
    except Exception as e:
        return 0, {"error": f"network:{e}"}, {}


def load_state(path):
    try:
        with open(path) as f:
            st = json.load(f)
    except Exception:
        st = {}
    today = datetime.date.today().isoformat()
    if st.get("date") != today:
        st = {"date": today, "count": 0, "done": []}
    st.setdefault("count", 0)
    st.setdefault("done", [])
    return st


def save_state(path, st):
    tmp = path + ".tmp"
    with open(tmp, "w") as f:
        json.dump(st, f, ensure_ascii=False)
    os.replace(tmp, path)


def baidu_state(base, timeout=20):
    code, j, _ = http_json("GET", base + "/engines/status", timeout=timeout)
    if code != 200:
        return None
    for e in j.get("engines", []):
        if e.get("engine") == "baidu":
            return e
    return None


def wait_baidu_ok(base, max_wait_s, poll_s=30):
    """等待百度退出 cooldown/circuit_open；返回 True=可查询，False=超时。"""
    deadline = time.time() + max_wait_s
    while True:
        st = baidu_state(base)
        if st is None:
            log("引擎状态读取失败，60s 后重试")
            time.sleep(60)
        elif st.get("state") == "ok":
            return True
        else:
            until = parse_ts(st.get("until"))
            remain = max(0, int(until - time.time())) if until else poll_s
            wait = min(max(remain, 10), poll_s)
            log(f"百度当前 {st.get('state')}（{st.get('reason') or '风控冷却'}），"
                f"等 {wait}s（剩余约 {remain}s）")
            time.sleep(wait)
        if time.time() > deadline:
            log(f"等待百度恢复超过 {max_wait_s}s，放弃本轮")
            return False


def read_urls(args):
    urls = []
    if args.urls:
        with open(args.urls, encoding="utf-8-sig") as f:
            for line in f:
                line = line.strip()
                if line and not line.startswith("#"):
                    urls.append(line)
    urls.extend(args.url or [])
    seen, out = set(), []
    for u in urls:
        if u not in seen:
            seen.add(u)
            out.append(u)
    return out


def main():
    ap = argparse.ArgumentParser(description="百度小批量收录查询（控节奏、无代理）")
    ap.add_argument("--urls", help="URL 列表文件，每行一个")
    ap.add_argument("--url", action="append", help="单个 URL，可重复")
    ap.add_argument("--base", default=os.environ.get("W2A_BASE", DEFAULT_BASE))
    ap.add_argument("--api-key", default=os.environ.get("W2A_KEY"))
    ap.add_argument("--batch", type=int, default=8, help="每批条数，建议 5-10，最大 20")
    ap.add_argument("--gap", type=int, default=300, help="批间隔秒数，默认 300")
    ap.add_argument("--daily-max", type=int, default=0, help="当日上限条数；0=不限（默认），需要限流时传正整数")
    ap.add_argument("--max-wait", type=int, default=2700, help="等百度冷却的上限秒数，默认 45 分钟")
    ap.add_argument("--timeout", type=int, default=300, help="单批请求超时秒数")
    ap.add_argument("--state", default=STATE_DEFAULT)
    ap.add_argument("--out", default=OUT_DEFAULT)
    ap.add_argument("--no-gap-first", action="store_true", help="首批不等待批次间隔")
    ap.add_argument("--dry-run", action="store_true", help="只打印计划，不发请求")
    args = ap.parse_args()

    base = args.base.rstrip("/")
    key = args.api_key
    if not key:
        for p in ("/root/.web2api-e2e-key", "/root/.web2api-admin-password"):
            if os.path.exists(p):
                key = open(p).read().strip()
                break
    if not key:
        log("缺少 API Key：用 --api-key 或环境变量 W2A_KEY")
        return 2
    if args.batch < 1 or args.batch > 20:
        log("--batch 必须在 1-20（同步接口上限 20）")
        return 2

    urls = read_urls(args)
    if not urls:
        log("没有 URL：用 --urls 或 --url")
        return 2

    state = load_state(args.state)
    done = set(state["done"])
    pending = [u for u in urls if u not in done]
    log(f"共 {len(urls)} 条，已完成 {len(urls)-len(pending)} 条，待查 {len(pending)} 条；"
        f"每批 {args.batch} 条，间隔 {args.gap}s，日上限 {args.daily_max if args.daily_max > 0 else '不限'}")

    if args.daily_max > 0:
        today_left = args.daily_max - state["count"]
        if today_left <= 0:
            log(f"今日已达上限 {args.daily_max} 条，明天再跑（state: {args.state}）")
            return 0
        pending = pending[:today_left]

    if args.dry_run:
        for i in range(0, len(pending), args.batch):
            log(f"计划批次 {i//args.batch+1}: {pending[i:i+args.batch]}")
        return 0

    out = open(args.out, "a", encoding="utf-8")
    consecutive_captcha = 0
    paused_until = 0.0
    batch_no = 0

    while pending:
        batch_no += 1
        # 批次间隔（首批可选跳过）
        wait = max(paused_until - time.time(), 0)
        if batch_no > 1 or not args.no_gap_first:
            gap = args.gap if batch_no > 1 else 0
            wait = max(wait, gap)
        if wait > 0:
            log(f"批次间隔等待 {int(wait)}s 后开始第 {batch_no} 批")
            time.sleep(wait)

        if not wait_baidu_ok(base, args.max_wait):
            log("百度长时间不可用，停止（进度已保存，可重跑续跑）")
            break

        batch = pending[:args.batch]
        log(f"第 {batch_no} 批，{len(batch)} 条：{batch[0]} ...")
        code, body, hdr = http_json("POST", base + "/v1/index/check", key=key,
                                    payload={"urls": batch, "engines": ["baidu"]},
                                    timeout=args.timeout)

        captcha = False
        if code == 200:
            if body.get("meta", {}).get("cache") == "hit":
                pass
            for item in body.get("results", []):
                v = (item.get("engines") or {}).get("baidu", {})
                err = (v.get("error") or {}).get("code") if isinstance(v.get("error"), dict) else None
                if err in ("captcha_detected", "blocked", "rate_limited", "engine_cooldown"):
                    captcha = True
                rec = {"time": now(), "url": item.get("url"), "indexed": v.get("indexed"),
                       "match": v.get("match"), "error": err or ""}
                out.write(json.dumps(rec, ensure_ascii=False) + "\n")
                out.flush()
                if not err:
                    state["done"].append(item.get("url"))
            state["count"] += len(batch)
            save_state(args.state, state)
            log(f"第 {batch_no} 批完成：计费 {body.get('meta',{}).get('units')} 单元；"
                f"今日累计 {state['count']}/{args.daily_max if args.daily_max > 0 else '不限'}")
        elif code == 429:
            captcha = True
            log(f"触发风控（429）：{json.dumps(body, ensure_ascii=False)[:200]}")
        elif code == 503:
            retry = int(body.get("retry_after_seconds") or 60)
            log(f"百度冷却中（503），等 {retry}s 后重试同一批")
            time.sleep(min(retry, 3600))
            continue
        else:
            log(f"请求失败 code={code} body={json.dumps(body, ensure_ascii=False)[:200]}；60s 后重试同一批")
            time.sleep(60)
            continue

        if captcha:
            consecutive_captcha += 1
            pause = 600 if consecutive_captcha == 1 else 1800
            paused_until = time.time() + pause
            log(f"本批出现验证码/风控（连续第 {consecutive_captcha} 次），暂停 {pause//60} 分钟")
        else:
            consecutive_captcha = 0

        # 移出本轮已处理（成功或失败的都算处理过，失败可下次重跑）
        pending = pending[len(batch):]

    out.close()
    save_state(args.state, state)
    log(f"结束：今日累计 {state['count']} 条；结果文件 {args.out}；状态文件 {args.state}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
