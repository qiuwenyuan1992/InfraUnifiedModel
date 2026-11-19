#!/usr/bin/env python3
"""抓取 cloudbee event-strategy-main 全量策略（926 条）。
来源：http://cloudbee-api.jd.local/api/v1/event-strategy-main/search（POST 分页）
抓取时间：见输出目录 fetch_meta.json
纪律：原样保存（只进不改）；无敏感字段（ip 数组为空、无主机名）。
"""
import json
import time
import urllib.request

API = "http://cloudbee-api.jd.local/api/v1/event-strategy-main/search"
PAGE_SIZE = 50
OUT = "/Users/qiuwenyuan/Documents/workspace/AIgen/InfraUnifiedModel/UnifiedInfrastructureModel/raw/server/cloudbee_strategies.json"
META = "/Users/qiuwenyuan/Documents/workspace/AIgen/InfraUnifiedModel/UnifiedInfrastructureModel/raw/server/fetch_meta.json"


def fetch_page(page_index):
    body = json.dumps({"pageIndex": page_index, "pageSize": PAGE_SIZE}).encode()
    req = urllib.request.Request(API, data=body, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)


def main():
    first = fetch_page(1)
    total = first["data"]["count"]
    all_items = list(first["data"]["list"])
    print(f"total={total}, got {len(all_items)}")
    pages = (total + PAGE_SIZE - 1) // PAGE_SIZE
    for page in range(2, pages + 1):
        time.sleep(0.5)  # 控制速率，避免压力
        resp = fetch_page(page)
        items = resp["data"]["list"]
        if not items:
            print(f"page {page}: empty, stop")
            break
        all_items.extend(items)
        print(f"page {page}/{pages}: +{len(items)} (cum {len(all_items)})")
    # 按 id 去重（防御翻页重叠）
    seen = {}
    for it in all_items:
        seen[it["id"]] = it
    items = sorted(seen.values(), key=lambda x: x["id"])
    with open(OUT, "w") as f:
        json.dump(items, f, ensure_ascii=False, indent=1)
    meta = {
        "source": API,
        "fetched_at": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
        "total_reported": total,
        "total_fetched": len(items),
        "page_size": PAGE_SIZE,
    }
    with open(META, "w") as f:
        json.dump(meta, f, ensure_ascii=False, indent=2)
    print(f"saved {len(items)} -> {OUT}")


if __name__ == "__main__":
    main()
