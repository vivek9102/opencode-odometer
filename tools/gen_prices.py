"""Generate prices.json from models.dev.

models.dev is the same public price catalogue OpenCode itself uses, so the
numbers here match what the CLI would report for providers that do return
cost. Run this to refresh the shipped price table:

    python tools/gen_prices.py

Private / reseller providers (e.g. an internal AI hub that returns no cost
in its API responses) are deliberately NOT included. Keep those in a local
overlay file instead - see prices.local.example.json.
"""

import json
import os
import sys
import urllib.request

API = "https://models.dev/api.json"

# Providers worth shipping by default. models.dev carries 200+ providers,
# most of which are niche gateways; a smaller curated set keeps the file
# reviewable and avoids duplicate model ids with wildly different rates.
DEFAULT_PROVIDERS = [
    "anthropic",
    "openai",
    "google",
    "google-vertex",
    "azure",
    "amazon-bedrock",
    "mistral",
    "deepseek",
    "xai",
    "groq",
    "cerebras",
    "openrouter",
    "github-copilot",
    "opencode",
    "alibaba",
    "fireworks-ai",
    "togetherai",
    "deepinfra",
]

# Benchmark used to value free/sovereign model usage ("what would this have
# cost on a paid model"). Must exist in the generated table.
REFERENCE_MODEL = "anthropic/claude-sonnet-4-5"

HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OUT = os.path.join(HERE, "prices.json")


def looks_free(model_id, cost):
    low = model_id.lower()
    if low.endswith("-free") or "sovereign" in low or "-free-" in low:
        return True
    # zero across the board means the provider genuinely bills nothing
    return not any(float(cost.get(k) or 0) > 0
                   for k in ("input", "output", "cache_read", "cache_write"))


def fetch(url):
    """models.dev rejects the default urllib user-agent with 403."""
    req = urllib.request.Request(url, headers={
        "User-Agent": "opencode-odometer/1.0 (+https://github.com)",
        "Accept": "application/json",
    })
    with urllib.request.urlopen(req, timeout=90) as r:
        return json.loads(r.read().decode("utf-8"))


def build(providers, data=None):
    if data is None:
        data = fetch(API)

    models = {}
    skipped = 0
    for pid in providers:
        prov = data.get(pid)
        if not prov:
            print(f"  ! provider not found: {pid}")
            continue
        for mid, m in (prov.get("models") or {}).items():
            cost = m.get("cost") or {}
            # models.dev omits `cost` entirely for some entries; without a
            # price we cannot account for them, so skip rather than invent.
            if not cost:
                skipped += 1
                continue
            key = f"{pid}/{mid}"
            entry = {
                "name": m.get("name") or mid,
                "input": float(cost.get("input") or 0),
                "output": float(cost.get("output") or 0),
                "cache_read": float(cost.get("cache_read") or 0),
                "cache_write": float(cost.get("cache_write") or 0),
            }
            if looks_free(mid, cost):
                entry["free"] = True
            models[key] = entry
        print(f"  {pid:20} {len(prov.get('models') or {}):>4} models")

    return models, skipped


def main():
    # optional: pass a path to a previously downloaded api.json to work offline
    local = sys.argv[1] if len(sys.argv) > 1 else None
    if local:
        print(f"reading {local} ...")
        with open(local, encoding="utf-8-sig") as f:
            data = json.load(f)
    else:
        print(f"fetching {API} ...")
        data = None

    models, skipped = build(DEFAULT_PROVIDERS, data)

    if REFERENCE_MODEL not in models:
        print(f"\nERROR: reference model {REFERENCE_MODEL} missing from output.")
        return 1

    doc = {
        "_comment": (
            "USD per 1,000,000 tokens. Generated from models.dev by "
            "tools/gen_prices.py - edit freely, the app always trusts this file. "
            "Add private/reseller providers via prices.local.json instead of "
            "editing this file, so regenerating does not clobber them."
        ),
        "_source": API,
        "reference_model": REFERENCE_MODEL,
        "models": dict(sorted(models.items())),
    }

    with open(OUT, "w", encoding="utf-8") as f:
        json.dump(doc, f, indent=2)
        f.write("\n")

    free = sum(1 for m in models.values() if m.get("free"))
    print(f"\nwrote {OUT}")
    print(f"  {len(models)} models ({free} free), {skipped} skipped (no price)")
    print(f"  reference: {REFERENCE_MODEL}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
